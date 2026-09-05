package quiet_test

import (
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/quiet"
)

// ist is the zone every case in this file is expressed in, because the band is
// a wall-clock band and a case written in UTC would be testing arithmetic
// rather than the rule.
var ist = quiet.IST()

func at(hour, minute int) time.Time {
	return time.Date(2026, 9, 5, hour, minute, 0, 0, ist)
}

func TestISTIsFiveThirtyAheadOfUTC(t *testing.T) {
	_, offset := time.Date(2026, 9, 5, 12, 0, 0, 0, quiet.IST()).Zone()
	if want := 5*3600 + 30*60; offset != want {
		t.Errorf("IST offset = %d seconds, want %d", offset, want)
	}

	// India observes no daylight saving, so the offset in January is the
	// offset in July. This is what makes the fixed-zone fallback correct
	// rather than an approximation.
	_, january := time.Date(2026, 1, 5, 12, 0, 0, 0, quiet.IST()).Zone()
	if january != offset {
		t.Errorf("the offset moved across the year: %d in September, %d in January", offset, january)
	}
}

func TestInWindowCoversTheDefaultBand(t *testing.T) {
	for _, tc := range []struct {
		name string
		t    time.Time
		want bool
	}{
		{"just before the band opens", at(8, 59), false},
		{"exactly at the opening minute", at(9, 0), true},
		{"the middle of the day", at(13, 30), true},
		{"the last minute inside", at(20, 59), true},
		{"exactly at the closing minute", at(21, 0), false},
		{"the evening", at(22, 30), false},
		{"the small hours", at(3, 0), false},
		{"midnight", at(0, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := quiet.InWindow(tc.t, ist); got != tc.want {
				t.Errorf("InWindow(%s) = %t, want %t", tc.t.Format("15:04"), got, tc.want)
			}
		})
	}
}

// TestInWindowReadsTheInstantInTheGivenZone is the case a band expressed in
// local minutes exists for. The same instant is inside the band in one zone and
// outside it in another, and the zone is the caller's to supply.
func TestInWindowReadsTheInstantInTheGivenZone(t *testing.T) {
	// 05:00 UTC is 10:30 IST, inside the band, and 05:00 UTC itself is not.
	instant := time.Date(2026, 9, 5, 5, 0, 0, 0, time.UTC)

	if !quiet.InWindow(instant, ist) {
		t.Error("05:00 UTC is 10:30 IST and was read as outside the band")
	}
	if quiet.InWindow(instant, time.UTC) {
		t.Error("05:00 read in UTC is before 09:00 and was read as inside the band")
	}

	// A nil zone means IST, which is the zone this project's customers are in.
	if quiet.InWindow(instant, nil) != quiet.InWindow(instant, ist) {
		t.Error("a nil location did not read as IST")
	}
}

func TestZeroWindowIsTheDefaultBandAndNotAnEmptyOne(t *testing.T) {
	var zero quiet.Window

	if !zero.IsZero() {
		t.Fatal("the zero Window does not report itself as zero")
	}
	if got, want := zero.WithDefault(), quiet.DefaultWindow(); got != want {
		t.Errorf("the zero Window filled to %v, want the default %v", got, want)
	}

	// The failure this convention exists to prevent: a policy built from a
	// Config nobody filled in refusing every notification.
	if !zero.Contains(at(13, 0), ist) {
		t.Error("the zero Window refused the middle of the afternoon, so it was read as an empty band")
	}
	if zero.Contains(at(2, 0), ist) {
		t.Error("the zero Window allowed 02:00, so it was read as a whole day")
	}
}

func TestAlwaysOpenContainsEveryMinuteOfTheDay(t *testing.T) {
	w := quiet.AlwaysOpen()
	for minute := range quiet.MinutesPerDay {
		if !w.Contains(at(minute/60, minute%60), ist) {
			t.Fatalf("AlwaysOpen excluded %02d:%02d", minute/60, minute%60)
		}
	}
}

// TestWindowWrapsPastMidnight covers the shape a band gets when a merchant
// configures a night window. Nothing in this project uses one, and the branch
// is here so that configuring one does not silently invert the rule.
func TestWindowWrapsPastMidnight(t *testing.T) {
	night := quiet.At(22, 0, 6, 0)

	for _, tc := range []struct {
		t    time.Time
		want bool
	}{
		{at(21, 59), false},
		{at(22, 0), true},
		{at(23, 59), true},
		{at(0, 0), true},
		{at(5, 59), true},
		{at(6, 0), false},
		{at(13, 0), false},
	} {
		if got := night.Contains(tc.t, ist); got != tc.want {
			t.Errorf("%s in a 22:00 to 06:00 window = %t, want %t", tc.t.Format("15:04"), got, tc.want)
		}
	}
}

func TestValidateRejectsTheShapesThatCanBeReadTwoWays(t *testing.T) {
	for _, tc := range []struct {
		name    string
		w       quiet.Window
		wantErr bool
	}{
		{"the zero window stands for the default", quiet.Window{}, false},
		{"the default band", quiet.DefaultWindow(), false},
		{"always open", quiet.AlwaysOpen(), false},
		{"a wrapping band", quiet.At(22, 0, 6, 0), false},
		{"start equal to end", quiet.At(9, 0, 9, 0), true},
		{"a negative start", quiet.Window{StartMinute: -1, EndMinute: 600}, true},
		{"an end past the day", quiet.Window{StartMinute: 60, EndMinute: quiet.MinutesPerDay + 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.w.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("%v validated, want an error", tc.w)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("%v did not validate: %v", tc.w, err)
			}
		})
	}

	// An ambiguous band that skipped validation contacts nobody. The safe
	// reading of a config nobody can agree on is the one that sends nothing.
	if quiet.At(9, 0, 9, 0).Contains(at(9, 0), ist) {
		t.Error("a window whose start equals its end allowed a contact")
	}
}

func TestWindowStringNamesTheBand(t *testing.T) {
	for _, tc := range []struct {
		w    quiet.Window
		want string
	}{
		{quiet.Window{}, "09:00-21:00"},
		{quiet.DefaultWindow(), "09:00-21:00"},
		{quiet.At(22, 0, 6, 30), "22:00-06:30"},
		{quiet.AlwaysOpen(), "00:00-24:00"},
	} {
		if got := tc.w.String(); got != tc.want {
			t.Errorf("Window%v.String() = %q, want %q", tc.w, got, tc.want)
		}
	}
}
