package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/middleware"
	"github.com/jtdowney/tsbridge/internal/proxy"
	"github.com/stretchr/testify/assert"
)

func TestRequestIDIntegration(t *testing.T) {
	// Create a test backend that captures headers
	var capturedBackendHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBackendHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	// Create proxy handler
	transportConfig := &proxy.TransportConfig{
		ResponseHeaderTimeout: 30 * time.Second,
		DialTimeout:           30 * time.Second,
		KeepAliveTimeout:      30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	proxyHandler, err := proxy.NewHandler(&proxy.HandlerConfig{
		BackendAddr:     backend.URL,
		TransportConfig: transportConfig,
		TrustedProxies:  nil,
	})
	if err != nil {
		t.Fatalf("Failed to create proxy handler: %v", err)
	}

	// Wrap with RequestID middleware
	handler := middleware.RequestID(proxyHandler)

	// Test with provided request ID
	t.Run("with provided request ID", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Request-ID", "test-request-123")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		// Verify response
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "test-request-123", rr.Header().Get("X-Request-ID"))

		// Verify backend received the request ID
		assert.Equal(t, "test-request-123", capturedBackendHeaders.Get("X-Request-ID"))
	})

	// Test without provided request ID
	t.Run("without provided request ID", func(t *testing.T) {
		// Reset captured headers
		capturedBackendHeaders = nil

		req := httptest.NewRequest("GET", "/test", nil)

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		// Verify response
		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify a request ID was generated
		responseRequestID := rr.Header().Get("X-Request-ID")
		assert.NotEmpty(t, responseRequestID)
		assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, responseRequestID)

		// Verify backend received the generated request ID
		assert.Equal(t, responseRequestID, capturedBackendHeaders.Get("X-Request-ID"))
	})
}

func TestRequestIDWithContext(t *testing.T) {
	// Test that request ID is properly stored in context
	var contextRequestID string

	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextRequestID = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "context-test-456")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, "context-test-456", contextRequestID)
}
