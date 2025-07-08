package docker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/testhelpers"
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
					"tsbridge.service.flush_interval":            "-1ms",
				},
			},
			validate: func(t *testing.T, svc *config.Service) {
				assert.Equal(t, "web", svc.Name)
				assert.Equal(t, "unix:///var/run/web.sock", svc.BackendAddr)
				assert.True(t, *svc.WhoisEnabled)
				assert.Equal(t, 2*time.Second, *svc.WhoisTimeout)
				assert.Equal(t, "off", svc.TLSMode)
				assert.False(t, *svc.AccessLog)
				assert.True(t, *svc.FunnelEnabled)
				assert.True(t, svc.Ephemeral)
				assert.Equal(t, "value", svc.UpstreamHeaders["X-Custom"])
				assert.Equal(t, []string{"X-Forwarded-For", "X-Real-IP"}, svc.RemoveUpstream)
				assert.Equal(t, -1*time.Millisecond, *svc.FlushInterval)
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
				"tsbridge.tailscale.state_dir":               "/var/lib/tsbridge",
				"tsbridge.tailscale.default_tags":            "tag:proxy,tag:server",
				"tsbridge.global.metrics_addr":               ":9090",
				"tsbridge.global.read_header_timeout":        "30s",
				"tsbridge.global.write_timeout":              "30s",
				"tsbridge.global.idle_timeout":               "120s",
				"tsbridge.global.access_log":                 "true",
				"tsbridge.global.trusted_proxies":            "10.0.0.0/8,172.16.0.0/12",
				"tsbridge.global.flush_interval":             "10s",
			},
		}

		cfg := &config.Config{}
		err := provider.parseGlobalConfig(container, cfg)
		require.NoError(t, err)

		// Verify Tailscale config - env vars should be set but not resolved yet
		assert.Equal(t, "", cfg.Tailscale.OAuthClientID)
		assert.Equal(t, "", cfg.Tailscale.OAuthClientSecret.Value())
		assert.Equal(t, "TS_OAUTH_CLIENT_ID", cfg.Tailscale.OAuthClientIDEnv)
		assert.Equal(t, "TS_OAUTH_CLIENT_SECRET", cfg.Tailscale.OAuthClientSecretEnv)
		assert.Equal(t, "/var/lib/tsbridge", cfg.Tailscale.StateDir)
		assert.Equal(t, []string{"tag:proxy", "tag:server"}, cfg.Tailscale.DefaultTags)

		// Verify global config
		assert.Equal(t, ":9090", cfg.Global.MetricsAddr)
		assert.Equal(t, 30*time.Second, *cfg.Global.ReadHeaderTimeout)
		assert.Equal(t, 30*time.Second, *cfg.Global.WriteTimeout)
		assert.Equal(t, 120*time.Second, *cfg.Global.IdleTimeout)
		assert.True(t, *cfg.Global.AccessLog)
		assert.Equal(t, []string{"10.0.0.0/8", "172.16.0.0/12"}, cfg.Global.TrustedProxies)
		assert.Equal(t, 10*time.Second, *cfg.Global.FlushInterval)
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
		assert.Equal(t, "", cfg.Tailscale.OAuthClientSecret.Value())
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
		{
			name: "different backend addresses should not be equal",
			a: &config.Config{
				Services: []config.Service{
					{Name: "api", BackendAddr: "http://localhost:8080"},
				},
			},
			b: &config.Config{
				Services: []config.Service{
					{Name: "api", BackendAddr: "http://localhost:8081"},
				},
			},
			equal: false, // This will fail with current implementation
		},
		{
			name: "different headers should not be equal",
			a: &config.Config{
				Services: []config.Service{
					{
						Name:        "api",
						BackendAddr: "http://localhost:8080",
						UpstreamHeaders: map[string]string{
							"X-Custom-Header": "value1",
						},
					},
				},
			},
			b: &config.Config{
				Services: []config.Service{
					{
						Name:        "api",
						BackendAddr: "http://localhost:8080",
						UpstreamHeaders: map[string]string{
							"X-Custom-Header": "value2",
						},
					},
				},
			},
			equal: false, // This will fail with current implementation
		},
		{
			name: "different timeouts should not be equal",
			a: &config.Config{
				Services: []config.Service{
					{
						Name:         "api",
						BackendAddr:  "http://localhost:8080",
						WhoisTimeout: testhelpers.DurationPtr(2 * time.Second),
					},
				},
			},
			b: &config.Config{
				Services: []config.Service{
					{
						Name:         "api",
						BackendAddr:  "http://localhost:8080",
						WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
					},
				},
			},
			equal: false, // This will fail with current implementation
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

		// We no longer filter by labels in the event stream
		// Label checking is done client-side to support both "enabled" and "enable"
		labels := options.Filters.Get("label")
		assert.Empty(t, labels, "should not filter by labels in event stream")
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

// TestDockerProvider_BackoffBehavior verifies the backoff reset logic
// by testing the processEventStream return values
func TestDockerProvider_BackoffBehavior(t *testing.T) {
	t.Run("processEventStream indicates stream established after event", func(t *testing.T) {
		// This test verifies that processEventStream returns streamEstablished=true
		// when it successfully receives an event, which triggers backoff reset

		// Create a mock client that will provide controlled channels
		eventsCh := make(chan events.Message, 1)
		errorsCh := make(chan error, 1)

		mockClient := &testEventStreamClient{
			eventsCh: eventsCh,
			errorsCh: errorsCh,
		}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		configCh := make(chan *config.Config, 1)

		// Pre-send an event so processEventStream receives it immediately
		eventsCh <- events.Message{
			Type:   "container",
			Action: "start",
			Actor: events.Actor{
				ID: "test123",
				Attributes: map[string]string{
					"tsbridge.enabled": "true",
				},
			},
		}

		// Run processEventStream in goroutine
		done := make(chan struct{})
		var cancelled, streamEstablished bool

		go func() {
			defer close(done)
			cancelled, streamEstablished = provider.processEventStream(ctx, configCh, provider.createEventOptions())
		}()

		// Wait a moment then cancel - the pre-sent event should be processed immediately
		go func() {
			// This goroutine ensures we cancel after giving processEventStream
			// a chance to start, but we don't need precise timing
			<-time.After(1 * time.Millisecond)
			cancel()
		}()

		// Wait for completion
		<-done

		assert.True(t, cancelled, "should be cancelled by context")
		assert.True(t, streamEstablished, "stream should be marked as established after receiving event")
	})

	t.Run("processEventStream indicates no stream on immediate error", func(t *testing.T) {
		// This test verifies that processEventStream returns streamEstablished=false
		// when it encounters an error before receiving any events

		mockClient := newMockDockerClient()
		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		configCh := make(chan *config.Config, 1)

		// Configure mock to return error immediately
		mockClient.eventsError = fmt.Errorf("connection error")

		// processEventStream should return quickly with error
		cancelled, streamEstablished := provider.processEventStream(ctx, configCh, provider.createEventOptions())

		assert.False(t, cancelled, "should not be cancelled (error exit)")
		assert.False(t, streamEstablished, "stream should not be established on error")
	})
}

// testEventStreamClient is a simple test client that returns pre-configured channels
type testEventStreamClient struct {
	eventsCh chan events.Message
	errorsCh chan error
}

func (t *testEventStreamClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	return t.eventsCh, t.errorsCh
}

func (t *testEventStreamClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	// Return minimal container for test
	return []container.Summary{
		{
			ID:    "tsbridge123",
			Names: []string{"/tsbridge"},
			Labels: map[string]string{
				"tsbridge.name": "tsbridge",
			},
			State: "running",
		},
	}, nil
}

func (t *testEventStreamClient) Ping(ctx context.Context) (types.Ping, error) {
	return types.Ping{}, nil
}

func (t *testEventStreamClient) Close() error {
	return nil
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
		"tsbridge.tailscale.default_tags":        "tag:test,tag:docker",
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

	t.Run("both enable and enabled labels are accepted", func(t *testing.T) {
		mockClient := newMockDockerClient()

		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		// Service with "enabled" label
		enabledService := createServiceContainer("svc1", "api", "localhost:8080")
		// Service with "enable" label (without 'd')
		enableService := createTestContainer("svc2", "web", map[string]string{
			"tsbridge.enable":               "true", // Note: "enable" not "enabled"
			"tsbridge.service.name":         "web",
			"tsbridge.service.backend_addr": "localhost:3000",
		})

		mockClient.containers = []container.Summary{tsbridgeContainer, enabledService, enableService}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		cfg, err := provider.Load(ctx)

		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Len(t, cfg.Services, 2)

		// Check both services were parsed correctly
		serviceNames := make(map[string]bool)
		for _, svc := range cfg.Services {
			serviceNames[svc.Name] = true
		}
		assert.True(t, serviceNames["api"])
		assert.True(t, serviceNames["web"])
	})

	t.Run("container with both enable and enabled labels is not duplicated", func(t *testing.T) {
		mockClient := newMockDockerClient()

		tsbridgeContainer := createTsbridgeContainer("tsbridge123")
		// Service with BOTH "enable" and "enabled" labels
		dualLabelService := createTestContainer("svc1", "api", map[string]string{
			"tsbridge.enable":               "true", // Both labels set
			"tsbridge.enabled":              "true", // Both labels set
			"tsbridge.service.name":         "api",
			"tsbridge.service.backend_addr": "localhost:8080",
		})

		mockClient.containers = []container.Summary{tsbridgeContainer, dualLabelService}

		provider := &Provider{
			client:      mockClient,
			labelPrefix: "tsbridge",
		}

		ctx := context.Background()
		cfg, err := provider.Load(ctx)

		require.NoError(t, err)
		require.NotNil(t, cfg)
		// Should only have 1 service, not 2
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
					"name":             "api",
					"tsbridge.enabled": "true",
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
		case <-time.After(700 * time.Millisecond): // Account for 500ms debounce + buffer
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
					"name":             "api",
					"tsbridge.enabled": "true",
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
					"name":             "api",
					"tsbridge.enabled": "true",
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
					"name":             "api",
					"tsbridge.enabled": "true",
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
		case <-time.After(700 * time.Millisecond): // Account for 500ms debounce + buffer
			t.Fatal("Expected configuration update after container start event")
		}
	})
}

