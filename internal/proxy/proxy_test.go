package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/errors"
)

// defaultTestTransportConfig returns a TransportConfig with reasonable test defaults
func defaultTestTransportConfig() *TransportConfig {
	return &TransportConfig{
		ResponseHeaderTimeout: 5 * time.Second,
		DialTimeout:           constants.DefaultDialTimeout,
		KeepAliveTimeout:      constants.DefaultKeepAliveTimeout,
		IdleConnTimeout:       constants.DefaultIdleConnTimeout,
		TLSHandshakeTimeout:   constants.DefaultTLSHandshakeTimeout,
		ExpectContinueTimeout: constants.DefaultExpectContinueTimeout,
	}
}

func TestHTTPProxy(t *testing.T) {
	// Create a test backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back request details
		w.Header().Set("X-Test-Backend", "true")
		w.Header().Set("X-Request-Path", r.URL.RequestURI())
		w.Header().Set("X-Request-Method", r.Method)

		// Echo any body content
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			fmt.Fprintf(w, "backend response: %s", string(body))
		} else {
			fmt.Fprint(w, "backend response: no body")
		}
	}))
	defer backend.Close()

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		expectedStatus int
		expectedBody   string
		checkHeaders   map[string]string
	}{
		{
			name:           "GET request",
			method:         "GET",
			path:           "/api/test",
			expectedStatus: http.StatusOK,
			expectedBody:   "backend response: no body",
			checkHeaders: map[string]string{
				"X-Test-Backend":   "true",
				"X-Request-Path":   "/api/test",
				"X-Request-Method": "GET",
			},
		},
		{
			name:           "POST request with body",
			method:         "POST",
			path:           "/api/create",
			body:           `{"name": "test"}`,
			expectedStatus: http.StatusOK,
			expectedBody:   `backend response: {"name": "test"}`,
			checkHeaders: map[string]string{
				"X-Test-Backend":   "true",
				"X-Request-Path":   "/api/create",
				"X-Request-Method": "POST",
			},
		},
		{
			name:           "PUT request",
			method:         "PUT",
			path:           "/api/update/123",
			body:           `{"updated": true}`,
			expectedStatus: http.StatusOK,
			expectedBody:   `backend response: {"updated": true}`,
			checkHeaders: map[string]string{
				"X-Request-Method": "PUT",
			},
		},
		{
			name:           "DELETE request",
			method:         "DELETE",
			path:           "/api/delete/123",
			expectedStatus: http.StatusOK,
			expectedBody:   "backend response: no body",
			checkHeaders: map[string]string{
				"X-Request-Method": "DELETE",
			},
		},
		{
			name:           "request with query params",
			method:         "GET",
			path:           "/api/search?q=test&limit=10",
			expectedStatus: http.StatusOK,
			expectedBody:   "backend response: no body",
			checkHeaders: map[string]string{
				"X-Request-Path": "/api/search?q=test&limit=10",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create proxy handler
			handler, err := NewHandler(backend.URL, defaultTestTransportConfig(), nil)
			if err != nil {
				t.Fatalf("Failed to create handler: %v", err)
			}

			// Create test request
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, body)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			// Create response recorder
			rr := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(rr, req)

			// Check status code
			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			// Check response body
			if rr.Body.String() != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, rr.Body.String())
			}

			// Check headers
			for key, expected := range tt.checkHeaders {
				actual := rr.Header().Get(key)
				if actual != expected {
					t.Errorf("expected header %s=%q, got %q", key, expected, actual)
				}
			}
		})
	}
}

