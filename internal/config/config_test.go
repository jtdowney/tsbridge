package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a Duration with IsSet=true
func makeDuration(d time.Duration) Duration {
	return Duration{Duration: d, IsSet: true}
}

func TestLoad(t *testing.T) {
	// Focus on testing our custom logic, not library functionality

	t.Run("empty config path returns error", func(t *testing.T) {
		_, err := Load("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config path cannot be empty")
	})

	t.Run("nonexistent config file returns error", func(t *testing.T) {
		_, err := Load("/nonexistent/config.toml")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "loading config file")
	})

	t.Run("malformed TOML returns error", func(t *testing.T) {
		tmpfile, err := os.CreateTemp("", "invalid-*.toml")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.WriteString("invalid toml [[[")
		require.NoError(t, err)
		tmpfile.Close()

		_, err = Load(tmpfile.Name())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "loading config file")
	})

	// Test secret resolution from env vars
	t.Run("resolve secrets from environment variables", func(t *testing.T) {
		configContent := `
[tailscale]
oauth_client_id_env = "CUSTOM_CLIENT_ID"
oauth_client_secret_env = "CUSTOM_SECRET"

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		// Set the custom environment variables
		t.Setenv("CUSTOM_CLIENT_ID", "resolved-client-id")
		t.Setenv("CUSTOM_SECRET", "resolved-secret")

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Secrets should be resolved from the specified env vars
		assert.Equal(t, "resolved-client-id", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "resolved-secret", cfg.Tailscale.OAuthClientSecret)
	})

	// Test secret resolution from files
	t.Run("resolve secrets from files", func(t *testing.T) {
		tmpDir := t.TempDir()
		clientIDFile := filepath.Join(tmpDir, "client-id.txt")
		secretFile := filepath.Join(tmpDir, "secret.txt")

		require.NoError(t, os.WriteFile(clientIDFile, []byte("file-client-id"), 0600))
		require.NoError(t, os.WriteFile(secretFile, []byte("file-secret"), 0600))

		configContent := `
[tailscale]
oauth_client_id_file = "` + clientIDFile + `"
oauth_client_secret_file = "` + secretFile + `"

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(tmpDir, "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Secrets should be resolved from files
		assert.Equal(t, "file-client-id", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "file-secret", cfg.Tailscale.OAuthClientSecret)
	})

	// Test default values
	t.Run("applies default values", func(t *testing.T) {
		configContent := `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Check that defaults are applied
		assert.Equal(t, 30*time.Second, cfg.Global.ReadHeaderTimeout.Duration)
		assert.Equal(t, 30*time.Second, cfg.Global.WriteTimeout.Duration)
		assert.Equal(t, 120*time.Second, cfg.Global.IdleTimeout.Duration)
		assert.Equal(t, 30*time.Second, cfg.Global.ShutdownTimeout.Duration)

		// Service defaults
		svc := cfg.Services[0]
		assert.NotNil(t, svc.WhoisEnabled)
		assert.False(t, *svc.WhoisEnabled)
		assert.Equal(t, 5*time.Second, svc.WhoisTimeout.Duration)
		assert.Equal(t, "auto", svc.TLSMode)
	})

	// Test ReadHeaderTimeout configuration
	t.Run("ReadHeaderTimeout configuration", func(t *testing.T) {
		configContent := `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[global]
read_header_timeout = "60s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
read_header_timeout = "90s"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Check ReadHeaderTimeout values
		assert.Equal(t, 60*time.Second, cfg.Global.ReadHeaderTimeout.Duration)
		assert.Equal(t, 90*time.Second, cfg.Services[0].ReadHeaderTimeout.Duration)
	})

	// Test ReadHeaderTimeout environment variable override
	t.Run("ReadHeaderTimeout environment override", func(t *testing.T) {
		configContent := `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		t.Setenv("TSBRIDGE_GLOBAL_READ_HEADER_TIMEOUT", "120s")

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Environment variable should override config file
		assert.Equal(t, 120*time.Second, cfg.Global.ReadHeaderTimeout.Duration)
	})

	// Test empty file path validation
	t.Run("handles empty file path", func(t *testing.T) {
		_, err := Load("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config path cannot be empty")
	})

	// Test priority: inline > env > file > fallback
	t.Run("respects secret priority order", func(t *testing.T) {
		tmpDir := t.TempDir()
		secretFile := filepath.Join(tmpDir, "secret.txt")
		require.NoError(t, os.WriteFile(secretFile, []byte("file-secret"), 0600))

		configContent := `
[tailscale]
# Test different priority combinations for OAuth
oauth_client_id = "inline-id"  # Should win over env/file
oauth_client_secret_env = "CUSTOM_SECRET"  # Should use env value
# Note: we're not setting auth_key to avoid conflicts

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(tmpDir, "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		// Set environment variables
		t.Setenv("CUSTOM_SECRET", "env-secret")
		t.Setenv("TS_OAUTH_CLIENT_SECRET", "fallback-secret") // Should not be used

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, "inline-id", cfg.Tailscale.OAuthClientID)      // Inline wins
		assert.Equal(t, "env-secret", cfg.Tailscale.OAuthClientSecret) // Env specified
	})

	t.Run("OAuth secret resolution from files", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create secret files
		clientIDFile := filepath.Join(tmpDir, "client_id.txt")
		clientSecretFile := filepath.Join(tmpDir, "client_secret.txt")

		require.NoError(t, os.WriteFile(clientIDFile, []byte("file-client-id"), 0600))
		require.NoError(t, os.WriteFile(clientSecretFile, []byte("file-client-secret"), 0600))

		configContent := `
[tailscale]
oauth_client_id_file = "` + clientIDFile + `"
oauth_client_secret_file = "` + clientSecretFile + `"

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(tmpDir, "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, "file-client-id", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "file-client-secret", cfg.Tailscale.OAuthClientSecret)
	})

	t.Run("auth key resolution from file", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create auth key file
		authKeyFile := filepath.Join(tmpDir, "auth_key.txt")
		require.NoError(t, os.WriteFile(authKeyFile, []byte("file-auth-key"), 0600))

		configContent := `
[tailscale]
auth_key_file = "` + authKeyFile + `"

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(tmpDir, "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, "file-auth-key", cfg.Tailscale.AuthKey)
	})

	t.Run("fallback to TS_ OAuth environment variables", func(t *testing.T) {
		configContent := `
[tailscale]
# No secrets configured at all

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		// Set fallback OAuth environment variables (not auth key to avoid conflict)
		t.Setenv("TS_OAUTH_CLIENT_ID", "fallback-client-id")
		t.Setenv("TS_OAUTH_CLIENT_SECRET", "fallback-client-secret")

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, "fallback-client-id", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "fallback-client-secret", cfg.Tailscale.OAuthClientSecret)
	})

	t.Run("fallback to TS_AUTHKEY environment variable", func(t *testing.T) {
		configContent := `
[tailscale]
# No secrets configured at all

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		// Set fallback auth key environment variable only
		t.Setenv("TS_AUTHKEY", "fallback-auth-key")

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, "fallback-auth-key", cfg.Tailscale.AuthKey)
	})

	t.Run("TSBRIDGE_ environment variables override config", func(t *testing.T) {
		configContent := `
[tailscale]
oauth_client_id = "config-id"
oauth_client_secret = "config-secret"

[global]
read_header_timeout = "30s"
write_timeout = "40s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		// Set TSBRIDGE_ environment variables to override
		t.Setenv("TSBRIDGE_TAILSCALE_OAUTH_CLIENT_ID", "tsbridge-override-id")
		t.Setenv("TSBRIDGE_GLOBAL_READ_HEADER_TIMEOUT", "60s")

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, "tsbridge-override-id", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "config-secret", cfg.Tailscale.OAuthClientSecret) // Not overridden
		assert.Equal(t, 60*time.Second, cfg.Global.ReadHeaderTimeout.Duration)
		assert.Equal(t, 40*time.Second, cfg.Global.WriteTimeout.Duration) // Not overridden
	})

	t.Run("validation error propagates", func(t *testing.T) {
		configContent := `
[tailscale]
# No auth configuration at all

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		_, err := Load(tmpFile)
		assert.Error(t, err)
		// The error message now mentions OAuth client ID specifically
		assert.Contains(t, err.Error(), "OAuth client ID must be provided")
	})

	t.Run("error from secret file resolution", func(t *testing.T) {
		configContent := `
[tailscale]
oauth_client_id_file = "/nonexistent/file.txt"

[global]
read_header_timeout = "30s"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`

		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		_, err := Load(tmpFile)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resolving OAuth client ID")
	})
}
func TestResolveSecrets(t *testing.T) {
	t.Run("resolves OAuth client ID from env", func(t *testing.T) {
		t.Setenv("TEST_OAUTH_ID", "test-id")
		cfg := &Config{
			Tailscale: Tailscale{
				OAuthClientIDEnv: "TEST_OAUTH_ID",
			},
		}

		err := resolveSecrets(cfg)
		if err != nil {
			t.Fatalf("resolveSecrets() error = %v", err)
		}

		if cfg.Tailscale.OAuthClientID != "test-id" {
			t.Errorf("OAuthClientID = %v, want %v", cfg.Tailscale.OAuthClientID, "test-id")
		}
	})

	t.Run("resolves OAuth client secret from file", func(t *testing.T) {
		secretFile := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(secretFile, []byte("test-secret"), 0600); err != nil {
			t.Fatal(err)
		}

		cfg := &Config{
			Tailscale: Tailscale{
				OAuthClientSecretFile: secretFile,
			},
		}

		err := resolveSecrets(cfg)
		if err != nil {
			t.Fatalf("resolveSecrets() error = %v", err)
		}

		if cfg.Tailscale.OAuthClientSecret != "test-secret" {
			t.Errorf("OAuthClientSecret = %v, want %v", cfg.Tailscale.OAuthClientSecret, "test-secret")
		}
	})

	t.Run("resolves auth key with fallback env", func(t *testing.T) {
		t.Setenv("TS_AUTHKEY", "fallback-key")
		cfg := &Config{
			Tailscale: Tailscale{},
		}

		err := resolveSecrets(cfg)
		if err != nil {
			t.Fatalf("resolveSecrets() error = %v", err)
		}

		if cfg.Tailscale.AuthKey != "fallback-key" {
			t.Errorf("AuthKey = %v, want %v", cfg.Tailscale.AuthKey, "fallback-key")
		}
	})

	t.Run("clears direct values when env/file sources are set", func(t *testing.T) {
		t.Setenv("TEST_OAUTH_ID", "env-id")
		cfg := &Config{
			Tailscale: Tailscale{
				OAuthClientID:    "direct-id",
				OAuthClientIDEnv: "TEST_OAUTH_ID",
			},
		}

		err := resolveSecrets(cfg)
		if err != nil {
			t.Fatalf("resolveSecrets() error = %v", err)
		}

		if cfg.Tailscale.OAuthClientID != "env-id" {
			t.Errorf("OAuthClientID = %v, want %v", cfg.Tailscale.OAuthClientID, "env-id")
		}
	})
}
func TestValidateAuthKeySources(t *testing.T) {
	tests := []struct {
		name      string
		tailscale Tailscale
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid AuthKey",
			tailscale: Tailscale{
				AuthKey: "test-auth-key",
			},
			wantErr: false,
		},
		{
			name: "AuthKey with oauth_tags",
			tailscale: Tailscale{
				AuthKey:   "test-auth-key",
				OAuthTags: []string{"tag:tsbridge"},
			},
			wantErr: true,
			errMsg:  "oauth_tags can only be used with OAuth authentication",
		},
		{
			name: "empty AuthKey is valid",
			tailscale: Tailscale{
				AuthKey: "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuthKeySources(tt.tailscale)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAuthKeySources() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateAuthKeySources() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

func TestValidateOAuthSources(t *testing.T) {
	tests := []struct {
		name      string
		tailscale Tailscale
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid OAuth credentials",
			tailscale: Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: "test-secret",
			},
			wantErr: false,
		},
		{
			name: "missing OAuth client ID",
			tailscale: Tailscale{
				OAuthClientID:     "",
				OAuthClientSecret: "test-secret",
			},
			wantErr: true,
			errMsg:  "OAuth client ID must be provided",
		},
		{
			name: "missing OAuth client secret",
			tailscale: Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: "",
			},
			wantErr: true,
			errMsg:  "OAuth client secret must be provided",
		},
		{
			name: "OAuth with tags",
			tailscale: Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: "test-secret",
				OAuthTags:         []string{"tag:tsbridge"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOAuthSources(tt.tailscale)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateOAuthSources() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateOAuthSources() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

func TestValidateAuthMethodSelection(t *testing.T) {
	tests := []struct {
		name      string
		tailscale Tailscale
		wantErr   bool
		errMsg    string
	}{
		{
			name: "OAuth only",
			tailscale: Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: "test-secret",
			},
			wantErr: false,
		},
		{
			name: "AuthKey only",
			tailscale: Tailscale{
				AuthKey: "test-auth-key",
			},
			wantErr: false,
		},
		{
			name: "both OAuth and AuthKey",
			tailscale: Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: "test-secret",
				AuthKey:           "test-auth-key",
			},
			wantErr: true,
			errMsg:  "cannot specify both OAuth and AuthKey credentials",
		},
		{
			name: "partial OAuth with AuthKey",
			tailscale: Tailscale{
				OAuthClientID: "test-id",
				AuthKey:       "test-auth-key",
			},
			wantErr: true,
			errMsg:  "cannot specify both OAuth and AuthKey credentials",
		},
		{
			name:      "no auth method",
			tailscale: Tailscale{},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuthMethodSelection(tt.tailscale)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAuthMethodSelection() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateAuthMethodSelection() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name    string
		config  *Config
		wantErr string
	}{
		{
			name: "valid config",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:         "api",
						BackendAddr:  "127.0.0.1:8080",
						WhoisEnabled: &trueVal,
						WhoisTimeout: makeDuration(1 * time.Second),
					},
				},
			},
			wantErr: "",
		},
		{
			name: "missing OAuth credentials",
			config: &Config{
				Tailscale: Tailscale{},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "OAuth client ID",
		},
		{
			name: "no services",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{},
			},
			wantErr: "at least one service",
		},
		{
			name: "duplicate service names",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8081",
					},
				},
			},
			wantErr: "duplicate service name",
		},
		{
			name: "service missing name",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "service name is required",
		},
		{
			name: "service missing backend address",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "",
					},
				},
			},
			wantErr: "backend address is required",
		},
		{
			name: "invalid backend address format",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "not-a-valid-address",
					},
				},
			},
			wantErr: "invalid backend address",
		},
		{
			name: "negative durations",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(-5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "read_header_timeout cannot be negative",
		},
		{
			name: "valid unix socket",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:         "api",
						BackendAddr:  "unix:///var/run/api.sock",
						WhoisEnabled: &falseVal,
					},
				},
			},
			wantErr: "",
		},
		{
			name: "invalid metrics address",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
					MetricsAddr:       "not-a-valid-addr",
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "invalid metrics address",
		},
		{
			name: "invalid trusted proxy IP",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
					TrustedProxies:    []string{"invalid-ip"},
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "invalid trusted proxy IP",
		},
		{
			name: "invalid trusted proxy CIDR",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
					TrustedProxies:    []string{"10.0.0.0/33"},
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "invalid trusted proxy CIDR",
		},
		{
			name: "valid trusted proxies",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
					TrustedProxies:    []string{"192.168.1.1", "10.0.0.0/8", "172.16.0.0/12"},
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "",
		},
		{
			name: "OAuth and AuthKey both configured",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
					AuthKey:           "test-auth-key",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "cannot specify both OAuth and AuthKey",
		},
		{
			name: "AuthKey inline",
			config: &Config{
				Tailscale: Tailscale{
					AuthKey: "test-auth-key",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "",
		},
		{
			name: "oauth_tags with OAuth is valid",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
					OAuthTags:         []string{"tag:tsbridge", "tag:role=proxy"},
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "",
		},

		{
			name: "oauth_tags with AuthKey is invalid",
			config: &Config{
				Tailscale: Tailscale{
					AuthKey:   "test-auth-key",
					OAuthTags: []string{"tag:tsbridge"},
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "oauth_tags can only be used with OAuth",
		},
		{
			name: "valid state directory",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
					StateDir:          "/var/lib/tsbridge",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "",
		},
		{
			name: "ephemeral state directory",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
					StateDir:          "",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
					},
				},
			},
			wantErr: "",
		},
		{
			name: "service with funnel enabled",
			config: &Config{
				Tailscale: Tailscale{
					OAuthClientID:     "test-id",
					OAuthClientSecret: "test-secret",
				},
				Global: Global{
					ReadHeaderTimeout: makeDuration(5 * time.Second),
					WriteTimeout:      makeDuration(10 * time.Second),
					IdleTimeout:       makeDuration(120 * time.Second),
					ShutdownTimeout:   makeDuration(15 * time.Second),
				},
				Services: []Service{
					{
						Name:          "api",
						BackendAddr:   "127.0.0.1:8080",
						FunnelEnabled: &trueVal,
					},
				},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() error = %v, wantErr = nil", err)
				}
			} else {
				if err == nil {
					t.Errorf("Validate() error = nil, wantErr containing %q", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Validate() error = %v, should contain %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected *Config
	}{
		{
			name: "service inherits all global timeouts",
			config: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:        "api",
						BackendAddr: "127.0.0.1:8080",
						// No timeouts specified
					},
				},
			},
			expected: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:                  "api",
						BackendAddr:           "127.0.0.1:8080",
						ReadHeaderTimeout:     makeDuration(5 * time.Second),
						WriteTimeout:          makeDuration(10 * time.Second),
						IdleTimeout:           makeDuration(120 * time.Second),
						ResponseHeaderTimeout: makeDuration(30 * time.Second),
					},
				},
			},
		},
		{
			name: "service keeps its own timeouts",
			config: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:                  "api",
						BackendAddr:           "127.0.0.1:8080",
						ReadHeaderTimeout:     makeDuration(15 * time.Second),
						WriteTimeout:          makeDuration(20 * time.Second),
						IdleTimeout:           makeDuration(180 * time.Second),
						ResponseHeaderTimeout: makeDuration(45 * time.Second),
					},
				},
			},
			expected: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:                  "api",
						BackendAddr:           "127.0.0.1:8080",
						ReadHeaderTimeout:     makeDuration(15 * time.Second),
						WriteTimeout:          makeDuration(20 * time.Second),
						IdleTimeout:           makeDuration(180 * time.Second),
						ResponseHeaderTimeout: makeDuration(45 * time.Second),
					},
				},
			},
		},
		{
			name: "service inherits only missing timeouts",
			config: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:              "api",
						BackendAddr:       "127.0.0.1:8080",
						ReadHeaderTimeout: makeDuration(15 * time.Second),
						WriteTimeout:      Duration{Duration: 0, IsSet: false}, // Should inherit from global
						IdleTimeout:       makeDuration(180 * time.Second),
						// ResponseHeaderTimeout not set, should inherit
					},
				},
			},
			expected: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:                  "api",
						BackendAddr:           "127.0.0.1:8080",
						ReadHeaderTimeout:     makeDuration(15 * time.Second),
						WriteTimeout:          makeDuration(10 * time.Second), // Inherited
						IdleTimeout:           makeDuration(180 * time.Second),
						ResponseHeaderTimeout: makeDuration(30 * time.Second), // Inherited
					},
				},
			},
		},
		{
			name: "multiple services normalized correctly",
			config: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:              "api",
						BackendAddr:       "127.0.0.1:8080",
						ReadHeaderTimeout: makeDuration(15 * time.Second),
					},
					{
						Name:         "web",
						BackendAddr:  "127.0.0.1:8081",
						WriteTimeout: makeDuration(25 * time.Second),
					},
				},
			},
			expected: &Config{
				Global: Global{
					ReadHeaderTimeout:     makeDuration(5 * time.Second),
					WriteTimeout:          makeDuration(10 * time.Second),
					IdleTimeout:           makeDuration(120 * time.Second),
					ResponseHeaderTimeout: makeDuration(30 * time.Second),
				},
				Services: []Service{
					{
						Name:                  "api",
						BackendAddr:           "127.0.0.1:8080",
						ReadHeaderTimeout:     makeDuration(15 * time.Second),
						WriteTimeout:          makeDuration(10 * time.Second),  // Inherited
						IdleTimeout:           makeDuration(120 * time.Second), // Inherited
						ResponseHeaderTimeout: makeDuration(30 * time.Second),  // Inherited
					},
					{
						Name:                  "web",
						BackendAddr:           "127.0.0.1:8081",
						ReadHeaderTimeout:     makeDuration(5 * time.Second), // Inherited
						WriteTimeout:          makeDuration(25 * time.Second),
						IdleTimeout:           makeDuration(120 * time.Second), // Inherited
						ResponseHeaderTimeout: makeDuration(30 * time.Second),  // Inherited
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.Normalize()

			// Compare global config (should not change)
			if !reflect.DeepEqual(tt.config.Global, tt.expected.Global) {
				t.Errorf("Global config mismatch:\ngot:  %+v\nwant: %+v", tt.config.Global, tt.expected.Global)
			}

			// Compare services
			if len(tt.config.Services) != len(tt.expected.Services) {
				t.Fatalf("Service count mismatch: got %d, want %d", len(tt.config.Services), len(tt.expected.Services))
			}

			for i, svc := range tt.config.Services {
				expectedSvc := tt.expected.Services[i]
				if svc.Name != expectedSvc.Name {
					t.Errorf("Service[%d] name mismatch: got %q, want %q", i, svc.Name, expectedSvc.Name)
				}
				if svc.BackendAddr != expectedSvc.BackendAddr {
					t.Errorf("Service[%d] backend mismatch: got %q, want %q", i, svc.BackendAddr, expectedSvc.BackendAddr)
				}
				if svc.ReadHeaderTimeout != expectedSvc.ReadHeaderTimeout {
					t.Errorf("Service[%d] ReadHeaderTimeout mismatch: got %v, want %v", i, svc.ReadHeaderTimeout, expectedSvc.ReadHeaderTimeout)
				}
				if svc.WriteTimeout != expectedSvc.WriteTimeout {
					t.Errorf("Service[%d] WriteTimeout mismatch: got %v, want %v", i, svc.WriteTimeout, expectedSvc.WriteTimeout)
				}
				if svc.IdleTimeout != expectedSvc.IdleTimeout {
					t.Errorf("Service[%d] IdleTimeout mismatch: got %v, want %v", i, svc.IdleTimeout, expectedSvc.IdleTimeout)
				}
				if svc.ResponseHeaderTimeout != expectedSvc.ResponseHeaderTimeout {
					t.Errorf("Service[%d] ResponseHeaderTimeout mismatch: got %v, want %v", i, svc.ResponseHeaderTimeout, expectedSvc.ResponseHeaderTimeout)
				}
			}
		})
	}
}
func TestTailscaleString(t *testing.T) {
	tests := []struct {
		name        string
		ts          Tailscale
		contains    []string
		notContains []string
	}{
		{
			name: "redacts oauth client secret",
			ts: Tailscale{
				OAuthClientID:     "client-123",
				OAuthClientSecret: "super-secret-value",
				StateDir:          "/var/lib/tsbridge",
			},
			contains: []string{
				"OAuthClientID: client-123",
				"OAuthClientSecret: [REDACTED]",
				"StateDir: /var/lib/tsbridge",
			},
			notContains: []string{
				"super-secret-value",
			},
		},
		{
			name: "redacts oauth client secret env",
			ts: Tailscale{
				OAuthClientID:        "client-123",
				OAuthClientSecretEnv: "SECRET_ENV_VAR",
				StateDir:             "/var/lib/tsbridge",
			},
			contains: []string{
				"OAuthClientID: client-123",
				"OAuthClientSecretEnv: SECRET_ENV_VAR",
			},
			notContains: []string{},
		},
		{
			name: "redacts oauth client secret file",
			ts: Tailscale{
				OAuthClientID:         "client-123",
				OAuthClientSecretFile: "/path/to/secret/file",
				StateDir:              "/var/lib/tsbridge",
			},
			contains: []string{
				"OAuthClientID: client-123",
				"OAuthClientSecretFile: /path/to/secret/file",
			},
			notContains: []string{},
		},
		{
			name: "redacts auth key",
			ts: Tailscale{
				OAuthClientID: "client-123",
				AuthKey:       "tskey-auth-1234567890",
				StateDir:      "/var/lib/tsbridge",
			},
			contains: []string{
				"AuthKey: [REDACTED]",
			},
			notContains: []string{
				"tskey-auth-1234567890",
			},
		},
		{
			name: "redacts auth key env",
			ts: Tailscale{
				OAuthClientID: "client-123",
				AuthKeyEnv:    "TAILSCALE_AUTH_KEY",
				StateDir:      "/var/lib/tsbridge",
			},
			contains: []string{
				"AuthKeyEnv: TAILSCALE_AUTH_KEY",
			},
			notContains: []string{},
		},
		{
			name: "redacts auth key file",
			ts: Tailscale{
				OAuthClientID: "client-123",
				AuthKeyFile:   "/path/to/auth/key",
				StateDir:      "/var/lib/tsbridge",
			},
			contains: []string{
				"AuthKeyFile: /path/to/auth/key",
			},
			notContains: []string{},
		},
		{
			name: "shows non-sensitive fields",
			ts: Tailscale{
				OAuthClientID:     "client-123",
				OAuthClientSecret: "secret",
				OAuthTags:         []string{"tag:server", "tag:prod"},
				StateDir:          "/var/lib/tsbridge",
			},
			contains: []string{
				"OAuthClientID: client-123",
				"OAuthTags: [tag:server tag:prod]",
				"StateDir: /var/lib/tsbridge",
			},
		},
		{
			name: "handles empty fields gracefully",
			ts:   Tailscale{},
			contains: []string{
				"OAuthClientID: ",
				"OAuthClientSecret: ",
				"StateDir: ",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.ts.String()

			for _, expected := range tt.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("String() result should contain %q, got:\n%s", expected, result)
				}
			}

			for _, notExpected := range tt.notContains {
				if strings.Contains(result, notExpected) {
					t.Errorf("String() result should NOT contain %q, got:\n%s", notExpected, result)
				}
			}
		})
	}
}