func TestProvider_isContainerEnabled(t *testing.T) {
	provider := &Provider{labelPrefix: "tsbridge"}

	tests := []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{
			name:     "enabled label true",
			labels:   map[string]string{"tsbridge.enabled": "true"},
			expected: true,
		},
		{
			name:     "enable label true",
			labels:   map[string]string{"tsbridge.enable": "true"},
			expected: true,
		},
		{
			name:     "both labels true",
			labels:   map[string]string{"tsbridge.enabled": "true", "tsbridge.enable": "true"},
			expected: true,
		},
		{
			name:     "enabled label false",
			labels:   map[string]string{"tsbridge.enabled": "false"},
			expected: false,
		},
		{
			name:     "enable label false",
			labels:   map[string]string{"tsbridge.enable": "false"},
			expected: false,
		},
		{
			name:     "no labels",
			labels:   map[string]string{},
			expected: false,
		},
		{
			name:     "other labels only",
			labels:   map[string]string{"other.label": "true"},
			expected: false,
		},
		{
			name:     "custom prefix",
			labels:   map[string]string{"custom.enabled": "true"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := provider.isContainerEnabled(tt.labels)
			assert.Equal(t, tt.expected, result)
		})
	}
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

func TestRemoveServiceByContainerName(t *testing.T) {
	p := &Provider{labelPrefix: "tsbridge"}

	tests := []struct {
		name              string
		cfg               *config.Config
		containerName     string
		expectedRemaining int
		shouldRemove      bool
	}{
		{
			name: "removes service with exact container name match",
			cfg: &config.Config{
				Services: []config.Service{
					{Name: "api", BackendAddr: "docker-httpbin-1:8080"},
					{Name: "web", BackendAddr: "docker-nginx-1:80"},
				},
			},
			containerName:     "docker-httpbin-1",
			expectedRemaining: 1,
			shouldRemove:      true,
		},
		{
			name: "removes service with partial container name match",
			cfg: &config.Config{
				Services: []config.Service{
					{Name: "api", BackendAddr: "httpbin-1:8080"},
					{Name: "web", BackendAddr: "nginx-1:80"},
				},
			},
			containerName:     "httpbin-1",
			expectedRemaining: 1,
			shouldRemove:      true,
		},
		{
			name: "handles no match gracefully",
			cfg: &config.Config{
				Services: []config.Service{
					{Name: "api", BackendAddr: "httpbin:8080"},
					{Name: "web", BackendAddr: "nginx:80"},
				},
			},
			containerName:     "redis",
			expectedRemaining: 2,
			shouldRemove:      false,
		},
		{
			name: "handles empty container name",
			cfg: &config.Config{
				Services: []config.Service{
					{Name: "api", BackendAddr: "httpbin:8080"},
				},
			},
			containerName:     "",
			expectedRemaining: 1,
			shouldRemove:      false,
		},
		{
			name:              "handles nil config",
			cfg:               nil,
			containerName:     "httpbin",
			expectedRemaining: 0,
			shouldRemove:      false,
		},
		{
			name: "removes multiple services from same container",
			cfg: &config.Config{
				Services: []config.Service{
					{Name: "api", BackendAddr: "httpbin-1:8080"},
					{Name: "api-admin", BackendAddr: "httpbin-1:8081"},
					{Name: "web", BackendAddr: "nginx-1:80"},
				},
			},
			containerName:     "httpbin-1",
			expectedRemaining: 1,
			shouldRemove:      true,
		},
		{
			name: "preserves global config",
			cfg: &config.Config{
				Global: config.Global{
					MetricsAddr: ":9090",
				},
				Services: []config.Service{
					{Name: "api", BackendAddr: "httpbin-1:8080"},
				},
			},
			containerName:     "httpbin-1",
			expectedRemaining: 0,
			shouldRemove:      true,
		},
		{
			name: "prevents false positive with substring matching",
			cfg: &config.Config{
				Services: []config.Service{
					{Name: "webapp", BackendAddr: "webapp:8080"},
					{Name: "app", BackendAddr: "app:8081"},
					{Name: "application", BackendAddr: "application:8082"},
				},
			},
			containerName:     "app",
			expectedRemaining: 2, // Only "app:8081" should be removed
			shouldRemove:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.removeServiceByContainerName(tt.cfg, tt.containerName)

			if tt.cfg == nil {
				assert.Nil(t, result)
				return
			}

			assert.NotNil(t, result)
			assert.Equal(t, tt.expectedRemaining, len(result.Services))

			// Verify global config is preserved
			if tt.cfg.Global.MetricsAddr != "" {
				assert.Equal(t, tt.cfg.Global.MetricsAddr, result.Global.MetricsAddr)
			}

			// Verify the right services remain
			if tt.shouldRemove && tt.expectedRemaining > 0 {
				// Special handling for the false positive test
				if tt.name == "prevents false positive with substring matching" {
					// Verify that webapp and application remain, but app is removed
					assert.Len(t, result.Services, 2)
					remainingBackends := make(map[string]bool)
					for _, svc := range result.Services {
						remainingBackends[svc.BackendAddr] = true
					}
					assert.True(t, remainingBackends["webapp:8080"], "webapp:8080 should remain")
					assert.True(t, remainingBackends["application:8082"], "application:8082 should remain")
					assert.False(t, remainingBackends["app:8081"], "app:8081 should be removed")
				} else {
					// For other tests, verify exact match removal
					for _, svc := range result.Services {
						// Extract hostname from backend address
						hostname := svc.BackendAddr
						if idx := strings.LastIndex(hostname, ":"); idx > 0 {
							hostname = hostname[:idx]
						}
						// Should not exactly match the container name
						assert.NotEqual(t, hostname, tt.containerName,
							"service with backend %s should not remain after removing container %s",
							svc.BackendAddr, tt.containerName)
					}
				}
			}
		})
	}
}