func TestUnixSocketProxy(t *testing.T) {
	// Create temporary directory for unix socket
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create unix socket server
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create unix socket: %v", err)
	}
	defer listener.Close()

	// Start HTTP server on unix socket
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Unix-Socket", "true")
		fmt.Fprintf(w, "unix socket response: %s", r.URL.Path)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Logf("unix socket server error: %v", err)
		}
	}()
	defer server.Close()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "root path",
			path:           "/",
			expectedStatus: http.StatusOK,
			expectedBody:   "unix socket response: /",
		},
		{
			name:           "api path",
			path:           "/api/test",
			expectedStatus: http.StatusOK,
			expectedBody:   "unix socket response: /api/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create proxy handler for unix socket
			handler, err := NewHandler("unix://"+socketPath, defaultTestTransportConfig(), nil)
			if err != nil {
				t.Fatalf("Failed to create handler: %v", err)
			}

			// Create test request
			req := httptest.NewRequest("GET", tt.path, nil)
			rr := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(rr, req)

			// Check status
			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			// Check body
			if rr.Body.String() != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, rr.Body.String())
			}

			// Check unix socket header
			if rr.Header().Get("X-Unix-Socket") != "true" {
				t.Error("expected X-Unix-Socket header")
			}
		})
	}
}

func TestProxyWithTimeouts(t *testing.T) {
	// Create a slow backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delay := r.URL.Query().Get("delay")
		if delay != "" {
			d, _ := time.ParseDuration(delay)
			time.Sleep(d)
		}
		fmt.Fprint(w, "response after delay")
	}))
	defer backend.Close()

	tests := []struct {
		name          string
		delay         string
		timeout       time.Duration
		expectTimeout bool
	}{
		{
			name:          "fast response",
			delay:         "10ms",
			timeout:       100 * time.Millisecond,
			expectTimeout: false,
		},
		{
			name:          "slow response triggers timeout",
			delay:         "200ms",
			timeout:       50 * time.Millisecond,
			expectTimeout: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create proxy with timeout
			transportConfig := defaultTestTransportConfig()
			transportConfig.ResponseHeaderTimeout = tt.timeout
			handler, err := NewHandler(backend.URL, transportConfig, nil)
			if err != nil {
				t.Fatalf("Failed to create handler: %v", err)
			}

			// Create request
			req := httptest.NewRequest("GET", "/?delay="+tt.delay, nil)
			rr := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(rr, req)

			if tt.expectTimeout {
				// Should get a timeout error
				if rr.Code != http.StatusGatewayTimeout {
					t.Errorf("expected timeout status 504, got %d", rr.Code)
				}
			} else {
				// Should succeed
				if rr.Code != http.StatusOK {
					t.Errorf("expected status 200, got %d", rr.Code)
				}
				if rr.Body.String() != "response after delay" {
					t.Errorf("unexpected response: %q", rr.Body.String())
				}
			}
		})
	}
}

func TestProxyErrorHandling(t *testing.T) {
	tests := []struct {
		name           string
		backendAddr    string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "invalid backend address",
			backendAddr:    "http://invalid.backend.address:99999",
			expectedStatus: http.StatusBadGateway,
			expectedError:  "Bad Gateway",
		},
		{
			name:           "backend refused connection",
			backendAddr:    "http://127.0.0.1:1", // Port 1 should be refused
			expectedStatus: http.StatusBadGateway,
			expectedError:  "Bad Gateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create proxy handler
			handler, err := NewHandler(tt.backendAddr, defaultTestTransportConfig(), nil)
			if tt.backendAddr == "not-a-valid-url" && err != nil {
				// Expected error for invalid URL
				handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "Bad Gateway", http.StatusBadGateway)
				})
			} else if err != nil {
				t.Fatalf("Failed to create handler: %v", err)
			}

			// Create test request
			req := httptest.NewRequest("GET", "/", nil)
			rr := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(rr, req)

			// Check status
			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			// Check error message
			if !strings.Contains(rr.Body.String(), tt.expectedError) {
				t.Errorf("expected error containing %q, got %q", tt.expectedError, rr.Body.String())
			}
		})
	}
}

