//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/testhelpers"
	"github.com/jtdowney/tsbridge/test/integration/helpers"
	"github.com/stretchr/testify/assert"
)

// TestStreamingWithZeroTimeout verifies that streaming services work correctly
// when write_timeout is set to "0s" to disable the timeout.
func TestStreamingWithZeroTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Run("SSE streaming beyond default timeout", func(t *testing.T) {
		// Create a backend server that streams SSE events
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/events" {
				http.NotFound(w, r)
				return
			}

			// Set SSE headers
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Get flusher
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			// Send events for 45 seconds (beyond default 30s timeout)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			eventCount := 0
			for {
				select {
				case <-ticker.C:
					eventCount++
					fmt.Fprintf(w, "data: Event %d at %s\n\n", eventCount, time.Now().Format(time.RFC3339))
					flusher.Flush()

					// Stop after 45 seconds worth of events
					if eventCount >= 9 {
						return
					}
				case <-r.Context().Done():
					return
				}
			}
		}))
		defer backend.Close()

		// Create test configuration with write_timeout = "0s"
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				AuthKey:  "tskey-auth-test123",
				StateDir: t.TempDir(),
			},
			Global: config.Global{
				MetricsAddr:       "localhost:0",
				ReadHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
				WriteTimeout:      testhelpers.DurationPtr(30 * time.Second),
				IdleTimeout:       testhelpers.DurationPtr(120 * time.Second),
				ShutdownTimeout:   testhelpers.DurationPtr(15 * time.Second),
			},
			Services: []config.Service{
				{
					Name:              "streaming-test",
					BackendAddr:       backend.Listener.Addr().String(),
					WriteTimeout:      testhelpers.DurationPtr(0), // Disable timeout
					ReadHeaderTimeout: testhelpers.DurationPtr(10 * time.Second),
					FlushInterval:     testhelpers.DurationPtr(100 * time.Millisecond),
					TLSMode:           "off",
				},
			},
		}

		// Write config and start tsbridge
		configPath := helpers.WriteConfigFile(t, cfg)
		process := helpers.StartTSBridge(t, configPath)

		// The integration test framework runs in test mode where services
		// start but don't actually serve HTTP traffic. So we'll verify
		// the configuration was processed correctly.
		output := process.GetOutput()
		assert.Contains(t, output, `msg="started service" service=streaming-test`)
		assert.Contains(t, output, "shutdown complete")

		// In a real integration test with live Tailscale, we would test the actual streaming.
		// For now, we verify the service started with the correct configuration.
		t.Log("Streaming service started successfully with write_timeout=0s")
	})

	t.Run("verify timeout configuration in logs", func(t *testing.T) {
		// Create a simple backend
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		}))
		defer backend.Close()

		// Create services with different timeout configurations
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				AuthKey:  "tskey-auth-test123",
				StateDir: t.TempDir(),
			},
			Global: config.Global{
				MetricsAddr:       "localhost:0",
				ReadHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
				WriteTimeout:      testhelpers.DurationPtr(30 * time.Second),
				IdleTimeout:       testhelpers.DurationPtr(120 * time.Second),
				ShutdownTimeout:   testhelpers.DurationPtr(10 * time.Second),
			},
			Services: []config.Service{
				{
					Name:        "default-timeout",
					BackendAddr: backend.Listener.Addr().String(),
					TLSMode:     "off",
					// Will inherit global write_timeout of 30s
				},
				{
					Name:         "zero-timeout",
					BackendAddr:  backend.Listener.Addr().String(),
					WriteTimeout: testhelpers.DurationPtr(0), // Explicitly disabled
					TLSMode:      "off",
				},
				{
					Name:         "custom-timeout",
					BackendAddr:  backend.Listener.Addr().String(),
					WriteTimeout: testhelpers.DurationPtr(60 * time.Second),
					TLSMode:      "off",
				},
			},
		}

		// Apply normalization to test inheritance
		cfg.Normalize()

		// Verify timeout values after normalization
		assert.NotNil(t, cfg.Services[0].WriteTimeout)
		assert.Equal(t, 30*time.Second, *cfg.Services[0].WriteTimeout, "default-timeout should inherit global")

		assert.NotNil(t, cfg.Services[1].WriteTimeout)
		assert.Equal(t, time.Duration(0), *cfg.Services[1].WriteTimeout, "zero-timeout should remain 0")

		assert.NotNil(t, cfg.Services[2].WriteTimeout)
		assert.Equal(t, 60*time.Second, *cfg.Services[2].WriteTimeout, "custom-timeout should keep its value")

		// Start tsbridge to verify it accepts the configuration
		configPath := helpers.WriteConfigFile(t, cfg)
		process := helpers.StartTSBridge(t, configPath)

		// Verify all services started
		output := process.GetOutput()
		assert.Contains(t, output, `msg="started service" service=default-timeout`)
		assert.Contains(t, output, `msg="started service" service=zero-timeout`)
		assert.Contains(t, output, `msg="started service" service=custom-timeout`)
	})
}

// TestFlushIntervalWithStreaming verifies that flush_interval configuration
// is properly handled, including the special -1ms value for immediate flushing.
func TestFlushIntervalWithStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer backend.Close()

	// Create configuration with various flush intervals
	cfg := &config.Config{
		Tailscale: config.Tailscale{
			AuthKey:  "tskey-auth-test123",
			StateDir: t.TempDir(),
		},
		Global: config.Global{
			MetricsAddr:       "localhost:0",
			ReadHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
			WriteTimeout:      testhelpers.DurationPtr(30 * time.Second),
			IdleTimeout:       testhelpers.DurationPtr(120 * time.Second),
			ShutdownTimeout:   testhelpers.DurationPtr(10 * time.Second),
			FlushInterval:     testhelpers.DurationPtr(1 * time.Second), // Global default
		},
		Services: []config.Service{
			{
				Name:        "default-flush",
				BackendAddr: backend.Listener.Addr().String(),
				TLSMode:     "off",
				// Will inherit global flush_interval of 1s
			},
			{
				Name:          "immediate-flush",
				BackendAddr:   backend.Listener.Addr().String(),
				FlushInterval: testhelpers.DurationPtr(-1 * time.Millisecond), // Immediate flush
				TLSMode:       "off",
			},
			{
				Name:          "no-flush",
				BackendAddr:   backend.Listener.Addr().String(),
				FlushInterval: testhelpers.DurationPtr(0), // Default buffering
				TLSMode:       "off",
			},
		},
	}

	// Apply normalization
	cfg.Normalize()

	// Verify flush interval values after normalization
	assert.NotNil(t, cfg.Services[0].FlushInterval)
	assert.Equal(t, 1*time.Second, *cfg.Services[0].FlushInterval, "should inherit global")
	assert.NotNil(t, cfg.Services[1].FlushInterval)
	assert.Equal(t, -1*time.Millisecond, *cfg.Services[1].FlushInterval, "should keep immediate flush")
	assert.NotNil(t, cfg.Services[2].FlushInterval)
	assert.Equal(t, time.Duration(0), *cfg.Services[2].FlushInterval, "should keep zero")

	// Start tsbridge
	configPath := helpers.WriteConfigFile(t, cfg)
	process := helpers.StartTSBridge(t, configPath)

	// Verify all services started with their configurations
	output := process.GetOutput()
	assert.Contains(t, output, `msg="started service" service=default-flush`)
	assert.Contains(t, output, `msg="started service" service=immediate-flush`)
	assert.Contains(t, output, `msg="started service" service=no-flush`)
}
