package classify_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/networkcodes"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// The two documented live-mode reason lists, written out here rather than read
// out of the implementation. A table-driven test that ranged over the map it is
// testing would pass for any map.
//
// Sources, read 2026-09-01:
//
//	cards: https://razorpay.com/docs/errors/payments/cards/
//	upi:   https://razorpay.com/docs/errors/payments/upi/
//
// The reason strings are Razorpay's. The class each one maps to is this
// project's judgment and is cited nowhere, which is stated in docs/EVIDENCE.md
// and is the honest half of adopting a documented vocabulary.
var documentedCardReasons = map[string]classify.Class{
	"payment_timed_out":                 classify.TransientRetryEligible,
	"gateway_technical_error":           classify.TransientRetryEligible,
	"bank_technical_error":              classify.TransientRetryEligible,
	"payment_cancelled":                 classify.ReauthRequired,
	"authentication_failed":             classify.ReauthRequired,
	"incorrect_cvv":                     classify.ReauthRequired,
	"insufficient_funds":                classify.RetryEligible,
	"card_declined":                     classify.NewInstrumentRequired,
	"card_not_enrolled":                 classify.NewInstrumentRequired,
	"card_disabled_for_online_payments": classify.NewInstrumentRequired,
	"card_expired":                      classify.NewInstrumentRequired,
	"debit_instrument_inactive":         classify.NewInstrumentRequired,
	"debit_instrument_blocked":          classify.NewInstrumentRequired,
	"transaction_limit_exceeded":        classify.NewInstrumentRequired,
	"payment_risk_check_failed":         classify.NeverRetry,
}

var documentedUPIReasons = map[string]classify.Class{
	"bank_technical_error":            classify.TransientRetryEligible,
	"credit_failed":                   classify.TransientRetryEligible,
	"vpa_resolution_failed":           classify.TransientRetryEligible,
	"payment_timed_out":               classify.TransientRetryEligible,
	"insufficient_funds":              classify.RetryEligible,
	"payment_collect_request_expired": classify.ReauthRequired,
	"payment_declined":                classify.ReauthRequired,
	"invalid_vpa":                     classify.NewInstrumentRequired,
}

func TestClassifierMapsEveryDocumentedCardReason(t *testing.T) {
	if len(documentedCardReasons) != 15 {
		t.Fatalf("the card list in this test has %d reasons, and the documentation has 15", len(documentedCardReasons))
	}
	for reason, want := range documentedCardReasons {
		t.Run(reason, func(t *testing.T) {
			got := classify.Classify(classify.Failure{Method: classify.MethodCard, Reason: reason})
			if got != want {
				t.Errorf("card %s classified as %v, want %v", reason, got, want)
			}
		})
	}
}

func TestClassifierMapsEveryDocumentedUPIReason(t *testing.T) {
	if len(documentedUPIReasons) != 8 {
		t.Fatalf("the upi list in this test has %d reasons, and the documentation has 8", len(documentedUPIReasons))
	}
	for reason, want := range documentedUPIReasons {
		t.Run(reason, func(t *testing.T) {
			got := classify.Classify(classify.Failure{Method: classify.MethodUPI, Reason: reason})
			if got != want {
				t.Errorf("upi %s classified as %v, want %v", reason, got, want)
			}
		})
	}
}

// TestInsufficientFundsIsSpelledTheWayTheLiveDocsSpellIt is a bug fix with a
// test on it. The repository has spelled this reason singular since phase 0,
// because that is the spelling on the test-card page, and the live-mode error
// documentation spells it plural. A classifier that only knows the singular
// form would return unclassified for the real one.
func TestInsufficientFundsIsSpelledTheWayTheLiveDocsSpellIt(t *testing.T) {
	for _, method := range []classify.Method{classify.MethodAny, classify.MethodCard, classify.MethodUPI} {
		got := classify.Classify(classify.Failure{Method: method, Reason: "insufficient_funds"})
		if got != classify.RetryEligible {
			t.Errorf("insufficient_funds on method %q classified as %v, want %v", method, got, classify.RetryEligible)
		}
	}
}

