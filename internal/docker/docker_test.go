package docker

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLabelValue(t *testing.T) {
	labels := map[string]string{
		"tsbridge.service.name":         "api",
		"tsbridge.service.backend_addr": "localhost:8080",
		"other.label":                   "ignored",
	}

	parser := newLabelParser(labels, "tsbridge")
	assert.Equal(t, "api", parser.getString("service.name"))
	assert.Equal(t, "localhost:8080", parser.getString("service.backend_addr"))
	assert.Equal(t, "", parser.getString("nonexistent"))
}

func TestParseHeaders(t *testing.T) {
	labels := map[string]string{
		"tsbridge.service.upstream_headers.X-Custom-Header": "value1",
		"tsbridge.service.upstream_headers.X-Another":       "value2",
		"tsbridge.service.downstream_headers.X-Response":    "value3",
		"other.label": "ignored",
	}

	parser := newLabelParser(labels, "tsbridge")
	upstreamHeaders := parser.getHeaders("service.upstream_headers")
	assert.Equal(t, 2, len(upstreamHeaders))
	assert.Equal(t, "value1", upstreamHeaders["X-Custom-Header"])
	assert.Equal(t, "value2", upstreamHeaders["X-Another"])

	downstreamHeaders := parser.getHeaders("service.downstream_headers")
	assert.Equal(t, 1, len(downstreamHeaders))
	assert.Equal(t, "value3", downstreamHeaders["X-Response"])
}

func TestParseServiceConfig(t *testing.T) {
	provider := &Provider{labelPrefix: "tsbridge"}

	tests := []struct {
		name      string
		container types.Container
		wantErr   bool
		validate  func(t *testing.T, svc *config.Service)
	}{
		{
			name: "basic service",
			container: types.Container{
				Names: []string{"/test-api"},
				Labels: map[string]string{
					"tsbridge.enabled":              "true",
					"tsbridge.service.name":         "api",
					"tsbridge.service.backend_addr": "localhost:8080",
				},
			},
			validate: func(t *testing.T, svc *config.Service) {
				assert.Equal(t, "api", svc.Name)
				assert.Equal(t, "localhost:8080", svc.BackendAddr)
			},
		},
		{
			name: "service with all options",
			container: types.Container{
				Names: []string{"/test-web"},
				Labels: map[string]string{
					"tsbridge.enabled":                           "true",
					"tsbridge.service.name":                      "web",
					"tsbridge.service.backend_addr":              "unix:///var/run/web.sock",
					"tsbridge.service.whois_enabled":             "true",
					"tsbridge.service.whois_timeout":             "2s",
					"tsbridge.service.tls_mode":                  "off",
					"tsbridge.service.access_log":                "false",
					"tsbridge.service.funnel_enabled":            "true",
					"tsbridge.service.ephemeral":                 "true",
					"tsbridge.service.upstream_headers.X-Custom": "value",
					"tsbridge.service.remove_upstream":           "X-Forwarded-For,X-Real-IP",
				},
			},
			validate: func(t *testing.T, svc *config.Service) {
				assert.Equal(t, "web", svc.Name)
				assert.Equal(t, "unix:///var/run/web.sock", svc.BackendAddr)
				assert.True(t, *svc.WhoisEnabled)
				assert.Equal(t, 2*time.Second, svc.WhoisTimeout.Duration)
				assert.Equal(t, "off", svc.TLSMode)
				assert.False(t, *svc.AccessLog)
				assert.True(t, *svc.FunnelEnabled)
				assert.True(t, svc.Ephemeral)
				assert.Equal(t, "value", svc.UpstreamHeaders["X-Custom"])
				assert.Equal(t, []string{"X-Forwarded-For", "X-Real-IP"}, svc.RemoveUpstream)
			},
		},
		{
			name: "service with port inference",
			container: types.Container{
				Names: []string{"/test-app"},
				Labels: map[string]string{
					"tsbridge.enabled":      "true",
					"tsbridge.service.port": "3000",
				},
				Ports: []types.Port{
					{PrivatePort: 3000, PublicPort: 0},
				},
			},
			validate: func(t *testing.T, svc *config.Service) {
				assert.Equal(t, "test-app", svc.Name)
				assert.Contains(t, svc.BackendAddr, ":3000")
			},
		},
		{
			name: "service without backend address",
			container: types.Container{
				Names: []string{"/test-fail"},
				Labels: map[string]string{
					"tsbridge.enabled":      "true",
					"tsbridge.service.name": "fail",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := provider.parseServiceConfig(tt.container)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, svc)
			if tt.validate != nil {
				tt.validate(t, svc)
			}
		})
	}
}

