package razorpay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
func (f Fixture) DecodeBody(v any) error {
	raw, err := json.Marshal(f.Response.Body)
	if err != nil {
		return fmt.Errorf("razorpay: re-encode a fixture body: %w", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("razorpay: decode a fixture body: %w", err)
	}
	return nil
}

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
func NewReplay(opts ReplayOptions) (*Client, error) {
	dir := opts.Dir
	if dir == "" {
		var err error
		dir, err = RecordedDir()
		if err != nil {
			return nil, err
		}
	}

	fixtures, err := LoadFixtures(dir, opts.IncludeSynthetic)
	if err != nil {
		return nil, err
	}

	// No credentials: there is nothing on the other end to authenticate to,
	// and a replay client that held a key pair would be a key pair in one more
	// place for no reason.
	return newClient(ClientOptions{
		BaseURL:        ReplayBaseURL,
		Transport:      &fixtureTransport{fixtures: fixtures},
		TracerProvider: opts.TracerProvider,
	})
}

// fixtureKey is how a request is matched to a fixture.
func fixtureKey(method, path string) string {
	return strings.ToUpper(method) + " " + path
}

// LoadFixtures reads every fixture in dir. Files whose name starts with
// SyntheticPrefix are included only when includeSynthetic is set.
func LoadFixtures(dir string, includeSynthetic bool) (map[string]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("razorpay: read %s: %w", dir, err)
	}

	out := make(map[string]Fixture)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, SyntheticPrefix) && !includeSynthetic {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("razorpay: read %s: %w", name, err)
		}
		var fixture Fixture
		if err := json.Unmarshal(raw, &fixture); err != nil {
			return nil, fmt.Errorf("razorpay: parse %s: %w", name, err)
		}
		if fixture.Request.Method == "" || fixture.Request.Path == "" {
			return nil, fmt.Errorf("razorpay: %s records no request method or path", name)
		}
		// A file named synthetic_ that does not say so in its own _meta would
		// pass for a capture the moment somebody renamed it.
		if strings.HasPrefix(name, SyntheticPrefix) && !fixture.Meta.Synthetic {
			return nil, fmt.Errorf("razorpay: %s is named as synthetic but its _meta does not say so", name)
		}
		if !strings.HasPrefix(name, SyntheticPrefix) && fixture.Meta.Synthetic {
			return nil, fmt.Errorf("razorpay: %s says it is synthetic but is not named with the %s prefix", name, SyntheticPrefix)
		}

		key := fixtureKey(fixture.Request.Method, fixture.Request.Path)
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("razorpay: two fixtures answer %s, and %s is the second", key, name)
		}
		out[key] = fixture
	}
	return out, nil
}

// RecordedDir returns the absolute path of DefaultRecordedDir, resolved from
// this package's source file rather than the working directory, so it does not
// matter which directory a test runs in.
func RecordedDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("razorpay: cannot locate this package on disk")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, DefaultRecordedDir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("razorpay: no go.mod above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// fixtureTransport answers requests from a fixture map.
type fixtureTransport struct {
	fixtures map[string]Fixture
}

var _ http.RoundTripper = (*fixtureTransport)(nil)

// RoundTrip answers from the fixture map. A path nothing was recorded for gets
// a 404, which the client turns into the port's not-found error, exactly as a
// live 404 would be.
func (t *fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := fixtureKey(req.Method, req.URL.Path)

	fixture, ok := t.fixtures[key]
	if !ok {
		return jsonResponse(req, http.StatusNotFound,
			[]byte(`{"description":"no recorded fixture answers this call"}`)), nil
	}

	body, err := json.Marshal(fixture.Response.Body)
	if err != nil {
		return nil, fmt.Errorf("razorpay: encode the fixture answering %s: %w", key, err)
	}

	status := fixture.Response.Status
	if status == 0 {
		status = http.StatusOK
	}
	return jsonResponse(req, status, body), nil
}

func jsonResponse(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}
