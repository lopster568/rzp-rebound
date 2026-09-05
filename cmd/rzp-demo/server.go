package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/riskrun"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// The names the embedded artifacts go by. fixtureManifestName is also what the
// run reports as its manifest path, because a run that named an absolute path
// on somebody's laptop would be naming a machine nobody watching has.
const (
	fixtureManifestName = "cmd/rzp-demo/testdata/fixture-manifest.json"
)

// Stream and run bounds. Every one of them exists because this endpoint is
// public and a stranger is going to hold the button down.
const (
	// runConcurrency is how many engine runs may be in flight at once. One. The
	// run is a few hundred milliseconds of work and a second of pacing, and a
	// free-tier container has no reason to be doing two of them.
	runConcurrency = 1
	// queueWait is how long a second request waits for the slot before it is
	// refused. It is the whole queue: a short wait absorbs two people clicking
	// at the same moment, and anything past it gets a 429 rather than a
	// connection held open.
	queueWait = 3 * time.Second
	// streamPace is the gap between two item events. It is what makes the run
	// legible: eleven decisions arriving in four milliseconds is a flash, and
	// the point of the view is that a reader can watch each rule fire.
	streamPace = 140 * time.Millisecond
	// runDeadline bounds one run whatever happens inside it.
	runDeadline = 60 * time.Second
)

// server holds what every request reads. All of it is immutable after startup
// except the run slot.
type server struct {
	mux *http.ServeMux

	// book is the fixture manifest the engine view runs over, decoded once.
	book seed.Manifest
	// replay is the assembled committed-run payload, encoded once. It never
	// changes, so building it per request would be work with no reason.
	replay []byte

	// runSlot is the concurrency bound. A run holds one token for its life.
	runSlot chan struct{}
	// queueWait and pace are fields rather than the constants above so a test
	// can drive the handler without waiting on a wall clock.
	queueWait time.Duration
	pace      time.Duration
}

// newServer builds the handler tree. It reads the embedded artifacts and
// nothing else: no file outside the binary, no environment, and no network.
func newServer() (*server, error) {
	manifestBytes, err := assets.ReadFile("testdata/fixture-manifest.json")
	if err != nil {
		return nil, fmt.Errorf("demo: read the fixture manifest: %w", err)
	}
	book, err := decodeManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		return nil, err
	}

	replay, err := buildReplay()
	if err != nil {
		return nil, err
	}

	s := &server{
		mux:       http.NewServeMux(),
		book:      book,
		replay:    replay,
		runSlot:   make(chan struct{}, runConcurrency),
		queueWait: queueWait,
		pace:      streamPace,
	}

	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/replay", s.handleReplay)
	s.mux.HandleFunc("GET /api/run", s.handleRun)
	s.mux.HandleFunc("GET /", s.handleIndex)
	return s, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// handleHealthz is what a platform health check reads. It answers from memory
// and touches nothing, so a green healthz means the process is up and the
// embedded artifacts decoded at startup, which is the whole of what this binary
// depends on.
func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"service":     projectSlug,
		"mode":        "replay and simulation only, no gateway credentials in this process",
		"fixture":     fixtureManifestName,
		"book_items":  len(s.book.Items),
		"replay_run":  replayRunTag,
		"concurrency": runConcurrency,
	})
}

// handleIndex serves the one page.
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := assets.ReadFile("index.html")
	if err != nil {
		http.Error(w, "the page is missing from this build", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is a build artifact, so it is cacheable, but a demo that a judge
	// reloads after a redeploy has to show the redeploy.
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}

// handleReplay serves the committed run.
func (s *server) handleReplay(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(s.replay)
}

// handleRun executes the pipeline and streams it back as server-sent events.
//
// It is a GET because EventSource only issues GETs, and it is safe to repeat:
// the run allocates a fresh recorder, a fresh gateway, and a fresh ledger every
// time, writes to no file, and leaves nothing behind when the connection drops.
func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "this server needs a response writer that can flush", http.StatusInternalServerError)
		return
	}

	// The concurrency bound, taken before a single byte of the response. A
	// refusal has to be a status code, and once the stream has started the
	// status is already sent.
	if !s.acquire(r.Context()) {
		w.Header().Set("Retry-After", "5")
		http.Error(w,
			"another visitor is running the engine right now. This endpoint runs one at a time. Try again in a few seconds.",
			http.StatusTooManyRequests)
		return
	}
	defer s.release()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Render and most reverse proxies buffer a response by default, which turns
	// a stream into one delivery at the end. This is the header they read.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithTimeout(r.Context(), runDeadline)
	defer cancel()

	send := func(ev runEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		payload, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("demo: encode a %s event: %w", ev.Kind, err)
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := runEngine(ctx, s.book, s.pace, send); err != nil {
		// The connection is already open with a 200 on it, so there is no status
		// code left to change. One last event is the only way to say anything,
		// and it fails silently when the reason is that the reader left.
		_ = send(runEvent{Kind: "error", Text: err.Error()})
	}
}

