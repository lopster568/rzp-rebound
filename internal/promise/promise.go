package promise

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Record is one promise to pay, logged against one risk item.
//
// The two instants are Unix seconds rather than time.Time because a Record is
// an audit row before it is anything else: it round-trips through JSON into
// the trail and back, and a Unix second survives that with no zone, no
// monotonic reading, and no RFC 3339 parse to get wrong. The accessors below
// hand back time.Time for the code that reasons in durations.
//
// PromisedAtUnix is when the promise was made, which is not the same as when
// the hold starts: a promise logged in the morning to pay by Friday holds from
// the moment it is logged. HoldUntilUnix is when the merchant said it would
// wait until, and it is exclusive: at exactly HoldUntilUnix the hold is over.
//
// Nothing here observes a payment. A Record says a customer said something. It
// does not say money moved, and Breached below means only that the window went
// by, not that the customer defaulted: the caller that knows whether the debt
// is still outstanding decides that.
type Record struct {
	// RiskItemID is riskitem.RiskItem.ID, the sighting this promise is
	// against.
	RiskItemID string `json:"risk_item_id"`
	// PromisedAtUnix is the Unix second the promise was logged.
	PromisedAtUnix int64 `json:"promised_at_unix"`
	// HoldUntilUnix is the Unix second the hold expires, exclusive.
	HoldUntilUnix int64 `json:"hold_until_unix"`
	// Note is the free text a human attached, such as what the customer
	// actually said. It is optional and it is never parsed.
	Note string `json:"note,omitempty"`
}

// New builds a Record from instants and the duration of the hold.
func New(riskItemID string, promisedAt time.Time, hold time.Duration, note string) Record {
	return Record{
		RiskItemID:     riskItemID,
		PromisedAtUnix: promisedAt.Unix(),
		HoldUntilUnix:  promisedAt.Add(hold).Unix(),
		Note:           note,
	}
}

// PromisedAt is when the promise was logged.
func (r Record) PromisedAt() time.Time { return time.Unix(r.PromisedAtUnix, 0).UTC() }

// HoldUntil is when the hold expires, exclusive.
func (r Record) HoldUntil() time.Time { return time.Unix(r.HoldUntilUnix, 0).UTC() }

// Validate reports what is wrong with r, or nil.
//
// A hold that ends at or before the promise was made is rejected rather than
// stored as an already-expired row, because the only way to write one is a bug
// in the caller and a ledger that accepts it hides the bug behind a hold that
// never holds.
func (r Record) Validate() error {
	switch {
	case r.RiskItemID == "":
		return fmt.Errorf("promise: a record with no risk item id cannot be held against anything")
	case r.PromisedAtUnix <= 0:
		return fmt.Errorf("promise: %s has no promised-at instant", r.RiskItemID)
	case r.HoldUntilUnix <= r.PromisedAtUnix:
		return fmt.Errorf("promise: %s holds until %d, which is not after the promise at %d",
			r.RiskItemID, r.HoldUntilUnix, r.PromisedAtUnix)
	default:
		return nil
	}
}

// ActiveAt reports whether the hold covers now.
//
// The window is closed at the start and open at the end: the second the
// promise was made is inside it and HoldUntil is not. An instant before the
// promise was made is outside it, which matters when a caller replays a trail
// and asks what was true at an earlier point.
func (r Record) ActiveAt(now time.Time) bool {
	sec := now.Unix()
	return sec >= r.PromisedAtUnix && sec < r.HoldUntilUnix
}

// BreachedAt reports whether the hold has run out by now.
//
// It is the complement of ActiveAt on the far side only. It says the window
// went by. It does not say the customer failed to pay, because this package
// sees no payments: a caller that has read the item's amount_due decides that.
func (r Record) BreachedAt(now time.Time) bool { return now.Unix() >= r.HoldUntilUnix }

// Store is an in-memory promise ledger, keyed by risk item id.
//
// It keeps every record rather than the latest one. A customer who promises
// twice has made two promises, and the first one is the row that shows the
// second was a renegotiation. Log appends; nothing here deletes.
//
// The zero Store is not usable. Build one with NewStore.
type Store struct {
	mu      sync.RWMutex
	byItem  map[string][]Record
	ordered []Record
}

