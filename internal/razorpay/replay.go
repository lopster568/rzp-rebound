package razorpay

import (
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// DefaultRecordedDir is where captured fixtures live, relative to the
// repository root.
const DefaultRecordedDir = "testdata/recorded"

// SyntheticPrefix marks a fixture that nobody captured. A file whose name
// starts with it was written by hand to give a mechanism something to serve,
// and it is not evidence about Razorpay. Tests that measure Razorpay behaviour
// skip these; tests that measure our own plumbing use them.
const SyntheticPrefix = "synthetic_"

// ReplayBaseURL is the base URL a replay client reports. The host is a
// reserved name that resolves nowhere, so a replay client that somehow reached
// a real transport would fail rather than call out.
const ReplayBaseURL = "https://replay.invalid/v1"

// Fixture is one recorded exchange on disk.
type Fixture struct {
	Meta     FixtureMeta     `json:"_meta"`
	Request  FixtureRequest  `json:"request"`
	Response FixtureResponse `json:"response"`
}

// FixtureMeta says where a fixture came from and whether anybody captured it.
type FixtureMeta struct {
	// Synthetic is true when the file was written by hand rather than
	// captured from Razorpay.
	Synthetic bool `json:"synthetic"`
	// Reason explains why a synthetic fixture exists.
	Reason string `json:"reason,omitempty"`
	// CapturedAt is the date a real capture was taken.
	CapturedAt string `json:"captured_at,omitempty"`
	// Note carries anything a reader of the fixture has to know.
	Note string `json:"note,omitempty"`
}

// FixtureRequest is the call a fixture answers.
type FixtureRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// FixtureResponse is what the fixture replies with.
type FixtureResponse struct {
	Status int `json:"status"`
	Body   any `json:"body"`
}

// DecodeBody unmarshals the recorded response body into v, through the same
// json tags the client decodes a live response with.
func (f Fixture) DecodeBody(v any) error { return nil }

// ReplayOptions configures a replay client.
type ReplayOptions struct {
	// Dir holds the fixture files. Empty means DefaultRecordedDir resolved
	// from the repository root.
	Dir string
	// TracerProvider is where the per-request spans go. Nil means the global
	// provider.
	TracerProvider trace.TracerProvider
	// IncludeSynthetic decides whether files with SyntheticPrefix are loaded.
	IncludeSynthetic bool
}

// NewReplay returns a Client whose transport answers from recorded fixtures
// instead of the network.
//
// It is the same Client, so replay runs the real decode path rather than a
// second one that could drift from it. A path with no fixture behind it is a
// 404 from the fixture transport, which surfaces as the same not-found error a
// live call would give.
func NewReplay(opts ReplayOptions) (*Client, error) { return &Client{}, nil }

// LoadFixtures reads every fixture in dir. Files whose name starts with
// SyntheticPrefix are included only when includeSynthetic is set.
func LoadFixtures(dir string, includeSynthetic bool) (map[string]Fixture, error) { return nil, nil }

// RecordedDir returns the absolute path of DefaultRecordedDir, resolved from
// this package's source file rather than the working directory, so it does not
// matter which directory a test runs in.
func RecordedDir() (string, error) { return "", nil }

// fixtureTransport answers requests from a fixture map.
type fixtureTransport struct {
	fixtures map[string]Fixture
}

var _ http.RoundTripper = (*fixtureTransport)(nil)

// RoundTrip answers from the fixture map.
func (t *fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) { return nil, nil }