func TestProxyHeaderHandling(t *testing.T) {
	// Create backend that echoes headers
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo certain headers back
		if auth := r.Header.Get("Authorization"); auth != "" {
			w.Header().Set("X-Echo-Authorization", auth)
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("X-Echo-Content-Type", ct)
		}
		// Check that certain headers are handled correctly
		w.Header().Set("X-Forwarded-For", r.Header.Get("X-Forwarded-For"))
		w.Header().Set("X-Forwarded-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("X-Real-IP", r.Header.Get("X-Real-IP"))
		fmt.Fprint(w, "headers echoed")
	}))
	defer backend.Close()

	// Create proxy
	handler, err := NewHandler(backend.URL, defaultTestTransportConfig(), nil)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request with headers
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Header", "custom-value")
	req.RemoteAddr = "10.0.0.1:12345"

	rr := httptest.NewRecorder()

	// Handle request
	handler.ServeHTTP(rr, req)

	// Check that headers were passed through
	if rr.Header().Get("X-Echo-Authorization") != "Bearer test-token" {
		t.Error("Authorization header not passed through")
	}
	if rr.Header().Get("X-Echo-Content-Type") != "application/json" {
		t.Error("Content-Type header not passed through")
	}

	// Check forwarding headers were added
	if rr.Header().Get("X-Forwarded-For") == "" {
		t.Error("X-Forwarded-For header not added")
	}
	if rr.Header().Get("X-Real-IP") == "" {
		t.Error("X-Real-IP header not added")
	}
}

func TestXForwardedForSecurity(t *testing.T) {
	// Create backend that echoes X-Forwarded-For header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Forwarded-For", r.Header.Get("X-Forwarded-For"))
		w.Header().Set("X-Echo-Real-IP", r.Header.Get("X-Real-IP"))
		fmt.Fprint(w, "OK")
	}))
	defer backend.Close()

	tests := []struct {
		name                 string
		clientForwardedFor   string
		remoteAddr           string
		expectedForwardedFor string
		expectedRealIP       string
	}{
		{
			name:                 "no existing X-Forwarded-For",
			clientForwardedFor:   "",
			remoteAddr:           "192.168.1.100:12345",
			expectedForwardedFor: "192.168.1.100",
			expectedRealIP:       "192.168.1.100",
		},
		{
			name:                 "client provides X-Forwarded-For - should be ignored",
			clientForwardedFor:   "1.2.3.4, 5.6.7.8",
			remoteAddr:           "192.168.1.100:12345",
			expectedForwardedFor: "192.168.1.100", // Should only contain real client IP
			expectedRealIP:       "192.168.1.100",
		},
		{
			name:                 "client attempts IP spoofing - should be ignored",
			clientForwardedFor:   "trusted.internal.ip",
			remoteAddr:           "evil.client.ip:12345",
			expectedForwardedFor: "evil.client.ip", // Should only contain real client IP
			expectedRealIP:       "evil.client.ip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create proxy
			handler, err := NewHandler(backend.URL, defaultTestTransportConfig(), nil)
			if err != nil {
				t.Fatalf("Failed to create handler: %v", err)
			}

			// Create request with potentially spoofed X-Forwarded-For
			req := httptest.NewRequest("GET", "/", nil)
			if tt.clientForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.clientForwardedFor)
			}
			req.RemoteAddr = tt.remoteAddr

			rr := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(rr, req)

			// Check that proxy set correct forwarding headers
			actualForwardedFor := rr.Header().Get("X-Echo-Forwarded-For")
			if actualForwardedFor != tt.expectedForwardedFor {
				t.Errorf("X-Forwarded-For: expected %q, got %q", tt.expectedForwardedFor, actualForwardedFor)
			}

			actualRealIP := rr.Header().Get("X-Echo-Real-IP")
			if actualRealIP != tt.expectedRealIP {
				t.Errorf("X-Real-IP: expected %q, got %q", tt.expectedRealIP, actualRealIP)
			}
		})
	}
}

