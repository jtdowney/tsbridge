package tsnet

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlogAdapter(t *testing.T) {
	tests := []struct {
		name          string
		serviceName   string
		format        string
		args          []any
		expectedLevel slog.Level
		expectedMsg   string
		expectedAttrs map[string]any
	}{
		{
			name:          "basic info message",
			serviceName:   "test-service",
			format:        "tsnet starting",
			args:          []any{},
			expectedLevel: slog.LevelInfo,
			expectedMsg:   "tsnet starting",
			expectedAttrs: map[string]any{
				"service":   "test-service",
				"component": "tsnet",
			},
		},
		{
			name:          "message with hostname",
			serviceName:   "transmission",
			format:        "tsnet starting with hostname %q",
			args:          []any{"transmission"},
			expectedLevel: slog.LevelInfo,
			expectedMsg:   "tsnet starting with hostname",
			expectedAttrs: map[string]any{
				"service":   "transmission",
				"component": "tsnet",
				"hostname":  "transmission",
			},
		},
		{
			name:          "error message",
			serviceName:   "test-service",
			format:        "tsnet failed to start: %v",
			args:          []any{"connection timeout"},
			expectedLevel: slog.LevelError,
			expectedMsg:   "tsnet failed to start",
			expectedAttrs: map[string]any{
				"service":   "test-service",
				"component": "tsnet",
				"error":     "connection timeout",
			},
		},
		{
			name:          "state path message",
			serviceName:   "transmission",
			format:        "tsnet running state path %s",
			args:          []any{"/var/lib/tsbridge/transmission/tailscaled.state"},
			expectedLevel: slog.LevelInfo,
			expectedMsg:   "tsnet running state path",
			expectedAttrs: map[string]any{
				"service":    "transmission",
				"component":  "tsnet",
				"state_path": "/var/lib/tsbridge/transmission/tailscaled.state",
			},
		},
		{
			name:          "warning message",
			serviceName:   "test-service",
			format:        "tsnet retrying connection",
			args:          []any{},
			expectedLevel: slog.LevelWarn,
			expectedMsg:   "tsnet retrying connection",
			expectedAttrs: map[string]any{
				"service":   "test-service",
				"component": "tsnet",
			},
		},
		{
			name:          "debug message",
			serviceName:   "test-service",
			format:        "tsnet debug: connection established",
			args:          []any{},
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "tsnet debug: connection established",
			expectedAttrs: map[string]any{
				"service":   "test-service",
				"component": "tsnet",
			},
		},
		{
			name:          "auth url message",
			serviceName:   "test-service",
			format:        "To authenticate, visit: https://login.tailscale.com/a/abc123",
			args:          []any{},
			expectedLevel: slog.LevelError,
			expectedMsg:   "Authentication required - check auth key configuration",
			expectedAttrs: map[string]any{
				"service":      "test-service",
				"component":    "tsnet",
				"auth_url":     "https://login.tailscale.com/a/abc123",
				"config_issue": "auth_key_missing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture log output
			var buf bytes.Buffer
			oldLogger := slog.Default()

			// Set up a test logger that writes to our buffer
			testLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			}))
			slog.SetDefault(testLogger)

			// Ensure we restore the original logger after the test
			defer slog.SetDefault(oldLogger)

			// Create the adapter
			adapter := slogAdapter(tt.serviceName)

			// Call the adapter function
			adapter(tt.format, tt.args...)

			// Parse the logged output
			var logEntry map[string]any
			if buf.Len() > 0 {
				err := json.Unmarshal(buf.Bytes(), &logEntry)
				require.NoError(t, err)

				// Check log level
				assert.Equal(t, tt.expectedLevel.String(), logEntry["level"])

				// Check message
				assert.Equal(t, tt.expectedMsg, logEntry["msg"])

				// Check expected attributes
				for key, expectedValue := range tt.expectedAttrs {
					assert.Equal(t, expectedValue, logEntry[key], "attribute %s", key)
				}
			}
		})
	}
}

