package promise_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/promise"
)

// start is the instant every case here is measured from. A fixed one keeps a
// failure message readable.
var start = time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)

const item = "ri_abcdef012345"

func TestNewFillsBothInstantsFromTheHold(t *testing.T) {
	r := promise.New(item, start, 48*time.Hour, "said they would pay on Monday")

	if r.RiskItemID != item {
		t.Errorf("RiskItemID = %q, want %q", r.RiskItemID, item)
	}
	if got := r.PromisedAt(); !got.Equal(start) {
		t.Errorf("PromisedAt = %s, want %s", got, start)
	}
	if got, want := r.HoldUntil(), start.Add(48*time.Hour); !got.Equal(want) {
		t.Errorf("HoldUntil = %s, want %s", got, want)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("a record built by New did not validate: %v", err)
	}
}

// TestHoldWindowIsClosedAtTheStartAndOpenAtTheEnd is the whole of the hold. An
// off-by-one at either edge either contacts a customer inside a window the
// merchant promised to wait out, or holds off forever.
func TestHoldWindowIsClosedAtTheStartAndOpenAtTheEnd(t *testing.T) {
	r := promise.New(item, start, time.Hour, "")

	for _, tc := range []struct {
		name       string
		now        time.Time
		wantActive bool
		wantBreach bool
	}{
		{"before the promise was made", start.Add(-time.Second), false, false},
		{"the second it was made", start, true, false},
		{"inside the hold", start.Add(30 * time.Minute), true, false},
		{"the last second inside", start.Add(time.Hour - time.Second), true, false},
		{"exactly at the hold's end", start.Add(time.Hour), false, true},
		{"well past it", start.Add(72 * time.Hour), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.ActiveAt(tc.now); got != tc.wantActive {
				t.Errorf("ActiveAt(%s) = %t, want %t", tc.now, got, tc.wantActive)
			}
			if got := r.BreachedAt(tc.now); got != tc.wantBreach {
				t.Errorf("BreachedAt(%s) = %t, want %t", tc.now, got, tc.wantBreach)
			}
		})
	}
}

func TestValidateRejectsARecordThatCannotHold(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    promise.Record
	}{
		{"no risk item id", promise.Record{PromisedAtUnix: start.Unix(), HoldUntilUnix: start.Unix() + 60}},
		{"no promised-at instant", promise.Record{RiskItemID: item, HoldUntilUnix: start.Unix()}},
		{"a hold that ends before it starts", promise.Record{RiskItemID: item, PromisedAtUnix: start.Unix(), HoldUntilUnix: start.Unix() - 60}},
		{"a zero-length hold", promise.Record{RiskItemID: item, PromisedAtUnix: start.Unix(), HoldUntilUnix: start.Unix()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.r.Validate(); err == nil {
				t.Errorf("%+v validated", tc.r)
			}
			s := promise.NewStore()
			if err := s.Log(tc.r); err == nil {
				t.Error("the store logged it anyway")
			}
			if s.Len() != 0 {
				t.Errorf("the store holds %d records after a refused log", s.Len())
			}
		})
	}
}

func TestActiveHoldFindsTheLiveWindowAndNothingElse(t *testing.T) {
	s := promise.NewStore()
	if err := s.Log(promise.New(item, start, 2*time.Hour, "first")); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.ActiveHold(item, start.Add(time.Hour)); !ok {
		t.Error("no active hold an hour into a two hour promise")
	}
	if _, ok := s.ActiveHold(item, start.Add(3*time.Hour)); ok {
		t.Error("an active hold an hour after the promise expired")
	}
	if _, ok := s.ActiveHold("ri_someone_else", start.Add(time.Hour)); ok {
		t.Error("a promise on one item held off contact on another")
	}
	if _, ok := s.ActiveHold(item, start.Add(-time.Hour)); ok {
		t.Error("a promise held before it was made")
	}
}

// TestASecondPromiseRenegotiatesTheFirst is what the ledger keeping every row
// is for. A customer who promises again while the first hold is running has not
// breached anything, and the merchant is bound by the later date.
func TestASecondPromiseRenegotiatesTheFirst(t *testing.T) {
	s := promise.NewStore()
	if err := s.Log(promise.New(item, start, time.Hour, "pay by noon")); err != nil {
		t.Fatal(err)
	}
	if err := s.Log(promise.New(item, start.Add(30*time.Minute), 4*time.Hour, "make that this evening")); err != nil {
		t.Fatal(err)
	}

	// Two hours in, the first promise has expired and the second has not. The
	// item is on hold, not in breach.
	now := start.Add(2 * time.Hour)
	held, ok := s.ActiveHold(item, now)
	if !ok {
		t.Fatal("no active hold while the second promise is running")
	}
	if got, want := held.HoldUntil(), start.Add(4*time.Hour+30*time.Minute); !got.Equal(want) {
		t.Errorf("the active hold ends at %s, want the later promise's %s", got, want)
	}
	if _, breached := s.Breached(item, now); breached {
		t.Error("an item with a live second promise reported as breached")
	}

	// Past both, the breach is the later promise, because that is the one the
	// merchant actually waited on.
	now = start.Add(6 * time.Hour)
	broken, ok := s.Breached(item, now)
	if !ok {
		t.Fatal("no breach after both holds ran out")
	}
	if got, want := broken.HoldUntil(), start.Add(4*time.Hour+30*time.Minute); !got.Equal(want) {
		t.Errorf("the breached record ends at %s, want the later promise's %s", got, want)
	}

	if got := s.Records(item); len(got) != 2 {
		t.Errorf("the ledger kept %d records, want both promises", len(got))
	}
}