func TestConfigString(t *testing.T) {
	cfg := &Config{
		Tailscale: Tailscale{
			OAuthClientID:     "client-123",
			OAuthClientSecret: "super-secret",
			AuthKey:           "tskey-auth-secret",
			StateDir:          "/var/lib/tsbridge",
		},
		Global: Global{
			ReadHeaderTimeout: makeDuration(5 * time.Second),
			WriteTimeout:      makeDuration(10 * time.Second),
			IdleTimeout:       makeDuration(120 * time.Second),
			ShutdownTimeout:   makeDuration(15 * time.Second),
			MetricsAddr:       ":9090",
		},
		Services: []Service{
			{
				Name:         "api",
				BackendAddr:  "127.0.0.1:8080",
				WhoisEnabled: &[]bool{true}[0],
				WhoisTimeout: makeDuration(1 * time.Second),
			},
			{
				Name:        "web",
				BackendAddr: "unix:///var/run/web.sock",
			},
		},
	}

	result := cfg.String()

	// Check that sensitive fields are redacted
	sensitiveValues := []string{
		"super-secret",
		"tskey-auth-secret",
	}
	for _, sensitive := range sensitiveValues {
		if strings.Contains(result, sensitive) {
			t.Errorf("String() result should NOT contain sensitive value %q, got:\n%s", sensitive, result)
		}
	}

	// Check that non-sensitive fields are present
	expectedValues := []string{
		"client-123",        // OAuth client ID should be visible
		"/var/lib/tsbridge", // State dir should be visible
		"5s",                // Timeouts should be visible
		":9090",             // Metrics address should be visible
		"api",               // Service names should be visible
		"127.0.0.1:8080",    // Backend addresses should be visible
		"[REDACTED]",        // Redacted values should show this placeholder
	}
	for _, expected := range expectedValues {
		if !strings.Contains(result, expected) {
			t.Errorf("String() result should contain %q, got:\n%s", expected, result)
		}
	}

	// Ensure the output is properly formatted
	if !strings.Contains(result, "Tailscale:") {
		t.Error("String() result should contain 'Tailscale:' section")
	}
	if !strings.Contains(result, "Global:") {
		t.Error("String() result should contain 'Global:' section")
	}
	if !strings.Contains(result, "Services:") {
		t.Error("String() result should contain 'Services:' section")
	}
}

