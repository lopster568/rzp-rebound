package clock_test

import (
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
)

// start is a fixed instant so no test here depends on wall time.
var start = time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

func TestFakeClockNowIsStableWithoutAdvance(t *testing.T) {
	c := clock.NewFake(start)

	first := c.Now()
	second := c.Now()

	if !first.Equal(start) {
		t.Errorf("first Now() = %s, want the instant the fake was built at, %s", first, start)
	}
	if !first.Equal(second) {
		t.Errorf("Now() moved without Advance: %s then %s", first, second)
	}
}

func TestFakeClockAdvanceMovesNowForward(t *testing.T) {
	tests := []struct {
		name string
		step time.Duration
	}{
		{"one second", time.Second},
		{"thirty minutes", 30 * time.Minute},
		{"two days", 48 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := clock.NewFake(start)

			c.Advance(tc.step)

			want := start.Add(tc.step)
			if got := c.Now(); !got.Equal(want) {
				t.Errorf("after Advance(%s) Now() = %s, want %s", tc.step, got, want)
			}
		})
	}
}
