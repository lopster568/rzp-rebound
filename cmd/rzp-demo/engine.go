package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/quiet"
	"github.com/lopster568/rzp-recovery-agent/internal/riskrun"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// The run parameters, fixed rather than taken from the request.
//
// Every one of them is a knob cmd/rzp risk-run exposes as a flag, and none of
// them is a query parameter here. A public endpoint that let a stranger set the
// action budget, the kill switch, or the contact window would be an endpoint
// that lets a stranger decide how much this program does, and the interesting
// thing about the run is that the same rules fire every time.
const (
	// demoSeed drives the arm assignment. Fixed, so the split between the
	// control arm and the engine arm is the same on every visit and two people
	// watching the page see the same run.
	demoSeed = 1234
	// demoDetectGrace is the overdue-invoice detector's own grace period,
	// measured against the fixture's issued_at. The 24 hour default would
	// report nothing at all over a book whose entities the fixture stamps at
	// its own created_at, which is the same reason README's offline command
	// passes a small value.
	demoDetectGrace = time.Second
	// demoNotifyWindow opens R6, the run-wide send rate. Its one second default
	// means exactly one notification per run, which would make every later
	// allow into an R6 denial and hide every other rule behind it.
	demoNotifyWindow = time.Nanosecond
)

// runEvent is one line of the stream the page reads. It is a flat envelope with
// a kind and a payload, so the browser switches on one field.
type runEvent struct {
	Kind string `json:"kind"`
	// Text carries a log line for the kinds that are prose.
	Text string `json:"text,omitempty"`
	// Item is one riskrun.ItemResult, verbatim, for kind "item". It is the same
	// row the committed results.jsonl carries, because it is written by the
	// same encoder in the same function.
	Item *riskrun.ItemResult `json:"item,omitempty"`
	// Summary is riskrun.Summary for kind "summary".
	Summary *riskrun.Summary `json:"summary,omitempty"`
	// Ledger is the audit rows the run wrote, for kind "ledger".
	Ledger []json.RawMessage `json:"ledger,omitempty"`
	// Escalations is what the escalation sink took, for kind "escalations".
	Escalations []intervene.Escalation `json:"escalations,omitempty"`
	// Calls is what the simulated gateway was asked to do, for kind "calls".
	Calls []string `json:"calls,omitempty"`
}

// emit is how a run hands one event to whatever is watching. Returning an error
// stops the run: the browser went away, and there is nobody left to stream to.
type emit func(runEvent) error

// runEngine drives the real pipeline over the fixture book and streams what
// happens.
//
// Everything the pipeline does here is the pipeline. detect's three detectors,
// detect.Collapse, riskrun.AssignArms, riskrun.ProposeAction, policy.Evaluate,
// and intervene.Engine.Apply all run exactly as cmd/rzp risk-run runs them,
// through riskrun.Run itself rather than through a copy of its loop. The two
// things that are not real are on the far side of two interfaces: the detectors
// read riskrun.NewManifestSource instead of Razorpay, and the intervention
// engine calls simGateway instead of Razorpay. riskrun.Options.Simulated is set
// so that every row the run writes says so.
func runEngine(ctx context.Context, book seed.Manifest, pace time.Duration, send emit) error {
	var ledger bytes.Buffer
	recorder, err := audit.NewRecorder(audit.Options{Writer: &ledger})
	if err != nil {
		return fmt.Errorf("demo: build the audit recorder: %w", err)
	}

	gateway := newSimGateway(book)
	escalations := intervene.NewMemorySink()

	results := &itemStream{ctx: ctx, pace: pace, send: send}
	logs := &lineStream{ctx: ctx, send: send}

	summary, runErr := riskrun.Run(ctx, riskrun.Options{
		Manifest:     book,
		ManifestPath: fixtureManifestName,
		RunTag:       "rebound-demo",
		Seed:         demoSeed,
		Simulated:    true,
		SimulateAge:  true,
		DetectConfig: detect.Config{Grace: demoDetectGrace},
		SinceSource:  "the whole fixture book: a simulated run is never scoped to an account's history",
		PolicyConfig: policy.Config{
			NotifyWindow: demoNotifyWindow,
			// The contact band is opened for the same reason the README tells an
			// operator to pass --contact-always-open after 21:00 IST: R12 is a
			// real rule, and a page that showed nothing but R12 denials to every
			// visitor in the wrong half of the day would be showing the clock
			// rather than the gate. The page says so where it names the run's
			// settings.
			ContactWindow: quiet.AlwaysOpen(),
		},
		Clock:       clock.Real(),
		API:         riskrun.NewManifestSource(book),
		Gateway:     gateway,
		Recorder:    recorder,
		Escalations: escalations,
		Results:     results,
		Log:         logs,
	})

	// A write error out of the two streams is the browser having gone away, and
	// it arrives here wrapped by riskrun.Run. There is nothing left to report it
	// to, so it is returned and the handler drops the connection.
	if results.err != nil {
		return results.err
	}
	if logs.err != nil {
		return logs.err
	}
	if runErr != nil {
		if err := send(runEvent{Kind: "log", Text: "run error: " + runErr.Error()}); err != nil {
			return err
		}
	}

	if err := send(runEvent{Kind: "calls", Calls: gateway.Calls()}); err != nil {
		return err
	}
	if err := send(runEvent{Kind: "ledger", Ledger: jsonLines(ledger.Bytes())}); err != nil {
		return err
	}
	if err := send(runEvent{Kind: "escalations", Escalations: escalations.Escalations()}); err != nil {
		return err
	}
	if err := send(runEvent{Kind: "summary", Summary: &summary}); err != nil {
		return err
	}
	return send(runEvent{Kind: "done"})
}