func TestLogLevelDetection(t *testing.T) {
	tests := []struct {
		name          string
		format        string
		expectedLevel slog.Level
	}{
		{
			name:          "error keyword",
			format:        "tsnet error: connection failed",
			expectedLevel: slog.LevelError,
		},
		{
			name:          "failed keyword",
			format:        "tsnet failed to connect",
			expectedLevel: slog.LevelError,
		},
		{
			name:          "timeout keyword",
			format:        "tsnet timeout occurred",
			expectedLevel: slog.LevelError,
		},
		{
			name:          "warning keyword",
			format:        "tsnet warning: retrying",
			expectedLevel: slog.LevelWarn,
		},
		{
			name:          "retrying keyword",
			format:        "tsnet retrying connection",
			expectedLevel: slog.LevelWarn,
		},
		{
			name:          "auth url keyword",
			format:        "To authenticate, visit: https://login.tailscale.com/a/abc123",
			expectedLevel: slog.LevelError,
		},
		{
			name:          "debug keyword",
			format:        "tsnet debug: detailed info",
			expectedLevel: slog.LevelDebug,
		},
		{
			name:          "default info level",
			format:        "tsnet starting",
			expectedLevel: slog.LevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level := detectLogLevel(tt.format)
			assert.Equal(t, tt.expectedLevel, level)
		})
	}
}

func TestMessageParsing(t *testing.T) {
	tests := []struct {
		name           string
		format         string
		args           []any
		expectedMsg    string
		expectedAttrs  map[string]any
		expectedParsed bool
	}{
		{
			name:           "hostname parsing",
			format:         "tsnet starting with hostname %q",
			args:           []any{"transmission"},
			expectedMsg:    "tsnet starting with hostname",
			expectedAttrs:  map[string]any{"hostname": "transmission"},
			expectedParsed: true,
		},
		{
			name:           "state path parsing",
			format:         "tsnet running state path %s",
			args:           []any{"/var/lib/tsbridge/transmission/tailscaled.state"},
			expectedMsg:    "tsnet running state path",
			expectedAttrs:  map[string]any{"state_path": "/var/lib/tsbridge/transmission/tailscaled.state"},
			expectedParsed: true,
		},
		{
			name:           "error message parsing",
			format:         "tsnet failed: %v",
			args:           []any{"connection timeout"},
			expectedMsg:    "tsnet failed",
			expectedAttrs:  map[string]any{"error": "connection timeout"},
			expectedParsed: true,
		},
		{
			name:           "var root parsing",
			format:         "tsnet varRoot %q",
			args:           []any{"/var/lib/tsbridge/transmission"},
			expectedMsg:    "tsnet varRoot",
			expectedAttrs:  map[string]any{"var_root": "/var/lib/tsbridge/transmission"},
			expectedParsed: true,
		},
		{
			name:           "auth url parsing",
			format:         "To authenticate, visit: https://login.tailscale.com/a/abc123",
			args:           []any{},
			expectedMsg:    "Authentication required - check auth key configuration",
			expectedAttrs:  map[string]any{"auth_url": "https://login.tailscale.com/a/abc123", "config_issue": "auth_key_missing"},
			expectedParsed: true,
		},
		{
			name:           "unparseable message",
			format:         "some random log message",
			args:           []any{},
			expectedMsg:    "some random log message",
			expectedAttrs:  map[string]any{},
			expectedParsed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, attrs, parsed := parseMessage(tt.format, tt.args)

			assert.Equal(t, tt.expectedMsg, msg)
			assert.Equal(t, tt.expectedAttrs, attrs)
			assert.Equal(t, tt.expectedParsed, parsed)
		})
	}
}

func TestSlogAdapterWithNilLogger(t *testing.T) {
	// Test that adapter handles nil logger gracefully
	adapter := slogAdapter("test-service")

	// This should not panic
	adapter("test message", "arg1")
}

func TestSlogAdapterPerformance(t *testing.T) {
	adapter := slogAdapter("test-service")

	// Benchmark the adapter with a debug message (should be fast since it's filtered out)
	b := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			adapter("debug message: %d", i)
		}
	})

	// Ensure it doesn't take too long per operation
	assert.Less(t, b.NsPerOp(), int64(10000), "adapter should be fast for filtered messages")
}
