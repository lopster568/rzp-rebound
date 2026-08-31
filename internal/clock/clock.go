package clock

import (
	"sync"
	"time"
)

// Clock is the time seam. Code that schedules a retry takes a Clock instead of
// calling time.Now, so a test can move time forward without sleeping.
type Clock interface {
	// Now returns the instant the clock currently reads.
	Now() time.Time
}

// Real returns a Clock backed by the wall clock.
func Real() Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// FakeClock is a Clock whose reading only changes when Advance is called.
// It is safe for concurrent use.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a FakeClock reading start.
func NewFake(start time.Time) *FakeClock { return &FakeClock{now: start} }

// Now returns the instant the fake currently reads.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the fake forward by d. A negative d moves it back, which is
// how a test reaches a deadline that has already passed.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
