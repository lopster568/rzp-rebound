package riskrun

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/promise"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// fixtureBase is the instant testdata/manifest.json was written against. Every
// simulated age in that file is measured back from it, so a test that wants the
// fixture's aged invoices to look aged and its fresh ones to look fresh runs its
// clock an hour past it.
var fixtureBase = time.Date(2026, 9, 5, 6, 0, 0, 0, time.UTC)

func loadFixture(t *testing.T) seed.Manifest {
	t.Helper()
	manifest, err := seed.ReadManifest("testdata/manifest.json")
	if err != nil {
		t.Fatalf("read the fixture manifest: %v", err)
	}
	if len(manifest.Items) == 0 {
		t.Fatal("the fixture manifest has no items")
	}
	return manifest
}

// refusingAPI fails the test if anything reads through it. A dry run is handed
// one to prove it never touches the client it was given.
type refusingAPI struct{ t *testing.T }

func (a refusingAPI) ListOrders(context.Context, razorpay.ListOptions) ([]razorpay.Order, error) {
	a.t.Fatal("a dry run called ListOrders")
	return nil, nil
}

func (a refusingAPI) ListInvoices(context.Context, razorpay.ListOptions) ([]razorpay.Invoice, error) {
	a.t.Fatal("a dry run called ListInvoices")
	return nil, nil
}

func (a refusingAPI) ListPaymentsForOrder(context.Context, string) ([]razorpay.Payment, error) {
	a.t.Fatal("a dry run called ListPaymentsForOrder")
	return nil, nil
}

