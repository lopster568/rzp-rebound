// Command rzp-demo serves the public demo page for the revenue-at-risk engine.
//
// It has two views and neither of them can reach Razorpay.
//
// The replay view serves the artifacts of one real run against Razorpay test
// mode, committed under testdata/ beside this file: the seeded book, the
// summary, the result rows, the audit ledger, the escalation queue, and the two
// snapshots taken around the single payment a person made in a browser on
// 2026-09-05. They are files. Serving them calls nothing.
//
// The run view executes the pipeline in this process: the three real detectors,
// the real dedupe, the real fifteen-rule gate, and the real intervention
// engine. What it does not have is Razorpay. The detectors read a fixture book
// through riskrun.NewManifestSource and the intervention engine calls
// simGateway, which is a map. riskrun.Options.Simulated labels every row the
// run writes, so nothing it emits can be mistaken for a live run.
//
// # Why this binary cannot make a live call
//
// The argument is four parts, and safety_test.go holds three of them as tests
// over this package's own syntax tree rather than as prose.
//
//  1. It reads one environment variable, PORT, and no other. There is no key
//     id, no key secret, and no path to a credentials file. internal/config,
//     which is the only thing in this repository that loads Razorpay keys, is
//     not imported.
//  2. It never constructs a Razorpay client. internal/razorpay is reachable in
//     the import graph, because internal/intervene declares its Gateway
//     interface in terms of that package's types and asserts the live client
//     satisfies it. A type is not a client: the constructor is never called
//     here, so no HTTP transport with an Authorization header on it is ever
//     built.
//  3. It makes no outbound request of any kind. net/http appears here as a
//     server and nowhere as a client.
//  4. Every run parameter is a constant in this package. Nothing a visitor can
//     type reaches the policy config, the action budget, the kill switch, or
//     the book.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// The name this page carries.
//
// projectName is the brand and projectSlug is the repository-facing title. They
// are two constants rather than one string interpolated in three files, because
// this name changed once already and the page, the health check, and the footer
// all have to move together when it changes again.
const (
	projectName = "Rebound"
	projectSlug = "rzp-rebound"
	// repoURL is where the code is. The repository was not renamed with the
	// project, so this is deliberately not derived from either constant above.
	repoURL = "https://github.com/lopster568/rzp-recovery-agent"
)

// replayRunTag is the run tag of the committed artifacts the replay view
// serves. It is here so the health check can name what it is serving without
// decoding the summary.
const replayRunTag = "risk-1788614771"

// assets is everything this binary serves. The page and the committed run are
// compiled into it, so a running container reads no file at all after start.
//
//go:embed index.html testdata/book.json testdata/summary.json
//go:embed testdata/results.jsonl testdata/ledger.jsonl testdata/escalations.jsonl
//go:embed testdata/before.json testdata/after.json testdata/fixture-manifest.json
var assets embed.FS

// defaultPort is what the server listens on when PORT says nothing. Render and
// every other free-tier host sets PORT; 8080 is for a laptop.
const defaultPort = "8080"

func main() {
	if err := run(); err != nil {
		log.Fatalf("rzp-demo: %v", err)
	}
}

func run() error {
	addr := net.JoinHostPort("", listenPort())

	srv, err := newServer()
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:    addr,
		Handler: srv,
		// A run holds its connection open for as long as it streams, so the
		// write timeout has to clear runDeadline rather than the usual few
		// seconds. The read side has no such need: every request here is a GET
		// with no body.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      runDeadline + 30*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		log.Printf("%s (%s) listening on %s", projectName, projectSlug, addr)
		log.Printf("replay: the committed run %s; engine: the fixture book, simulated gateway", replayRunTag)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// listenPort is the one and only place this binary reads its environment.
//
// PORT is the whole of it. There is no credential to read, so there is nothing
// else worth reading, and keeping the read in one function is what lets
// safety_test.go assert that by walking this package rather than by trusting a
// sentence. A value that is not a port number is refused rather than silently
// replaced, because a container that came up on the wrong port would fail its
// health check with no reason attached.
func listenPort() string {
	port, ok := os.LookupEnv("PORT")
	if !ok || port == "" {
		return defaultPort
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		log.Printf("PORT is %q, which is not a port number; using %s", port, defaultPort)
		return defaultPort
	}
	return port
}
