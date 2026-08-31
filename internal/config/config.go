package config

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
	// GatewayFake is the in-memory gateway. It needs no credentials.
	GatewayFake = "fake"
	// GatewayLive talks to Razorpay test mode and needs both keys.
	GatewayLive = "live"
)

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
	// run uses. Phase 2 and phase 3 give them meaning; phase 0 only carries
	// them.
	Layer string
	Arm   string
}

// Load reads the configuration from the process environment.
func Load() (Config, error) { return Config{}, nil }

// LoadFrom reads the configuration through getenv.
func LoadFrom(getenv func(string) string) (Config, error) { return Config{}, nil }

// RequireLiveAccess returns an error naming the missing variable when the
// configuration cannot reach the Razorpay API.
func (c Config) RequireLiveAccess() error { return nil }

// String renders the configuration without either credential in it.
func (c Config) String() string { return "" }