// acquire takes the run slot, waiting up to queueWait for it.
//
// A zero queueWait refuses immediately, which is what a test wants and is also
// a defensible production setting: the honest answer to a busy single-run
// endpoint is a 429 the page can retry, not a held connection.
func (s *server) acquire(ctx context.Context) bool {
	select {
	case s.runSlot <- struct{}{}:
		return true
	default:
	}
	if s.queueWait <= 0 {
		return false
	}

	timer := time.NewTimer(s.queueWait)
	defer timer.Stop()
	select {
	case s.runSlot <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (s *server) release() { <-s.runSlot }

// replayPayload is what the replay view reads. Every field is either an
// embedded artifact decoded as it sits on disk or, for Delta, computed from two
// of them by the same function cmd/rzp risk-poll calls.
type replayPayload struct {
	// Book is the seedbook manifest the run was about.
	Book seed.Manifest `json:"book"`
	// Summary is the run's own summary.json.
	Summary riskrun.Summary `json:"summary"`
	// Results is results.jsonl, one row per item.
	Results []riskrun.ItemResult `json:"results"`
	// Ledger is ledger.jsonl, the append-only audit trail, raw.
	Ledger []json.RawMessage `json:"ledger"`
	// Escalations is escalations.jsonl.
	Escalations []json.RawMessage `json:"escalations"`
	// Before and After are the two snapshots risk-poll took around the one
	// payment a person made in a browser.
	Before riskrun.Snapshot `json:"before"`
	After  riskrun.Snapshot `json:"after"`
	// Delta is riskrun.Diff over those two snapshots, computed when this
	// process starts rather than copied out of a log. The recovered figure on
	// the page is therefore the output of the repository's own diff over
	// committed inputs, which is a thing a reader can check by running the same
	// function over the same two files.
	Delta riskrun.Delta `json:"delta"`
}

// buildReplay decodes the committed artifacts and encodes them as one payload.
func buildReplay() ([]byte, error) {
	var p replayPayload

	if err := readEmbeddedJSON("testdata/book.json", &p.Book); err != nil {
		return nil, err
	}
	if err := readEmbeddedJSON("testdata/summary.json", &p.Summary); err != nil {
		return nil, err
	}
	if err := readEmbeddedJSON("testdata/before.json", &p.Before); err != nil {
		return nil, err
	}
	if err := readEmbeddedJSON("testdata/after.json", &p.After); err != nil {
		return nil, err
	}

	rows, err := assets.ReadFile("testdata/results.jsonl")
	if err != nil {
		return nil, fmt.Errorf("demo: read the results: %w", err)
	}
	for i, line := range jsonLines(rows) {
		var row riskrun.ItemResult
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("demo: decode result row %d: %w", i+1, err)
		}
		p.Results = append(p.Results, row)
	}

	ledger, err := assets.ReadFile("testdata/ledger.jsonl")
	if err != nil {
		return nil, fmt.Errorf("demo: read the ledger: %w", err)
	}
	p.Ledger = jsonLines(ledger)

	escalations, err := assets.ReadFile("testdata/escalations.jsonl")
	if err != nil {
		return nil, fmt.Errorf("demo: read the escalations: %w", err)
	}
	p.Escalations = jsonLines(escalations)

	p.Delta = riskrun.Diff(p.Before, p.After)

	out, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("demo: encode the replay payload: %w", err)
	}
	return out, nil
}

func readEmbeddedJSON(name string, into any) error {
	b, err := assets.ReadFile(name)
	if err != nil {
		return fmt.Errorf("demo: read %s: %w", name, err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("demo: decode %s: %w", name, err)
	}
	return nil
}
