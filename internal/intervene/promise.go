package intervene

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultPromiseHold is how long a logged promise holds an item off the next
// sweep when Options.PromiseHold is not set.
//
// It is a configured choice and not a measurement. Three days covers a weekend
// without a promise parking a debt indefinitely, and nothing in this repository
// has observed how long a customer who says they will pay actually takes. A
// caller with its own number sets Options.PromiseHold.
const DefaultPromiseHold = 72 * time.Hour

// ErrNoPromiseLedger is returned when an Engine is built with nowhere to write
// a promise.
var ErrNoPromiseLedger = errors.New("intervene: needs a PromiseLedger")

// PromiseRecord is one promise to pay.
//
// The field set is the seam with internal/promise: it is that package's Record
// field for field, so a caller wiring the two together writes a conversion and
// not a translation. Struct tags do not take part in a Go conversion, so the
// two types stay convertible even if the ledger tags its own for JSON.
//
// This package holds its own copy rather than importing the ledger, so the
// intervention engine compiles, tests, and ships without it. internal/promise
// landed on 2026-09-05 with the same four fields in the same order, and the
// conversion was compiled against it on that date. The adapter is:
//
//	func (a adapter) Log(_ context.Context, rec intervene.PromiseRecord) error {
//		return a.inner.Log(promise.Record(rec))
//	}
//
// promise.Store.Log takes no context, which is why the adapter drops it.
//
// Nothing enforces the convertibility from inside this package, because
// enforcing it means importing the package this type exists to avoid. A field
// reordered on either side breaks the adapter at the wiring site, which is the
// place that already imports both and the right place for the assertion.
type PromiseRecord struct {
	// RiskItemID is riskitem.RiskItem.ID, the per-detector sighting.
	RiskItemID string
	// PromisedAtUnix is the second the promise was recorded.
	PromisedAtUnix int64
	// HoldUntilUnix is the second the hold expires. It is PromisedAtUnix plus
	// the configured hold.
	HoldUntilUnix int64
	// Note is why the promise was logged, from the reason on the context or a
	// generated one.
	Note string
}

// PromiseLedger is the slice of the promise ledger this package needs.
//
// It is Log and nothing else. ActiveHold and Breached are read by the policy
// gate, which decides whether to act; this package only writes, so widening
// the interface here would make the intervention engine depend on calls it
// never makes.
type PromiseLedger interface {
	Log(ctx context.Context, rec PromiseRecord) error
}

// MemoryPromiseLedger is a PromiseLedger that keeps records in memory. It is
// what this package tests against and what a caller uses before internal/promise
// is wired in. Records do not survive the process.
type MemoryPromiseLedger struct {
	mu      sync.Mutex
	records []PromiseRecord
}

var _ PromiseLedger = (*MemoryPromiseLedger)(nil)

// NewMemoryPromiseLedger returns an empty ledger.
func NewMemoryPromiseLedger() *MemoryPromiseLedger { return &MemoryPromiseLedger{} }

// Log appends rec.
func (l *MemoryPromiseLedger) Log(_ context.Context, rec PromiseRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
	return nil
}

// Records returns a copy of what has been logged, oldest first.
func (l *MemoryPromiseLedger) Records() []PromiseRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]PromiseRecord, len(l.records))
	copy(out, l.records)
	return out
}
