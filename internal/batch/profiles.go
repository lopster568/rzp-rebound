package batch

import (
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
)

// ObservedMixFileEnv names the environment variable holding the path to a real
// merchant's failure-mix file.
const ObservedMixFileEnv = "RZP_OBSERVED_MIX_FILE"

// ClassShare is one class's share of a batch.
type ClassShare struct {
	Class classify.Class
	Share float64
	// Cited reports that this share comes from the profile's published source
	// rather than from a residual the source does not break out.
	Cited bool
	// Note says what an uncited share is.
	Note string
}

// Profile is a named failure mix.
type Profile struct {
	// Name is what a caller asks for and what a batch id carries.
	Name string
	// Vintage is the year the source describes.
	Vintage string
	// Source is where the shares came from, or the word invented.
	Source string
	// Cited reports that the shares come from a published source.
	Cited bool
}

// Shares returns the non-bait class shares.
func (p Profile) Shares() []ClassShare { return nil }

// BaitShare returns the share of the batch this profile says no arm should act
// on, or zero when the profile leaves bait to an explicit count.
func (p Profile) BaitShare() float64 { return 0 }

// DefinesBait reports whether the profile sets its own bait share.
func (p Profile) DefinesBait() bool { return false }

// Spec turns the profile into a batch specification.
func (p Profile) Spec(seed int64, n, bait int) (Spec, error) { return Spec{}, nil }

// Profiles returns every profile.
func Profiles() []Profile { return nil }

// ProfileByName returns the profile with this name.
func ProfileByName(name string) (Profile, bool) { return Profile{}, false }