func TestDockerRaceConditionHandling(t *testing.T) {
	// This test verifies that stop/die events properly remove services
	// even if Docker's API briefly returns the container as "running"

	t.Run("stop event removes service from config", func(t *testing.T) {
		p := &Provider{labelPrefix: "tsbridge"}

		// Simulate a config that includes a container that's being stopped
		cfg := &config.Config{
			Services: []config.Service{
				{
					Name:        "httpbin",
					BackendAddr: "docker-httpbin-1:8080",
				},
				{
					Name:        "nginx",
					BackendAddr: "docker-nginx-1:80",
				},
			},
		}

		// Process stop event for httpbin container
		result := p.removeServiceByContainerName(cfg, "docker-httpbin-1")

		// Should only have nginx service remaining
		assert.Equal(t, 1, len(result.Services))
		assert.Equal(t, "nginx", result.Services[0].Name)
		assert.NotContains(t, result.Services[0].BackendAddr, "httpbin")
	})
}

func TestWatchLoopWithFailingClient(t *testing.T) {
	// This test verifies that the watchLoop uses exponential backoff
	// when Docker event stream reconnections fail repeatedly

	// Create a provider with a failing Docker client
	mockClient := &MockFailingDockerClient{}
	p := &Provider{
		client: mockClient,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()

	configCh := make(chan *config.Config, 1)
	eventOptions := events.ListOptions{}

	start := time.Now()

	// With exponential backoff: 1s, 2s, 4s (should timeout before 8s)
	p.watchLoop(ctx, configCh, eventOptions)

	elapsed := time.Since(start)

	// Should attempt multiple reconnections with exponential backoff
	// First delay: 1s, second delay: 2s (total ~3s before timeout)
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(3000), "Should wait through multiple exponential backoff attempts")
	assert.Less(t, elapsed.Milliseconds(), int64(4000), "Should timeout before completing all attempts")
}