func TestXForwardedForWithTrustedProxies(t *testing.T) {
	// Create backend that echoes X-Forwarded-For header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Forwarded-For", r.Header.Get("X-Forwarded-For"))
		w.Header().Set("X-Echo-Real-IP", r.Header.Get("X-Real-IP"))
		fmt.Fprint(w, "OK")
	}))
	defer backend.Close()

	tests := []struct {
		name                 string
		trustedProxies       []string
		clientForwardedFor   string
		remoteAddr           string
		expectedForwardedFor string
		expectedRealIP       string
	}{
		{
			name:                 "request from trusted proxy - append to X-Forwarded-For",
			trustedProxies:       []string{"10.0.0.0/8"},
			clientForwardedFor:   "1.2.3.4",
			remoteAddr:           "10.0.0.1:12345",
			expectedForwardedFor: "1.2.3.4, 10.0.0.1",
			expectedRealIP:       "1.2.3.4", // Real IP from the X-Forwarded-For
		},
		{
			name:                 "request from untrusted proxy - ignore X-Forwarded-For",
			trustedProxies:       []string{"10.0.0.0/8"},
			clientForwardedFor:   "1.2.3.4",
			remoteAddr:           "192.168.1.100:12345",
			expectedForwardedFor: "192.168.1.100",
			expectedRealIP:       "192.168.1.100",
		},
		{
			name:                 "request from trusted proxy with multiple IPs in X-Forwarded-For",
			trustedProxies:       []string{"10.0.0.0/8"},
			clientForwardedFor:   "1.2.3.4, 5.6.7.8",
			remoteAddr:           "10.0.0.1:12345",
			expectedForwardedFor: "1.2.3.4, 5.6.7.8, 10.0.0.1",
			expectedRealIP:       "1.2.3.4", // First IP in the chain
		},
		{
			name:                 "request from specific trusted IP",
			trustedProxies:       []string{"192.168.1.1"},
			clientForwardedFor:   "1.2.3.4",
			remoteAddr:           "192.168.1.1:12345",
			expectedForwardedFor: "1.2.3.4, 192.168.1.1",
			expectedRealIP:       "1.2.3.4",
		},
		{
			name:                 "request from trusted proxy without X-Forwarded-For",
			trustedProxies:       []string{"10.0.0.0/8"},
			clientForwardedFor:   "",
			remoteAddr:           "10.0.0.1:12345",
			expectedForwardedFor: "10.0.0.1",
			expectedRealIP:       "10.0.0.1",
		},
		{
			name:                 "no trusted proxies configured - default behavior",
			trustedProxies:       nil,
			clientForwardedFor:   "1.2.3.4",
			remoteAddr:           "192.168.1.100:12345",
			expectedForwardedFor: "192.168.1.100",
			expectedRealIP:       "192.168.1.100",
		},
		{
			name:                 "multiple trusted proxy ranges",
			trustedProxies:       []string{"10.0.0.0/8", "172.16.0.0/12"},
			clientForwardedFor:   "1.2.3.4",
			remoteAddr:           "172.16.0.1:12345",
			expectedForwardedFor: "1.2.3.4, 172.16.0.1",
			expectedRealIP:       "1.2.3.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create proxy with trusted proxies
			handler, err := NewHandler(backend.URL, defaultTestTransportConfig(), tt.trustedProxies)
			if err != nil {
				t.Fatalf("Failed to create handler: %v", err)
			}

			// Create request
			req := httptest.NewRequest("GET", "/", nil)
			if tt.clientForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.clientForwardedFor)
			}
			req.RemoteAddr = tt.remoteAddr

			rr := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(rr, req)

			// Check that proxy set correct forwarding headers
			actualForwardedFor := rr.Header().Get("X-Echo-Forwarded-For")
			if actualForwardedFor != tt.expectedForwardedFor {
				t.Errorf("X-Forwarded-For: expected %q, got %q", tt.expectedForwardedFor, actualForwardedFor)
			}

			actualRealIP := rr.Header().Get("X-Echo-Real-IP")
			if actualRealIP != tt.expectedRealIP {
				t.Errorf("X-Real-IP: expected %q, got %q", tt.expectedRealIP, actualRealIP)
			}
		})
	}
}