// TestTestModeCardTableSpellingsStillClassify keeps the observed test-mode
// vocabulary working. Every committed batch under results/batches/ carries
// these spellings, the fake gateway seeds them, and dropping them would make
// the published runs unreplayable to make a documentation list tidy.
func TestTestModeCardTableSpellingsStillClassify(t *testing.T) {
	tests := map[string]classify.Class{
		"insufficient_fund":   classify.RetryEligible,
		"card_number_invalid": classify.NewInstrumentRequired,
	}
	for reason, want := range tests {
		t.Run(reason, func(t *testing.T) {
			if got := classify.Classify(classify.Failure{Reason: reason}); got != want {
				t.Errorf("%s classified as %v, want %v", reason, got, want)
			}
			// It is a test-mode spelling and it must not be published as a
			// documented live-mode one.
			if _, ok := classify.DocumentedReasons(classify.MethodCard)[reason]; ok {
				t.Errorf("%s is in the documented live-mode card table, and it is a test-card page spelling", reason)
			}
		})
	}
}

func TestPaymentRiskCheckFailedIsNeverRetry(t *testing.T) {
	got := classify.Classify(classify.Failure{Reason: classify.ReasonPaymentRiskCheckFailed})

	if got != classify.NeverRetry {
		t.Errorf("%s classified as %v, want %v", classify.ReasonPaymentRiskCheckFailed, got, classify.NeverRetry)
	}
	if got.IsRetryEligible() {
		t.Error("a risk block came back retry eligible")
	}
	if classify.ReasonPaymentRiskCheckFailed != "payment_risk_check_failed" {
		t.Errorf("ReasonPaymentRiskCheckFailed = %q, and the documented reason is payment_risk_check_failed",
			classify.ReasonPaymentRiskCheckFailed)
	}
}

// TestCardExpiredAndBlockedInstrumentAreNewInstrumentRequiredNotNeverRetry
// records a judgment call, because the two classes are close and the difference
// decides whether a customer hears from us at all.
//
// Both classes forbid another attempt on the same instrument: IsRetryEligible
// is false for each. What separates them is whether asking the customer for a
// different instrument is allowed. An expired card and a blocked debit
// instrument are both "this instrument cannot pay, send a payment link", which
// is recoverable revenue. A risk block is not: contacting a customer a risk
// engine has flagged is itself an action, so never_retry means no action of any
// kind and escalates to a person under R4.
func TestCardExpiredAndBlockedInstrumentAreNewInstrumentRequiredNotNeverRetry(t *testing.T) {
	for _, reason := range []string{"card_expired", "debit_instrument_blocked"} {
		t.Run(reason, func(t *testing.T) {
			got := classify.Classify(classify.Failure{Method: classify.MethodCard, Reason: reason})

			if got != classify.NewInstrumentRequired {
				t.Errorf("%s classified as %v, want %v", reason, got, classify.NewInstrumentRequired)
			}
			if got.IsRetryEligible() {
				t.Errorf("%s came back retry eligible, and the same instrument will fail again", reason)
			}
		})
	}
	// The one reason that does forbid every action.
	if got := classify.Classify(classify.Failure{Reason: classify.ReasonPaymentRiskCheckFailed}); got != classify.NeverRetry {
		t.Errorf("the risk block classified as %v, so never_retry now means nothing distinct", got)
	}
}

// TestNoReasonClassifiesDifferentlyAcrossMethods is what makes a lookup with no
// method safe. Three reasons appear in both documented lists and all three
// agree, so a caller that does not know the payment method still gets an
// answer. If a future documentation change breaks that, this test says so
// before the merged lookup starts silently picking one.
func TestNoReasonClassifiesDifferentlyAcrossMethods(t *testing.T) {
	shared := 0
	for reason, cardClass := range documentedCardReasons {
		upiClass, both := documentedUPIReasons[reason]
		if !both {
			continue
		}
		shared++
		if cardClass != upiClass {
			t.Errorf("%s is %v for cards and %v for upi", reason, cardClass, upiClass)
		}
		if got := classify.Classify(classify.Failure{Reason: reason}); got != cardClass {
			t.Errorf("%s with no method classified as %v, want %v", reason, got, cardClass)
		}
	}
	if shared != 3 {
		t.Errorf("%d reasons appear in both lists, want 3 (bank_technical_error, insufficient_funds, payment_timed_out)", shared)
	}
}