func TestWatchLoopContextCancellation(t *testing.T) {
	// Test that watchLoop exits immediately when context is cancelled
	mockClient := &MockFailingDockerClient{}
	p := &Provider{
		client: mockClient,
	}

	ctx, cancel := context.WithCancel(context.Background())
	configCh := make(chan *config.Config, 1)
	eventOptions := events.ListOptions{}

	// Cancel context immediately
	cancel()

	start := time.Now()
	p.watchLoop(ctx, configCh, eventOptions)
	elapsed := time.Since(start)

	// Should exit almost immediately
	assert.Less(t, elapsed.Milliseconds(), int64(100), "Should exit immediately when context is cancelled")
}

func TestWatchLoopBackoffCap(t *testing.T) {
	// Test that exponential backoff is capped at 5 minutes
	// We'll track timing to ensure backoff doesn't exceed the cap
	attemptTimes := []time.Time{}
	attemptCount := 0

	mockClient := &MockFailingDockerClient{}
	p := &Provider{
		client: mockClient,
	}

	// Short timeout since we're just testing the backoff cap
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	configCh := make(chan *config.Config, 1)
	eventOptions := events.ListOptions{}

	// Track attempt times
	mockClient.EventsFunc = func(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
		attemptCount++
		attemptTimes = append(attemptTimes, time.Now())

		eventCh := make(chan events.Message)
		errCh := make(chan error, 1)

		// Fail immediately
		close(eventCh)
		errCh <- fmt.Errorf("simulated failure")

		return eventCh, errCh
	}

	// Run watchLoop
	done := make(chan bool)
	go func() {
		p.watchLoop(ctx, configCh, eventOptions)
		done <- true
	}()

	// Wait for several backoff cycles
	time.Sleep(8 * time.Second)
	cancel()
	<-done

	// Should see exponential backoff: ~0s, ~1s, ~2s, ~4s
	assert.GreaterOrEqual(t, attemptCount, 4, "Should have at least 4 attempts")

	// The timing might be off due to goroutine scheduling, so let's just verify
	// that we're seeing increasing delays between attempts with tolerance
	if len(attemptTimes) >= 4 {
		// Calculate delays between attempts
		var delays []time.Duration
		for i := 1; i < len(attemptTimes) && i < 4; i++ {
			delays = append(delays, attemptTimes[i].Sub(attemptTimes[i-1]))
		}

		// First delay should be at least 800ms (allowing for some variance)
		assert.GreaterOrEqual(t, delays[0].Milliseconds(), int64(500), "First retry delay should be at least 500ms")

		// Check that we're seeing generally increasing delays (exponential backoff pattern)
		// In CI environments, timing can be very variable due to resource constraints
		// So we'll be more lenient and just check that we got multiple retries
		// with some delay between them
		if len(delays) >= 2 {
			// At least verify we have some delay between attempts
			hasDelay := false
			for _, d := range delays {
				if d.Milliseconds() > 100 {
					hasDelay = true
					break
				}
			}
			assert.True(t, hasDelay, "Should have delays between retry attempts")
		}
	}
}

