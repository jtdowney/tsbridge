package config

import (
	"context"
	"os"
	"time"
)

// TestFixture represents a reusable test configuration
type TestFixture struct {
	Name           string  // Name of the fixture
	Description    string  // What this fixture tests
	Content        string  // TOML content
	ExpectError    string  // Expected error message (empty if valid)
	ExpectedConfig *Config // Expected configuration after processing (nil for error cases)
}

// GetTestFixtures returns all available test fixtures
func GetTestFixtures() []TestFixture {
	// Helper to create bool pointers
	boolPtr := func(b bool) *bool { return &b }

	return []TestFixture{
		{
			Name:        "minimal_valid",
			Description: "Minimal valid configuration with one service",
			Content: `
[tailscale]
oauth_client_id = "test-client-id"
oauth_client_secret = "test-client-secret"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`,
			ExpectedConfig: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-client-id",
					OAuthClientSecret: "test-client-secret",
				},
				Services: []Service{
					{
						Name:        "test-service",
						BackendAddr: "localhost:8080",
					},
				},
			},
		},
		{
			Name:        "full_featured",
			Description: "Full-featured configuration with global settings and multiple services",
			Content: `
[tailscale]
oauth_client_id = "prod-client-id"
oauth_client_secret = "prod-client-secret"

[global]
read_timeout = "30s"
write_timeout = "30s"
idle_timeout = "120s"
shutdown_timeout = "30s"
response_header_timeout = "10s"
metrics_addr = ":9090"
access_log = true
trusted_proxies = ["10.0.0.0/8", "172.16.0.0/12"]

[[services]]
name = "api"
backend_addr = "localhost:8080"
whois_enabled = true
whois_timeout = "5s"
tls_mode = "off"
read_timeout = "60s"
write_timeout = "60s"
access_log = false
funnel_enabled = true
ephemeral = false

[[services.upstream_headers]]
name = "X-Custom-Header"
value = "custom-value"

[[services]]
name = "web"
backend_addr = "localhost:3000"
whois_enabled = false
`,
			ExpectedConfig: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "prod-client-id",
					OAuthClientSecret: "prod-client-secret",
				},
				Global: Global{
					ReadTimeout:           Duration{30 * time.Second},
					WriteTimeout:          Duration{30 * time.Second},
					IdleTimeout:           Duration{120 * time.Second},
					ShutdownTimeout:       Duration{30 * time.Second},
					ResponseHeaderTimeout: Duration{10 * time.Second},
					MetricsAddr:           ":9090",
					AccessLog:             boolPtr(true),
					TrustedProxies:        []string{"10.0.0.0/8", "172.16.0.0/12"},
				},
				Services: []Service{
					{
						Name:          "api",
						BackendAddr:   "localhost:8080",
						WhoisEnabled:  boolPtr(true),
						WhoisTimeout:  Duration{5 * time.Second},
						TLSMode:       "off",
						ReadTimeout:   Duration{60 * time.Second},
						WriteTimeout:  Duration{60 * time.Second},
						AccessLog:     boolPtr(false),
						FunnelEnabled: boolPtr(true),
						Ephemeral:     false,
						UpstreamHeaders: map[string]string{
							"X-Custom-Header": "custom-value",
						},
					},
					{
						Name:         "web",
						BackendAddr:  "localhost:3000",
						WhoisEnabled: boolPtr(false),
					},
				},
			},
		},
		{
			Name:        "env_secret_resolution",
			Description: "Configuration using environment variable for secret",
			Content: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret_env = "TEST_OAUTH_SECRET"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`,
			ExpectedConfig: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "secret-from-env", // Assumes TEST_OAUTH_SECRET is set
				},
				Services: []Service{
					{
						Name:        "test-service",
						BackendAddr: "localhost:8080",
					},
				},
			},
		},
		{
			Name:        "unix_socket_backend",
			Description: "Service using Unix socket backend",
			Content: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "unix-service"
backend_addr = "unix:///var/run/app.sock"
`,
			ExpectedConfig: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Services: []Service{
					{
						Name:        "unix-service",
						BackendAddr: "unix:///var/run/app.sock",
					},
				},
			},
		},
		// Error cases
		{
			Name:        "missing_oauth",
			Description: "Missing OAuth credentials",
			Content: `
[tailscale]
# missing oauth credentials

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`,
			ExpectError: "oauth_client_id and oauth_client_secret are required",
		},
		{
			Name:        "missing_backend_addr",
			Description: "Service missing backend address",
			Content: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test-service"
# missing backend_addr
`,
			ExpectError: "backend_addr is required",
		},
		{
			Name:        "duplicate_service_names",
			Description: "Multiple services with same name",
			Content: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "duplicate"
backend_addr = "localhost:8080"

[[services]]
name = "duplicate"
backend_addr = "localhost:8081"
`,
			ExpectError: "duplicate service name",
		},
		{
			Name:        "invalid_backend_format",
			Description: "Invalid backend address format",
			Content: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test-service"
backend_addr = "not-a-valid-address"
`,
			ExpectError: "invalid backend address",
		},
		{
			Name:        "invalid_unix_socket",
			Description: "Invalid Unix socket path",
			Content: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test-service"
backend_addr = "unix://"
`,
			ExpectError: "invalid unix socket address",
		},
	}
}

// ParseConfigFromString parses TOML configuration from a string
func ParseConfigFromString(content string) (*Config, error) {
	// Create a temporary file to use with file provider
	tmpFile, err := os.CreateTemp("", "test-config-*.toml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())

	// Write content to temp file
	if _, err := tmpFile.WriteString(content); err != nil {
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}

	// Use the FileProvider to load the config
	provider := NewFileProvider(tmpFile.Name())
	return provider.Load(context.Background())
}
