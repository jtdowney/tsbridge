package docker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
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
		container container.Summary
		wantErr   bool
		validate  func(t *testing.T, svc *config.Service)
	}{
		{
			name: "basic service",
			container: container.Summary{
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
			container: container.Summary{
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
			container: container.Summary{
				Names: []string{"/test-app"},
				Labels: map[string]string{
					"tsbridge.enabled":      "true",
					"tsbridge.service.port": "3000",
				},
				Ports: []container.Port{
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
			container: container.Summary{
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

		container := &container.Summary{
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

		container := &container.Summary{
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
		container container.Summary
		want      string
	}{
		{
			name: "with names",
			container: container.Summary{
				Names: []string{"/my-container"},
				ID:    "abc123def456",
			},
			want: "my-container",
		},
		{
			name: "without names",
			container: container.Summary{
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
		container    container.Summary
		wantErr      bool
		wantErrType  errors.ErrorType
		wantContains []string
	}{
		{
			name: "missing service name",
			container: container.Summary{
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
			container: container.Summary{
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

func TestDockerProvider_WatchWithEvents(t *testing.T) {
	// This test verifies that the Watch method uses Docker events instead of polling

	t.Run("watch method signature", func(t *testing.T) {
		// Test that the Watch method signature hasn't changed
		provider := &Provider{
			labelPrefix: "tsbridge",
		}

		// Test signature without calling - this verifies method exists
		watchMethod := provider.Watch
		assert.NotNil(t, watchMethod)
	})

	t.Run("createEventOptions configuration", func(t *testing.T) {
		// Test the event options creation without requiring Docker client
		provider := &Provider{
			labelPrefix: "tsbridge",
		}

		options := provider.createEventOptions()
		assert.NotNil(t, options.Filters)

		// Verify event filters are properly configured
		filters := options.Filters.Get("type")
		assert.Contains(t, filters, "container")

		events := options.Filters.Get("event")
		assert.Contains(t, events, "start")
		assert.Contains(t, events, "stop")
		assert.Contains(t, events, "die")
		assert.Contains(t, events, "pause")
		assert.Contains(t, events, "unpause")

		labels := options.Filters.Get("label")
		assert.Contains(t, labels, "tsbridge.enabled=true")
	})

	t.Run("watch event filtering configuration", func(t *testing.T) {
		// Verify that the Watch method would use proper event filters
		// This is a unit test that doesn't require actual Docker connection
		provider := &Provider{
			labelPrefix: "tsbridge",
		}

		// Test that the provider has the correct label prefix
		assert.Equal(t, "tsbridge", provider.labelPrefix)

		// The actual event filtering is tested in integration tests
		// where we can verify the filters are applied correctly
	})
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

// mockDockerClient is a mock implementation of DockerClient for testing
type mockDockerClient struct {
	containers       []container.Summary
	eventsChan       chan events.Message
	errsChan         chan error
	listError        error
	eventsError      error
	pingError        error
	closeError       error
	eventsSent       []events.Message
	mu               sync.Mutex
	eventsStarted    bool
	eventsCloseCount int
}

func newMockDockerClient() *mockDockerClient {
	return &mockDockerClient{
		eventsChan: make(chan events.Message, 10),
		errsChan:   make(chan error, 10),
	}
}

func (m *mockDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	if m.listError != nil {
		return nil, m.listError
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Apply filters if any
	var result []container.Summary

	// Get filters
	labelFilters := options.Filters.Get("label")
	statusFilters := options.Filters.Get("status")

	for _, c := range m.containers {
		includeContainer := true

		// Check status filters
		if len(statusFilters) > 0 {
			statusMatch := false
			for _, status := range statusFilters {
				if c.State == status {
					statusMatch = true
					break
				}
			}
			if !statusMatch {
				includeContainer = false
			}
		}

		// Check label filters
		if includeContainer && len(labelFilters) > 0 {
			for _, filter := range labelFilters {
				// Simple label filter check (format: "key=value" or just "key")
				if strings.Contains(filter, "=") {
					parts := strings.SplitN(filter, "=", 2)
					key, value := parts[0], parts[1]
					if labelValue, ok := c.Labels[key]; !ok || labelValue != value {
						includeContainer = false
						break
					}
				} else {
					// Just check if label exists
					if _, ok := c.Labels[filter]; !ok {
						includeContainer = false
						break
					}
				}
			}
		}

		if includeContainer {
			result = append(result, c)
		}
	}

	return result, nil
}

func (m *mockDockerClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	m.mu.Lock()
	m.eventsStarted = true
	// Create fresh channels for this Events call
	eventsChan := make(chan events.Message, 10)
	errsChan := make(chan error, 10)
	m.eventsChan = eventsChan
	m.errsChan = errsChan
	m.mu.Unlock()

	if m.eventsError != nil {
		go func() {
			errsChan <- m.eventsError
		}()
	}

	// Handle context cancellation
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		m.eventsCloseCount++
		m.mu.Unlock()
		close(eventsChan)
		close(errsChan)
	}()

	return eventsChan, errsChan
}

func (m *mockDockerClient) Ping(ctx context.Context) (types.Ping, error) {
	if m.pingError != nil {
		return types.Ping{}, m.pingError
	}
	return types.Ping{APIVersion: "1.41"}, nil
}

func (m *mockDockerClient) Close() error {
	return m.closeError
}

func (m *mockDockerClient) sendEvent(event events.Message) {
	m.mu.Lock()
	m.eventsSent = append(m.eventsSent, event)
	eventsChan := m.eventsChan
	m.mu.Unlock()

	if eventsChan != nil {
		select {
		case eventsChan <- event:
		default:
			// Channel full, skip
		}
	}
}

func (m *mockDockerClient) sendError(err error) {
	m.mu.Lock()
	errsChan := m.errsChan
	m.mu.Unlock()

	if errsChan != nil {
		select {
		case errsChan <- err:
		default:
			// Channel full, skip
		}
	}
}

// Helper function to create test containers
func createTestContainer(id, name string, labels map[string]string) container.Summary {
	if labels == nil {
		labels = make(map[string]string)
	}
	return container.Summary{
		ID:     id,
		Names:  []string{"/" + name},
		Labels: labels,
		State:  "running",
	}
}

// Helper function to create tsbridge self container
func createTsbridgeContainer(id string) container.Summary {
	// Note: tsbridge self container should NOT have tsbridge.enabled=true
	// as it's not a service container
	labels := map[string]string{
		"tsbridge.tailscale.oauth_client_id":     "tskey-123",
		"tsbridge.tailscale.oauth_client_secret": "secret-456",
		"tsbridge.tailscale.hostname":            "test-bridge",
	}
	return container.Summary{
		ID:     id,
		Names:  []string{"/" + "tsbridge"},
		Labels: labels,
		State:  "running",
	}
}

// Helper function to create service container
func createServiceContainer(id, name, backendAddr string) container.Summary {
	return createTestContainer(id, name, map[string]string{
		"tsbridge.enabled":              "true",
		"tsbridge.service.name":         name,
		"tsbridge.service.backend_addr": backendAddr,
	})
}

func TestProvider_Load(t *testing.T) {
	t.Run("success with multiple services", func(t *testing.T) {
		mockClient := newMockDockerClient()

		// Setup containers
		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		service1 := createServiceContainer("svc1", "api", "localhost:8080")
		service2 := createServiceContainer("svc2", "web", "localhost:3000")

		mockClient.containers = []container.Summary{tsbridgeContainer, service1, service2}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		cfg, err := provider.Load(ctx)

		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Len(t, cfg.Services, 2)

		// Check services were parsed correctly
		serviceNames := make(map[string]bool)
		for _, svc := range cfg.Services {
			serviceNames[svc.Name] = true
		}
		assert.True(t, serviceNames["api"])
		assert.True(t, serviceNames["web"])
	})

	t.Run("self container not found", func(t *testing.T) {
		// Mock readFile to simulate hostname reading failure
		oldReadFile := readFile
		readFile = func(name string) ([]byte, error) {
			return nil, fmt.Errorf("file not found")
		}
		defer func() { readFile = oldReadFile }()

		mockClient := newMockDockerClient()
		// No tsbridge container in list - only service containers
		mockClient.containers = []container.Summary{
			createServiceContainer("svc1", "api", "localhost:8080"),
		}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		_, err := provider.Load(ctx)

		assert.Error(t, err)
		// Now properly returns error about missing tsbridge container
		assert.Contains(t, err.Error(), "no tsbridge container found")
	})

	t.Run("docker api error", func(t *testing.T) {
		mockClient := newMockDockerClient()
		mockClient.listError = fmt.Errorf("connection refused")

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		_, err := provider.Load(ctx)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "finding tsbridge container")
	})

	t.Run("no service containers", func(t *testing.T) {
		mockClient := newMockDockerClient()

		// Only tsbridge container, no services
		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		mockClient.containers = []container.Summary{tsbridgeContainer}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		cfg, err := provider.Load(ctx)

		// Docker provider should allow empty services
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Len(t, cfg.Services, 0)
	})

	t.Run("mixed enabled and disabled containers", func(t *testing.T) {
		mockClient := newMockDockerClient()

		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		enabledService := createServiceContainer("svc1", "api", "localhost:8080")
		disabledService := createTestContainer("svc2", "disabled", map[string]string{
			"tsbridge.enabled":      "false",
			"tsbridge.service.name": "disabled",
		})

		mockClient.containers = []container.Summary{tsbridgeContainer, enabledService, disabledService}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		cfg, err := provider.Load(ctx)

		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Len(t, cfg.Services, 1)
		assert.Equal(t, "api", cfg.Services[0].Name)
	})

	t.Run("malformed service labels", func(t *testing.T) {
		mockClient := newMockDockerClient()

		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		badService := createTestContainer("svc1", "bad", map[string]string{
			"tsbridge.enabled": "true",
			// Missing required service.name and backend_addr
		})
		goodService := createServiceContainer("svc2", "good", "localhost:8080")

		mockClient.containers = []container.Summary{tsbridgeContainer, badService, goodService}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		cfg, err := provider.Load(ctx)

		require.NoError(t, err)
		require.NotNil(t, cfg)
		// Bad service should be skipped, only good service included
		assert.Len(t, cfg.Services, 1)
		assert.Equal(t, "good", cfg.Services[0].Name)
	})
}

func TestProvider_Watch_Enhanced(t *testing.T) {
	t.Run("container start event triggers config reload", func(t *testing.T) {
		mockClient := newMockDockerClient()

		// Initial state: only tsbridge container
		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		mockClient.containers = []container.Summary{tsbridgeContainer}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		// Load initial config to set lastConfig
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		_, err := provider.Load(ctx)
		require.NoError(t, err)

		configCh, err := provider.Watch(ctx)
		require.NoError(t, err)
		require.NotNil(t, configCh)

		// Wait for watch goroutine to start
		time.Sleep(50 * time.Millisecond)

		// Add a new service container and send start event
		newService := createServiceContainer("svc1", "api", "localhost:8080")
		mockClient.mu.Lock()
		mockClient.containers = append(mockClient.containers, newService)
		mockClient.mu.Unlock()

		startEvent := events.Message{
			Type:   "container",
			Action: "start",
			Actor: events.Actor{
				ID: "svc1",
				Attributes: map[string]string{
					"name": "api",
				},
			},
		}

		// Send event after a small delay
		time.Sleep(10 * time.Millisecond)
		mockClient.sendEvent(startEvent)

		// Should receive new configuration
		select {
		case cfg := <-configCh:
			assert.NotNil(t, cfg)
			assert.Len(t, cfg.Services, 1)
			assert.Equal(t, "api", cfg.Services[0].Name)
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Expected configuration update after container start event")
		}
	})

	t.Run("container stop event triggers config reload", func(t *testing.T) {
		mockClient := newMockDockerClient()

		// Initial state: tsbridge + service container
		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		serviceContainer := createServiceContainer("svc1", "api", "localhost:8080")
		mockClient.containers = []container.Summary{tsbridgeContainer, serviceContainer}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		// Load initial config to set lastConfig
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		_, err := provider.Load(ctx)
		require.NoError(t, err)

		configCh, err := provider.Watch(ctx)
		require.NoError(t, err)

		// Wait for watch goroutine to start
		time.Sleep(50 * time.Millisecond)

		// Remove service container and send stop event
		mockClient.mu.Lock()
		mockClient.containers = []container.Summary{tsbridgeContainer}
		mockClient.mu.Unlock()

		stopEvent := events.Message{
			Type:   "container",
			Action: "die",
			Actor: events.Actor{
				ID: "svc1",
				Attributes: map[string]string{
					"name": "api",
				},
			},
		}

		// Send event after a small delay
		time.Sleep(10 * time.Millisecond)
		mockClient.sendEvent(stopEvent)

		// Should receive updated configuration (no services)
		select {
		case cfg := <-configCh:
			assert.NotNil(t, cfg)
			assert.Len(t, cfg.Services, 0)
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Expected configuration update after container stop event")
		}
	})

	t.Run("events stream error handling", func(t *testing.T) {
		mockClient := newMockDockerClient()
		mockClient.containers = []container.Summary{createTsbridgeContainer("tsbridge123")}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		configCh, err := provider.Watch(ctx)
		require.NoError(t, err)

		// Wait for events to start
		time.Sleep(10 * time.Millisecond)

		// Send an error on the events stream
		mockClient.sendError(fmt.Errorf("docker daemon connection lost"))

		// Watch should handle error gracefully and continue
		// Channel should remain open
		select {
		case <-configCh:
			// If we get a config, that's fine too
		case <-time.After(50 * time.Millisecond):
			// No config received, which is also acceptable for error handling
		}

		// Channel should still be open
		select {
		case <-configCh:
			t.Fatal("Channel should not be closed after error")
		default:
			// Good, channel is still open
		}
	})

	t.Run("context cancellation closes channel", func(t *testing.T) {
		mockClient := newMockDockerClient()
		mockClient.containers = []container.Summary{createTsbridgeContainer("tsbridge123")}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx, cancel := context.WithCancel(context.Background())

		configCh, err := provider.Watch(ctx)
		require.NoError(t, err)

		// Wait for events to start
		time.Sleep(10 * time.Millisecond)

		// Cancel context
		cancel()

		// Channel should close
		select {
		case _, ok := <-configCh:
			if ok {
				t.Error("Expected channel to be closed after context cancellation")
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Channel did not close within expected timeframe")
		}
	})

	t.Run("non-container events are ignored", func(t *testing.T) {
		mockClient := newMockDockerClient()
		mockClient.containers = []container.Summary{createTsbridgeContainer("tsbridge123")}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		configCh, err := provider.Watch(ctx)
		require.NoError(t, err)

		// Wait for events to start
		time.Sleep(10 * time.Millisecond)

		// Send non-container event
		networkEvent := events.Message{
			Type:   "network",
			Action: "create",
		}

		mockClient.sendEvent(networkEvent)

		// Should not receive any configuration update
		select {
		case <-configCh:
			t.Fatal("Should not receive config update for non-container events")
		case <-time.After(50 * time.Millisecond):
			// Good, no config update received
		}
	})

	t.Run("no config change means no update sent", func(t *testing.T) {
		mockClient := newMockDockerClient()

		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		serviceContainer := createServiceContainer("svc1", "api", "localhost:8080")
		mockClient.containers = []container.Summary{tsbridgeContainer, serviceContainer}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Load initial config to set lastConfig
		_, err := provider.Load(ctx)
		require.NoError(t, err)

		configCh, err := provider.Watch(ctx)
		require.NoError(t, err)

		// Wait for events to start
		time.Sleep(10 * time.Millisecond)

		// Send event for existing container (no config change)
		restartEvent := events.Message{
			Type:   "container",
			Action: "restart",
			Actor: events.Actor{
				ID: "svc1",
				Attributes: map[string]string{
					"name": "api",
				},
			},
		}

		mockClient.sendEvent(restartEvent)

		// Should not receive configuration update since config didn't change
		select {
		case <-configCh:
			t.Fatal("Should not receive config update when configuration hasn't changed")
		case <-time.After(50 * time.Millisecond):
			// Good, no unnecessary update sent
		}
	})

	t.Run("lastConfig is updated after config change notification", func(t *testing.T) {
		mockClient := newMockDockerClient()

		// Initial state: only tsbridge container
		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		mockClient.containers = []container.Summary{tsbridgeContainer}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		// Load initial config
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		initialCfg, err := provider.Load(ctx)
		require.NoError(t, err)
		require.NotNil(t, initialCfg)
		assert.Len(t, initialCfg.Services, 0)

		// Verify lastConfig is set to initial config
		lastCfg := provider.getLastConfig()
		assert.Equal(t, initialCfg, lastCfg)

		configCh, err := provider.Watch(ctx)
		require.NoError(t, err)

		// Wait for watch goroutine to start
		time.Sleep(50 * time.Millisecond)

		// Add a new service container
		newService := createServiceContainer("svc1", "api", "localhost:8080")
		mockClient.mu.Lock()
		mockClient.containers = append(mockClient.containers, newService)
		mockClient.mu.Unlock()

		// Send container start event
		startEvent := events.Message{
			Type:   "container",
			Action: "start",
			Actor: events.Actor{
				ID: "svc1",
				Attributes: map[string]string{
					"name": "api",
				},
			},
		}

		// Get lastConfig before sending event
		configBeforeEvent := provider.getLastConfig()
		assert.Len(t, configBeforeEvent.Services, 0)

		// Send event
		mockClient.sendEvent(startEvent)

		// Should receive new configuration
		select {
		case newCfg := <-configCh:
			require.NotNil(t, newCfg)
			assert.Len(t, newCfg.Services, 1)
			assert.Equal(t, "api", newCfg.Services[0].Name)

			// Verify lastConfig was updated after sending the config
			time.Sleep(10 * time.Millisecond) // Small delay to ensure update happens
			updatedLastCfg := provider.getLastConfig()
			assert.Equal(t, newCfg, updatedLastCfg)
			assert.Len(t, updatedLastCfg.Services, 1)
			assert.NotEqual(t, configBeforeEvent, updatedLastCfg)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected configuration update after container start event")
		}
	})
}

func TestProvider_SimpleMethods(t *testing.T) {
	t.Run("Name returns correct provider name", func(t *testing.T) {
		provider := &Provider{
			labelPrefix: "tsbridge",
		}

		assert.Equal(t, "docker", provider.Name())
	})

	t.Run("Close calls client close", func(t *testing.T) {
		mockClient := newMockDockerClient()
		provider := &Provider{
			client: mockClient,
		}

		err := provider.Close()
		assert.NoError(t, err)
	})

	t.Run("Close handles client close error", func(t *testing.T) {
		mockClient := newMockDockerClient()
		mockClient.closeError = fmt.Errorf("close failed")

		provider := &Provider{
			client: mockClient,
		}

		err := provider.Close()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "close failed")
	})

	t.Run("Close handles nil client", func(t *testing.T) {
		provider := &Provider{
			client: nil,
		}

		err := provider.Close()
		assert.NoError(t, err)
	})

	t.Run("getLastConfig returns stored config", func(t *testing.T) {
		expectedConfig := &config.Config{
			Services: []config.Service{
				{Name: "test-service"},
			},
		}

		provider := &Provider{
			lastConfig: expectedConfig,
		}

		result := provider.getLastConfig()
		assert.Equal(t, expectedConfig, result)
	})

	t.Run("getLastConfig is thread-safe", func(t *testing.T) {
		provider := &Provider{}

		// Test concurrent access
		done := make(chan bool, 2)

		go func() {
			for i := 0; i < 100; i++ {
				provider.getLastConfig()
			}
			done <- true
		}()

		go func() {
			for i := 0; i < 100; i++ {
				provider.mu.Lock()
				provider.lastConfig = &config.Config{}
				provider.mu.Unlock()
			}
			done <- true
		}()

		// Wait for both goroutines
		<-done
		<-done

		// If we get here without panic, the test passed
	})
}

func TestProvider_ConfigEqual(t *testing.T) {
	provider := &Provider{}

	t.Run("nil configs are equal", func(t *testing.T) {
		assert.True(t, provider.configEqual(nil, nil))
	})

	t.Run("nil and non-nil configs are not equal", func(t *testing.T) {
		cfg := &config.Config{}
		assert.False(t, provider.configEqual(nil, cfg))
		assert.False(t, provider.configEqual(cfg, nil))
	})

	t.Run("different service counts are not equal", func(t *testing.T) {
		cfg1 := &config.Config{
			Services: []config.Service{{Name: "svc1"}},
		}
		cfg2 := &config.Config{
			Services: []config.Service{{Name: "svc1"}, {Name: "svc2"}},
		}

		assert.False(t, provider.configEqual(cfg1, cfg2))
	})

	t.Run("same service names are equal", func(t *testing.T) {
		cfg1 := &config.Config{
			Services: []config.Service{{Name: "svc1"}, {Name: "svc2"}},
		}
		cfg2 := &config.Config{
			Services: []config.Service{{Name: "svc2"}, {Name: "svc1"}}, // Different order
		}

		assert.True(t, provider.configEqual(cfg1, cfg2))
	})

	t.Run("different service names are not equal", func(t *testing.T) {
		cfg1 := &config.Config{
			Services: []config.Service{{Name: "svc1"}},
		}
		cfg2 := &config.Config{
			Services: []config.Service{{Name: "svc2"}},
		}

		assert.False(t, provider.configEqual(cfg1, cfg2))
	})
}