func TestBreachedIsFalseForAnItemWithNoPromises(t *testing.T) {
	s := promise.NewStore()
	if _, ok := s.Breached("ri_never_promised", start); ok {
		t.Error("an item that never promised anything reported as breached")
	}
	if _, ok := s.ActiveHold("ri_never_promised", start); ok {
		t.Error("an item that never promised anything reported as on hold")
	}
}

func TestRecordsAndSnapshotAreCopies(t *testing.T) {
	s := promise.NewStore()
	if err := s.Log(promise.New(item, start, time.Hour, "note")); err != nil {
		t.Fatal(err)
	}

	got := s.Records(item)
	got[0].Note = "edited through the returned slice"
	if s.Records(item)[0].Note != "note" {
		t.Error("writing to the slice Records returned edited the ledger")
	}

	snap := s.Snapshot()
	snap[0].RiskItemID = "ri_rewritten"
	if s.Snapshot()[0].RiskItemID != item {
		t.Error("writing to the slice Snapshot returned edited the ledger")
	}
}

func TestStoreRoundTripsThroughJSON(t *testing.T) {
	s := promise.NewStore()
	for _, r := range []promise.Record{
		promise.New(item, start, time.Hour, "pay by noon"),
		promise.New("ri_second_item", start.Add(time.Minute), 72*time.Hour, ""),
	} {
		if err := s.Log(r); err != nil {
			t.Fatal(err)
		}
	}

	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The trail is an array of rows a person can scan, not a map keyed by item.
	if encoded[0] != '[' {
		t.Errorf("the ledger encoded as %c..., want a JSON array", encoded[0])
	}

	loaded := promise.NewStore()
	if err := json.Unmarshal(encoded, loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got, want := loaded.Len(), s.Len(); got != want {
		t.Fatalf("the reloaded ledger holds %d records, want %d", got, want)
	}
	for i, r := range loaded.Snapshot() {
		if r != s.Snapshot()[i] {
			t.Errorf("record %d came back as %+v, want %+v", i, r, s.Snapshot()[i])
		}
	}

	// And the holds still answer the same questions after the round trip.
	if _, ok := loaded.ActiveHold(item, start.Add(30*time.Minute)); !ok {
		t.Error("a hold that was active before the round trip is not active after it")
	}
}

func TestEmptyStoreEncodesAsAnEmptyArray(t *testing.T) {
	encoded, err := json.Marshal(promise.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != "[]" {
		t.Errorf("an empty ledger encoded as %s, want []", got)
	}
}

// TestUnmarshalFailsTheWholeLoadOnABadRow covers the thing that would be worst
// to do quietly. A trail with a row skipped is a trail that says the merchant
// contacted somebody it had promised to wait for.
func TestUnmarshalFailsTheWholeLoadOnABadRow(t *testing.T) {
	const trail = `[
	  {"risk_item_id":"ri_ok","promised_at_unix":1000,"hold_until_unix":2000},
	  {"risk_item_id":"","promised_at_unix":1000,"hold_until_unix":2000}
	]`

	loaded := promise.NewStore()
	if err := json.Unmarshal([]byte(trail), loaded); err == nil {
		t.Fatal("a trail with an unloadable row loaded without an error")
	}
	if loaded.Len() != 0 {
		t.Errorf("a failed load left %d records behind", loaded.Len())
	}
}

func TestStoreIsSafeForConcurrentUse(t *testing.T) {
	s := promise.NewStore()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := range 200 {
			_ = s.Log(promise.New(item, start.Add(time.Duration(i)*time.Second), time.Hour, ""))
		}
	}()
	for range 200 {
		s.ActiveHold(item, start)
		s.Breached(item, start.Add(48*time.Hour))
		s.Snapshot()
	}
	<-done

	if got := s.Len(); got != 200 {
		t.Errorf("the ledger holds %d records, want 200", got)
	}
}