// itemStream is riskrun.Options.Results. Run encodes one ItemResult per item
// into it as the item finishes, so decoding each line here is what makes the
// page fill in as the run happens rather than after it.
type itemStream struct {
	ctx  context.Context
	pace time.Duration
	send emit
	err  error
	buf  bytes.Buffer
}

func (s *itemStream) Write(p []byte) (int, error) {
	s.buf.Write(p)
	for {
		line, err := s.buf.ReadBytes('\n')
		if err != nil {
			// A partial line. Put it back and wait for the rest.
			s.buf.Write(line)
			return len(p), s.err
		}
		if err := s.emitLine(line); err != nil {
			s.err = err
			return len(p), err
		}
	}
}

func (s *itemStream) emitLine(line []byte) error {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil
	}
	var row riskrun.ItemResult
	if err := json.Unmarshal(trimmed, &row); err != nil {
		return fmt.Errorf("demo: decode a result row: %w", err)
	}
	// The pace is here rather than in the handler because it is what makes the
	// stream a stream: sleeping between two events the run has already finished
	// producing would be a replay wearing a progress bar. This blocks the run
	// itself, which is honest and is also the rate limit doing its job.
	if s.pace > 0 {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(s.pace):
		}
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	return s.send(runEvent{Kind: "item", Item: &row})
}

// lineStream is riskrun.Options.Log. It forwards the run header and the
// per-item progress lines verbatim, so the page shows the same text a terminal
// running cmd/rzp risk-run shows.
type lineStream struct {
	ctx  context.Context
	send emit
	err  error
	buf  bytes.Buffer
}

func (s *lineStream) Write(p []byte) (int, error) {
	s.buf.Write(p)
	for {
		line, err := s.buf.ReadString('\n')
		if err != nil {
			s.buf.WriteString(line)
			return len(p), s.err
		}
		if err := s.ctx.Err(); err != nil {
			s.err = err
			return len(p), err
		}
		if err := s.send(runEvent{Kind: "log", Text: strings.TrimRight(line, "\n")}); err != nil {
			s.err = err
			return len(p), err
		}
	}
}

// jsonLines splits a JSONL buffer into raw messages, dropping blank lines.
func jsonLines(b []byte) []json.RawMessage {
	out := []json.RawMessage{}
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		out = append(out, json.RawMessage(append([]byte(nil), line...)))
	}
	return out
}

// decodeManifest reads a seedbook manifest out of the embedded bytes.
func decodeManifest(r io.Reader) (seed.Manifest, error) {
	var m seed.Manifest
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return seed.Manifest{}, fmt.Errorf("demo: decode the manifest: %w", err)
	}
	return m, nil
}
