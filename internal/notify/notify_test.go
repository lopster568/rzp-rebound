package notify_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/notify"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

var notifyStart = time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

// forbidden is what this system may never say about a notification. Each one
// asserts something about a person, and the only thing observed is an HTTP
// response. scripts/check-docs.sh enforces the same rule over prose; this list
// enforces it over the strings the code emits.
var forbidden = []string{
	"customer notified",
	"customer was notified",
	"notified the customer",
	"customer received",
	"customer read",
	"customer informed",
	"informed the customer",
	"reached the customer",
	"told the customer",
	"message delivered",
	"notification delivered",
	"delivered to the customer",
	"sms delivered",
	"email delivered",
}

func newFakeGateway(t *testing.T) (*razorpay.Fake, razorpay.PaymentLink) {
	t.Helper()

	f, err := razorpay.NewFake(razorpay.FakeOptions{Seed: 9, Clock: clock.NewFake(notifyStart)})
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}
	link, err := f.CreatePaymentLink(context.Background(), razorpay.CreatePaymentLinkRequest{
		AmountPaise: 120000,
		Currency:    "INR",
		ReferenceID: "ref_notify",
	})
	if err != nil {
		t.Fatalf("CreatePaymentLink: %v", err)
	}
	return f, link
}

func TestMockNotifierRecordsSendAndReportsAPICallSucceeded(t *testing.T) {
	mock := notify.NewMock(clock.NewFake(notifyStart))
	n, err := notify.New(notify.Options{Port: mock, Clock: clock.NewFake(notifyStart)})
	if err != nil {
		t.Fatalf("notify.New: %v", err)
	}

	receipt, err := n.SendPaymentLink(context.Background(), "plink_mock00000001", razorpay.MediumSMS)
	if err != nil {
		t.Fatalf("SendPaymentLink: %v", err)
	}

	sends := mock.Sends()
	if len(sends) != 1 {
		t.Fatalf("mock recorded %d send(s), want 1", len(sends))
	}
	if sends[0].LinkID != "plink_mock00000001" {
		t.Errorf("recorded link id = %q, want plink_mock00000001", sends[0].LinkID)
	}
	if sends[0].Medium != razorpay.MediumSMS {
		t.Errorf("recorded medium = %q, want %q", sends[0].Medium, razorpay.MediumSMS)
	}

	if !receipt.APICallSucceeded {
		t.Error("receipt does not report that the notification API call succeeded")
	}
	if receipt.AuditPhrase != notify.AuditPhraseAPICallSucceeded {
		t.Errorf("audit phrase = %q, want %q", receipt.AuditPhrase, notify.AuditPhraseAPICallSucceeded)
	}
	if receipt.LinkID != "plink_mock00000001" || receipt.Medium != razorpay.MediumSMS {
		t.Errorf("receipt = %+v, want the link id and medium that were sent", receipt)
	}
	if receipt.RequestedAt.IsZero() {
		t.Error("receipt carries no requested_at")
	}

	// The same notifier, against the fake gateway, with no credential.
	f, link := newFakeGateway(t)
	viaFake, err := notify.New(notify.Options{Port: f, Clock: clock.NewFake(notifyStart)})
	if err != nil {
		t.Fatalf("notify.New over the fake: %v", err)
	}
	fakeReceipt, err := viaFake.SendPaymentLink(context.Background(), link.ID, razorpay.MediumEmail)
	if err != nil {
		t.Fatalf("SendPaymentLink over the fake: %v", err)
	}
	if !fakeReceipt.APICallSucceeded {
		t.Error("a send through the fake gateway did not report the API call succeeding")
	}
}

func TestReceiptDeliveryConfirmedIsAlwaysFalse(t *testing.T) {
	ctx := context.Background()

	succeeding := notify.NewMock(clock.NewFake(notifyStart))
	failing := notify.NewMock(clock.NewFake(notifyStart))
	failing.Err = errors.New("razorpay: gateway refused the resend")

	cases := []struct {
		name   string
		port   notify.NotifierPort
		medium string
		wantOK bool
	}{
		{"api call succeeded", succeeding, razorpay.MediumSMS, true},
		{"api call failed", failing, razorpay.MediumEmail, false},
		{"medium rejected before any call", succeeding, "carrier_pigeon", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := notify.New(notify.Options{Port: tc.port, Clock: clock.NewFake(notifyStart)})
			if err != nil {
				t.Fatalf("notify.New: %v", err)
			}

			receipt, _ := n.SendPaymentLink(ctx, "plink_delivery0001", tc.medium)

			if receipt.DeliveryConfirmed {
				t.Error("DeliveryConfirmed is true, and nothing in this system observes a person receiving anything")
			}
			if receipt.APICallSucceeded != tc.wantOK {
				t.Errorf("APICallSucceeded = %v, want %v", receipt.APICallSucceeded, tc.wantOK)
			}
			if receipt.AuditPhrase == "" {
				t.Error("receipt carries no audit phrase")
			}
		})
	}
}

func TestNotifierNeverClaimsCustomerNotified(t *testing.T) {
	ctx := context.Background()

	phrases := notify.AuditPhrases()
	if len(phrases) == 0 {
		t.Fatal("AuditPhrases returned nothing, so this test would assert over an empty set")
	}

	succeeding := notify.NewMock(clock.NewFake(notifyStart))
	failing := notify.NewMock(clock.NewFake(notifyStart))
	failing.Err = errors.New("razorpay: gateway refused the resend")

	emitted := append([]string(nil), phrases...)
	for _, port := range []notify.NotifierPort{succeeding, failing} {
		n, err := notify.New(notify.Options{Port: port, Clock: clock.NewFake(notifyStart)})
		if err != nil {
			t.Fatalf("notify.New: %v", err)
		}
		for _, medium := range []string{razorpay.MediumSMS, razorpay.MediumEmail, "carrier_pigeon"} {
			receipt, _ := n.SendPaymentLink(ctx, "plink_wording00001", medium)
			emitted = append(emitted, receipt.AuditPhrase)
		}
	}

	for _, s := range emitted {
		lowered := strings.ToLower(s)
		for _, bad := range forbidden {
			if strings.Contains(lowered, bad) {
				t.Errorf("an audit string claims something about a person: %q contains %q", s, bad)
			}
		}
	}

	// Every phrase the package emits has to be one of its constants, so the
	// set a reviewer can read is the set the system can say.
	known := make(map[string]bool, len(phrases))
	for _, p := range phrases {
		known[p] = true
	}
	for _, s := range emitted {
		if !known[s] {
			t.Errorf("emitted audit string %q is not one of the package constants", s)
		}
	}
}