// TestAmbiguousReasonAcrossMethodsIsUnclassified proves the rule the test above
// currently has no case for. Today no reason disagrees across methods, so the
// merged lookup's tie-breaking behaviour would be untested and would stay
// untested until the day it mattered. ClassifyAcross takes the tables as an
// argument so the ambiguous case can be constructed.
func TestAmbiguousReasonAcrossMethodsIsUnclassified(t *testing.T) {
	tables := []map[string]classify.Class{
		{"a_disputed_reason": classify.RetryEligible},
		{"a_disputed_reason": classify.NeverRetry},
	}

	got := classify.ClassifyAcross(tables, "a_disputed_reason")

	if got != classify.Unclassified {
		t.Errorf("a reason two tables disagree about classified as %v, want %v", got, classify.Unclassified)
	}
	if got.IsRetryEligible() {
		t.Error("a disputed reason came back retry eligible, which is the one thing an ambiguous failure must never be")
	}

	agreeing := []map[string]classify.Class{
		{"an_agreed_reason": classify.RetryEligible},
		{"an_agreed_reason": classify.RetryEligible},
	}
	if got := classify.ClassifyAcross(agreeing, "an_agreed_reason"); got != classify.RetryEligible {
		t.Errorf("a reason two tables agree about classified as %v, want %v", got, classify.RetryEligible)
	}
}