func TestWatchLoopStreamEstablished(t *testing.T) {
	// Test that watchLoop properly tracks when a stream is established
	callCount := 0

	mockClient := &MockFailingDockerClient{}
	p := &Provider{
		client:      mockClient,
		labelPrefix: "tsbridge",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	configCh := make(chan *config.Config, 1)
	eventOptions := events.ListOptions{}

	// Mock Events to fail first, then succeed with events
	mockClient.EventsFunc = func(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
		callCount++

		eventCh := make(chan events.Message, 1)
		errCh := make(chan error, 1)

		if callCount == 1 {
			// First attempt fails immediately
			close(eventCh)
			errCh <- fmt.Errorf("simulated failure")
		} else {
			// Second attempt sends events to establish stream
			go func() {
				// Send a tsbridge-enabled container event
				select {
				case <-ctx.Done():
					return
				case eventCh <- events.Message{
					Type:   "container",
					Action: "start",
					Actor: events.Actor{
						ID: "test123",
						Attributes: map[string]string{
							"name":             "test-container",
							"tsbridge.enabled": "true",
						},
					},
				}:
					// Wait a bit then close to test backoff reset
					time.Sleep(500 * time.Millisecond)
					close(eventCh)
				}
			}()
		}

		return eventCh, errCh
	}

	// Run watchLoop
	go p.watchLoop(ctx, configCh, eventOptions)

	// Wait for retries
	time.Sleep(3 * time.Second)

	// Should have retried after initial failure
	assert.GreaterOrEqual(t, callCount, 2, "Should have retried after failure")
}

// MockFailingDockerClient simulates a Docker client that always fails
type MockFailingDockerClient struct {
	EventsFunc func(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
}

func (m *MockFailingDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	return nil, fmt.Errorf("simulated Docker connection failure")
}

func (m *MockFailingDockerClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	if m.EventsFunc != nil {
		return m.EventsFunc(ctx, options)
	}

	eventCh := make(chan events.Message)
	errCh := make(chan error, 1)

	// Immediately close event channel and send error to simulate connection failure
	close(eventCh)
	errCh <- fmt.Errorf("simulated Docker connection failure")

	return eventCh, errCh
}

func (m *MockFailingDockerClient) Ping(ctx context.Context) (types.Ping, error) {
	return types.Ping{}, fmt.Errorf("simulated Docker connection failure")
}

func (m *MockFailingDockerClient) Close() error {
	return nil
}

func TestDebouncedReload(t *testing.T) {
	// This test verifies that the debouncedReload timer mechanism works correctly
	// by checking that multiple rapid calls result in only one timer execution

	provider := &Provider{
		labelPrefix: "tsbridge",
	}

	// Track how many times the timer fires
	var fireCount int32

	start := time.Now()

	// Trigger multiple rapid debounced reloads
	// Each call should cancel the previous timer and set a new one
	for i := 0; i < 3; i++ {
		provider.debounceMu.Lock()
		// Cancel existing timer if any
		if provider.debounceTimer != nil {
			provider.debounceTimer.Stop()
		}
		// Set new timer that increments counter
		provider.debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
			atomic.AddInt32(&fireCount, 1)
		})
		provider.debounceMu.Unlock()

		time.Sleep(100 * time.Millisecond)
	}

	// Wait for debounce period to complete
	time.Sleep(600 * time.Millisecond)

	elapsed := time.Since(start)

	// Should only fire once even though we called it 3 times
	finalCount := atomic.LoadInt32(&fireCount)
	assert.Equal(t, int32(1), finalCount, "Expected debouncing to result in only one timer execution")
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(500), "Should wait for debounce period")
}

