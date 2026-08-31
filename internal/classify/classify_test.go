package classify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
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

// TestClassifierLeavesTheObservedLiveReasonUnclassified pins what the phase 1
// live half found, which is uncomfortable and is the point.
//
// The eight reasons this classifier maps come from Razorpay's documented test
// cards. On 2026-08-31 all eight of those cards were driven through the only
// mechanism that can make a test-mode payment attempt happen, and every one of
// them came back with error.reason "payment_failed" and error.code
// "BAD_REQUEST_ERROR". Not one documented reason string was ever produced.
// docs/RAZORPAY-TEST-MODE-NOTES.md has the walk.
//
// "payment_failed" names no cause. Nothing about it says whether a balance, a
// disabled card, or a gateway hiccup stopped the charge, so there is no basis
// on which to call a retry eligible, and inventing one would be the exact
// dishonesty this project is built to avoid. It therefore stays out of the
// reasons table and falls to the fail-closed default.
//
// It is listed in error_codes.json under _meta.pending, so the totality test
// skips it rather than failing, and a reader of that file finds it rather than
// wondering why the live reason is missing.
func TestClassifierLeavesTheObservedLiveReasonUnclassified(t *testing.T) {
	got := classify.Classify(classify.Failure{
		Code:   razorpay.ErrorClassBadRequest,
		Reason: razorpay.ReasonPaymentFailed,
		Source: razorpay.ErrorSourceGateway,
		Step:   razorpay.ErrorStepPaymentAuthorization,
	})

	if got != classify.Unclassified {
		t.Errorf("%s classified as %v, want unclassified: it names no cause a retry could act on",
			razorpay.ReasonPaymentFailed, got)
	}
	if got.IsRetryEligible() {
		t.Errorf("%s is retry eligible, which is a retry with nothing behind it",
			razorpay.ReasonPaymentFailed)
	}

	// The coarse class alone must not rescue it either. BAD_REQUEST_ERROR maps
	// to NeverRetry, and reason wins over code, so a caller must not be able
	// to get a decision by dropping the reason.
	if bare := classify.Classify(classify.Failure{Code: razorpay.ErrorClassBadRequest}); bare != classify.NeverRetry {
		t.Errorf("%s alone classified as %v, want never_retry", razorpay.ErrorClassBadRequest, bare)
	}

	// The file has to carry it, or the next person reads a table of eight
	// reasons and believes test mode produces them.
	f := loadErrorCodes(t)
	listed := false
	for _, entry := range f.Codes {
		if entry.Code == razorpay.ReasonPaymentFailed {
			listed = true
			if entry.Kind != "reason" {
				t.Errorf("%s is listed with kind %q, want reason", entry.Code, entry.Kind)
			}
		}
	}
	if !listed {
		t.Errorf("%s is the only failure reason live test mode produces and %s does not list it",
			razorpay.ReasonPaymentFailed, errorCodesPath)
	}
	if !slices.Contains(f.Meta.Pending, razorpay.ReasonPaymentFailed) {
		t.Errorf("%s is not in _meta.pending, so the totality test will demand a class for a reason that names no cause",
			razorpay.ReasonPaymentFailed)
	}
}
