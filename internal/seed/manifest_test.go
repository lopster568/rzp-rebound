package seed

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// fixedTime has no monotonic reading, which is what lets a decoded
// time.Time compare equal to it with reflect.DeepEqual: time.Now() carries a
// monotonic component that JSON round-tripping strips, so a manifest built
// with it would never compare equal to what ReadManifest hands back even
// when both represent the same instant.
var fixedTime = time.Date(2026, 9, 5, 10, 30, 0, 0, time.UTC)

func exampleManifest() Manifest {
	return Manifest{
		RunTag:     "seedbook-test-1",
		Profile:    "demo",
		CreatedAt:  fixedTime,
		Gateway:    GatewayLiveTestMode,
		CallBudget: 60,
		CallsUsed:  24,
		Items: []Item{
			{
				Kind:                 EntityInvoice,
				ID:                   "inv_0001",
				OrderID:              "order_0001",
				ShortURL:             "https://rzp.io/i/abc123",
				CustomerID:           "cust_0001",
				CustomerName:         "Aanya Shah",
				CustomerEmail:        "aanya.shah+seedbook-test-1.1@example.com",
				CustomerContact:      "9123456789",
				AmountPaise:          150000,
				Currency:             "INR",
				AgeBucket:            Age60,
				SimulatedAtRiskSince: fixedTime.Add(-60 * 24 * time.Hour).Unix(),
				Flags:                Flags{Disputed: true},
				ExpectedRiskSource:   riskitem.SourceOverdueInvoice,
				CreatedAt:            fixedTime,
				Status:               "issued",
			},
			{
				Kind:                 EntityOrder,
				ID:                   "order_0099",
				AmountPaise:          75000,
				Currency:             "INR",
				AgeBucket:            AgeFresh,
				SimulatedAtRiskSince: fixedTime.Unix(),
				ExpectedRiskSource:   riskitem.SourceUnpaidOrder,
				CreatedAt:            fixedTime,
				Status:               "created",
			},
		},
		Instructions: Instructions{
			Headline: "fail these by hand",
			Targets: []FailTarget{
				{Kind: EntityInvoice, ID: "inv_0001", URL: "https://rzp.io/i/abc123"},
			},
			TestCards: []TestCard{
				{Number: "4100280000080001", ErrorCode: "insufficient_fund"},
			},
		},
	}
}

func TestManifestWriteReadRoundTrip(t *testing.T) {
	want := exampleManifest()
	path := filepath.Join(t.TempDir(), "seedbook.json")

	if err := want.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip changed the manifest.\nwant %+v\ngot  %+v", want, got)
	}
}

func TestManifestWriteCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "seedbook.json")

	if err := exampleManifest().Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := ReadManifest(path); err != nil {
		t.Fatalf("ReadManifest after Write into a new directory: %v", err)
	}
}

func TestReadManifestMissingFile(t *testing.T) {
	_, err := ReadManifest(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("ReadManifest on a missing file returned no error")
	}
}
