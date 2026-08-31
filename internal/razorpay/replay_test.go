package razorpay_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// syntheticPaymentID is the payment in
// testdata/recorded/synthetic_failed_payment_insufficient_fund.json. Nobody
// captured that file. Its _meta says so, and this is the only test allowed to
// read it, because it tests the replay mechanism rather than Razorpay.
const syntheticPaymentID = "pay_synthetic00001"

func TestReplayServesRecordedFailedPaymentPayload(t *testing.T) {
	ctx := context.Background()

	c, err := razorpay.NewReplay(razorpay.ReplayOptions{IncludeSynthetic: true})
	if err != nil {
		t.Fatalf("NewReplay: %v", err)
	}

	payment, err := c.FetchPayment(ctx, syntheticPaymentID)
	if err != nil {
		t.Fatalf("FetchPayment(%s): %v", syntheticPaymentID, err)
	}

	if payment.ID != syntheticPaymentID {
		t.Errorf("id = %q, want %q", payment.ID, syntheticPaymentID)
	}
	if payment.OrderID == "" {
		t.Error("the replayed payment carries no order_id, so nothing can join it to an order")
	}
	if payment.Status != razorpay.PaymentStatusFailed {
		t.Errorf("status = %q, want %q", payment.Status, razorpay.PaymentStatusFailed)
	}
	if payment.ErrorReason != "insufficient_fund" {
		t.Errorf("error_reason = %q, want insufficient_fund", payment.ErrorReason)
	}
	// The same three fields the port contract requires of a failed payment.
	if payment.ErrorCode == "" {
		t.Error("the replayed payment carries no error_code")
	}
	if payment.ErrorSource == "" {
		t.Error("the replayed payment carries no error_source")
	}
	if payment.ErrorStep == "" {
		t.Error("the replayed payment carries no error_step")
	}

	// A replay client built for measurement leaves the synthetic files out, so
	// no number can come from one by accident.
	measuring, err := razorpay.NewReplay(razorpay.ReplayOptions{})
	if err != nil {
		t.Fatalf("NewReplay without synthetic fixtures: %v", err)
	}
	if _, err := measuring.FetchPayment(ctx, syntheticPaymentID); !errors.Is(err, razorpay.ErrPaymentNotFound) {
		t.Errorf("a synthetic fixture was served to a client that did not ask for one: err = %v", err)
	}
}

func TestClassifierHandlesEveryRecordedErrorPayload(t *testing.T) {
	dir, err := razorpay.RecordedDir()
	if err != nil {
		t.Fatalf("RecordedDir: %v", err)
	}

	fixtures, err := razorpay.LoadFixtures(dir, false)
	if err != nil {
		t.Fatalf("LoadFixtures(%s): %v", dir, err)
	}

	if len(fixtures) == 0 {
		t.Logf("skipped: %s holds no captured fixture on 2026-08-31, only synthetic ones, "+
			"which this test excludes on purpose. It starts asserting something once the "+
			"phase 1 live half captures real responses.", dir)
		return
	}

	classified := 0
	for key, fixture := range fixtures {
		if fixture.Meta.Synthetic {
			t.Errorf("%s is marked synthetic but was loaded into the measuring set", key)
			continue
		}
		if !strings.Contains(fixture.Request.Path, "/payments") {
			continue
		}

		var payment razorpay.Payment
		if err := fixture.DecodeBody(&payment); err != nil {
			// A collection endpoint decodes into a payment as a zero value
			// rather than an error, so an undecodable body is a real problem.
			t.Errorf("%s: decode body into a Payment: %v", key, err)
			continue
		}
		if payment.Status != razorpay.PaymentStatusFailed {
			continue
		}

		if payment.ErrorCode == "" && payment.ErrorReason == "" {
			t.Errorf("%s: a failed payment carries neither error_code nor error_reason, "+
				"so nothing downstream can classify it without parsing a description string", key)
			continue
		}

		class := classify.Classify(classify.Failure{
			Code:   payment.ErrorCode,
			Reason: payment.ErrorReason,
			Source: payment.ErrorSource,
			Step:   payment.ErrorStep,
		})
		if class.String() == "" {
			t.Errorf("%s: classified to %d, which is not one of the six classes", key, int(class))
			continue
		}
		if class == classify.Unclassified {
			// Fail closed is the right answer, and it is also a finding: the
			// reason is real and the classifier table does not know it.
			t.Logf("%s: reason %q and code %q classify as unclassified, so the table is missing an entry",
				key, payment.ErrorReason, payment.ErrorCode)
		}
		classified++
	}

	t.Logf("classified %d recorded failed payment(s) out of %d fixture(s)", classified, len(fixtures))
}