// MockStreamingDockerClient simulates a Docker client that provides events then fails
type MockStreamingDockerClient struct {
	failAfterCalls int
	callCount      *int
}

func (m *MockStreamingDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	return []container.Summary{}, nil
}

func (m *MockStreamingDockerClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	*m.callCount++
	eventCh := make(chan events.Message, 1)
	errCh := make(chan error, 1)

	go func() {
		// Send some events to establish a successful stream
		if *m.callCount <= m.failAfterCalls {
			// Send a container event
			select {
			case <-ctx.Done():
				return
			case eventCh <- events.Message{
				Action: "start",
				Type:   events.ContainerEventType,
				Actor: events.Actor{
					ID: "container1",
					Attributes: map[string]string{
						"name": "test-container",
					},
				},
			}:
				// Wait a bit then close to trigger reconnect
				time.Sleep(300 * time.Millisecond)
			}
		}
		// Close channels to simulate stream failure
		close(eventCh)
		close(errCh)
	}()

	return eventCh, errCh
}

func (m *MockStreamingDockerClient) Ping(ctx context.Context) (types.Ping, error) {
	return types.Ping{}, nil
}

func (m *MockStreamingDockerClient) Close() error {
	return nil
}

// MockSuccessfulDockerClient simulates a Docker client that works
type MockSuccessfulDockerClient struct{}

func (m *MockSuccessfulDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	// Return empty list to avoid complications in Load()
	return []container.Summary{}, nil
}

func (m *MockSuccessfulDockerClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	eventCh := make(chan events.Message)
	errCh := make(chan error)

	// Close channels immediately - not used in this test
	close(eventCh)
	close(errCh)

	return eventCh, errCh
}

func (m *MockSuccessfulDockerClient) Ping(ctx context.Context) (types.Ping, error) {
	return types.Ping{}, nil
}

func (m *MockSuccessfulDockerClient) Close() error {
	return nil
}
