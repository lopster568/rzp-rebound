package intervene

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ErrNoEscalationSink is returned when an Engine is built with nowhere to put
// an escalation.
var ErrNoEscalationSink = errors.New("intervene: needs an EscalationSink")

// Escalation is one risk item handed to a person.
//
// It carries a timestamp and the identity of the debt because an escalation
// that is only a counter cannot be worked: a reviewer reading a run needs to
// know which item, for how much, and when. No contact detail is on it. The
// item's customer fields are in the detector output and in Razorpay, and
// copying an address into a queue file would put one somewhere the audit
// redactor does not reach.
type Escalation struct {
	// RiskItemID is the per-detector sighting id.
	RiskItemID string `json:"risk_item_id"`
	// DedupeKey is what the queue collapses on, so two sightings of one debt
	// escalate under one key.
	DedupeKey string `json:"dedupe_key"`
	// RootOrderID is the order behind the debt, empty when there is none.
	RootOrderID string `json:"root_order_id,omitempty"`
	// Source is the detector that produced the sighting.
	Source string `json:"source"`
	// AmountDuePaise is what is outstanding, in paise.
	AmountDuePaise int64 `json:"amount_due_paise"`
	// Currency is the ISO code Razorpay reported.
	Currency string `json:"currency,omitempty"`
	// Reason is why the item was escalated, from the reason on the context or
	// a generated one.
	Reason string `json:"reason"`
	// RaisedAt is when the escalation was written.
	RaisedAt time.Time `json:"raised_at"`
}

// EscalationSink is where an escalation goes. Escalate returns an error when
// the record could not be written, and the intervention that raised it reports
// that as a refusal rather than as a completed escalation.
type EscalationSink interface {
	Escalate(ctx context.Context, esc Escalation) error
}

// WriterSink writes one JSON line per escalation, which makes the queue a file
// a person can open and a later run can read back. It is safe for concurrent
// use.
type WriterSink struct {
	mu sync.Mutex
	w  io.Writer
}

var _ EscalationSink = (*WriterSink)(nil)

// NewWriterSink returns a sink that appends to w.
func NewWriterSink(w io.Writer) (*WriterSink, error) {
	if w == nil {
		return nil, ErrNoEscalationSink
	}
	return &WriterSink{w: w}, nil
}

// Escalate appends esc as one JSON line.
//
// A failed write is an error rather than a swallowed one. An escalation that
// was not written is an item nobody will look at, and reporting it as raised
// would be the one claim this package must not make.
func (s *WriterSink) Escalate(_ context.Context, esc Escalation) error {
	encoded, err := json.Marshal(esc)
	if err != nil {
		return fmt.Errorf("intervene: encode the escalation for %s: %w", esc.RiskItemID, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("intervene: write the escalation for %s: %w", esc.RiskItemID, err)
	}
	return nil
}

// MemorySink keeps escalations in memory. It is what this package tests
// against and what a caller uses when the queue is drained in the same
// process. Records do not survive it.
type MemorySink struct {
	mu    sync.Mutex
	items []Escalation
}

var _ EscalationSink = (*MemorySink)(nil)

// NewMemorySink returns an empty sink.
func NewMemorySink() *MemorySink { return &MemorySink{} }

// Escalate appends esc.
func (s *MemorySink) Escalate(_ context.Context, esc Escalation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, esc)
	return nil
}

// Escalations returns a copy of what has been raised, oldest first.
func (s *MemorySink) Escalations() []Escalation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Escalation, len(s.items))
	copy(out, s.items)
	return out
}
