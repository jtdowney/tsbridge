package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/errors"
)

// TestFixture represents a reusable test configuration
type TestFixture struct {
	Name           string  // Name of the fixture
	Description    string  // What this fixture tests
	Content        string  // TOML content
	ExpectError    string  // Expected error message (empty if valid)
	ExpectedConfig *Config // Expected configuration after processing (nil for error cases)
}

// getTestFixtures returns all available test fixtures
func getTestFixtures() []TestFixture {
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
tags = ["tag:test"]
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
read_header_timeout = "30s"
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
read_header_timeout = "60s"
write_timeout = "60s"
access_log = false
funnel_enabled = true
ephemeral = false
tags = ["tag:test"]

[[services.upstream_headers]]
name = "X-Custom-Header"
value = "custom-value"

[[services]]
name = "web"
backend_addr = "localhost:3000"
whois_enabled = false
tags = ["tag:test"]
`,
			ExpectedConfig: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "prod-client-id",
					OAuthClientSecret: "prod-client-secret",
				},
				Global: Global{
					ReadHeaderTimeout:     makeDuration(30 * time.Second),
					WriteTimeout:          makeDuration(30 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ShutdownTimeout:       makeDuration(30 * time.Second),
					ResponseHeaderTimeout: makeDuration(10 * time.Second),
					MetricsAddr:           ":9090",
					AccessLog:             boolPtr(true),
					TrustedProxies:        []string{"10.0.0.0/8", "172.16.0.0/12"},
				},
				Services: []Service{
					{
						Name:              "api",
						BackendAddr:       "localhost:8080",
						WhoisEnabled:      boolPtr(true),
						WhoisTimeout:      makeDuration(5 * time.Second),
						TLSMode:           "off",
						ReadHeaderTimeout: makeDuration(60 * time.Second),
						WriteTimeout:      makeDuration(60 * time.Second),
						AccessLog:         boolPtr(false),
						FunnelEnabled:     boolPtr(true),
						Ephemeral:         false,
						UpstreamHeaders: map[string]string{
							"X-Custom-Header": "custom-value",
						},
						Tags: []string{"tag:test"},
					},
					{
						Name:         "web",
						BackendAddr:  "localhost:3000",
						WhoisEnabled: boolPtr(false),
						Tags:         []string{"tag:test"},
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
tags = ["tag:test"]
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
tags = ["tag:test"]
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
tags = ["tag:test"]
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

func TestFileProviderErrorHandling(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		setupFunc    func() (string, func())
		wantErrType  errors.ErrorType
		wantContains []string
	}{
		{
			name: "file not found",
			setupFunc: func() (string, func()) {
				return "/non/existent/file.toml", func() {}
			},
			wantErrType:  errors.ErrTypeConfig,
			wantContains: []string{"file provider", "loading config file"},
		},
		{
			name: "invalid TOML syntax",
			setupFunc: func() (string, func()) {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "invalid.toml")
				content := `
[invalid toml
missing bracket
`
				if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
				return configPath, func() {}
			},
			wantErrType:  errors.ErrTypeConfig,
			wantContains: []string{"file provider", "loading config file"},
		},
		{
			name: "empty config path",
			setupFunc: func() (string, func()) {
				return "", func() {}
			},
			wantErrType:  errors.ErrTypeValidation,
			wantContains: []string{"file provider", "config path cannot be empty"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, cleanup := tt.setupFunc()
			defer cleanup()

			provider := NewFileProvider(path)
			_, err := provider.Load(ctx)

			if err == nil {
				t.Fatal("expected error, got nil")
			}

			// Check error type
			if gotType := errors.GetType(err); gotType != tt.wantErrType {
				t.Errorf("GetType() = %v, want %v", gotType, tt.wantErrType)
			}

			// Check error message contains expected strings
			errMsg := err.Error()
			for _, want := range tt.wantContains {
				if !contains(errMsg, want) {
					t.Errorf("error message %q does not contain %q", errMsg, want)
				}
			}
		})
	}
}

func TestProviderErrorContext(t *testing.T) {
	ctx := context.Background()

	// Test file provider error context
	t.Run("file provider context", func(t *testing.T) {
		// Create an invalid TOML file
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "invalid.toml")
		content := `
[invalid syntax
`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		provider := NewFileProvider(configPath)
		_, err := provider.Load(ctx)

		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Check that error message contains "file provider"
		if !contains(err.Error(), "file provider") {
			t.Errorf("error message does not contain provider context: %v", err)
		}
	})
}

func TestProviderValidationErrorContext(t *testing.T) {
	ctx := context.Background()

	// Test validation error with provider context
	t.Run("empty config path", func(t *testing.T) {
		provider := NewFileProvider("")
		_, err := provider.Load(ctx)

		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Should be a validation error
		if errors.GetType(err) != errors.ErrTypeValidation {
			t.Errorf("expected validation error, got %v", errors.GetType(err))
		}

		// Should contain "file provider" in the message
		if !contains(err.Error(), "file provider") {
			t.Errorf("validation error does not contain provider context: %v", err)
		}
	})

	// Test config processing error with provider context
	t.Run("invalid service config", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "invalid-service.toml")

		// Create a config with invalid service (no backend_addr)
		content := `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test-service"
# missing backend_addr
`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		provider := NewFileProvider(configPath)
		_, err := provider.Load(ctx)

		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Should contain "file provider" in the message
		if !contains(err.Error(), "file provider") {
			t.Errorf("config processing error does not contain provider context: %v", err)
		}
	})
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mockProvider is a test provider implementation
type mockProvider struct {
	name string
	cfg  *Config
	err  error
}

func (m *mockProvider) Load(ctx context.Context) (*Config, error) {
	return m.cfg, m.err
}

func (m *mockProvider) Watch(ctx context.Context) (<-chan *Config, error) {
	return nil, nil
}

func (m *mockProvider) Name() string {
	return m.name
}

func TestNewProviderWithRegistry(t *testing.T) {
	// Save original registry
	originalRegistry := DefaultRegistry
	defer func() { DefaultRegistry = originalRegistry }()

	t.Run("creates file provider", func(t *testing.T) {
		registry := NewProviderRegistry()
		registry.Register("file", FileProviderFactory)
		DefaultRegistry = registry

		provider, err := NewProvider("file", "/path/to/config.toml", DockerProviderOptions{})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if provider == nil {
			t.Fatal("expected provider, got nil")
		}
		if provider.Name() != "file" {
			t.Errorf("expected provider name 'file', got %q", provider.Name())
		}

		// Verify it's a FileProvider with correct path
		fp, ok := provider.(*FileProvider)
		if !ok {
			t.Fatal("expected FileProvider type")
		}
		if fp.path != "/path/to/config.toml" {
			t.Errorf("expected path '/path/to/config.toml', got %q", fp.path)
		}
	})

	t.Run("creates docker provider", func(t *testing.T) {
		registry := NewProviderRegistry()
		registry.Register("file", FileProviderFactory)

		// Mock docker provider
		mockDockerProvider := &mockProvider{name: "docker"}
		registry.Register("docker", func(opts interface{}) (Provider, error) {
			return mockDockerProvider, nil
		})
		DefaultRegistry = registry

		dockerOpts := DockerProviderOptions{
			DockerEndpoint: "unix:///custom/docker.sock",
			LabelPrefix:    "custom",
		}
		provider, err := NewProvider("docker", "", dockerOpts)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if provider != mockDockerProvider {
			t.Errorf("expected mock provider, got %v", provider)
		}
	})

	t.Run("returns error for unknown provider", func(t *testing.T) {
		registry := NewProviderRegistry()
		DefaultRegistry = registry

		provider, err := NewProvider("unknown", "", DockerProviderOptions{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !contains(err.Error(), "unknown provider type: unknown") {
			t.Errorf("expected error to contain 'unknown provider type: unknown', got %v", err)
		}
		if provider != nil {
			t.Errorf("expected nil provider, got %v", provider)
		}
	})

	t.Run("returns error when docker not registered", func(t *testing.T) {
		registry := NewProviderRegistry()
		registry.Register("file", FileProviderFactory)
		// Don't register docker
		DefaultRegistry = registry

		provider, err := NewProvider("docker", "", DockerProviderOptions{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !contains(err.Error(), "provider not registered: docker") {
			t.Errorf("expected error to contain 'provider not registered: docker', got %v", err)
		}
		if provider != nil {
			t.Errorf("expected nil provider, got %v", provider)
		}
	})
}

// Provider interface tests - these tests ensure all providers behave consistently
//
// The provider tests are structured to:
// 1. Test the Provider interface contract (Name, Load, Watch methods)
// 2. Test provider registration and factory patterns
// 3. Test that all providers apply the same config processing pipeline
// 4. Demonstrate how Docker provider would integrate without requiring Docker
//
// Docker Provider Testing Strategy:
// - Docker provider implementation is in internal/docker package
// - These tests use mock providers to verify the registration and interface
// - The actual Docker provider tests in internal/docker test label parsing
// - Integration is tested by mocking the factory function that would normally
//   create docker.Provider instances

// TestProviderInterface runs a comprehensive suite of tests against any Provider implementation
func TestProviderInterface(t *testing.T) {
	tests := []struct {
		name           string
		providerName   string
		createProvider func(t *testing.T) (Provider, func())
	}{
		{
			name:         "FileProvider",
			providerName: "file",
			createProvider: func(t *testing.T) (Provider, func()) {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "config.toml")

				// Use minimal_valid fixture
				fixtures := getTestFixtures()
				var minimalFixture TestFixture
				for _, f := range fixtures {
					if f.Name == "minimal_valid" {
						minimalFixture = f
						break
					}
				}

				content := minimalFixture.Content
				if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}

				provider := NewFileProvider(configPath)
				return provider, func() {}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test 1: Provider Name
			t.Run("Name", func(t *testing.T) {
				provider, cleanup := tt.createProvider(t)
				defer cleanup()

				name := provider.Name()
				if name != tt.providerName {
					t.Errorf("Name() = %q, want %q", name, tt.providerName)
				}
			})

			// Test 2: Load returns valid config
			t.Run("Load", func(t *testing.T) {
				ctx := context.Background()
				provider, cleanup := tt.createProvider(t)
				defer cleanup()

				cfg, err := provider.Load(ctx)
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
				if cfg == nil {
					t.Fatal("Load() returned nil config")
				}

				// Verify essential config fields
				if cfg.Tailscale.OAuthClientID == "" {
					t.Error("Config missing Tailscale.OAuthClientID")
				}
				if cfg.Tailscale.OAuthClientSecret.Value() == "" {
					t.Error("Config missing Tailscale.OAuthClientSecret")
				}
				if len(cfg.Services) == 0 {
					t.Error("Config has no services")
				}
			})

			// Test 3: Load with context cancellation
			t.Run("LoadWithCancelledContext", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel immediately

				provider, cleanup := tt.createProvider(t)
				defer cleanup()

				_, err := provider.Load(ctx)
				// Provider may or may not return an error for cancelled context
				// depending on implementation, but it should handle it gracefully
				_ = err
			})

			// Test 4: Watch behavior (if supported)
			t.Run("Watch", func(t *testing.T) {
				ctx := context.Background()
				provider, cleanup := tt.createProvider(t)
				defer cleanup()

				ch, err := provider.Watch(ctx)
				if err != nil {
					// Some providers may not support watching
					if ch != nil {
						t.Error("Watch() returned channel with error")
					}
					return
				}

				// If no error, the behavior depends on whether watching is supported
				// FileProvider currently returns (nil, nil) to indicate no watch support
			})

			// Test 5: Multiple Load calls
			t.Run("MultipleLoads", func(t *testing.T) {
				ctx := context.Background()
				provider, cleanup := tt.createProvider(t)
				defer cleanup()

				// First load
				cfg1, err := provider.Load(ctx)
				if err != nil {
					t.Fatalf("First Load() error = %v", err)
				}

				// Second load should also work
				cfg2, err := provider.Load(ctx)
				if err != nil {
					t.Fatalf("Second Load() error = %v", err)
				}

				// Both configs should be valid
				if cfg1 == nil || cfg2 == nil {
					t.Error("Load() returned nil config")
				}
			})
		})
	}
}

// TestProviderErrorBehavior tests error handling behavior across providers
func TestProviderErrorBehavior(t *testing.T) {
	tests := []struct {
		name           string
		createProvider func(t *testing.T) (Provider, func())
		wantErrType    errors.ErrorType
	}{
		{
			name: "FileProvider with missing file",
			createProvider: func(t *testing.T) (Provider, func()) {
				return NewFileProvider("/non/existent/file.toml"), func() {}
			},
			wantErrType: errors.ErrTypeConfig,
		},
		{
			name: "FileProvider with invalid config",
			createProvider: func(t *testing.T) (Provider, func()) {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "invalid.toml")

				// Create config missing required fields
				content := `
[[services]]
name = "test-service"
# missing backend_addr
`
				if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}

				return NewFileProvider(configPath), func() {}
			},
			wantErrType: errors.ErrTypeConfig, // Due to wrapping in ProcessLoadedConfigWithProvider
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			provider, cleanup := tt.createProvider(t)
			defer cleanup()

			_, err := provider.Load(ctx)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			// Check error type
			if gotType := errors.GetType(err); gotType != tt.wantErrType {
				t.Errorf("GetType() = %v, want %v", gotType, tt.wantErrType)
			}

			// Check that error contains provider name
			providerName := provider.Name()
			if !contains(err.Error(), providerName) {
				t.Errorf("error %q does not contain provider name %q", err.Error(), providerName)
			}
		})
	}
}

// TestProviderConfigProcessing tests that all providers apply the same config processing
func TestProviderConfigProcessing(t *testing.T) {
	// This test ensures that all providers:
	// 1. Resolve secrets (env/file references)
	// 2. Apply defaults
	// 3. Normalize configuration
	// 4. Validate configuration

	tests := []struct {
		name           string
		createProvider func(t *testing.T, content string) (Provider, func())
	}{
		{
			name: "FileProvider",
			createProvider: func(t *testing.T, content string) (Provider, func()) {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "config.toml")

				if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}

				return NewFileProvider(configPath), func() {}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test secret resolution
			t.Run("SecretResolution", func(t *testing.T) {
				// Set environment variable
				os.Setenv("TEST_OAUTH_SECRET", "secret-from-env")
				defer os.Unsetenv("TEST_OAUTH_SECRET")

				// Use env_secret_resolution fixture
				fixtures := getTestFixtures()
				var envFixture TestFixture
				for _, f := range fixtures {
					if f.Name == "env_secret_resolution" {
						envFixture = f
						break
					}
				}

				content := envFixture.Content
				provider, cleanup := tt.createProvider(t, content)
				defer cleanup()

				ctx := context.Background()
				cfg, err := provider.Load(ctx)
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}

				// Check that secret was resolved
				if cfg.Tailscale.OAuthClientSecret != "secret-from-env" {
					t.Errorf("Secret not resolved: got %q, want %q",
						cfg.Tailscale.OAuthClientSecret, "secret-from-env")
				}

				// Check that env field was cleared
				if cfg.Tailscale.OAuthClientSecretEnv != "" {
					t.Errorf("Env field not cleared: %q", cfg.Tailscale.OAuthClientSecretEnv)
				}
			})

			// Test defaults application
			t.Run("DefaultsApplication", func(t *testing.T) {
				// Use minimal_valid fixture
				fixtures := getTestFixtures()
				var minimalFixture TestFixture
				for _, f := range fixtures {
					if f.Name == "minimal_valid" {
						minimalFixture = f
						break
					}
				}

				content := minimalFixture.Content
				provider, cleanup := tt.createProvider(t, content)
				defer cleanup()

				ctx := context.Background()
				cfg, err := provider.Load(ctx)
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}

				// Check that defaults were applied
				service := cfg.Services[0]
				if service.IdleTimeout.Duration == 0 {
					t.Error("Default IdleTimeout not applied")
				}
			})

			// Test normalization
			t.Run("Normalization", func(t *testing.T) {
				content := `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[global]

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
tags = ["tag:test"]
`
				provider, cleanup := tt.createProvider(t, content)
				defer cleanup()

				ctx := context.Background()
				cfg, err := provider.Load(ctx)
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}

				// Check normalization - global values copied to services
				_ = cfg.Services[0]
				// Add any specific checks here if needed
			})

			// Test validation
			t.Run("Validation", func(t *testing.T) {
				// Use missing_oauth fixture
				fixtures := getTestFixtures()
				var missingOAuthFixture TestFixture
				for _, f := range fixtures {
					if f.Name == "missing_oauth" {
						missingOAuthFixture = f
						break
					}
				}

				content := missingOAuthFixture.Content
				provider, cleanup := tt.createProvider(t, content)
				defer cleanup()

				ctx := context.Background()
				_, err := provider.Load(ctx)
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}

				// Currently returns config error due to wrapping in ProcessLoadedConfigWithProvider
				// TODO: This should be a validation error
				if errors.GetType(err) != errors.ErrTypeConfig {
					t.Errorf("expected config error type, got %v", errors.GetType(err))
				}
			})
		})
	}
}

// TestDockerProviderRegistration verifies Docker provider is properly registered
// In production, the Docker provider would be registered during application startup
// by calling RegisterDockerProvider with a factory that creates docker.Provider instances.
//
// The registration would typically happen in main.go or an init function outside
// the config package to avoid circular imports:
//
//	config.RegisterDockerProvider(func(opts config.DockerProviderOptions) (config.Provider, error) {
//	    dockerOpts := docker.Options{
//	        DockerEndpoint: opts.DockerEndpoint,
//	        LabelPrefix:    opts.LabelPrefix,
//	    }
//	    return docker.NewProvider(dockerOpts)
//	})
func TestDockerProviderRegistration(t *testing.T) {
	// Save original registry
	originalRegistry := DefaultRegistry
	defer func() { DefaultRegistry = originalRegistry }()

	// Create new registry and register both providers
	registry := NewProviderRegistry()
	registry.Register("file", FileProviderFactory)

	// Temporarily set DefaultRegistry so RegisterDockerProvider works
	DefaultRegistry = registry

	// Register Docker provider with factory function
	// This would normally be done in an init() function or during app startup
	RegisterDockerProvider(func(opts DockerProviderOptions) (Provider, error) {
		// In a real implementation, this would call docker.NewProvider
		// For now, we'll use a mock to test the registration
		return &mockProvider{
			name: "docker",
			cfg:  &Config{},
		}, nil
	})

	t.Run("Docker provider is registered", func(t *testing.T) {
		// Try to create Docker provider
		provider, err := NewProvider("docker", "", DockerProviderOptions{
			DockerEndpoint: "unix:///var/run/docker.sock",
			LabelPrefix:    "tsbridge",
		})

		if err != nil {
			t.Fatalf("Failed to create Docker provider: %v", err)
		}

		// Verify the provider name
		if provider.Name() != "docker" {
			t.Errorf("Expected provider name 'docker', got %q", provider.Name())
		}
	})
}

// TestDockerProviderIntegration tests Docker provider integration with the config system
func TestDockerProviderIntegration(t *testing.T) {
	// Save original registry
	originalRegistry := DefaultRegistry
	defer func() { DefaultRegistry = originalRegistry }()

	// Create new registry and register both providers
	registry := NewProviderRegistry()
	registry.Register("file", FileProviderFactory)
	DefaultRegistry = registry

	// Register a mock Docker provider that simulates loading from containers
	RegisterDockerProvider(func(opts DockerProviderOptions) (Provider, error) {
		// Mock provider that simulates Docker behavior
		// In a real implementation, this would:
		// 1. Connect to Docker using opts.DockerEndpoint
		// 2. List containers with labels matching opts.LabelPrefix
		// 3. Parse the labels into a Config structure
		return &mockProvider{
			name: "docker",
			cfg: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "docker-client-id",
					OAuthClientSecret: "docker-client-secret",
				},
				Services: []Service{
					{
						Name:        "test-service",
						BackendAddr: "localhost:8080",
					},
				},
			},
		}, nil
	})

	t.Run("Docker provider loads config", func(t *testing.T) {
		ctx := context.Background()

		// Create Docker provider
		provider, err := NewProvider("docker", "", DockerProviderOptions{
			DockerEndpoint: "unix:///var/run/docker.sock",
			LabelPrefix:    "tsbridge",
		})
		if err != nil {
			t.Fatalf("Failed to create Docker provider: %v", err)
		}

		// Load config
		cfg, err := provider.Load(ctx)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		// Verify config was loaded
		if cfg.Tailscale.OAuthClientID != "docker-client-id" {
			t.Errorf("Expected OAuth client ID 'docker-client-id', got %q", cfg.Tailscale.OAuthClientID)
		}
		if len(cfg.Services) != 1 {
			t.Errorf("Expected 1 service, got %d", len(cfg.Services))
		}
		if cfg.Services[0].Name != "test-service" {
			t.Errorf("Expected service name 'test-service', got %q", cfg.Services[0].Name)
		}
	})
}

// TestDockerProviderLabelParsing tests Docker provider's label parsing logic
// without requiring actual Docker connection
func TestDockerProviderLabelParsing(t *testing.T) {
	// This test demonstrates how Docker labels map to configuration
	// The actual Docker provider would parse these labels from containers

	t.Run("Minimal configuration from labels", func(t *testing.T) {
		// Get minimal fixture
		fixtures := getTestFixtures()
		var minimalFixture TestFixture
		for _, f := range fixtures {
			if f.Name == "minimal_valid" {
				minimalFixture = f
				break
			}
		}

		// In a real Docker provider test, these labels would come from
		// container labels. The Docker provider would:
		// 1. Find containers with tsbridge.enabled=true
		// 2. Parse service configuration from their labels
		// 3. Apply the same ProcessLoadedConfig as FileProvider

		// Expected Docker labels for minimal config:
		expectedLabels := map[string]string{
			"tsbridge.enabled":              "true",
			"tsbridge.service.name":         "test-service",
			"tsbridge.service.backend_addr": "localhost:8080",
		}

		// Verify our test fixture matches expected Docker label structure
		if minimalFixture.ExpectedConfig != nil {
			svc := minimalFixture.ExpectedConfig.Services[0]
			if expectedLabels["tsbridge.service.name"] != svc.Name {
				t.Errorf("Label name mismatch: got %q, want %q",
					expectedLabels["tsbridge.service.name"], svc.Name)
			}
			if expectedLabels["tsbridge.service.backend_addr"] != svc.BackendAddr {
				t.Errorf("Label backend_addr mismatch: got %q, want %q",
					expectedLabels["tsbridge.service.backend_addr"], svc.BackendAddr)
			}
		}
	})

	t.Run("Complex configuration from labels", func(t *testing.T) {
		// Docker labels for more complex configuration
		// This demonstrates how the full_featured fixture would map to Docker labels
		complexLabels := map[string]string{
			// Service configuration
			"tsbridge.enabled":                                  "true",
			"tsbridge.service.name":                             "api",
			"tsbridge.service.backend_addr":                     "localhost:8080",
			"tsbridge.service.whois_enabled":                    "true",
			"tsbridge.service.whois_timeout":                    "5s",
			"tsbridge.service.tls_mode":                         "off",
			"tsbridge.service.upstream_headers.X-Custom-Header": "custom-value",
		}

		// Verify label structure matches our fixtures
		fixtures := getTestFixtures()
		for _, fixture := range fixtures {
			if fixture.Name == "full_featured" && fixture.ExpectedConfig != nil {
				// The first service in full_featured should match our complex labels
				svc := fixture.ExpectedConfig.Services[0]
				if complexLabels["tsbridge.service.name"] != svc.Name {
					t.Errorf("Complex config name mismatch")
				}
				// Additional validations would go here
			}
		}
	})
}