func TestParseGlobalConfig(t *testing.T) {
	provider := &Provider{labelPrefix: "tsbridge"}

	t.Run("with explicit env vars in labels", func(t *testing.T) {
		// Set environment variables for testing
		t.Setenv("TS_OAUTH_CLIENT_ID", "test-client-id")
		t.Setenv("TS_OAUTH_CLIENT_SECRET", "test-client-secret")

		container := &types.Container{
			Labels: map[string]string{
				"tsbridge.tailscale.oauth_client_id_env":     "TS_OAUTH_CLIENT_ID",
				"tsbridge.tailscale.oauth_client_secret_env": "TS_OAUTH_CLIENT_SECRET",
				"tsbridge.tailscale.oauth_tags":              "tag:proxy, tag:server",
				"tsbridge.tailscale.state_dir":               "/var/lib/tsbridge",
				"tsbridge.global.metrics_addr":               ":9090",
				"tsbridge.global.read_timeout":               "30s",
				"tsbridge.global.write_timeout":              "30s",
				"tsbridge.global.idle_timeout":               "120s",
				"tsbridge.global.access_log":                 "true",
				"tsbridge.global.trusted_proxies":            "10.0.0.0/8,172.16.0.0/12",
			},
		}

		cfg := &config.Config{}
		err := provider.parseGlobalConfig(container, cfg)
		require.NoError(t, err)

		// Verify Tailscale config - env vars should be set but not resolved yet
		assert.Equal(t, "", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "", cfg.Tailscale.OAuthClientSecret)
		assert.Equal(t, "TS_OAUTH_CLIENT_ID", cfg.Tailscale.OAuthClientIDEnv)
		assert.Equal(t, "TS_OAUTH_CLIENT_SECRET", cfg.Tailscale.OAuthClientSecretEnv)
		assert.Equal(t, []string{"tag:proxy", "tag:server"}, cfg.Tailscale.OAuthTags)
		assert.Equal(t, "/var/lib/tsbridge", cfg.Tailscale.StateDir)

		// Verify global config
		assert.Equal(t, ":9090", cfg.Global.MetricsAddr)
		assert.Equal(t, 30*time.Second, cfg.Global.ReadTimeout.Duration)
		assert.Equal(t, 30*time.Second, cfg.Global.WriteTimeout.Duration)
		assert.Equal(t, 120*time.Second, cfg.Global.IdleTimeout.Duration)
		assert.True(t, *cfg.Global.AccessLog)
		assert.Equal(t, []string{"10.0.0.0/8", "172.16.0.0/12"}, cfg.Global.TrustedProxies)
	})

	t.Run("with fallback to standard env vars", func(t *testing.T) {
		// Set environment variables for testing
		t.Setenv("TS_OAUTH_CLIENT_ID", "fallback-client-id")
		t.Setenv("TS_OAUTH_CLIENT_SECRET", "fallback-client-secret")

		container := &types.Container{
			Labels: map[string]string{
				// No oauth env labels - should fallback to standard env vars
				"tsbridge.tailscale.state_dir": "/var/lib/tsbridge",
				"tsbridge.global.metrics_addr": ":9090",
			},
		}

		cfg := &config.Config{}
		err := provider.parseGlobalConfig(container, cfg)
		require.NoError(t, err)

		// Verify no env vars are set - secrets will be resolved later by ProcessLoadedConfig
		assert.Equal(t, "", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "", cfg.Tailscale.OAuthClientSecret)
		assert.Equal(t, "", cfg.Tailscale.OAuthClientIDEnv)
		assert.Equal(t, "", cfg.Tailscale.OAuthClientSecretEnv)
		assert.Equal(t, "/var/lib/tsbridge", cfg.Tailscale.StateDir)
		assert.Equal(t, ":9090", cfg.Global.MetricsAddr)
	})
}