func TestLoadConfigErrorTypes(t *testing.T) {
	t.Run("non-existent file returns config error", func(t *testing.T) {
		_, err := Load("/nonexistent/file")
		if err == nil {
			t.Fatal("expected error for nonexistent file")
		}
		if !errors.IsConfig(err) {
			t.Errorf("expected config error, got %T", err)
		}
	})

	t.Run("invalid TOML returns config error", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.toml")
		err := os.WriteFile(configPath, []byte("invalid = toml syntax ["), 0600)
		require.NoError(t, err)

		_, err = Load(configPath)
		if err == nil {
			t.Fatal("expected error for invalid TOML")
		}
		if !errors.IsConfig(err) {
			t.Errorf("expected config error, got %T", err)
		}
	})
}

func TestValidateConfigErrorTypes(t *testing.T) {
	t.Run("validation failures return validation error", func(t *testing.T) {
		cfg := &Config{
			Tailscale: Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: "test-secret",
			},
			Global: Global{
				MetricsAddr: ":8080",
			},
			Services: []Service{
				{Name: "", BackendAddr: "http://localhost:3000"},
			},
		}
		cfg.SetDefaults()

		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected validation error")
		}

		if !errors.IsValidation(err) {
			t.Errorf("expected validation error, got %v", err)
		}
	})
}

