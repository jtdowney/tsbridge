package middleware

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/assert"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccessLog(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		requestID      string
		responseStatus int
		responseSize   int
		userAgent      string
		wantLogged     bool
		checkFields    map[string]interface{}
	}{
		{
			name:           "logs successful request",
			method:         "GET",
			path:           "/api/test",
			requestID:      "test-123",
			responseStatus: http.StatusOK,
			responseSize:   100,
			userAgent:      "test-agent/1.0",
			wantLogged:     true,
			checkFields: map[string]interface{}{
				"method":     "GET",
				"path":       "/api/test",
				"status":     200,
				"size":       100,
				"request_id": "test-123",
				"user_agent": "test-agent/1.0",
			},
		},
		{
			name:           "logs error request",
			method:         "POST",
			path:           "/api/error",
			requestID:      "error-456",
			responseStatus: http.StatusInternalServerError,
			responseSize:   50,
			wantLogged:     true,
			checkFields: map[string]interface{}{
				"method":     "POST",
				"path":       "/api/error",
				"status":     500,
				"size":       50,
				"request_id": "error-456",
			},
		},
		{
			name:           "logs request without request ID",
			method:         "GET",
			path:           "/health",
			requestID:      "",
			responseStatus: http.StatusOK,
			responseSize:   10,
			wantLogged:     true,
			checkFields: map[string]interface{}{
				"method": "GET",
				"path":   "/health",
				"status": 200,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture log output
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))

			// Create test handler
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.responseStatus)
				if tt.responseSize > 0 {
					w.Write(make([]byte, tt.responseSize))
				}
			})

			// Wrap with access log middleware
			wrapped := AccessLog(logger, "test-service")(handler)

			// Create request
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.requestID != "" {
				req.Header.Set("X-Request-ID", tt.requestID)
			}
			if tt.userAgent != "" {
				req.Header.Set("User-Agent", tt.userAgent)
			}

			// Execute request
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)

			// Check response
			assert.Equal(t, tt.responseStatus, rr.Code)

			// Check logs
			logOutput := buf.String()
			if tt.wantLogged {
				assert.NotEmpty(t, logOutput)

				// Verify log contains expected fields
				for field, value := range tt.checkFields {
					if field == "request_id" && value == "" {
						// Should not contain request_id field if empty
						assert.NotContains(t, logOutput, `"request_id"`)
					} else {
						assert.Contains(t, logOutput, field)
						// Convert value to string for comparison
						switch v := value.(type) {
						case string:
							assert.Contains(t, logOutput, v)
						case int:
							assert.Contains(t, logOutput, strings.TrimSpace(fmt.Sprintf("%d", v)))
						}
					}
				}

				// Should always contain duration
				assert.Contains(t, logOutput, "duration_ms")
				assert.Contains(t, logOutput, "service")
				assert.Contains(t, logOutput, "test-service")
			} else {
				assert.Equal(t, "", logOutput)
			}
		})
	}
}

func TestAccessLogWithRequestIDFromContext(t *testing.T) {
	// Test that access log can get request ID from context (set by RequestID middleware)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Chain RequestID -> AccessLog -> Handler
	chain := RequestID(AccessLog(logger, "test-service")(handler))

	// Request without X-Request-ID header (will be generated)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	// Check that log contains the generated request ID
	logOutput := buf.String()
	assert.Contains(t, logOutput, "request_id")
	// Should contain a UUID pattern
	assert.Regexp(t, `"request_id":"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"`, logOutput)
}

func TestAccessLogResponseWriter(t *testing.T) {
	// Test the response writer wrapper tracks size correctly
	rw := &accessLogResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		statusCode:     http.StatusOK,
	}

	// Write some data
	data := []byte("Hello, World!")
	n, err := rw.Write(data)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, len(data), rw.size)

	// Write more data
	moreData := []byte(" More data")
	n, err = rw.Write(moreData)
	assert.NoError(t, err)
	assert.Equal(t, len(moreData), n)
	assert.Equal(t, len(data)+len(moreData), rw.size)

	// Test WriteHeader
	rw.WriteHeader(http.StatusNotFound)
	assert.Equal(t, http.StatusNotFound, rw.statusCode)
}

func TestAccessLogWebSocketSupport(t *testing.T) {
	// Test that access log middleware supports WebSocket hijacking
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create a handler that requires hijacking
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			hijacker, ok := w.(http.Hijacker)
			assert.True(t, ok, "ResponseWriter should implement http.Hijacker for WebSocket support")

			conn, bufrw, err := hijacker.Hijack()
			assert.NoError(t, err)
			defer conn.Close()

			// Write WebSocket upgrade response
			response := "HTTP/1.1 101 Switching Protocols\r\n" +
				"Upgrade: websocket\r\n" +
				"Connection: Upgrade\r\n" +
				"\r\n"
			bufrw.WriteString(response)
			bufrw.Flush()
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		}
	})

	// Wrap with access log middleware
	wrapped := AccessLog(logger, "websocket-test")(handler)

	t.Run("regular HTTP request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "OK", rec.Body.String())
	})

	t.Run("WebSocket upgrade request", func(t *testing.T) {
		server := httptest.NewServer(wrapped)
		defer server.Close()

		// Create WebSocket upgrade request
		req, err := http.NewRequest("GET", server.URL, nil)
		assert.NoError(t, err)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

		// Make the request
		client := &http.Client{}
		resp, err := client.Do(req)
		assert.NoError(t, err)
		defer resp.Body.Close()

		// Should get 101 Switching Protocols
		assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
		assert.Equal(t, "websocket", resp.Header.Get("Upgrade"))
	})
}

func TestAccessLogFlushSupport(t *testing.T) {
	// Test that access log middleware supports http.Flusher for streaming
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create a handler that uses flushing
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		assert.True(t, ok, "ResponseWriter should implement http.Flusher for streaming support")

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "chunk1")
		flusher.Flush()
		fmt.Fprint(w, "chunk2")
		flusher.Flush()
	})

	// Wrap with access log middleware
	wrapped := AccessLog(logger, "streaming-test")(handler)

	// Test streaming response
	req := httptest.NewRequest("GET", "/stream", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "chunk1chunk2", rec.Body.String())
}
