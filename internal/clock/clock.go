package clock

import "time"

// Clock is the time seam. Code that schedules a retry takes a Clock instead of
// calling time.Now, so a test can move time forward without sleeping.
type Clock interface {
	// Now returns the instant the clock currently reads.
	Now() time.Time
}

// Real returns a Clock backed by the wall clock.
func Real() Clock { return nil }

type realClock struct{}

func (realClock) Now() time.Time { return time.Time{} }

// FakeClock is a Clock whose reading only changes when Advance is called.
type FakeClock struct {
	now time.Time
}

// NewFake returns a FakeClock reading start.
func NewFake(start time.Time) *FakeClock { return nil }

// Now returns the instant the fake currently reads.
func (f *FakeClock) Now() time.Time { return time.Time{} }

// Advance moves the fake forward by d.
func (f *FakeClock) Advance(d time.Duration) {}