func TestAccessLoggingConfiguration(t *testing.T) {
	// Test SetDefaults sets access_log to true
	t.Run("default access log enabled", func(t *testing.T) {
		cfg := &Config{}
		cfg.SetDefaults()

		assert.NotNil(t, cfg.Global.AccessLog)
		assert.True(t, *cfg.Global.AccessLog)
	})

	// Test explicit false is preserved
	t.Run("explicit false preserved", func(t *testing.T) {
		accessLogFalse := false
		cfg := &Config{
			Global: Global{
				AccessLog: &accessLogFalse,
			},
		}
		cfg.SetDefaults()

		assert.NotNil(t, cfg.Global.AccessLog)
		assert.False(t, *cfg.Global.AccessLog)
	})
}

func TestFunnelEnabledConfiguration(t *testing.T) {
	// Test funnel enabled config can be loaded
	t.Run("service with funnel enabled", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
funnel_enabled = true
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 1, len(cfg.Services))
		assert.NotNil(t, cfg.Services[0].FunnelEnabled)
		assert.True(t, *cfg.Services[0].FunnelEnabled)
	})

	// Test funnel disabled config
	t.Run("service with funnel disabled", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
funnel_enabled = false
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 1, len(cfg.Services))
		assert.NotNil(t, cfg.Services[0].FunnelEnabled)
		assert.False(t, *cfg.Services[0].FunnelEnabled)
	})

	// Test funnel not specified (nil)
	t.Run("service without funnel config", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 1, len(cfg.Services))
		assert.Nil(t, cfg.Services[0].FunnelEnabled)
	})

	// Test transport timeout configurations
	t.Run("transport timeouts configuration", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"
dial_timeout = "15s"
keep_alive_timeout = "20s"
idle_conn_timeout = "60s"
tls_handshake_timeout = "5s"
expect_continue_timeout = "2s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 15*time.Second, cfg.Global.DialTimeout.Duration)
		assert.Equal(t, 20*time.Second, cfg.Global.KeepAliveTimeout.Duration)
		assert.Equal(t, 60*time.Second, cfg.Global.IdleConnTimeout.Duration)
		assert.Equal(t, 5*time.Second, cfg.Global.TLSHandshakeTimeout.Duration)
		assert.Equal(t, 2*time.Second, cfg.Global.ExpectContinueTimeout.Duration)
	})

	// Test transport timeouts use defaults when not specified
	t.Run("transport timeouts defaults", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Check that defaults are set
		assert.Equal(t, 30*time.Second, cfg.Global.DialTimeout.Duration)
		assert.Equal(t, 30*time.Second, cfg.Global.KeepAliveTimeout.Duration)
		assert.Equal(t, 90*time.Second, cfg.Global.IdleConnTimeout.Duration)
		assert.Equal(t, 10*time.Second, cfg.Global.TLSHandshakeTimeout.Duration)
		assert.Equal(t, 1*time.Second, cfg.Global.ExpectContinueTimeout.Duration)
	})

	// Test metrics server timeout configuration
	t.Run("metrics server timeout configuration", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"
metrics_addr = ":9090"
metrics_read_header_timeout = "10s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 10*time.Second, cfg.Global.MetricsReadHeaderTimeout.Duration)
	})

	// Test metrics server timeout default
	t.Run("metrics server timeout default", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"
metrics_addr = ":9090"

[[services]]
name = "api"
backend_addr = "localhost:8080"
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Check that default is set
		assert.Equal(t, 5*time.Second, cfg.Global.MetricsReadHeaderTimeout.Duration)
	})
}

