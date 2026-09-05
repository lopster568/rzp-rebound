package main

import (
	"io"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/quiet"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// parse is parseRiskRunFlags with the usage text sent nowhere, so a case that
// expects a parse failure does not print a flag list into the test log.
func parse(t *testing.T, args ...string) riskRunConfig {
	t.Helper()
	cfg, err := parseRiskRunFlags(args, io.Discard)
	if err != nil {
		t.Fatalf("parseRiskRunFlags(%v): %v", args, err)
	}
	return cfg
}

// TestRiskRunFlagDefaultsAreThePolicyDefaults pins the flags to the constants
// they mirror.
//
// A flag default that drifted from the policy's own would make the command line
// silently the authority on a number the policy documents, which is the failure
// cmd/rzp/policyconfig.go's doc comment describes from the other direction.
func TestRiskRunFlagDefaultsAreThePolicyDefaults(t *testing.T) {
	cfg := parse(t)

	if cfg.notifyWindow != policy.DefaultNotifyWindow {
		t.Errorf("-notify-window default = %s, want policy.DefaultNotifyWindow %s", cfg.notifyWindow, policy.DefaultNotifyWindow)
	}
	if cfg.actionBudget != policy.DefaultActionBudget {
		t.Errorf("-action-budget default = %d, want policy.DefaultActionBudget %d", cfg.actionBudget, policy.DefaultActionBudget)
	}
	if cfg.detectGrace != detect.DefaultGrace {
		t.Errorf("-detect-grace default = %s, want detect.DefaultGrace %s", cfg.detectGrace, detect.DefaultGrace)
	}
	if cfg.contactAlwaysOpen {
		t.Error("-contact-always-open defaults to true, which would open R12 without anybody asking")
	}
	if cfg.sinceSet {
		t.Error("-since reports itself as set when nobody passed it")
	}

	// The standard policy is what a run with no policy flags gets.
	got := riskPolicyConfig(cfg)
	if got.NotifyWindow != policy.DefaultNotifyWindow {
		t.Errorf("NotifyWindow = %s, want %s", got.NotifyWindow, policy.DefaultNotifyWindow)
	}
	if !got.ContactWindow.IsZero() {
		t.Errorf("ContactWindow = %s with no flag passed, want the zero window that means the default band", got.ContactWindow)
	}
}

// TestRiskRunPolicyFlagsReachThePolicyConfig is the wiring the runner did not
// have: riskrun.Options.PolicyConfig was never assigned, so every notification
// after the first was denied under R6 and an evening take was denied under R12,
// with no flag anywhere to say otherwise.
func TestRiskRunPolicyFlagsReachThePolicyConfig(t *testing.T) {
	cfg := parse(t, "-notify-window", "1ns", "-action-budget", "12", "-contact-always-open")

	got := riskPolicyConfig(cfg)
	if got.NotifyWindow != time.Nanosecond {
		t.Errorf("NotifyWindow = %s, want 1ns", got.NotifyWindow)
	}
	if got.ActionBudget != 12 {
		t.Errorf("ActionBudget = %d, want 12", got.ActionBudget)
	}
	if want := quiet.AlwaysOpen(); got.ContactWindow != want {
		t.Errorf("ContactWindow = %s, want %s", got.ContactWindow, want)
	}
}

// TestRiskRunSinceDefaultsToTheManifestCreatedAt is the scoping the sweep did
// not have.
//
// The demo account carries every order it has ever had. An unscoped sweep puts
// all of it in one queue, and the engine arm then mints fresh payment links
// against weeks-old debt the run was never about. The manifest knows when its
// book was created, so that is the floor unless the operator says otherwise.
func TestRiskRunSinceDefaultsToTheManifestCreatedAt(t *testing.T) {
	created := time.Date(2026, 9, 5, 6, 0, 0, 0, time.UTC)
	manifest := seed.Manifest{CreatedAt: created}

	want := created.Add(-sinceSkewMargin).Unix()
	if got := sinceFor(parse(t), manifest); got != want {
		t.Errorf("with no -since, sinceFor = %d, want the manifest's created_at less the skew margin %d", got, want)
	}

	// An explicit 0 is a real answer and it means an unscoped sweep. It has to
	// beat the manifest default, or a run over an account with older debt could
	// not ask for the whole book.
	if got := sinceFor(parse(t, "-since", "0"), manifest); got != 0 {
		t.Errorf("with an explicit -since 0, sinceFor = %d, want 0", got)
	}

	// A named floor wins over both.
	if got := sinceFor(parse(t, "-since", "1700000000"), manifest); got != 1700000000 {
		t.Errorf("with -since 1700000000, sinceFor = %d, want 1700000000", got)
	}

	// A manifest with no created_at, which is what a hand-written fixture has,
	// leaves the sweep unscoped rather than pinning it to 1970.
	if got := sinceFor(parse(t), seed.Manifest{}); got != 0 {
		t.Errorf("with a manifest carrying no created_at, sinceFor = %d, want 0", got)
	}
}

// TestRiskRunDryRunAndReplayAreOneFlag pins the alias, which the refactor into
// riskRunConfig moved out of the command body.
func TestRiskRunDryRunAndReplayAreOneFlag(t *testing.T) {
	for _, args := range [][]string{{"-dry-run"}, {"-replay"}, {"-dry-run", "-replay"}} {
		if !parse(t, args...).offline {
			t.Errorf("%v did not put the run offline", args)
		}
	}
	if parse(t).offline {
		t.Error("a run with neither flag came out offline")
	}
}
