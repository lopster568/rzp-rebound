package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/riskrun"
)

// testServer is a server with the pacing and the queue wait taken off, so a
// test never waits on a wall clock.
func testServer(t *testing.T) *server {
	t.Helper()
	s, err := newServer()
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	s.pace = 0
	s.queueWait = 0
	return s
}

func TestHealthzReportsReadyAndNamesWhatItIsServing(t *testing.T) {
	s := testServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the health body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if body["service"] != projectSlug {
		t.Errorf("service = %v, want %s", body["service"], projectSlug)
	}
	// The fixture has to have decoded at startup, or the run view is broken and
	// a green health check would be lying about it.
	if got := body["book_items"].(float64); got <= 0 {
		t.Errorf("book_items = %v, want the fixture book's item count", got)
	}
}

func TestIndexServesThePageUnderItsCurrentName(t *testing.T) {
	s := testServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	page := rec.Body.String()
	// The two name constants and the repo URL are the three strings the header
	// carries, and the name has moved once already.
	for _, want := range []string{projectName, projectSlug, repoURL} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not carry %q", want)
		}
	}
	// The honesty line is not decoration. A page that lost it would be making a
	// claim by omission.
	for _, want := range []string{"No live gateway sits behind this page", "n=1", "test mode"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not carry the honesty wording %q", want)
		}
	}
	// No CDN. The page has to render with no network at all.
	for _, banned := range []string{"https://cdn", "https://unpkg", "https://fonts.googleapis"} {
		if strings.Contains(page, banned) {
			t.Errorf("the page loads %q, and it has to work air-gapped", banned)
		}
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	s := testServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestReplayServesTheCommittedRunAndComputesItsOwnDelta(t *testing.T) {
	s := testServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/replay", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload replayPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the replay payload: %v", err)
	}

	if payload.Summary.RunTag != replayRunTag {
		t.Errorf("run tag = %q, want %q", payload.Summary.RunTag, replayRunTag)
	}
	if payload.Summary.Mode != riskrun.ModeLive {
		t.Errorf("mode = %q, want %q: the replay is a real test-mode run", payload.Summary.Mode, riskrun.ModeLive)
	}
	if len(payload.Results) != payload.Summary.ItemsTotal {
		t.Errorf("%d result row(s) for %d item(s), want one each", len(payload.Results), payload.Summary.ItemsTotal)
	}
	if len(payload.Ledger) == 0 {
		t.Error("the ledger is empty, and a run with no audit trail is the failure the ledger exists to prevent")
	}
	if len(payload.Book.Items) == 0 {
		t.Error("the seeded book is empty")
	}

	// The recovered figure is the one number on the page a reader is most
	// likely to check, and this is the assertion that it is arithmetic over the
	// two committed snapshots rather than a constant somebody typed.
	const wantRecoveredPaise = 418300
	if payload.Delta.RecoveredPaise != wantRecoveredPaise {
		t.Errorf("recovered = %d paise, want %d", payload.Delta.RecoveredPaise, wantRecoveredPaise)
	}
	if payload.Delta.AmountDueChange != -wantRecoveredPaise {
		t.Errorf("amount due change = %d paise, want %d", payload.Delta.AmountDueChange, -wantRecoveredPaise)
	}
	if payload.Delta.EntriesUnmatched != 0 {
		t.Errorf("%d unmatched entr(ies), want 0: an unmatched read is not a zero", payload.Delta.EntriesUnmatched)
	}

	// Recomputing it here from the same two snapshots is the check that the
	// figure travels with the snapshots rather than with this file.
	again := riskrun.Diff(payload.Before, payload.After)
	if again.RecoveredPaise != payload.Delta.RecoveredPaise {
		t.Errorf("Diff over the served snapshots gives %d, the served delta says %d",
			again.RecoveredPaise, payload.Delta.RecoveredPaise)
	}
}