// NewStore returns an empty ledger.
func NewStore() *Store {
	return &Store{byItem: make(map[string][]Record)}
}

// Log appends a promise. It returns the record's validation error and stores
// nothing when there is one.
func (s *Store) Log(r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byItem == nil {
		s.byItem = make(map[string][]Record)
	}
	s.byItem[r.RiskItemID] = append(s.byItem[r.RiskItemID], r)
	s.ordered = append(s.ordered, r)
	return nil
}

// ActiveHold returns the promise covering now for this item, if there is one.
//
// When two promises overlap, the one that ends latest wins, because that is
// the one the merchant is bound by. The bool is false when no hold covers now,
// which is the ordinary case and not an error.
func (s *Store) ActiveHold(riskItemID string, now time.Time) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestLocked(riskItemID, func(r Record) bool { return r.ActiveAt(now) })
}

// latestLocked returns the record matching want whose hold ends latest. The
// caller holds the lock.
//
// Both queries want the same thing for the same reason: when two promises
// overlap, the merchant is bound by the one that ends last, and when two have
// expired, the one that ended last is the one it actually waited on.
func (s *Store) latestLocked(riskItemID string, want func(Record) bool) (Record, bool) {
	var best Record
	var found bool
	for _, r := range s.byItem[riskItemID] {
		if !want(r) {
			continue
		}
		if !found || r.HoldUntilUnix > best.HoldUntilUnix {
			best, found = r, true
		}
	}
	return best, found
}

// Breached returns the promise this item broke, if it broke one.
//
// A live hold beats an expired one: an item whose customer promised again is
// not in breach while the second promise is running, whatever happened to the
// first. With no hold running, the record that ended latest is the one
// returned, because that is the promise the merchant actually waited on.
//
// The bool is false for an item with no promises at all, which is not the same
// as an item that kept one. This ledger cannot tell those apart, and a caller
// that needs to know reads the item's amount_due.
//
// The two questions are answered under one lock rather than by calling
// ActiveHold and then reading again. Two acquisitions would let a Log land
// between them, and the answer would then describe a ledger that never existed
// in that state.
func (s *Store) Breached(riskItemID string, now time.Time) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, active := s.latestLocked(riskItemID, func(r Record) bool { return r.ActiveAt(now) }); active {
		return Record{}, false
	}
	return s.latestLocked(riskItemID, func(r Record) bool { return r.BreachedAt(now) })
}

// Records returns every promise logged against one item, in the order they
// were logged. The slice is a copy.
func (s *Store) Records(riskItemID string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.byItem[riskItemID])
}

// Len is how many promises the ledger holds.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ordered)
}

// Snapshot returns every record, in the order they were logged. The slice is a
// copy, so a caller writing to it does not edit the ledger.
func (s *Store) Snapshot() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.ordered)
}

// MarshalJSON writes the ledger as the array of records it is, so that a
// promise trail in an audit file is a list a person can read rather than a map
// of item ids to lists that has to be flattened to be scanned.
func (s *Store) MarshalJSON() ([]byte, error) {
	snapshot := s.Snapshot()
	if snapshot == nil {
		snapshot = []Record{}
	}
	return json.Marshal(snapshot)
}

// UnmarshalJSON rebuilds a ledger from the array MarshalJSON wrote.
//
// It validates every row and fails the whole load on the first bad one rather
// than skipping it, because a promise trail with a row quietly dropped is a
// trail that says a merchant contacted somebody it was holding off.
func (s *Store) UnmarshalJSON(data []byte) error {
	var rows []Record
	if err := json.Unmarshal(data, &rows); err != nil {
		return err
	}

	rebuilt := NewStore()
	for i, r := range rows {
		if err := rebuilt.Log(r); err != nil {
			return fmt.Errorf("promise: record %d in the trail is not loadable: %w", i, err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byItem, s.ordered = rebuilt.byItem, rebuilt.ordered
	return nil
}