func TestConfigEqual(t *testing.T) {
	provider := &Provider{}

	tests := []struct {
		name  string
		a     *config.Config
		b     *config.Config
		equal bool
	}{
		{
			name:  "both nil",
			a:     nil,
			b:     nil,
			equal: true,
		},
		{
			name:  "one nil",
			a:     &config.Config{},
			b:     nil,
			equal: false,
		},
		{
			name: "same services",
			a: &config.Config{
				Services: []config.Service{
					{Name: "api"},
					{Name: "web"},
				},
			},
			b: &config.Config{
				Services: []config.Service{
					{Name: "api"},
					{Name: "web"},
				},
			},
			equal: true,
		},
		{
			name: "different service count",
			a: &config.Config{
				Services: []config.Service{
					{Name: "api"},
				},
			},
			b: &config.Config{
				Services: []config.Service{
					{Name: "api"},
					{Name: "web"},
				},
			},
			equal: false,
		},
		{
			name: "different service names",
			a: &config.Config{
				Services: []config.Service{
					{Name: "api"},
					{Name: "web"},
				},
			},
			b: &config.Config{
				Services: []config.Service{
					{Name: "api"},
					{Name: "admin"},
				},
			},
			equal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.equal, provider.configEqual(tt.a, tt.b))
		})
	}
}

func TestGetContainerAddress(t *testing.T) {
	provider := &Provider{}

	tests := []struct {
		name      string
		container types.Container
		want      string
	}{
		{
			name: "with names",
			container: types.Container{
				Names: []string{"/my-container"},
				ID:    "abc123def456",
			},
			want: "my-container",
		},
		{
			name: "without names",
			container: types.Container{
				ID: "abc123def456789",
			},
			want: "abc123def456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, provider.getContainerAddress(tt.container))
		})
	}
}