func TestRunStreamsTheWholePipelineAndTerminates(t *testing.T) {
	s := testServer(t)

	srv := httptest.NewServer(s)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/run", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	events := readStream(t, resp)

	kinds := map[string]int{}
	var items []riskrun.ItemResult
	var summary *riskrun.Summary
	for _, ev := range events {
		kinds[ev.Kind]++
		if ev.Kind == "item" && ev.Item != nil {
			items = append(items, *ev.Item)
		}
		if ev.Kind == "summary" {
			summary = ev.Summary
		}
	}

	// Terminating is half of what this test is for. A stream that never sends
	// done is a connection a free-tier host holds open until it kills it.
	if kinds["done"] != 1 {
		t.Fatalf("done event count = %d, want exactly 1", kinds["done"])
	}
	if kinds["error"] != 0 {
		t.Errorf("the run emitted %d error event(s)", kinds["error"])
	}
	for _, kind := range []string{"log", "item", "calls", "ledger", "escalations", "summary"} {
		if kinds[kind] == 0 {
			t.Errorf("no %s event in the stream", kind)
		}
	}
	if summary == nil {
		t.Fatal("no summary event")
	}

	// The label. This is the assertion that nothing the page shows can be read
	// as a live run.
	if summary.Mode != riskrun.ModeSimulated {
		t.Errorf("summary mode = %q, want %q", summary.Mode, riskrun.ModeSimulated)
	}
	if len(items) != summary.ItemsTotal {
		t.Errorf("%d item event(s) for %d item(s) in the summary", len(items), summary.ItemsTotal)
	}
	for _, item := range items {
		if item.Mode != riskrun.ModeSimulated {
			t.Errorf("item %s is stamped %q, want %q", item.RiskItemID, item.Mode, riskrun.ModeSimulated)
		}
	}

	// The point of the view is that the real gate ran and the real engine acted
	// on what it allowed. A stream of eleven denials would pass every assertion
	// above and show nothing.
	if summary.VerdictTotals["allow"] == 0 {
		t.Error("no allow verdict: nothing was executed, so nothing was demonstrated")
	}
	if summary.VerdictTotals["escalate"] == 0 {
		t.Error("no escalate verdict: the refusal path is the interesting half")
	}
	if summary.Escalations == 0 {
		t.Error("no escalation reached the sink")
	}
	if len(summary.ActionsAccepted) == 0 {
		t.Error("the intervention engine accepted nothing, so it did not run")
	}
	if summary.Errors != 0 {
		t.Errorf("%d row(s) carry an error", summary.Errors)
	}
	if summary.CollapsedAway == 0 {
		t.Error("the dedupe merged nothing, so the queue collapse is not being shown")
	}
}

func TestRunIsDeterministic(t *testing.T) {
	s := testServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	first := runOnce(t, srv.URL)
	second := runOnce(t, srv.URL)

	if len(first) != len(second) {
		t.Fatalf("two runs decided %d and %d item(s)", len(first), len(second))
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.RiskItemID != b.RiskItemID || a.Arm != b.Arm || a.RuleID != b.RuleID || a.Verdict != b.Verdict {
			t.Errorf("row %d differs between runs: %s/%s/%s/%s and %s/%s/%s/%s",
				i, a.RiskItemID, a.Arm, a.Verdict, a.RuleID, b.RiskItemID, b.Arm, b.Verdict, b.RuleID)
		}
	}
}

func TestASecondConcurrentRunIsRefusedRatherThanQueuedForever(t *testing.T) {
	s := testServer(t)

	// Hold the slot the way a run in flight holds it. Reaching into the field
	// is what makes this deterministic: driving two real runs and hoping they
	// overlap is a test that passes on a fast machine and fails on a busy one.
	s.runSlot <- struct{}{}
	defer func() { <-s.runSlot }()

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/run", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("no Retry-After on the refusal, so a client has nothing to wait on")
	}
	// The refusal has to be a status code and not the first line of a stream. A
	// 200 with an error inside it is a stream the browser has already committed
	// to rendering.
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("the refusal opened a stream: Content-Type = %q", ct)
	}
}

func TestTheSlotIsReleasedSoTheNextRunGetsIt(t *testing.T) {
	s := testServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	// Two runs back to back. The second one only succeeds if the first released
	// the slot, and with queueWait at zero it cannot have waited for it.
	if got := len(runOnce(t, srv.URL)); got == 0 {
		t.Fatal("the first run decided nothing")
	}
	if got := len(runOnce(t, srv.URL)); got == 0 {
		t.Fatal("the second run decided nothing, so the slot was not released")
	}
}

// runOnce drives one engine run and returns the item rows it streamed.
func runOnce(t *testing.T, base string) []riskrun.ItemResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/run", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var rows []riskrun.ItemResult
	for _, ev := range readStream(t, resp) {
		if ev.Kind == "item" && ev.Item != nil {
			rows = append(rows, *ev.Item)
		}
	}
	return rows
}

// readStream reads server-sent events until the body ends.
func readStream(t *testing.T, resp *http.Response) []runEvent {
	t.Helper()

	var events []runEvent
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev runEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("decode an event: %v", err)
		}
		events = append(events, ev)
		if ev.Kind == "done" || ev.Kind == "error" {
			// The handler returns after done, which ends the body. Stopping here
			// rather than reading to EOF keeps a hung server from hanging the
			// test instead of failing it.
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	return events
}