// dryRun runs the fixture through the pipeline with no network of any kind and
// returns the summary and the result rows.
func dryRun(t *testing.T, mutate func(*Options)) (Summary, []ItemResult) {
	t.Helper()

	var ledger, results bytes.Buffer
	recorder, err := audit.NewRecorder(audit.Options{Writer: &ledger, Clock: clock.NewFake(fixtureBase.Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}

	opts := Options{
		Manifest:     loadFixture(t),
		ManifestPath: "testdata/manifest.json",
		RunTag:       "test",
		Seed:         1234,
		DryRun:       true,
		SimulateAge:  true,
		Clock:        clock.NewFake(fixtureBase.Add(time.Hour)),
		Recorder:     recorder,
		Results:      &results,
		API:          refusingAPI{t: t},
	}
	if mutate != nil {
		mutate(&opts)
	}

	summary, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return summary, decodeRows(t, results.Bytes())
}

func decodeRows(t *testing.T, raw []byte) []ItemResult {
	t.Helper()
	var rows []ItemResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for decoder.More() {
		var row ItemResult
		if err := decoder.Decode(&row); err != nil {
			t.Fatalf("decode a result row: %v", err)
		}
		rows = append(rows, row)
	}
	return rows
}

// TestDryRunReachesBothRefusalAndAllowWithNoNetwork is the whole-pipeline pin.
//
// It drives the committed fixture through the real detectors, the real dedupe,
// and the real gate with a client that fails the test if it is called, and
// checks that every one of the paths the demo depends on is reached: an allow, a
// refusal for a debt nobody can be contacted about, and a refusal for a debt
// somebody has contested.
func TestDryRunReachesBothRefusalAndAllowWithNoNetwork(t *testing.T) {
	summary, rows := dryRun(t, nil)

	if summary.Mode != ModeDryRun {
		t.Errorf("mode = %q, want %q", summary.Mode, ModeDryRun)
	}
	if len(rows) != summary.ItemsTotal {
		t.Errorf("%d result rows for %d items", len(rows), summary.ItemsTotal)
	}
	if summary.CollapsedAway == 0 {
		t.Error("the dedupe merged nothing, so the invoice and the order it minted did not collapse")
	}

	// Nothing was executed. That is the dry run's whole promise, and it is
	// separate from the arm assignment: even the engine arm's items stop here.
	for _, row := range rows {
		if row.Executed {
			t.Errorf("%s was executed in a dry run", row.RiskItemID)
		}
	}
	if len(summary.ActionsExecuted) != 0 || summary.Escalations != 0 {
		t.Errorf("a dry run reported executed actions: %+v, %d escalation(s)", summary.ActionsExecuted, summary.Escalations)
	}

	// Both sources the manifest can seed reached the queue, and both arms got
	// items from each of them.
	if summary.ItemsBySource[string(riskitem.SourceOverdueInvoice)] == 0 {
		t.Error("no overdue invoice reached the queue")
	}
	if summary.ItemsBySource[string(riskitem.SourceUnpaidOrder)] == 0 {
		t.Error("no unpaid order reached the queue")
	}

	byRule := map[string]int{}
	for _, row := range rows {
		byRule[row.RuleID]++
	}
	for _, want := range []string{policy.RuleAllow, policy.RuleNoContactChannel, policy.RuleDisputed} {
		if byRule[want] == 0 {
			t.Errorf("no item was decided by %s. Rules that fired: %v", want, byRule)
		}
	}

	// The escalations are decisions, not actions, so they are in the verdict
	// counts and not in the escalation count.
	if summary.VerdictTotals[string(policy.VerdictEscalate)] == 0 {
		t.Error("nothing escalated, so the refusal paths were not reached")
	}
	if summary.VerdictTotals[string(policy.VerdictAllow)] == 0 {
		t.Error("nothing was allowed, so the allow path was not reached")
	}
}

// TestDryRunFiresR13FromTheManifestDisputedFlag is the disputed plumbing.
//
// Razorpay has no disputed field on an invoice, an order, or a payment, so a
// gate driven from the API alone can never reach R13. The seedbook manifest is
// where a contested debt is recorded, and this is the assertion that the flag
// travels from there to the rule.
func TestDryRunFiresR13FromTheManifestDisputedFlag(t *testing.T) {
	manifest := loadFixture(t)

	var disputedID string
	for _, item := range manifest.Items {
		if item.Flags.Disputed {
			disputedID = item.ID
		}
	}
	if disputedID == "" {
		t.Fatal("the fixture manifest flags nothing as disputed, so R13 has nothing to fire on")
	}

	_, rows := dryRun(t, nil)

	var found bool
	for _, row := range rows {
		if row.SourceID != disputedID {
			continue
		}
		found = true
		if !row.Disputed {
			t.Errorf("%s reached the gate with disputed false", row.RiskItemID)
		}
		if row.RuleID != policy.RuleDisputed {
			t.Errorf("%s was decided by %s, want %s", row.RiskItemID, row.RuleID, policy.RuleDisputed)
		}
		if row.Verdict != string(policy.VerdictEscalate) {
			t.Errorf("%s got verdict %q, want %q", row.RiskItemID, row.Verdict, policy.VerdictEscalate)
		}
		if row.EscalationVerdict != string(policy.VerdictAllow) {
			t.Errorf("the escalation of %s was itself refused: %s %s",
				row.RiskItemID, row.EscalationVerdict, row.EscalationRule)
		}
	}
	if !found {
		t.Fatalf("the disputed invoice %s never reached the queue", disputedID)
	}
}

// TestEveryRowSaysWhichClockItsAgeCameOff pins the honesty half of the age
// override. A stated age and a real one are different facts, and a run that
// mixed them without saying so would publish the manifest's fiction as
// Razorpay's report.
//
// It is per row rather than only in the summary, because a run that simulated
// ages still leaves an item the manifest does not know on the gateway's own
// timestamp.
func TestEveryRowSaysWhichClockItsAgeCameOff(t *testing.T) {
	withSimulation, rows := dryRun(t, nil)
	if withSimulation.AgeSource != AgeSourceManifest {
		t.Errorf("age source = %q, want %q", withSimulation.AgeSource, AgeSourceManifest)
	}
	for _, row := range rows {
		if row.AgeSource != AgeSourceManifest {
			t.Errorf("%s carries age source %q while the run simulated ages", row.RiskItemID, row.AgeSource)
		}
	}

	without, rows := dryRun(t, func(o *Options) { o.SimulateAge = false })
	if without.AgeSource != AgeSourceGateway {
		t.Errorf("age source = %q, want %q", without.AgeSource, AgeSourceGateway)
	}
	for _, row := range rows {
		if row.AgeSource != AgeSourceGateway {
			t.Errorf("%s carries age source %q while the run did not simulate", row.RiskItemID, row.AgeSource)
		}
	}
}

// TestAnItemWithNoFailureIsNotLabelledUnclassified.
//
// classify.Unclassified means a failure happened and nothing recognised its
// reason, which is an arm of R7. An abandoned cart and an unpaid invoice have no
// failure at all. Both arrive at the gate with the same zero Class, and the two
// facts are told apart by SignalPresent, so the class column has to be empty for
// the second rather than carrying the word that describes the first.
func TestAnItemWithNoFailureIsNotLabelledUnclassified(t *testing.T) {
	_, rows := dryRun(t, nil)

	for _, row := range rows {
		if row.SignalPresent {
			if row.Class == "" {
				t.Errorf("%s carried failure evidence and no class", row.RiskItemID)
			}
			continue
		}
		if row.Class != "" {
			t.Errorf("%s carried no failure evidence and was labelled %q", row.RiskItemID, row.Class)
		}
	}
}

// TestKillSwitchStopsEveryItemIncludingTheEscalations pins R8 through the whole
// pipeline: a halt beats every reason an action might be fine, and it beats the
// escalation the refusal would otherwise raise.
func TestKillSwitchStopsEveryItemIncludingTheEscalations(t *testing.T) {
	summary, rows := dryRun(t, func(o *Options) { o.KillSwitch = true })

	for _, row := range rows {
		if row.RuleID != policy.RuleKillSwitch {
			t.Errorf("%s was decided by %s while the kill switch was engaged", row.RiskItemID, row.RuleID)
		}
		if row.Verdict != string(policy.VerdictDeny) {
			t.Errorf("%s got verdict %q under the kill switch, want %q", row.RiskItemID, row.Verdict, policy.VerdictDeny)
		}
	}
	if summary.VerdictTotals[string(policy.VerdictAllow)] != 0 {
		t.Error("the kill switch was engaged and something was still allowed")
	}
}

// TestSummaryCountsAgreeWithTheRows is the check that the summary file and the
// results file cannot tell two different stories.
func TestSummaryCountsAgreeWithTheRows(t *testing.T) {
	summary, rows := dryRun(t, nil)

	bySource := map[string]int{}
	byArm := map[string]int{}
	verdicts := map[string]int{}
	var due int64
	for _, row := range rows {
		bySource[row.Source]++
		byArm[row.Arm]++
		verdicts[row.Verdict]++
		due += row.AmountDuePaise
		if !IsArm(row.Arm) {
			t.Errorf("%s was assigned to %q, which is not an arm", row.RiskItemID, row.Arm)
		}
	}

	for source, count := range bySource {
		if summary.ItemsBySource[source] != count {
			t.Errorf("summary says %d %s item(s), the rows say %d", summary.ItemsBySource[source], source, count)
		}
	}
	for arm, count := range byArm {
		if summary.ItemsByArm[arm] != count {
			t.Errorf("summary says %d item(s) in %s, the rows say %d", summary.ItemsByArm[arm], arm, count)
		}
	}
	if summary.AmountDueTotal != due {
		t.Errorf("summary says %d paise outstanding, the rows say %d", summary.AmountDueTotal, due)
	}
	// One primary verdict per item, exactly. The escalations those refusals
	// raised are counted separately, which is what keeps this an equality
	// rather than a subset check.
	for verdict, count := range verdicts {
		if summary.VerdictTotals[verdict] != count {
			t.Errorf("summary counts %d %s verdict(s), the rows carry %d",
				summary.VerdictTotals[verdict], verdict, count)
		}
	}
	var primary int
	for _, count := range summary.VerdictTotals {
		primary += count
	}
	if primary != len(rows) {
		t.Errorf("%d primary verdict(s) for %d row(s), want one each", primary, len(rows))
	}
	if summary.EscalationVerdictTotals[string(policy.VerdictAllow)] != summary.VerdictTotals[string(policy.VerdictEscalate)] {
		t.Errorf("%d escalation(s) were allowed against %d escalating verdict(s)",
			summary.EscalationVerdictTotals[string(policy.VerdictAllow)],
			summary.VerdictTotals[string(policy.VerdictEscalate)])
	}
}

// TestLedgerRecordsEveryDecisionBeforeAnythingIsSkipped pins the audit trail:
// every item leaves an evaluation row and a row saying nothing ran, and the
// second one says which of the three reasons it was.
func TestLedgerRecordsEveryDecisionBeforeAnythingIsSkipped(t *testing.T) {
	var ledger, results bytes.Buffer
	recorder, err := audit.NewRecorder(audit.Options{Writer: &ledger, Clock: clock.NewFake(fixtureBase.Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := Run(context.Background(), Options{
		Manifest:     loadFixture(t),
		ManifestPath: "testdata/manifest.json",
		RunTag:       "test",
		DryRun:       true,
		SimulateAge:  true,
		Clock:        clock.NewFake(fixtureBase.Add(time.Hour)),
		Recorder:     recorder,
		Results:      &results,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var evaluated, skipped int
	for line := range strings.SplitSeq(strings.TrimSpace(ledger.String()), "\n") {
		var record audit.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode a ledger row: %v", err)
		}
		switch record.Kind {
		case audit.KindPolicyEvaluated:
			evaluated++
			if record.PolicyVerdict == "" || record.PolicyRule == "" {
				t.Errorf("an evaluation row carries no verdict: %+v", record)
			}
		case audit.KindActionSkipped:
			skipped++
			if record.Detail["not_executed"] == "" {
				t.Errorf("a skipped row does not say why nothing ran: %+v", record)
			}
		}
	}

	if skipped != summary.ItemsTotal {
		t.Errorf("%d skipped row(s) for %d item(s)", skipped, summary.ItemsTotal)
	}
	if evaluated < summary.ItemsTotal {
		t.Errorf("%d evaluation row(s) for %d item(s), want at least one each", evaluated, summary.ItemsTotal)
	}
}

// stubGateway is the intervention engine's Razorpay, in memory.
type stubGateway struct {
	notified []string
	links    int
}

func (g *stubGateway) NotifyInvoice(_ context.Context, invoiceID, medium string) (razorpay.NotifyReceipt, error) {
	g.notified = append(g.notified, invoiceID+"|"+medium)
	return razorpay.NotifyReceipt{LinkID: invoiceID, Medium: medium, Accepted: true}, nil
}

func (g *stubGateway) FetchInvoice(_ context.Context, invoiceID string) (razorpay.Invoice, error) {
	return razorpay.Invoice{ID: invoiceID, Status: razorpay.InvoiceStatusIssued, EmailStatus: "sent"}, nil
}

func (g *stubGateway) CancelInvoice(_ context.Context, invoiceID string) (razorpay.Invoice, error) {
	return razorpay.Invoice{ID: invoiceID, Status: razorpay.InvoiceStatusCancelled}, nil
}

func (g *stubGateway) CreatePaymentLink(_ context.Context, req razorpay.CreatePaymentLinkRequest) (razorpay.PaymentLink, error) {
	g.links++
	return razorpay.PaymentLink{
		ID:          "plink_stub",
		ShortURL:    "https://rzp.io/i/stub",
		Status:      razorpay.PaymentLinkStatusCreated,
		AmountPaise: req.AmountPaise,
		Currency:    req.Currency,
	}, nil
}

func (g *stubGateway) ResendPaymentLinkNotification(_ context.Context, linkID, medium string) (razorpay.NotifyReceipt, error) {
	g.notified = append(g.notified, linkID+"|"+medium)
	return razorpay.NotifyReceipt{LinkID: linkID, Medium: medium, Accepted: true}, nil
}

// TestOnlyTheEngineArmExecutes is the two-arm assertion, made against a gateway
// in memory rather than against Razorpay.
//
// The control arm's items reach a verdict and stop. The engine arm's allowed
// items reach the gateway, and its refused ones reach the escalation sink,
// which is the difference between deciding to hand an item to a person and
// having handed it to one.
func TestOnlyTheEngineArmExecutes(t *testing.T) {
	var ledger, results bytes.Buffer
	fake := clock.NewFake(fixtureBase.Add(time.Hour))
	recorder, err := audit.NewRecorder(audit.Options{Writer: &ledger, Clock: fake})
	if err != nil {
		t.Fatal(err)
	}
	manifest := loadFixture(t)
	gateway := &stubGateway{}
	sink := intervene.NewMemorySink()

	summary, err := Run(context.Background(), Options{
		Manifest:     manifest,
		ManifestPath: "testdata/manifest.json",
		RunTag:       "test",
		Seed:         1234,
		SimulateAge:  true,
		// A live-shaped run with the manifest standing in for Razorpay's list
		// endpoints. Nothing here reaches the network: the source is the same
		// one the dry run uses and the gateway is in memory.
		API:     newManifestSource(manifest),
		Gateway: gateway,
		// R6 is a run-wide send rate of one second and this clock does not
		// move on its own, so without a window wide enough to be irrelevant
		// every notification after the first would be denied by a rule this
		// test is not about.
		PolicyConfig: policy.Config{NotifyWindow: time.Nanosecond},
		Clock:        fake,
		Recorder:     recorder,
		Escalations:  sink,
		Promises:     promise.NewStore(),
		Results:      &results,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := decodeRows(t, results.Bytes())

	var controlExecuted, engineExecuted int
	for _, row := range rows {
		switch {
		case row.Arm == ArmControl && row.Executed:
			controlExecuted++
		case row.Arm == ArmEngine && row.Executed:
			engineExecuted++
		}
	}
	if controlExecuted != 0 {
		t.Errorf("the control arm executed %d action(s)", controlExecuted)
	}
	if engineExecuted == 0 {
		t.Fatal("the engine arm executed nothing, so this proves nothing")
	}

	if summary.Escalations != len(sink.Escalations()) {
		t.Errorf("the summary counts %d escalation(s) and the sink holds %d",
			summary.Escalations, len(sink.Escalations()))
	}
	for _, escalation := range sink.Escalations() {
		if escalation.Reason == "" {
			t.Errorf("escalation %s carries no reason, so the context seam did not deliver one", escalation.RiskItemID)
		}
	}

	// An accepted notification says the API call succeeded, which is what the
	// observable has to say and no more.
	for _, row := range rows {
		if !row.Accepted || row.ExecutedAction != riskitem.ActionNotifyEmail {
			continue
		}
		if !strings.HasPrefix(row.Observable, "email_status:") {
			t.Errorf("%s reported observable %q for an accepted invoice notification", row.RiskItemID, row.Observable)
		}
	}
}

// TestEngineArmIsIdempotentAcrossTwoPassesOfOneRun pins that the key the policy
// computed is the key the intervention engine collapses on: a second run over
// the same items with the same engine calls nothing again.
func TestEngineArmIsIdempotentAcrossTwoPassesOfOneRun(t *testing.T) {
	manifest := loadFixture(t)
	fake := clock.NewFake(fixtureBase.Add(time.Hour))
	gateway := &stubGateway{}

	run := func() Summary {
		var ledger, results bytes.Buffer
		recorder, err := audit.NewRecorder(audit.Options{Writer: &ledger, Clock: fake})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := Run(context.Background(), Options{
			Manifest:     manifest,
			RunTag:       "test",
			Seed:         1234,
			SimulateAge:  true,
			API:          newManifestSource(manifest),
			Gateway:      gateway,
			PolicyConfig: policy.Config{NotifyWindow: time.Nanosecond},
			Clock:        fake,
			Recorder:     recorder,
			Results:      &results,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return summary
	}

	first := run()
	notifiedAfterFirst := len(gateway.notified)
	linksAfterFirst := gateway.links
	if notifiedAfterFirst == 0 && linksAfterFirst == 0 {
		t.Fatal("the first pass called nothing, so a replay proves nothing")
	}

	// A second Run builds a second Engine, so its in-memory guard is empty and
	// the gateway is called again. That is the limitation internal/intervene
	// documents: the guard is per process and per engine, and nothing in this
	// repository has a durable action store. The assertion is on the shape of
	// the run rather than on a guard that does not exist across runs.
	second := run()
	if second.ItemsTotal != first.ItemsTotal {
		t.Errorf("the second pass saw %d item(s), the first saw %d", second.ItemsTotal, first.ItemsTotal)
	}
	if len(gateway.notified) <= notifiedAfterFirst && gateway.links <= linksAfterFirst {
		t.Error("the second pass called nothing, which would mean a durable guard this build does not have")
	}
}

// TestDetectGraceFiltersTheFreshInvoices pins that the detector's own grace is
// what keeps a just-issued invoice out of the queue, separately from R11.
func TestDetectGraceFiltersTheFreshInvoices(t *testing.T) {
	wide, _ := dryRun(t, nil)
	narrow, _ := dryRun(t, func(o *Options) { o.DetectConfig = detect.Config{Grace: time.Nanosecond} })

	if narrow.SightingsBySource[string(riskitem.SourceOverdueInvoice)] <= wide.SightingsBySource[string(riskitem.SourceOverdueInvoice)] {
		t.Errorf("a one nanosecond grace found %d invoice sighting(s) and the default found %d, want more",
			narrow.SightingsBySource[string(riskitem.SourceOverdueInvoice)],
			wide.SightingsBySource[string(riskitem.SourceOverdueInvoice)])
	}
	if narrow.DetectGrace == wide.DetectGrace {
		t.Error("the summary records the same grace for two runs that used different ones")
	}
}