func TestEphemeralConfiguration(t *testing.T) {
	// Test ephemeral enabled config
	t.Run("service with ephemeral enabled", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
ephemeral = true
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 1, len(cfg.Services))
		assert.True(t, cfg.Services[0].Ephemeral)
	})

	// Test ephemeral disabled config
	t.Run("service with ephemeral disabled", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
ephemeral = false
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 1, len(cfg.Services))
		assert.False(t, cfg.Services[0].Ephemeral)
	})

	// Test ephemeral defaults to false when not specified
	t.Run("service ephemeral defaults to false", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, 1, len(cfg.Services))
		assert.False(t, cfg.Services[0].Ephemeral) // Should default to false
	})
}

func TestProcessLoadedConfig(t *testing.T) {
	t.Run("success - processes config through all stages", func(t *testing.T) {
		cfg := &Config{
			Tailscale: Tailscale{
				AuthKeyFile: filepath.Join(t.TempDir(), "authkey"),
			},
			Global: Global{
				// No need to set anything - defaults will be applied
			},
			Services: []Service{
				{
					Name:        "test",
					BackendAddr: "localhost:8080", // Provide a valid backend
				},
			},
		}

		// Write auth key file
		assert.NoError(t, os.WriteFile(cfg.Tailscale.AuthKeyFile, []byte("test-auth-key"), 0600))

		err := ProcessLoadedConfig(cfg)
		assert.NoError(t, err)

		// Verify secrets were resolved
		if cfg.Tailscale.AuthKey != "test-auth-key" {
			t.Errorf("expected AuthKey to be resolved to 'test-auth-key', got %q", cfg.Tailscale.AuthKey)
		}
		if cfg.Tailscale.AuthKeyFile != "" {
			t.Errorf("expected AuthKeyFile to be cleared, got %q", cfg.Tailscale.AuthKeyFile)
		}

		// Verify defaults were set (ReadHeaderTimeout should have a default)
		if cfg.Global.ReadHeaderTimeout.Duration == 0 {
			t.Error("expected Global.ReadHeaderTimeout to have default value")
		}
	})

	t.Run("error - secret resolution fails", func(t *testing.T) {
		cfg := &Config{
			Tailscale: Tailscale{
				AuthKeyFile: "/nonexistent/file",
			},
		}

		err := ProcessLoadedConfig(cfg)
		assert.Error(t, err)
		if !strings.Contains(err.Error(), "resolving secrets") {
			t.Errorf("expected error to contain 'resolving secrets', got %v", err)
		}
	})

	t.Run("error - validation fails", func(t *testing.T) {
		cfg := &Config{
			Services: []Service{
				{
					Name:        "test",
					BackendAddr: "", // No backend specified and no global default - will fail validation
				},
			},
		}

		err := ProcessLoadedConfig(cfg)
		assert.Error(t, err)
		if !strings.Contains(err.Error(), "validating config") {
			t.Errorf("expected error to contain 'validating config', got %v", err)
		}
	})
}

func TestFlushIntervalConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		configContent  string
		checkGlobal    bool
		checkService   bool
		expectedGlobal time.Duration
		expectedSvc    time.Duration
		wantErr        bool
	}{
		{
			name: "global flush interval",
			configContent: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[global]
flush_interval = "100ms"

[[services]]
name = "test"
backend_addr = "localhost:8080"
`,
			checkGlobal:    true,
			expectedGlobal: 100 * time.Millisecond,
		},
		{
			name: "service-specific flush interval",
			configContent: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test"
backend_addr = "localhost:8080"
flush_interval = "200ms"
`,
			checkService: true,
			expectedSvc:  200 * time.Millisecond,
		},
		{
			name: "service inherits global flush interval",
			configContent: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[global]
flush_interval = "300ms"

[[services]]
name = "test"
backend_addr = "localhost:8080"
`,
			checkService: true,
			expectedSvc:  300 * time.Millisecond,
		},
		{
			name: "service overrides global flush interval",
			configContent: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[global]
flush_interval = "300ms"

[[services]]
name = "test"
backend_addr = "localhost:8080"
flush_interval = "50ms"
`,
			checkGlobal:    true,
			checkService:   true,
			expectedGlobal: 300 * time.Millisecond,
			expectedSvc:    50 * time.Millisecond,
		},
		{
			name: "negative flush interval for immediate flushing",
			configContent: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "streaming"
backend_addr = "localhost:8080"
flush_interval = "-1ms"
`,
			checkService: true,
			expectedSvc:  -1 * time.Millisecond,
		},
		{
			name: "zero flush interval",
			configContent: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test"
backend_addr = "localhost:8080"
flush_interval = "0s"
`,
			checkService: true,
			expectedSvc:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file with config
			tmpFile := filepath.Join(t.TempDir(), "config.toml")
			err := os.WriteFile(tmpFile, []byte(tt.configContent), 0644)
			require.NoError(t, err)

			// Load config
			cfg, err := Load(tmpFile)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Check global flush interval
			if tt.checkGlobal {
				assert.Equal(t, tt.expectedGlobal, cfg.Global.FlushInterval.Duration,
					"global flush interval mismatch")
			}

			// Check service flush interval
			if tt.checkService && len(cfg.Services) > 0 {
				assert.Equal(t, tt.expectedSvc, cfg.Services[0].FlushInterval.Duration,
					"service flush interval mismatch")
			}
		})
	}
}