// TestDocumentedErrorSourcesArePerMethod pins a correction. This project first
// modelled error.source as one flat nine-value enumeration, and Razorpay
// documents it per payment method: five values for cards, eight for UPI, and
// "issuer" is not one of them. A first-hand re-read of the documentation on
// 2026-09-01 found it.
func TestDocumentedErrorSourcesArePerMethod(t *testing.T) {
	card := []string{"business", "customer", "gateway", "internal", "issuer_bank"}
	upi := []string{
		"beneficiary_bank", "business", "customer", "customer_psp", "gateway",
		"internal", "issuer_bank", "network",
	}

	for _, tc := range []struct {
		method classify.Method
		want   []string
	}{
		{classify.MethodCard, card},
		{classify.MethodUPI, upi},
		// No method means the union, which is the UPI list because it is a
		// superset of the card one.
		{classify.MethodAny, upi},
	} {
		t.Run(string(tc.method)+"_method", func(t *testing.T) {
			var got []string
			for _, s := range classify.DocumentedSources(tc.method) {
				got = append(got, string(s))
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("DocumentedSources(%q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}

	for _, name := range upi {
		source, ok := classify.ParseSource(name)
		if !ok {
			t.Errorf("ParseSource(%q) failed, and it is on the documented list", name)
		}
		if !source.Documented() {
			t.Errorf("%q parsed and then reported itself undocumented", name)
		}
	}

	// The three UPI-only values are not documented for cards, and a Source has
	// to be able to say so, because a card payment carrying customer_psp would
	// be a shape the documentation does not describe.
	for _, name := range []string{"beneficiary_bank", "customer_psp", "network"} {
		if classify.Source(name).DocumentedFor(classify.MethodCard) {
			t.Errorf("%q reports itself documented for cards, and it is on the UPI list only", name)
		}
		if !classify.Source(name).DocumentedFor(classify.MethodUPI) {
			t.Errorf("%q is not documented for UPI, and it is on that list", name)
		}
	}
}

func TestUndocumentedErrorSourceDoesNotParse(t *testing.T) {
	// "issuer" is here on purpose. It was in this project's first, wrong,
	// flat enumeration and it is on neither documented list.
	for _, name := range []string{"", "bank", "Customer", "issuer_bank ", "acquirer", "issuer"} {
		if source, ok := classify.ParseSource(name); ok {
			t.Errorf("ParseSource(%q) returned %v, and it is on neither documented list", name, source)
		}
	}
	if classify.Source("acquirer").Documented() {
		t.Error("an undocumented source reported itself documented")
	}
}

// TestPaymentFailedIsDocumentedAndStillUnclassified is the second correction
// from the 2026-09-01 re-read, and it is the uncomfortable one.
//
// payment_failed is not an undocumented string this project stumbled on in test
// mode. Razorpay documents it as a live-mode card reason: the bank declined the
// payment without giving a specific reason. Its suggested action is to contact
// the bank or try a different card.
//
// The classification does not change, and the reason it does not is worth
// stating. The suggested action rules out a same-instrument retry, which is
// exactly what unclassified already delivers through R7-UNKNOWN-FAIL-CLOSED. It
// is not promoted to new_instrument_required, because a support instruction
// written for a human reading one order is not a class: acting on it would mean
// messaging every customer whose payment failed for a reason the bank did not
// give, which is the over-messaging R4 and R7 exist to prevent. DECISIONS.md
// entry 11 has the argument.
func TestPaymentFailedIsDocumentedAndStillUnclassified(t *testing.T) {
	got := classify.Classify(classify.Failure{Method: classify.MethodCard, Reason: razorpay.ReasonPaymentFailed})

	if got != classify.Unclassified {
		t.Errorf("%s classified as %v, want unclassified", razorpay.ReasonPaymentFailed, got)
	}
	if got.IsRetryEligible() {
		t.Errorf("%s is retry eligible, and the documented suggested action is to try a different card",
			razorpay.ReasonPaymentFailed)
	}
	// It is documented, so it must not be in the documented reason table, and
	// the file has to record both facts. The table is the classifier's, and a
	// reason with no class does not belong in it.
	if _, ok := classify.DocumentedReasons(classify.MethodCard)[razorpay.ReasonPaymentFailed]; ok {
		t.Errorf("%s is in the classifier's card table, and it has no class", razorpay.ReasonPaymentFailed)
	}
}

// TestErrorStepIsNotAnEnum pins an absence. Razorpay's payment error parameter
// page documents an enumeration for error.source and publishes none for
// error.step, so the field stays a string. Asserting the absence stops a later
// phase quietly inventing a list and presenting it as documented.
func TestErrorStepIsNotAnEnum(t *testing.T) {
	f := classify.Failure{Step: "a_step_nobody_published"}

	if f.Step != "a_step_nobody_published" {
		t.Errorf("Step = %q, and it is a free string because no enumeration is published for it", f.Step)
	}
	if got := classify.Classify(classify.Failure{Reason: "insufficient_funds", Step: "anything_at_all"}); got != classify.RetryEligible {
		t.Errorf("the step changed the class to %v, and nothing branches on a field with no published values", got)
	}
}

// TestClassifierHandlesTheProductionFailureShape is the only test in this
// package built from a payment that was neither seeded nor documented.
//
// A read-only probe of the author's own live Razorpay merchant account on
// 2026-09-01 returned two payments over 2026-07-15 to 2026-08-31. One was
// captured. The other failed, and its shape is below: a UPI payment that
// carried error_code BAD_REQUEST_ERROR, error_reason payment_timed_out,
// error_source customer, and error_step payment_authentication.
//
// Three things that shape confirms, and they are the reason this test exists:
// a documented live-mode reason string does appear in production, the
// documented error_source enum does appear in production, and the
// coarse-code-plus-specific-reason structure holds outside test mode, with the
// coarse code still BAD_REQUEST_ERROR and all the signal in error_reason.
//
// Aggregate fields only. No payload from that account is published anywhere in
// this repository, and n=2 is a specimen and not a distribution.
func TestClassifierHandlesTheProductionFailureShape(t *testing.T) {
	source, ok := classify.ParseSource("customer")
	if !ok {
		t.Fatal("customer did not parse as an error source, and a live payment carried it")
	}

	f := classify.Failure{
		Method: classify.MethodUPI,
		Code:   razorpay.ErrorClassBadRequest,
		Reason: "payment_timed_out",
		Source: source,
		Step:   "payment_authentication",
	}

	got := classify.Classify(f)

	if got != classify.TransientRetryEligible {
		t.Errorf("the production failure shape classified as %v, want %v", got, classify.TransientRetryEligible)
	}
	if !got.IsRetryEligible() {
		t.Error("a payment the customer ran out of time on is not retry eligible, and the customer never declined anything")
	}
	// The coarse class must not override the reason. BAD_REQUEST_ERROR alone
	// is never_retry, and reason wins.
	if bare := classify.Classify(classify.Failure{Code: razorpay.ErrorClassBadRequest}); bare != classify.NeverRetry {
		t.Errorf("%s alone classified as %v, want never_retry", razorpay.ErrorClassBadRequest, bare)
	}
}

// TestNetworkDeclineCodesClassifyThroughTheNetworkLists connects the policy's
// never-retry class to the networks' own rules. No Razorpay payload observed by
// this project carries a raw network response code, so this path is exercised
// here and in no run, which HONEST-LIMITATIONS records.
func TestNetworkDeclineCodesClassifyThroughTheNetworkLists(t *testing.T) {
	if got := classify.ClassifyNetworkDeclineCode(networkcodes.Visa, "41"); got != classify.NeverRetry {
		t.Errorf("visa 41, a lost card, classified as %v, want never_retry", got)
	}
	if got := classify.ClassifyNetworkDeclineCode(networkcodes.Mastercard, "03"); got != classify.NeverRetry {
		t.Errorf("mastercard advice code 03 classified as %v, want never_retry", got)
	}
	if got := classify.ClassifyNetworkDeclineCode(networkcodes.Visa, "05"); got != classify.Unclassified {
		t.Errorf("visa 05, a do-not-honour, classified as %v, and this function only speaks for the never-retry lists", got)
	}
	if got := classify.ClassifyNetworkDeclineCode("", "41"); got != classify.Unclassified {
		t.Errorf("a code with no network classified as %v, want unclassified", got)
	}
}

// TestErrorCodeFileLabelsEveryEntry is the phase 5 rule applied to the one file
// the classifier's totality test reads. Before phase 5 every row in it carried
// a date and nothing about where the string came from, so a reader could not
// tell a live-mode documented reason from a spelling only test mode returns.
// That distinction is the whole finding of phase 1.
func TestErrorCodeFileLabelsEveryEntry(t *testing.T) {
	raw, err := os.ReadFile(errorCodesPath)
	if err != nil {
		t.Fatalf("read %s: %v", errorCodesPath, err)
	}
	var f struct {
		Meta struct {
			Labels map[string]string `json:"labels"`
		} `json:"_meta"`
		Codes []struct {
			Code   string `json:"code"`
			Kind   string `json:"kind"`
			Label  string `json:"label"`
			Source string `json:"source"`
			Method string `json:"method"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse %s: %v", errorCodesPath, err)
	}
	if len(f.Meta.Labels) == 0 {
		t.Errorf("%s has no _meta.labels explaining what each label means", errorCodesPath)
	}

	valid := []string{"documented-live", "documented-test-mode", "observed-test-mode"}
	documentedLive := 0
	for _, entry := range f.Codes {
		if !slices.Contains(valid, entry.Label) {
			t.Errorf("%s has label %q, want one of %v", entry.Code, entry.Label, valid)
		}
		if entry.Source == "" {
			t.Errorf("%s has no source", entry.Code)
		}
		if _, ok := f.Meta.Labels[entry.Label]; entry.Label != "" && !ok {
			t.Errorf("%s uses label %q and _meta.labels does not define it", entry.Code, entry.Label)
		}
		if entry.Label != "documented-live" {
			continue
		}
		documentedLive++
		if entry.Kind != "reason" {
			continue
		}
		// A row claiming to be documented live-mode vocabulary has to be in the
		// live-mode table for the method it names, or the label is decoration.
		if _, ok := classify.DocumentedReasons(classify.Method(entry.Method))[entry.Code]; !ok {
			t.Errorf("%s is labelled documented-live for method %q and is not in that method's documented table",
				entry.Code, entry.Method)
		}
	}
	// 15 card reasons plus 8 UPI reasons, three of which are shared and get a
	// row each because they are documented on both pages.
	if want := 23; documentedLive < want {
		t.Errorf("%s carries %d documented-live rows, want at least %d", errorCodesPath, documentedLive, want)
	}
}
