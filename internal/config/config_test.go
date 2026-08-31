package config_test

import (
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/config"
)

// Placeholder credentials. They are deliberately not shaped like Razorpay
// keys, so the pre-commit secret check has nothing to catch.
const (
	testKeyID     = "key_id_placeholder"
	testKeySecret = "key_secret_placeholder"
)

// envMap builds a getenv function over a map, so a test can describe the
// whole environment it wants without touching the process.
func envMap(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestConfigLoadsKeysFromEnv(t *testing.T) {
	t.Setenv(config.EnvKeyID, testKeyID)
	t.Setenv(config.EnvKeySecret, testKeySecret)
	t.Setenv(config.EnvGateway, config.GatewayLive)
	t.Setenv(config.EnvOTLPEndpoint, "localhost:4317")
	t.Setenv(config.EnvServiceName, "rzp-recovery-agent")
	t.Setenv(config.EnvLayer, "policy")
	t.Setenv(config.EnvArm, "deterministic")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.RazorpayKeyID != testKeyID {
		t.Errorf("%s = %q, want %q", config.EnvKeyID, cfg.RazorpayKeyID, testKeyID)
	}
	if cfg.RazorpayKeySecret != testKeySecret {
		t.Errorf("%s was not read from the environment", config.EnvKeySecret)
	}
	if cfg.Gateway != config.GatewayLive {
		t.Errorf("gateway = %q, want %q", cfg.Gateway, config.GatewayLive)
	}
	if cfg.OTLPEndpoint != "localhost:4317" {
		t.Errorf("otlp endpoint = %q, want %q", cfg.OTLPEndpoint, "localhost:4317")
	}
	if cfg.Layer != "policy" {
		t.Errorf("layer = %q, want %q", cfg.Layer, "policy")
	}
	if cfg.Arm != "deterministic" {
		t.Errorf("arm = %q, want %q", cfg.Arm, "deterministic")
	}

	if err := cfg.RequireLiveAccess(); err != nil {
		t.Errorf("RequireLiveAccess with both keys set: %v", err)
	}
}

func TestConfigStringRedactsSecret(t *testing.T) {
	cfg, err := config.LoadFrom(envMap(map[string]string{
		config.EnvKeyID:     testKeyID,
		config.EnvKeySecret: testKeySecret,
		config.EnvGateway:   config.GatewayLive,
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	rendered := cfg.String()

	if rendered == "" {
		t.Fatal("String() returned nothing")
	}
	if strings.Contains(rendered, testKeySecret) {
		t.Errorf("String() printed the secret: %s", rendered)
	}
	if strings.Contains(rendered, testKeyID) {
		t.Errorf("String() printed the key id: %s", rendered)
	}
	if !strings.Contains(rendered, "redacted") {
		t.Errorf("String() = %q, which does not say the secret was redacted", rendered)
	}
	if !strings.Contains(rendered, config.GatewayLive) {
		t.Errorf("String() = %q, which does not say which gateway is in use", rendered)
	}
}

func TestConfigFailsFastWhenKeyIDMissing(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantError bool
	}{
		{
			name:      "live gateway with no key id",
			env:       map[string]string{config.EnvGateway: config.GatewayLive, config.EnvKeySecret: testKeySecret},
			wantError: true,
		},
		{
			name:      "live gateway with no secret",
			env:       map[string]string{config.EnvGateway: config.GatewayLive, config.EnvKeyID: testKeyID},
			wantError: true,
		},
		{
			name:      "fake gateway with no credentials at all",
			env:       map[string]string{},
			wantError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.LoadFrom(envMap(tc.env))

			if !tc.wantError {
				if err != nil {
					t.Fatalf("LoadFrom: %v", err)
				}
				if cfg.Gateway != config.GatewayFake {
					t.Errorf("gateway defaulted to %q, want %q", cfg.Gateway, config.GatewayFake)
				}
				return
			}

			if err == nil {
				t.Fatalf("LoadFrom returned no error for %v", tc.env)
			}
			if tc.env[config.EnvKeyID] == "" && !strings.Contains(err.Error(), config.EnvKeyID) {
				t.Errorf("error %q does not name %s", err, config.EnvKeyID)
			}
		})
	}

	// The same check is available to a caller that needs live access even
	// when the gateway is not set to live.
	cfg, err := config.LoadFrom(envMap(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	err = cfg.RequireLiveAccess()
	if err == nil {
		t.Fatal("RequireLiveAccess returned no error with no credentials set")
	}
	if !strings.Contains(err.Error(), config.EnvKeyID) {
		t.Errorf("error %q does not name %s", err, config.EnvKeyID)
	}
}

// TestConfigReadsJaegerUIURLAndBuildsATraceURL covers the live half's need to
// print a reviewer a link to the trace a run produced. The UI can be on
// another machine: phase 1's docker daemon runs over SSH on a build machine,
// so the host cannot be hardcoded to localhost.
func TestConfigReadsJaegerUIURLAndBuildsATraceURL(t *testing.T) {
	cfg, err := config.LoadFrom(envMap(map[string]string{
		config.EnvJaegerUIURL: "http://build-machine:16686/",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.JaegerUIURL != "http://build-machine:16686/" {
		t.Errorf("JaegerUIURL = %q, want the value from %s", cfg.JaegerUIURL, config.EnvJaegerUIURL)
	}

	// The trailing slash in the configured URL must not produce a double
	// slash in the link, because a reviewer pastes this into a browser.
	got := cfg.TraceURL("4bf92f3577b34da6a3ce929d0e0e4736")
	want := "http://build-machine:16686/trace/4bf92f3577b34da6a3ce929d0e0e4736"
	if got != want {
		t.Errorf("TraceURL = %q, want %q", got, want)
	}

	// An empty trace id means no span was recorded. A link to /trace/ with
	// nothing after it is a broken link presented as evidence, so there is
	// no link at all.
	if empty := cfg.TraceURL(""); empty != "" {
		t.Errorf("TraceURL with no trace id = %q, want an empty string", empty)
	}

	// The all-zero id is the one that actually shows up. trace.TraceID.String
	// never returns an empty string: a span context that is not recording
	// renders as 32 zeros, so guarding only the empty case left the caller
	// printing a link to a trace that cannot exist. Review finding,
	// 2026-08-31.
	if zero := cfg.TraceURL("00000000000000000000000000000000"); zero != "" {
		t.Errorf("TraceURL with the all-zero trace id = %q, want an empty string", zero)
	}
	if short := cfg.TraceURL("abc"); short != "" {
		t.Errorf("TraceURL with a malformed trace id = %q, want an empty string", short)
	}

	// Unset falls back to the port compose publishes, so a local run still
	// prints a working link.
	fallback, err := config.LoadFrom(envMap(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if fallback.JaegerUIURL != config.DefaultJaegerUIURL {
		t.Errorf("JaegerUIURL with %s unset = %q, want %q", config.EnvJaegerUIURL, fallback.JaegerUIURL, config.DefaultJaegerUIURL)
	}
}
