package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaxBytesHandler(t *testing.T) {
	tests := []struct {
		name           string
		maxBytes       int64
		contentLength  string
		bodySize       int
		expectedStatus int
		expectBodyRead bool
	}{
		{
			name:           "request within limit",
			maxBytes:       100,
			bodySize:       50,
			expectedStatus: http.StatusOK,
			expectBodyRead: true,
		},
		{
			name:           "request at limit",
			maxBytes:       100,
			bodySize:       100,
			expectedStatus: http.StatusOK,
			expectBodyRead: true,
		},
		{
			name:           "request exceeds limit",
			maxBytes:       100,
			bodySize:       150,
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectBodyRead: false,
		},
		{
			name:           "request with content-length exceeds limit",
			maxBytes:       100,
			contentLength:  "150",
			bodySize:       150,
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectBodyRead: false,
		},
		{
			name:           "negative limit disables check",
			maxBytes:       -1,
			bodySize:       1000,
			expectedStatus: http.StatusOK,
			expectBodyRead: true,
		},
		{
			name:           "zero body allowed",
			maxBytes:       100,
			bodySize:       0,
			expectedStatus: http.StatusOK,
			expectBodyRead: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Track if the handler was called
			handlerCalled := false
			bodyRead := false

			// Create test handler
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				body, err := io.ReadAll(r.Body)
				if err == nil {
					bodyRead = true
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(body)
				} else {
					// If we get an error reading the body, it's likely due to MaxBytesReader
					w.WriteHeader(http.StatusBadRequest)
				}
			})

			// Wrap with MaxBytesHandler
			wrapped := MaxBytesHandler(tt.maxBytes)(handler)

			// Create request
			body := bytes.Repeat([]byte("x"), tt.bodySize)
			req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))

			// Set Content-Length if specified
			if tt.contentLength != "" {
				req.Header.Set("Content-Length", tt.contentLength)
				req.ContentLength = int64(tt.bodySize)
			}

			// Execute request
			recorder := httptest.NewRecorder()
			wrapped.ServeHTTP(recorder, req)

			// Check results
			assert.Equal(t, tt.expectedStatus, recorder.Code)

			if tt.expectBodyRead {
				assert.True(t, handlerCalled, "handler should have been called")
				assert.True(t, bodyRead, "body should have been read")
				if tt.bodySize > 0 {
					assert.Equal(t, body, recorder.Body.Bytes(), "response should echo request body")
				}
			} else {
				// For content-length check, handler might not be called at all
				if tt.contentLength != "" && tt.maxBytes > 0 {
					assert.False(t, handlerCalled, "handler should not have been called")
				}
				assert.Contains(t, recorder.Body.String(), "Request body too large")
			}
		})
	}
}

func TestMaxBytesHandler_ChunkedEncoding(t *testing.T) {
	// Test with chunked encoding (no Content-Length header)
	maxBytes := int64(100)
	bodySize := 150

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			// MaxBytesReader will cause an error when limit is exceeded
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	wrapped := MaxBytesHandler(maxBytes)(handler)

	// Create request without Content-Length (simulating chunked encoding)
	body := strings.Repeat("x", bodySize)
	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.ContentLength = -1 // Force chunked encoding

	recorder := httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, req)

	// The handler will be called but reading the body will fail
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}
