package tsnet

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTSNetLogAdapter(t *testing.T) {
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
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "tsnet starting",
			expectedAttrs: map[string]any{
				"service": "test-service",
			},
		},
		{
			name:          "message with hostname",
			serviceName:   "transmission",
			format:        "tsnet starting with hostname %q",
			args:          []any{"transmission"},
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "tsnet starting with hostname \"transmission\"",
			expectedAttrs: map[string]any{
				"service": "transmission",
			},
		},
		{
			name:          "error message",
			serviceName:   "test-service",
			format:        "tsnet failed to start: %v",
			args:          []any{"connection timeout"},
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "tsnet failed to start: connection timeout",
			expectedAttrs: map[string]any{
				"service": "test-service",
			},
		},
		{
			name:          "state path message",
			serviceName:   "transmission",
			format:        "tsnet running state path %s",
			args:          []any{"/var/lib/tsbridge/transmission/tailscaled.state"},
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "tsnet running state path /var/lib/tsbridge/transmission/tailscaled.state",
			expectedAttrs: map[string]any{
				"service": "transmission",
			},
		},
		{
			name:          "auth url message",
			serviceName:   "test-service",
			format:        "To authenticate, visit: https://login.tailscale.com/a/abc123",
			args:          []any{},
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "To authenticate, visit: https://login.tailscale.com/a/abc123",
			expectedAttrs: map[string]any{
				"service": "test-service",
			},
		},
		{
			name:          "magicsock message",
			serviceName:   "test-service",
			format:        "magicsock: received packet from peer",
			args:          []any{},
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "magicsock: received packet from peer",
			expectedAttrs: map[string]any{
				"service": "test-service",
			},
		},
		{
			name:          "wgengine message",
			serviceName:   "test-service",
			format:        "wgengine: updating peer endpoints",
			args:          []any{},
			expectedLevel: slog.LevelDebug,
			expectedMsg:   "wgengine: updating peer endpoints",
			expectedAttrs: map[string]any{
				"service": "test-service",
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
			adapter := tsnetLogAdapter(tt.serviceName)

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

func TestTSNetLogAdapterWithNilLogger(t *testing.T) {
	// Test that adapter handles nil logger gracefully
	adapter := tsnetLogAdapter("test-service")

	// This should not panic
	adapter("test message", "arg1")
}

func TestTSNetLogAdapterPerformance(t *testing.T) {
	adapter := tsnetLogAdapter("test-service")

	// Benchmark the adapter with a debug message (should be fast since it's filtered out)
	b := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			adapter("debug message: %d", i)
		}
	})

	// Ensure it doesn't take too long per operation
	assert.Less(t, b.NsPerOp(), int64(10000), "adapter should be fast for filtered messages")
}