func TestDockerProviderErrorHandling(t *testing.T) {
	tests := []struct {
		name         string
		opts         Options
		wantErrType  errors.ErrorType
		wantContains []string
	}{
		{
			name: "docker connection error",
			opts: Options{
				DockerEndpoint: "tcp://invalid-docker-host:2375",
			},
			wantErrType:  errors.ErrTypeResource,
			wantContains: []string{"docker provider", "connecting to Docker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProvider(tt.opts)

			if err == nil {
				t.Skip("Docker connection succeeded unexpectedly - skipping test")
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

func TestDockerLabelParsingErrors(t *testing.T) {
	tests := []struct {
		name         string
		container    types.Container
		wantErr      bool
		wantErrType  errors.ErrorType
		wantContains []string
	}{
		{
			name: "missing service name",
			container: types.Container{
				ID:    "test-container",
				Names: []string{},
				Labels: map[string]string{
					"tsbridge.enabled": "true",
					// No tsbridge.name label
				},
			},
			wantErr:      true,
			wantErrType:  errors.ErrTypeValidation,
			wantContains: []string{"docker provider", "service name is required"},
		},
		{
			name: "missing backend address",
			container: types.Container{
				ID:    "test-container",
				Names: []string{"/test-service"},
				Labels: map[string]string{
					"tsbridge.enabled": "true",
					"tsbridge.name":    "test-service",
					// No backend address labels
				},
			},
			wantErr:      true,
			wantErrType:  errors.ErrTypeValidation,
			wantContains: []string{"docker provider", "backend address could not be determined"},
		},
	}

	provider := &Provider{
		labelPrefix: "tsbridge",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := provider.parseServiceConfig(tt.container)

			if tt.wantErr {
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
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
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

// TestRaceConditionFixed tests that the race condition is fixed with getLastConfig
func TestRaceConditionFixed(t *testing.T) {
	// This test verifies there's no race condition when using getLastConfig

	provider := &Provider{
		labelPrefix: "tsbridge",
		lastConfig: &config.Config{
			Services: []config.Service{
				{Name: "test", BackendAddr: "localhost:8080"},
			},
		},
	}

	// Start multiple goroutines that access lastConfig
	var wg sync.WaitGroup

	// Goroutine 1: Simulates Watch reading lastConfig using thread-safe getter
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			// This uses the thread-safe getLastConfig method
			cfg := provider.getLastConfig()
			if cfg != nil {
				_ = len(cfg.Services)
			}
			time.Sleep(time.Microsecond)
		}
	}()

	// Goroutine 2: Simulates Load writing lastConfig with mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			// Writing lastConfig with mutex protection
			provider.mu.Lock()
			provider.lastConfig = &config.Config{
				Services: []config.Service{
					{Name: "test-" + string(rune(i)), BackendAddr: "localhost:8080"},
				},
			}
			provider.mu.Unlock()
			time.Sleep(time.Microsecond)
		}
	}()

	wg.Wait()
}
func TestValidateDockerAccess(t *testing.T) {
	tests := []struct {
		name        string
		socketPath  string
		statFunc    func(string) (os.FileInfo, error)
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid socket exists and accessible",
			socketPath: "/var/run/docker.sock",
			statFunc: func(path string) (os.FileInfo, error) {
				return &mockFileInfo{mode: os.ModeSocket | 0660}, nil
			},
			wantErr: false,
		},
		{
			name:       "socket does not exist",
			socketPath: "/var/run/docker.sock",
			statFunc: func(path string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			},
			wantErr:     true,
			errContains: "Docker socket not found",
		},
		{
			name:       "socket permission denied",
			socketPath: "/var/run/docker.sock",
			statFunc: func(path string) (os.FileInfo, error) {
				return nil, os.ErrPermission
			},
			wantErr:     true,
			errContains: "permission denied",
		},
		{
			name:       "path is not a socket",
			socketPath: "/var/run/docker.sock",
			statFunc: func(path string) (os.FileInfo, error) {
				// Return a regular file instead of socket
				return &mockFileInfo{mode: 0644}, nil
			},
			wantErr:     true,
			errContains: "not a socket",
		},
		{
			name:       "custom socket path",
			socketPath: "/custom/docker.sock",
			statFunc: func(path string) (os.FileInfo, error) {
				if path != "/custom/docker.sock" {
					t.Errorf("unexpected path: %s", path)
				}
				return &mockFileInfo{mode: os.ModeSocket | 0660}, nil
			},
			wantErr: false,
		},
		{
			name:       "tcp socket should be accepted",
			socketPath: "tcp://localhost:2375",
			statFunc: func(path string) (os.FileInfo, error) {
				t.Error("stat should not be called for TCP sockets")
				return nil, nil
			},
			wantErr: false,
		},
		{
			name:       "http socket should be accepted",
			socketPath: "http://localhost:2375",
			statFunc: func(path string) (os.FileInfo, error) {
				t.Error("stat should not be called for HTTP sockets")
				return nil, nil
			},
			wantErr: false,
		},
		{
			name:       "unix socket prefix",
			socketPath: "unix:///var/run/docker.sock",
			statFunc: func(path string) (os.FileInfo, error) {
				// Should strip unix:// prefix
				if path != "/var/run/docker.sock" {
					t.Errorf("unexpected path: %s", path)
				}
				return &mockFileInfo{mode: os.ModeSocket | 0660}, nil
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original stat function
			originalStat := osStat
			defer func() { osStat = originalStat }()

			// Replace with mock
			osStat = tt.statFunc

			err := validateDockerAccess(tt.socketPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDockerAccess() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateDockerAccess() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

func TestValidateDockerAccess_ErrorMessages(t *testing.T) {
	tests := []struct {
		name           string
		socketPath     string
		statErr        error
		wantErrMessage string
	}{
		{
			name:           "not exist error provides helpful message",
			socketPath:     "/var/run/docker.sock",
			statErr:        os.ErrNotExist,
			wantErrMessage: "Docker socket not found at /var/run/docker.sock. Ensure Docker is installed and running.",
		},
		{
			name:           "permission error suggests solutions",
			socketPath:     "/var/run/docker.sock",
			statErr:        os.ErrPermission,
			wantErrMessage: "permission denied accessing Docker socket at /var/run/docker.sock. Try running with appropriate permissions or adding the user to the docker group.",
		},
		{
			name:           "generic error is wrapped",
			socketPath:     "/var/run/docker.sock",
			statErr:        os.ErrInvalid,
			wantErrMessage: "failed to access Docker socket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original stat function
			originalStat := osStat
			defer func() { osStat = originalStat }()

			// Replace with mock
			osStat = func(string) (os.FileInfo, error) {
				return nil, tt.statErr
			}

			err := validateDockerAccess(tt.socketPath)
			if err == nil {
				t.Fatal("expected error but got nil")
			}

			// Check error message
			if !strings.Contains(err.Error(), tt.wantErrMessage) {
				t.Errorf("error message = %v, want to contain %v", err.Error(), tt.wantErrMessage)
			}
		})
	}
}

// mockFileInfo implements os.FileInfo for testing
type mockFileInfo struct {
	mode os.FileMode
}

func (m *mockFileInfo) Name() string       { return "docker.sock" }
func (m *mockFileInfo) Size() int64        { return 0 }
func (m *mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() interface{}   { return nil }