func TestNewHandlerWithErrors(t *testing.T) {
	tests := []struct {
		name           string
		backendAddr    string
		trustedProxies []string
		wantErr        bool
		errContains    string
	}{
		{
			name:        "invalid backend URL scheme",
			backendAddr: "://invalid",
			wantErr:     true,
			errContains: "invalid backend address",
		},
		{
			name:        "empty backend URL",
			backendAddr: "",
			wantErr:     true,
			errContains: "invalid backend address",
		},
		{
			name:           "invalid trusted proxy IP",
			backendAddr:    "http://localhost:8080",
			trustedProxies: []string{"not-an-ip"},
			wantErr:        true,
			errContains:    "invalid trusted proxy IP",
		},
		{
			name:           "invalid trusted proxy CIDR",
			backendAddr:    "http://localhost:8080",
			trustedProxies: []string{"192.168.1.0/999"},
			wantErr:        true,
			errContains:    "invalid trusted proxy CIDR",
		},
		{
			name:           "valid configuration",
			backendAddr:    "http://localhost:8080",
			trustedProxies: []string{"192.168.1.0/24", "10.0.0.1"},
			wantErr:        false,
		},
		{
			name:        "valid unix socket",
			backendAddr: "unix:///tmp/test.sock",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewHandler(tt.backendAddr, defaultTestTransportConfig(), tt.trustedProxies)

			if tt.wantErr {
				if err == nil {
					t.Errorf("NewHandler() expected error but got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("NewHandler() error = %v, want error containing %q", err, tt.errContains)
				}
				if handler != nil {
					t.Errorf("NewHandler() returned non-nil handler with error")
				}
			} else {
				if err != nil {
					t.Errorf("NewHandler() unexpected error: %v", err)
				}
				if handler == nil {
					t.Errorf("NewHandler() returned nil handler without error")
				}
			}
		})
	}
}

func TestProxyErrorTypes(t *testing.T) {
	tests := []struct {
		name       string
		setupProxy func() http.Handler
		wantStatus int
	}{
		{
			name: "timeout error returns bad gateway",
			setupProxy: func() http.Handler {
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Simulate a slow backend
					time.Sleep(200 * time.Millisecond)
				}))
				t.Cleanup(backend.Close)

				transportConfig := defaultTestTransportConfig()
				transportConfig.ResponseHeaderTimeout = 100 * time.Millisecond
				handler, err := NewHandler(backend.URL, transportConfig, nil)
				if err != nil {
					t.Fatalf("Failed to create handler: %v", err)
				}
				return handler
			},
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name: "connection refused returns bad gateway",
			setupProxy: func() http.Handler {
				// Use an invalid address that will refuse connection
				handler, err := NewHandler("http://127.0.0.1:99999", defaultTestTransportConfig(), nil)
				if err != nil {
					t.Fatalf("Failed to create handler: %v", err)
				}
				return handler
			},
			wantStatus: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := tt.setupProxy()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestErrorHandlerUsesCorrectStatus(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "network error returns bad gateway",
			err:        errors.NewNetworkError("connection refused"),
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "validation error returns bad request",
			err:        errors.NewValidationError("invalid request"),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "resource error returns service unavailable",
			err:        errors.NewResourceError("backend overloaded"),
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "internal error returns internal server error",
			err:        errors.NewInternalError("unexpected error"),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "network error with timeout word returns bad gateway (not a real timeout)",
			err:        errors.NewNetworkError("request timeout"),
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "context deadline exceeded returns gateway timeout",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusGatewayTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			w := httptest.NewRecorder()

			// Create a simple error handler function
			errorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
				var status int

				// Check for timeout errors using proper type assertion
				if isTimeoutError(err) {
					status = http.StatusGatewayTimeout
				} else {
					// Use error type to determine status
					status = errors.HTTPStatus(err)
					// Override for network errors that aren't timeouts
					if errors.IsNetwork(err) && status != http.StatusGatewayTimeout {
						status = http.StatusBadGateway
					}
				}

				http.Error(w, err.Error(), status)
			}

			errorHandler(w, req, tt.err)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// mockTimeoutError implements net.Error with Timeout() returning true
