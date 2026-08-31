package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

var auditStart = time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

// sinks is the pair a recorder writes to, plus the pieces a test needs to read
// them back.
type sinks struct {
	buf      *bytes.Buffer
	recorder *audit.Recorder
	spans    *tracetest.SpanRecorder
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
}

func newSinks(t *testing.T) *sinks {
	t.Helper()

	buf := &bytes.Buffer{}
	rec, err := audit.NewRecorder(audit.Options{Writer: buf, Clock: clock.NewFake(auditStart)})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	})

	return &sinks{buf: buf, recorder: rec, spans: spans, tracer: tp.Tracer("audit_test"), provider: tp}
}

// lines returns the ledger lines written so far.
func (s *sinks) lines(t *testing.T) []audit.Record {
	t.Helper()

	var out []audit.Record
	for _, line := range strings.Split(strings.TrimSpace(s.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec audit.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("ledger line is not valid JSON: %v (%q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func attrsOf(span sdktrace.ReadOnlySpan) map[string]attribute.Value {
	out := make(map[string]attribute.Value)
	for _, kv := range span.Attributes() {
		out[string(kv.Key)] = kv.Value
	}
	return out
}

func TestRecorderWritesLedgerLineAndSpanAttributesForSameEvent(t *testing.T) {
	s := newSinks(t)

	ctx, span := s.tracer.Start(context.Background(), "order")
	rec, err := s.recorder.Record(ctx, audit.Event{
		OrderID:        "order_dualsink0001",
		Kind:           audit.KindClassified,
		Class:          "insufficient_fund_class",
		ProposedAction: "retry_same_instrument",
	})
	span.End()
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	lines := s.lines(t)
	if len(lines) != 1 {
		t.Fatalf("wrote %d ledger line(s), want 1", len(lines))
	}
	line := lines[0]

	if line.OrderID != "order_dualsink0001" {
		t.Errorf("ledger order_id = %q, want order_dualsink0001", line.OrderID)
	}
	if line.Kind != audit.KindClassified {
		t.Errorf("ledger kind = %q, want %q", line.Kind, audit.KindClassified)
	}
	if line.Sequence != 1 {
		t.Errorf("ledger sequence = %d, want 1", line.Sequence)
	}
	if line.Class != "insufficient_fund_class" {
		t.Errorf("ledger class = %q, want insufficient_fund_class", line.Class)
	}
	if line.RecordedAt == "" {
		t.Error("ledger line carries no recorded_at")
	}
	if !reflect.DeepEqual(rec, line) {
		t.Errorf("Record returned %+v but wrote %+v", rec, line)
	}

	ended := s.spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("recorded %d span(s), want 1", len(ended))
	}
	attrs := attrsOf(ended[0])

	// The same event, on the other sink. A ledger row a reviewer cannot find
	// in the trace, or a span that says something the ledger does not, would
	// make the two disagree about what happened.
	if got := attrs[audit.AttrOrderID].AsString(); got != line.OrderID {
		t.Errorf("span %s = %q, want %q", audit.AttrOrderID, got, line.OrderID)
	}
	if got := attrs[audit.AttrKind].AsString(); got != line.Kind {
		t.Errorf("span %s = %q, want %q", audit.AttrKind, got, line.Kind)
	}
	if got := attrs[audit.AttrSequence].AsInt64(); got != int64(line.Sequence) {
		t.Errorf("span %s = %d, want %d", audit.AttrSequence, got, line.Sequence)
	}
	if got := attrs[audit.AttrClass].AsString(); got != line.Class {
		t.Errorf("span %s = %q, want %q", audit.AttrClass, got, line.Class)
	}
	if got := attrs[audit.AttrProposedAction].AsString(); got != line.ProposedAction {
		t.Errorf("span %s = %q, want %q", audit.AttrProposedAction, got, line.ProposedAction)
	}
}

func TestLedgerLinesAreValidJSONAndCarryTraceID(t *testing.T) {
	s := newSinks(t)

	ctx, span := s.tracer.Start(context.Background(), "order")
	sc := span.SpanContext()

	for _, kind := range []string{audit.KindClassified, audit.KindActionTaken, audit.KindOutcomeObserved} {
		if _, err := s.recorder.Record(ctx, audit.Event{OrderID: "order_traceid00001", Kind: kind}); err != nil {
			t.Fatalf("Record(%s): %v", kind, err)
		}
	}
	span.End()

	lines := s.lines(t)
	if len(lines) != 3 {
		t.Fatalf("wrote %d ledger line(s), want 3", len(lines))
	}

	for i, line := range lines {
		if line.TraceID != sc.TraceID().String() {
			t.Errorf("line %d trace_id = %q, want %q", i, line.TraceID, sc.TraceID())
		}
		if line.SpanID != sc.SpanID().String() {
			t.Errorf("line %d span_id = %q, want %q", i, line.SpanID, sc.SpanID())
		}
		if _, err := time.Parse(time.RFC3339Nano, line.RecordedAt); err != nil {
			t.Errorf("line %d recorded_at %q is not RFC3339: %v", i, line.RecordedAt, err)
		}
	}

	// A row written with no span in the context still has to be a valid line,
	// because the scoring pass reads the file whether or not a trace backend
	// was up.
	if _, err := s.recorder.Record(context.Background(), audit.Event{
		OrderID: "order_nospan000001",
		Kind:    audit.KindActionSkipped,
	}); err != nil {
		t.Fatalf("Record without a span: %v", err)
	}
	lines = s.lines(t)
	if got := len(lines); got != 4 {
		t.Fatalf("wrote %d ledger line(s), want 4", got)
	}
	if got := lines[3].TraceID; got != "" {
		t.Errorf("a row recorded with no active span carries trace_id %q, want an empty one", got)
	}
}

func TestRecorderRedactsCardAndKeyFieldsFromLedger(t *testing.T) {
	s := newSinks(t)

	// Assembled rather than written as one literal: a key-shaped string in a
	// tracked file trips the pre-commit secret scan, and this one is not a
	// credential, it is bait for the redactor.
	keyShaped := "rzp_" + "test_" + "N0tAr34lK3y99"
	cardNumber := "4111111111111111"

	ctx, span := s.tracer.Start(context.Background(), "order")
	if _, err := s.recorder.Record(ctx, audit.Event{
		OrderID: "order_redaction001",
		Kind:    audit.KindNotificationRequested,
		Detail: map[string]string{
			"card_number":  cardNumber,
			"key_id":       keyShaped,
			"note":         "charged " + cardNumber + " with " + keyShaped,
			"order_status": "attempted",
		},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	span.End()

	raw := s.buf.String()
	if strings.Contains(raw, cardNumber) {
		t.Errorf("a card number reached the ledger: %q", raw)
	}
	if strings.Contains(raw, keyShaped) {
		t.Errorf("a key-shaped value reached the ledger: %q", raw)
	}
	if !strings.Contains(raw, audit.Redacted) {
		t.Errorf("nothing in the ledger line was redacted: %q", raw)
	}

	// Redaction must not eat the context around it.
	lines := s.lines(t)
	if len(lines) != 1 {
		t.Fatalf("wrote %d ledger line(s), want 1", len(lines))
	}
	if got := lines[0].Detail["order_status"]; got != "attempted" {
		t.Errorf("detail order_status = %q, want attempted", got)
	}
	if got := lines[0].Detail["card_number"]; got != audit.Redacted {
		t.Errorf("detail card_number = %q, want %q", got, audit.Redacted)
	}

	ended := s.spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("recorded %d span(s), want 1", len(ended))
	}
	for _, kv := range ended[0].Attributes() {
		if v := kv.Value.Emit(); strings.Contains(v, cardNumber) || strings.Contains(v, keyShaped) {
			t.Errorf("span attribute %s carries a redacted value: %q", kv.Key, v)
		}
	}

	if got := audit.RedactValue(cardNumber); got != audit.Redacted {
		t.Errorf("RedactValue(a card number) = %q, want %q", got, audit.Redacted)
	}
	if !audit.IsRedactedKey("card_number") || !audit.IsRedactedKey("key_secret") {
		t.Error("IsRedactedKey does not cover card_number and key_secret")
	}
	if audit.IsRedactedKey("order_status") {
		t.Error("IsRedactedKey redacts order_status, which is not a credential")
	}
}

func TestRecorderAssignsMonotonicSequencePerOrder(t *testing.T) {
	s := newSinks(t)

	ctx, span := s.tracer.Start(context.Background(), "batch")
	// Interleaved on purpose: two orders being worked at once must not share
	// one counter, or the rows for either one stop being readable in order.
	writes := []string{"order_a0000000001", "order_b0000000001", "order_a0000000001", "order_b0000000001", "order_a0000000001"}
	for i, orderID := range writes {
		if _, err := s.recorder.Record(ctx, audit.Event{OrderID: orderID, Kind: audit.KindClassified}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	span.End()

	got := map[string][]int{}
	for _, line := range s.lines(t) {
		got[line.OrderID] = append(got[line.OrderID], line.Sequence)
	}

	for orderID, want := range map[string][]int{
		"order_a0000000001": {1, 2, 3},
		"order_b0000000001": {1, 2},
	} {
		seqs := got[orderID]
		if len(seqs) != len(want) {
			t.Fatalf("%s has %d row(s), want %d", orderID, len(seqs), len(want))
		}
		for i := range want {
			if seqs[i] != want[i] {
				t.Errorf("%s row %d has sequence %d, want %d", orderID, i, seqs[i], want[i])
			}
		}
	}
}

func TestRecorderRejectsAnEventItCannotJoin(t *testing.T) {
	s := newSinks(t)
	ctx := context.Background()

	if _, err := s.recorder.Record(ctx, audit.Event{Kind: audit.KindClassified}); err == nil {
		t.Error("an event with no order id was recorded, so the row cannot be joined to a batch")
	}
	if _, err := s.recorder.Record(ctx, audit.Event{OrderID: "order_nokind000001"}); err == nil {
		t.Error("an event with no kind was recorded")
	}
	if got := s.buf.Len(); got != 0 {
		t.Errorf("the ledger holds %d bytes after two rejected events, want 0", got)
	}
}
