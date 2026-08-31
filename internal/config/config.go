package config

import (
	"fmt"
	"os"
	"strings"
)

// Environment variables this package reads.
const (
	EnvKeyID        = "RAZORPAY_KEY_ID"
	EnvKeySecret    = "RAZORPAY_KEY_SECRET"
	EnvGateway      = "RZP_GATEWAY"
	EnvLayer        = "RZP_LAYER"
	EnvArm          = "RZP_ARM"
	EnvOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	EnvServiceName  = "OTEL_SERVICE_NAME"
)

// Gateway values.
const (
	// GatewayFake is the in-memory gateway. It needs no credentials, and it
	// is the default so that nothing here demands a key to run.
	GatewayFake = "fake"
	// GatewayLive talks to Razorpay test mode and needs both keys.
	GatewayLive = "live"
)

// DefaultServiceName is what traces are attributed to when nothing says
// otherwise.
const DefaultServiceName = "rzp-recovery-agent"

// Config is the runtime settings, loaded from the environment.
type Config struct {
	RazorpayKeyID     string
	RazorpayKeySecret string
	// Gateway is GatewayFake or GatewayLive.
	Gateway string
	// OTLPEndpoint is empty when traces should go to stdout.
	OTLPEndpoint string
	ServiceName  string
	// Layer and Arm select which recovery layer and which experiment arm a
	// run belongs to. Phase 2 and phase 3 give them meaning; phase 0 reads
	// them and puts them in the run record.
	Layer string
	Arm   string
}

// Load reads the configuration from the process environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads the configuration through getenv, so a test can describe a
// whole environment without touching the process.
func LoadFrom(getenv func(string) string) (Config, error) {
	c := Config{
		RazorpayKeyID:     strings.TrimSpace(getenv(EnvKeyID)),
		RazorpayKeySecret: strings.TrimSpace(getenv(EnvKeySecret)),
		Gateway:           strings.TrimSpace(getenv(EnvGateway)),
		OTLPEndpoint:      strings.TrimSpace(getenv(EnvOTLPEndpoint)),
		ServiceName:       strings.TrimSpace(getenv(EnvServiceName)),
		Layer:             strings.TrimSpace(getenv(EnvLayer)),
		Arm:               strings.TrimSpace(getenv(EnvArm)),
	}

	if c.Gateway == "" {
		c.Gateway = GatewayFake
	}
	if c.ServiceName == "" {
		c.ServiceName = DefaultServiceName
	}

	switch c.Gateway {
	case GatewayFake:
	case GatewayLive:
		// Fail here rather than on the first API call, so a run that cannot
		// reach Razorpay stops before it has written half a batch.
		if err := c.RequireLiveAccess(); err != nil {
			return Config{}, err
		}
	default:
		return Config{}, fmt.Errorf("config: %s is %q, want %q or %q",
			EnvGateway, c.Gateway, GatewayFake, GatewayLive)
	}

	return c, nil
}

// RequireLiveAccess returns an error naming the missing variable when the
// configuration cannot reach the Razorpay API.
func (c Config) RequireLiveAccess() error {
	var missing []string
	if c.RazorpayKeyID == "" {
		missing = append(missing, EnvKeyID)
	}
	if c.RazorpayKeySecret == "" {
		missing = append(missing, EnvKeySecret)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("config: %s is not set, and live Razorpay access needs it (test-mode keys only)",
		strings.Join(missing, " and "))
}

// String renders the configuration without either credential in it. The key id
// is held back along with the secret: it is half of a credential pair and it
// has no business in a log line.
func (c Config) String() string {
	keyID := "unset"
	if c.RazorpayKeyID != "" {
		keyID = "redacted"
	}
	secret := "unset"
	if c.RazorpayKeySecret != "" {
		secret = "redacted"
	}
	endpoint := c.OTLPEndpoint
	if endpoint == "" {
		endpoint = "stdout"
	}

	return fmt.Sprintf("config{gateway=%s key_id=%s key_secret=%s traces=%s service=%s layer=%s arm=%s}",
		c.Gateway, keyID, secret, endpoint, c.ServiceName, c.Layer, c.Arm)
}