type mockTimeoutError struct {
	msg string
}

func (e mockTimeoutError) Error() string   { return e.msg }
func (e mockTimeoutError) Timeout() bool   { return true }
func (e mockTimeoutError) Temporary() bool { return true }

// mockNonTimeoutNetError implements net.Error with Timeout() returning false
type mockNonTimeoutNetError struct {
	msg string
}

func (e mockNonTimeoutNetError) Error() string   { return e.msg }
func (e mockNonTimeoutNetError) Timeout() bool   { return false }
func (e mockNonTimeoutNetError) Temporary() bool { return false }

// TestImprovedTimeoutDetection verifies that the proxy correctly identifies timeout errors
// using type assertions instead of string matching
func TestImprovedTimeoutDetection(t *testing.T) {

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "context.DeadlineExceeded returns gateway timeout",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "net.Error with Timeout() true returns gateway timeout",
			err:        mockTimeoutError{msg: "operation timed out"},
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "net.Error with Timeout() false returns bad gateway",
			err:        mockNonTimeoutNetError{msg: "connection refused"},
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "syscall.ETIMEDOUT returns gateway timeout",
			err:        syscall.ETIMEDOUT,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "wrapped timeout error returns gateway timeout",
			err:        fmt.Errorf("failed to connect: %w", mockTimeoutError{msg: "timeout"}),
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "generic error without 'timeout' word returns bad gateway",
			err:        errors.NewNetworkError("connection failed"),
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "error with 'timeout' in message but not a real timeout returns bad gateway",
			err:        errors.NewNetworkError("timeout configuration invalid"),
			wantStatus: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a simple backend that always succeeds
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer backend.Close()

			// Create proxy handler
			handler, err := NewHandler(backend.URL, defaultTestTransportConfig(), nil)
			if err != nil {
				t.Fatalf("Failed to create handler: %v", err)
			}

			// Get the httpHandler to access the error handler
			h := handler.(*httpHandler)

			// Create test request and response recorder
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			w := httptest.NewRecorder()

			// Call the error handler directly with our test error
			h.proxy.ErrorHandler(w, req, tt.err)

			if w.Code != tt.wantStatus {
				t.Errorf("Expected status %d for error %v, got %d", tt.wantStatus, tt.err, w.Code)
			}
		})
	}
}

func TestTransportConnectionPoolLimits(t *testing.T) {
	// Create a test backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	// Create proxy handler
	handler, err := NewHandler(backend.URL, defaultTestTransportConfig(), nil)
	require.NoError(t, err)

	// Get the httpHandler to access the transport
	h := handler.(*httpHandler)
	transport := h.proxy.Transport.(*http.Transport)

	// Verify connection pool limits are set
	assert.Equal(t, 50, transport.MaxConnsPerHost, "MaxConnsPerHost should be limited to 50")
	assert.Equal(t, 10, transport.MaxIdleConnsPerHost, "MaxIdleConnsPerHost should be limited to 10")

	// Verify other transport settings are still configured
	assert.Equal(t, 100, transport.MaxIdleConns, "MaxIdleConns should remain at 100")
	assert.NotNil(t, transport.DialContext, "DialContext should be configured")
}

