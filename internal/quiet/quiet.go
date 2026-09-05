package quiet

import (
	"fmt"
	"sync"
	"time"
)

// The default allowed-contact band, 09:00 to 21:00 local.
//
// This is a configured choice and it is not read off any regulation. It is
// TRAI-shaped: India's commercial-communication regime for SMS runs through
// TRAI's DLT registration and does restrict when promotional traffic may be
// delivered, but the exact band, the traffic categories it binds, and whether
// a payment reminder is promotional or transactional under it were not read
// from a primary TRAI document by this project. Treat the pair below as a
// merchant's own politeness rule with a NEEDS-VERIFICATION note against it,
// not as a compliance control, and do not quote it as a regulated window.
//
// The band is half open: DefaultStartHour is inside it and DefaultEndHour is
// not, so a run at exactly 21:00 is already outside. A closed upper bound
// would make 21:00:00.000 allowed and 21:00:00.001 refused, which is a
// distinction no operator means to draw.
const (
	DefaultStartHour = 9
	DefaultEndHour   = 21
)

// MinutesPerDay is the modulus every minute-of-day in this package lives under.
const MinutesPerDay = 24 * 60

// istZoneName is the tz database name for India Standard Time.
const istZoneName = "Asia/Kolkata"

// istOffsetSeconds is IST's fixed offset from UTC, five hours and thirty
// minutes. India has one zone and observes no daylight saving, so a fixed zone
// is a correct fallback rather than an approximation.
const istOffsetSeconds = 5*3600 + 30*60

var (
	istOnce sync.Once
	istZone *time.Location
)

// IST returns India Standard Time.
//
// It prefers the tz database, so that the zone a reader sees printed is the
// named one. A binary built without tzdata and running on a host with no zone
// files still has to answer, and there it falls back to the fixed offset,
// which for this zone is the same answer: India has a single time zone and no
// daylight saving, so nothing about IST changes across the year.
//
// The alternative was to return an error and make every caller handle a
// failure that cannot change the result. A quiet-hours check that fails
// because a zone file is missing would either refuse every contact or allow
// every contact, and both are worse than the fixed offset.
func IST() *time.Location {
	istOnce.Do(func() {
		if loc, err := time.LoadLocation(istZoneName); err == nil {
			istZone = loc
			return
		}
		istZone = time.FixedZone("IST", istOffsetSeconds)
	})
	return istZone
}

// Window is an allowed-contact band, as minutes since local midnight.
//
// StartMinute is inside the band and EndMinute is outside it. A window whose
// start is after its end wraps past midnight, so {StartMinute: 22 * 60,
// EndMinute: 6 * 60} is the ten hours from 22:00 to 06:00.
//
// The zero Window means the default band rather than an empty one, because the
// zero value of a config struct in this repository means "the standard
// setting" everywhere else and a policy that silently refused every
// notification because nobody filled in a field is the failure mode this
// convention exists to avoid. A caller that really wants no restriction asks
// for AlwaysOpen.
type Window struct {
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

// DefaultWindow is the 09:00 to 21:00 band the constants above describe.
func DefaultWindow() Window {
	return Window{StartMinute: DefaultStartHour * 60, EndMinute: DefaultEndHour * 60}
}

// AlwaysOpen is the window that contains every instant. It is how a caller
// turns the check off, since the zero Window means the default band.
func AlwaysOpen() Window {
	return Window{StartMinute: 0, EndMinute: MinutesPerDay}
}

// At builds a window from two wall-clock times, given as hour and minute.
func At(startHour, startMinute, endHour, endMinute int) Window {
	return Window{
		StartMinute: startHour*60 + startMinute,
		EndMinute:   endHour*60 + endMinute,
	}
}

// IsZero reports whether w is the zero Window, which stands for the default
// band.
func (w Window) IsZero() bool { return w.StartMinute == 0 && w.EndMinute == 0 }

// WithDefault returns w, or the default band when w is the zero Window.
func (w Window) WithDefault() Window {
	if w.IsZero() {
		return DefaultWindow()
	}
	return w
}

// Validate reports what is wrong with w, or nil.
//
// A start equal to its end is rejected rather than read as an empty band or as
// a whole day, because both readings are defensible and a config that can be
// read two ways is a config that will be read the wrong way. AlwaysOpen says
// "every instant" without ambiguity.
func (w Window) Validate() error {
	if w.IsZero() {
		return nil
	}
	for _, f := range []struct {
		name string
		v    int
	}{{"start", w.StartMinute}, {"end", w.EndMinute}} {
		if f.v < 0 || f.v > MinutesPerDay {
			return fmt.Errorf("quiet: the %s minute %d is outside 0 to %d", f.name, f.v, MinutesPerDay)
		}
	}
	if w.StartMinute == w.EndMinute {
		return fmt.Errorf("quiet: the window starts and ends at minute %d, which is ambiguous; use AlwaysOpen for no restriction", w.StartMinute)
	}
	return nil
}

// String renders the band as HH:MM-HH:MM, so an audit row names the window a
// decision was made against rather than two integers.
func (w Window) String() string {
	w = w.WithDefault()
	return fmt.Sprintf("%s-%s", clockString(w.StartMinute), clockString(w.EndMinute))
}

// clockString renders a minute-of-day as HH:MM.
//
// The end of an AlwaysOpen window is minute 1440, and it renders as 24:00
// rather than wrapping to 00:00, so that "00:00-24:00" reads as the whole day
// instead of "00:00-00:00", which reads as nothing.
func clockString(minute int) string {
	if minute == MinutesPerDay {
		return "24:00"
	}
	return fmt.Sprintf("%02d:%02d", minute/60%24, minute%60)
}

// Contains reports whether t, read in loc, falls inside the band.
//
// A nil loc means IST, which is the only zone this project's customers are in.
// The zero Window means the default band. Neither substitution is silent in
// the sense that matters: both are documented on the types, and a caller that
// wants something else says so.
func (w Window) Contains(t time.Time, loc *time.Location) bool {
	w = w.WithDefault()
	if loc == nil {
		loc = IST()
	}
	local := t.In(loc)
	minute := local.Hour()*60 + local.Minute()

	switch {
	case w.StartMinute == w.EndMinute:
		// Validate rejects this shape. Reaching it means a caller skipped
		// validation, and the safe reading of an ambiguous band is the one
		// that contacts nobody.
		return false
	case w.StartMinute < w.EndMinute:
		return minute >= w.StartMinute && minute < w.EndMinute
	default:
		return minute >= w.StartMinute || minute < w.EndMinute
	}
}

// InWindow reports whether t falls inside the default 09:00 to 21:00 band, read
// in loc. A nil loc means IST.
func InWindow(t time.Time, loc *time.Location) bool {
	return DefaultWindow().Contains(t, loc)
}
