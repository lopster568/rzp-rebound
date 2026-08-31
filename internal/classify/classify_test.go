package classify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
)

// errorCodesPath is testdata/error_codes.json seen from this package
// directory, which is where go test runs.
var errorCodesPath = filepath.Join("..", "..", "testdata", "error_codes.json")

type errorCodeFile struct {
	Meta struct {
		// Pending lists codes that have no classification yet. It is empty
		// while every documented code is mapped.
		Pending []string `json:"pending"`
	} `json:"_meta"`
	Codes []struct {
		Code string `json:"code"`
		Kind string `json:"kind"`
	} `json:"codes"`
}

func loadErrorCodes(t *testing.T) errorCodeFile {
	t.Helper()

	raw, err := os.ReadFile(errorCodesPath)
	if err != nil {
		t.Fatalf("read %s: %v", errorCodesPath, err)
	}
	var f errorCodeFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse %s: %v", errorCodesPath, err)
	}
	if len(f.Codes) == 0 {
		t.Fatalf("%s lists no codes", errorCodesPath)
	}
	return f
}

func TestClassifierMapsInsufficientFundToRetryEligible(t *testing.T) {
	got := classify.Classify(classify.Failure{Reason: "insufficient_fund"})

	if got != classify.RetryEligible {
		t.Errorf("insufficient_fund classified as %v, want %v", got, classify.RetryEligible)
	}
	if !got.IsRetryEligible() {
		t.Error("insufficient_fund is not retry eligible, but the balance can change between attempts")
	}
}

func TestClassifierMapsGatewayTechnicalErrorToTransientRetryEligible(t *testing.T) {
	got := classify.Classify(classify.Failure{Reason: "gateway_technical_error"})

	if got != classify.TransientRetryEligible {
		t.Errorf("gateway_technical_error classified as %v, want %v", got, classify.TransientRetryEligible)
	}
	if !got.IsRetryEligible() {
		t.Error("gateway_technical_error is not retry eligible")
	}
}

func TestClassifierMapsPaymentTimedOutToTransientRetryEligible(t *testing.T) {
	got := classify.Classify(classify.Failure{Reason: "payment_timed_out"})

	if got != classify.TransientRetryEligible {
		t.Errorf("payment_timed_out classified as %v, want %v", got, classify.TransientRetryEligible)
	}
	if !got.IsRetryEligible() {
		t.Error("payment_timed_out is not retry eligible")
	}
}

func TestClassifierMapsAuthenticationFailedToReauthRequired(t *testing.T) {
	got := classify.Classify(classify.Failure{Reason: "authentication_failed"})

	if got != classify.ReauthRequired {
		t.Errorf("authentication_failed classified as %v, want %v", got, classify.ReauthRequired)
	}
	if got.IsRetryEligible() {
		t.Error("authentication_failed came back retry eligible, but recharging without the customer is not an option")
	}
}

func TestClassifierMapsCardDeclinedToNewInstrumentRequired(t *testing.T) {
	got := classify.Classify(classify.Failure{Reason: "card_declined"})

	if got != classify.NewInstrumentRequired {
		t.Errorf("card_declined classified as %v, want %v", got, classify.NewInstrumentRequired)
	}
	if got.IsRetryEligible() {
		t.Error("card_declined came back retry eligible, but the same card will decline again")
	}
}

func TestClassifierMapsRiskBlockToNeverRetry(t *testing.T) {
	got := classify.Classify(classify.Failure{Reason: testcards.PendingRiskBlockCode})

	if got != classify.NeverRetry {
		t.Errorf("risk block classified as %v, want %v", got, classify.NeverRetry)
	}
	if got.IsRetryEligible() {
		t.Error("a risk block came back retry eligible")
	}
}

func TestClassifierUnknownErrorCodeIsUnclassifiedAndNotRetryEligible(t *testing.T) {
	tests := []struct {
		name    string
		failure classify.Failure
	}{
		{"unknown reason", classify.Failure{Reason: "no_such_reason_exists"}},
		{"unknown error class", classify.Failure{Code: "NO_SUCH_ERROR_CLASS"}},
		{"empty failure", classify.Failure{}},
		{
			// A reason nothing recognises does not fall back to the coarser
			// error class. The specific field is the one we failed to read.
			name:    "unknown reason under a known error class",
			failure: classify.Failure{Code: "GATEWAY_ERROR", Reason: "no_such_reason_exists"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify.Classify(tc.failure)

			if got != classify.Unclassified {
				t.Errorf("Classify(%+v) = %v, want %v", tc.failure, got, classify.Unclassified)
			}
			if got.IsRetryEligible() {
				t.Errorf("Classify(%+v) came back retry eligible, which is the one thing an unknown failure must never be", tc.failure)
			}
		})
	}
}

func TestClassifierIsTotalOverKnownRazorpayErrorCodes(t *testing.T) {
	f := loadErrorCodes(t)

	for _, entry := range f.Codes {
		t.Run(entry.Code, func(t *testing.T) {
			if slices.Contains(f.Meta.Pending, entry.Code) {
				t.Skipf("%s is listed in _meta.pending in %s", entry.Code, errorCodesPath)
			}

			var failure classify.Failure
			switch entry.Kind {
			case "reason":
				failure.Reason = entry.Code
			case "error_class":
				failure.Code = entry.Code
			default:
				t.Fatalf("%s has kind %q, which is neither reason nor error_class", entry.Code, entry.Kind)
			}

			got := classify.Classify(failure)

			if got == classify.Unclassified {
				t.Errorf("%s (%s) is documented in %s but classifies as %v", entry.Code, entry.Kind, errorCodesPath, got)
			}
			if got.String() == "" {
				t.Errorf("%s classified as %d, which has no name", entry.Code, got)
			}
		})
	}

	// The risk block is not in the file yet. It still has to classify.
	if got := classify.Classify(classify.Failure{Reason: testcards.PendingRiskBlockCode}); got == classify.Unclassified {
		t.Errorf("%s classifies as %v", testcards.PendingRiskBlockCode, got)
	}
}
