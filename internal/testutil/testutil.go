// Package testutil provides common test utilities for tsbridge tests.
package testutil

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	"github.com/jtdowney/tsbridge/internal/tsnet"
	"github.com/stretchr/testify/require"
)

// CreateTestUnixSocket creates a temporary unix socket for testing.
// The socket is automatically cleaned up when the test completes.
func CreateTestUnixSocket(t *testing.T) string {
	t.Helper()

	// Use a shorter path to avoid macOS unix socket path length limits
	// Replace slashes with dashes to make valid filename
	safeName := strings.ReplaceAll(t.Name(), "/", "-")
	socketPath := "/tmp/tsb-" + safeName + ".sock"

	// Remove any existing socket file
	os.Remove(socketPath)

	// Create a simple unix socket server
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		listener.Close()
		os.Remove(socketPath)
	})

	// Start a simple server in the background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	return socketPath
}

// CreateMockTailscaleServer creates a mock tailscale server for testing.
// If cfg is empty, it will use default test values.
func CreateMockTailscaleServer(t *testing.T, cfg config.Tailscale) *tailscale.Server {
	t.Helper()

	// Set defaults if not provided
	if cfg.AuthKey == "" {
		cfg.AuthKey = "test-key"
	}
	if cfg.StateDir == "" {
		cfg.StateDir = t.TempDir()
	}

	factory := func() tsnet.TSNetServer {
		return tsnet.NewMockTSNetServer()
	}

	server, err := tailscale.NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	return server
}

// CreateTestHTTPServer creates a test HTTP server with the given handler.
// If handler is nil, a default handler that returns 200 OK is used.
// The server is automatically closed when the test completes.
func CreateTestHTTPServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		}
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server
}

// HTTPServerOptions contains options for creating a test HTTP server
type HTTPServerOptions struct {
	// StatusCode to return (default 200)
	StatusCode int
	// Response body to return (default "OK")
	Response string
	// Headers to include in response
	Headers map[string]string
	// Timeout causes the handler to sleep and not respond
	Timeout bool
	// TimeoutDuration specifies how long to sleep (default 1 minute)
	TimeoutDuration time.Duration
}

// CreateTestHTTPServerWithOptions creates a test HTTP server with configurable behavior.
// This is useful for testing various server conditions like timeouts, errors, etc.
func CreateTestHTTPServerWithOptions(t *testing.T, opts HTTPServerOptions) *httptest.Server {
	t.Helper()

	// Set defaults
	if opts.StatusCode == 0 {
		opts.StatusCode = http.StatusOK
	}
	if opts.Response == "" {
		opts.Response = "OK"
	}
	if opts.TimeoutDuration == 0 {
		opts.TimeoutDuration = 1 * time.Minute
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		if opts.Timeout {
			time.Sleep(opts.TimeoutDuration)
			return
		}

		// Set headers
		for k, v := range opts.Headers {
			w.Header().Set(k, v)
		}

		w.WriteHeader(opts.StatusCode)
		_, _ = w.Write([]byte(opts.Response))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)

	return server
}

// CreateEchoServer creates a test HTTP server that echoes back request details.
// It sets headers with information about the request:
// - X-Echo-{HeaderName} for all request headers
// - X-Request-URI with the full request URI
// - X-Request-Method with the HTTP method
// The server automatically closes when the test completes.
func CreateEchoServer(t *testing.T) *httptest.Server {
	t.Helper()

	handler := func(w http.ResponseWriter, r *http.Request) {
		// Echo all headers with X-Echo- prefix
		for name, values := range r.Header {
			if len(values) > 0 {
				w.Header().Set("X-Echo-"+name, values[0])
			}
		}

		// Add request details
		w.Header().Set("X-Request-URI", r.RequestURI)
		w.Header().Set("X-Request-Method", r.Method)

		// Read and echo body
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			_, _ = w.Write([]byte("echo: " + string(body)))
		} else {
			_, _ = w.Write([]byte("echo: no body"))
		}
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)

	return server
}

// CreateDelayServer creates a test HTTP server that delays responses.
// The delay can be specified via the "delay" query parameter (e.g., ?delay=100ms).
// If no delay is specified, the server responds immediately.
// The server automatically closes when the test completes.
func CreateDelayServer(t *testing.T) *httptest.Server {
	t.Helper()

	handler := func(w http.ResponseWriter, r *http.Request) {
		delayStr := r.URL.Query().Get("delay")
		if delayStr != "" {
			delay, err := time.ParseDuration(delayStr)
			if err == nil {
				time.Sleep(delay)
			}
		}
		_, _ = w.Write([]byte("response after delay"))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)

	return server
}

// CreateHeaderEchoServer creates a test HTTP server that echoes specific headers.
// Only the headers specified in the headers parameter will be echoed back
// with an "X-Echo-" prefix. Other headers are ignored.
// The server automatically closes when the test completes.
func CreateHeaderEchoServer(t *testing.T, headers ...string) *httptest.Server {
	t.Helper()

	headerSet := make(map[string]bool)
	for _, h := range headers {
		headerSet[h] = true
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		for header := range headerSet {
			if value := r.Header.Get(header); value != "" {
				w.Header().Set("X-Echo-"+header, value)
			}
		}
		_, _ = w.Write([]byte("headers echoed"))
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)

	return server
}