func TestNewHandlerWithHeaders(t *testing.T) {
	tests := []struct {
		name                string
		upstreamHeaders     map[string]string
		downstreamHeaders   map[string]string
		removeUpstream      []string
		removeDownstream    []string
		requestHeaders      map[string]string
		expectedReqHeaders  map[string]string
		backendRespHeaders  map[string]string
		expectedRespHeaders map[string]string
	}{
		{
			name: "add upstream headers",
			upstreamHeaders: map[string]string{
				"X-Custom-Header": "custom-value",
				"X-Service-Name":  "tsbridge",
			},
			requestHeaders: map[string]string{
				"X-Existing": "existing-value",
			},
			expectedReqHeaders: map[string]string{
				"X-Existing":      "existing-value",
				"X-Custom-Header": "custom-value",
				"X-Service-Name":  "tsbridge",
			},
		},
		{
			name:           "remove upstream headers",
			removeUpstream: []string{"X-Remove-Me", "X-Also-Remove"},
			requestHeaders: map[string]string{
				"X-Keep-Me":     "keep-value",
				"X-Remove-Me":   "remove-value",
				"X-Also-Remove": "also-remove",
			},
			expectedReqHeaders: map[string]string{
				"X-Keep-Me": "keep-value",
			},
		},
		{
			name: "add downstream headers",
			downstreamHeaders: map[string]string{
				"X-Response-Custom": "response-value",
				"X-Powered-By":      "tsbridge",
			},
			backendRespHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			expectedRespHeaders: map[string]string{
				"Content-Type":      "application/json",
				"X-Response-Custom": "response-value",
				"X-Powered-By":      "tsbridge",
			},
		},
		{
			name:             "remove downstream headers",
			removeDownstream: []string{"X-Backend-Internal", "X-Debug-Info"},
			backendRespHeaders: map[string]string{
				"Content-Type":       "text/plain",
				"X-Backend-Internal": "internal-value",
				"X-Debug-Info":       "debug-data",
				"X-Keep-This":        "keep-value",
			},
			expectedRespHeaders: map[string]string{
				"Content-Type": "text/plain",
				"X-Keep-This":  "keep-value",
			},
		},
		{
			name: "combined upstream and downstream manipulation",
			upstreamHeaders: map[string]string{
				"X-Request-ID": "req-123",
			},
			removeUpstream: []string{"Authorization"},
			downstreamHeaders: map[string]string{
				"X-Cache-Status": "MISS",
			},
			removeDownstream: []string{"Server"},
			requestHeaders: map[string]string{
				"Authorization": "Bearer token",
				"Accept":        "application/json",
			},
			expectedReqHeaders: map[string]string{
				"Accept":       "application/json",
				"X-Request-ID": "req-123",
			},
			backendRespHeaders: map[string]string{
				"Server":       "nginx/1.19.0",
				"Content-Type": "application/json",
			},
			expectedRespHeaders: map[string]string{
				"Content-Type":   "application/json",
				"X-Cache-Status": "MISS",
			},
		},
		{
			name: "override existing headers",
			upstreamHeaders: map[string]string{
				"X-Forwarded-Host": "override.example.com",
			},
			requestHeaders: map[string]string{
				"X-Forwarded-Host": "original.example.com",
			},
			expectedReqHeaders: map[string]string{
				"X-Forwarded-Host": "override.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test backend server
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify expected request headers
				for key, expected := range tt.expectedReqHeaders {
					assert.Equal(t, expected, r.Header.Get(key), "request header %s", key)
				}

				// Verify removed headers are not present
				for _, key := range tt.removeUpstream {
					assert.Empty(t, r.Header.Get(key), "removed header %s should not be present", key)
				}

				// Set response headers from test case
				for key, value := range tt.backendRespHeaders {
					w.Header().Set(key, value)
				}

				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			}))
			defer backend.Close()

			// Create handler with header configuration
			handler, err := NewHandlerWithHeaders(
				backend.URL,
				defaultTestTransportConfig(),
				nil, // no trusted proxies for this test
				tt.upstreamHeaders,
				tt.downstreamHeaders,
				tt.removeUpstream,
				tt.removeDownstream,
			)
			require.NoError(t, err)

			// Create test request
			req := httptest.NewRequest("GET", "http://example.com/test", nil)
			for key, value := range tt.requestHeaders {
				req.Header.Set(key, value)
			}

			// Execute request
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Verify response headers
			for key, expected := range tt.expectedRespHeaders {
				assert.Equal(t, expected, w.Header().Get(key), "response header %s", key)
			}

			// Verify removed downstream headers are not present
			for _, key := range tt.removeDownstream {
				assert.Empty(t, w.Header().Get(key), "removed downstream header %s should not be present", key)
			}
		})
	}
}