func TestFlushIntervalNormalization(t *testing.T) {
	cfg := &Config{
		Global: Global{
			FlushInterval: makeDuration(100 * time.Millisecond),
		},
		Services: []Service{
			{
				Name:          "with-override",
				BackendAddr:   "localhost:8080",
				FlushInterval: makeDuration(50 * time.Millisecond),
			},
			{
				Name:        "without-override",
				BackendAddr: "localhost:8081",
				// FlushInterval not set, should inherit from global
			},
		},
	}

	// Apply defaults and normalization
	cfg.SetDefaults()
	cfg.Normalize()

	// Service with override should keep its value
	assert.Equal(t, 50*time.Millisecond, cfg.Services[0].FlushInterval.Duration)

	// Service without override should inherit global value
	assert.Equal(t, 100*time.Millisecond, cfg.Services[1].FlushInterval.Duration)
}

func TestZeroDurationHandling(t *testing.T) {
	// Test that "0s" in TOML results in Duration{0, true}
	t.Run("explicit zero duration in TOML", func(t *testing.T) {
		configContent := `
[tailscale]
auth_key = "test-key"

[global]
read_header_timeout = "30s"
write_timeout = "0s"

[[services]]
name = "api"
backend_addr = "localhost:8080"
`
		tmpFile := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(tmpFile, []byte(configContent), 0644))

		cfg, err := Load(tmpFile)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Check that write_timeout is 0 and IsSet is true
		assert.Equal(t, time.Duration(0), cfg.Global.WriteTimeout.Duration)
		assert.True(t, cfg.Global.WriteTimeout.IsSet)
	})

	// Test that missing value results in Duration{0, false}
	t.Run("missing duration in TOML", func(t *testing.T) {
		// Create a config directly without loading from file to test pre-SetDefaults state
		cfg := &Config{
			Global: Global{
				ReadHeaderTimeout: Duration{Duration: 30 * time.Second, IsSet: true},
				// WriteTimeout not set, should be Duration{0, false}
			},
		}

		// Before SetDefaults, write_timeout should be Duration{0, false}
		assert.Equal(t, time.Duration(0), cfg.Global.WriteTimeout.Duration)
		assert.False(t, cfg.Global.WriteTimeout.IsSet)

		// After SetDefaults, it should have the default value with IsSet=true
		cfg.SetDefaults()
		assert.Equal(t, 30*time.Second, cfg.Global.WriteTimeout.Duration)
		assert.True(t, cfg.Global.WriteTimeout.IsSet)
	})

	// Test that SetDefaults respects IsSet flag
	t.Run("SetDefaults respects IsSet flag", func(t *testing.T) {
		// Test case 1: When IsSet is false, should apply default
		cfg1 := &Config{
			Global: Global{
				WriteTimeout: Duration{Duration: 0, IsSet: false},
			},
		}
		cfg1.SetDefaults()
		assert.Equal(t, 30*time.Second, cfg1.Global.WriteTimeout.Duration)
		assert.True(t, cfg1.Global.WriteTimeout.IsSet)

		// Test case 2: When IsSet is true with 0 duration, should keep 0
		cfg2 := &Config{
			Global: Global{
				WriteTimeout: Duration{Duration: 0, IsSet: true},
			},
		}
		cfg2.SetDefaults()
		assert.Equal(t, time.Duration(0), cfg2.Global.WriteTimeout.Duration)
		assert.True(t, cfg2.Global.WriteTimeout.IsSet)
	})

	// Test that Normalize respects IsSet flag
	t.Run("Normalize respects IsSet flag", func(t *testing.T) {
		// Test case 1: Service inherits global when not set
		cfg1 := &Config{
			Global: Global{
				WriteTimeout: Duration{Duration: 60 * time.Second, IsSet: true},
			},
			Services: []Service{
				{
					Name:         "test",
					BackendAddr:  "localhost:8080",
					WriteTimeout: Duration{Duration: 0, IsSet: false},
				},
			},
		}
		cfg1.Normalize()
		assert.Equal(t, 60*time.Second, cfg1.Services[0].WriteTimeout.Duration)
		assert.True(t, cfg1.Services[0].WriteTimeout.IsSet)

		// Test case 2: Service keeps its own value when IsSet is true
		cfg2 := &Config{
			Global: Global{
				WriteTimeout: Duration{Duration: 60 * time.Second, IsSet: true},
			},
			Services: []Service{
				{
					Name:         "test",
					BackendAddr:  "localhost:8080",
					WriteTimeout: Duration{Duration: 0, IsSet: true},
				},
			},
		}
		cfg2.Normalize()
		assert.Equal(t, time.Duration(0), cfg2.Services[0].WriteTimeout.Duration)
		assert.True(t, cfg2.Services[0].WriteTimeout.IsSet)
	})
}
