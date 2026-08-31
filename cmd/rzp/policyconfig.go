package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/lopster568/rzp-recovery-agent/internal/policy"
)

// policyConfigJSON is the effective policy, in the shape the run manifest
// records it in.
//
// Durations are strings rather than the nanosecond integers encoding/json
// gives a time.Duration, because a run manifest is read by people as well as
// by the scorer and 30000000000 is not a cooldown anyone can check at a
// glance.
type policyConfigJSON struct {
	MaxAttemptsPerOrder int    `json:"max_attempts_per_order"`
	Cooldown            string `json:"cooldown"`
	NotifyWindow        string `json:"notify_window"`
	AmountCeilingPaise  int64  `json:"amount_ceiling_paise"`
	ActionBudget        int    `json:"action_budget"`
	KillSwitch          bool   `json:"kill_switch"`
}

// runPolicyConfig prints the policy a run would use, as JSON.
//
// It exists so the run manifest records the policy that actually ran rather
// than a copy of it kept somewhere else. The first version had the numbers
// written out in harness/orchestrator.py, and they went stale on 2026-08-31
// the same hour the amount ceiling moved: the manifest said 400000 while every
// arm in that run had used 450000. A manifest that disagrees with the run it
// describes is worse than one that omits the field.
func runPolicyConfig(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("policy-config", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := policy.New(policy.Config{}, nil).Config()
	encoded, err := json.MarshalIndent(policyConfigJSON{
		MaxAttemptsPerOrder: cfg.MaxAttemptsPerOrder,
		Cooldown:            cfg.Cooldown.String(),
		NotifyWindow:        cfg.NotifyWindow.String(),
		AmountCeilingPaise:  cfg.AmountCeilingPaise,
		ActionBudget:        cfg.ActionBudget,
		KillSwitch:          cfg.KillSwitch,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the policy config: %w", err)
	}
	fmt.Fprintln(os.Stdout, string(encoded))
	return nil
}
