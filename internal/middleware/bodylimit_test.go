package middleware

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaxBytesHandler(t *testing.T) {
	tests := []struct {
		name           string
		maxBytes       int64
		body           string
		contentLength  string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "request within limit",
			maxBytes:       10,
			body:           "hello",
			expectedStatus: http.StatusOK,
			expectedBody:   "hello",
		},
		{
			name:           "request at limit",
			maxBytes:       5,
			body:           "hello",
			expectedStatus: http.StatusOK,
			expectedBody:   "hello",
		},
		{
			name:           "request exceeds limit",
			maxBytes:       5,
			body:           "hello world",
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "Request body too large\n",
		},
		{
			name:           "content-length exceeds limit",
			maxBytes:       5,
			body:           "hello world", // actual body that exceeds limit
			contentLength:  "100",
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "Request body too large\n",
		},
		{
			name:           "negative limit (no limit)",
			maxBytes:       -1,
			body:           strings.Repeat("x", 1000),
			expectedStatus: http.StatusOK,
			expectedBody:   strings.Repeat("x", 1000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Write(body)
			})

			wrapped := MaxBytesHandler(tt.maxBytes)(handler)

			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			if tt.contentLength != "" {
				req.Header.Set("Content-Length", tt.contentLength)
			}

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, tt.expectedBody, rec.Body.String())
		})
	}
}

// mockHijacker is a test implementation of http.ResponseWriter that supports hijacking
type mockHijacker struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (m *mockHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijacked = true
	// Return a mock connection
	server, client := net.Pipe()
	// Close server side immediately for test
	server.Close()
	return client, bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client)), nil
}

func TestMaxBytesHandlerHijacking(t *testing.T) {
	// Test that hijacking still works through our wrapper
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		require.True(t, ok, "ResponseWriter should implement Hijacker")

		conn, _, err := hijacker.Hijack()
		require.NoError(t, err)
		conn.Close()
	})

	wrapped := MaxBytesHandler(1024)(handler)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")

	// Use our mock hijacker
	rec := &mockHijacker{ResponseRecorder: httptest.NewRecorder()}
	wrapped.ServeHTTP(rec, req)

	assert.True(t, rec.hijacked, "Handler should have hijacked the connection")
}

func TestMaxBytesHandlerNoHijackSupport(t *testing.T) {
	// Test when underlying ResponseWriter doesn't support hijacking
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		require.True(t, ok, "ResponseWriter should implement Hijacker")

		_, _, err := hijacker.Hijack()
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not support hijacking")

		// Write normal response when hijacking fails
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("hijacking not supported"))
	})

	wrapped := MaxBytesHandler(1024)(handler)

	req := httptest.NewRequest("GET", "/", nil)

	// Regular ResponseRecorder doesn't support hijacking
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "hijacking not supported", rec.Body.String())
}

func TestMaxBytesHandlerLargeBodyPartialRead(t *testing.T) {
	// Test that MaxBytesReader properly limits body reading
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read more than the limit
		buf := make([]byte, 20)
		n, err := r.Body.Read(buf)

		// Should read up to the limit and then get an error
		if err != nil && err != io.EOF {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}

		w.Write(buf[:n])
	})

	wrapped := MaxBytesHandler(10)(handler)

	req := httptest.NewRequest("POST", "/", strings.NewReader("this is a very long body"))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// The handler should have received the error from MaxBytesReader
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestEarlyBodySizeValidation(t *testing.T) {
	tests := []struct {
		name                 string
		maxBytes             int64
		body                 string
		contentLength        string
		expectBodyRead       bool
		expectedStatus       int
		expectedBodyContains string
	}{
		{
			name:                 "content-length validation prevents body read",
			maxBytes:             10,
			body:                 "this is a very long body that should not be read",
			contentLength:        "50",
			expectBodyRead:       false,
			expectedStatus:       http.StatusRequestEntityTooLarge,
			expectedBodyContains: "Request body too large",
		},
		{
			name:                 "missing content-length allows body processing",
			maxBytes:             10,
			body:                 "short",
			contentLength:        "", // Missing Content-Length
			expectBodyRead:       true,
			expectedStatus:       http.StatusOK,
			expectedBodyContains: "body was read",
		},
		{
			name:                 "content-length -1 (unknown) allows body processing with limit",
			maxBytes:             10,
			body:                 "short body",
			contentLength:        "-1",
			expectBodyRead:       true,
			expectedStatus:       http.StatusOK,
			expectedBodyContains: "body was read",
		},
		{
			name:                 "zero content-length is allowed",
			maxBytes:             10,
			body:                 "",
			contentLength:        "0",
			expectBodyRead:       true,
			expectedStatus:       http.StatusOK,
			expectedBodyContains: "body was read",
		},
		{
			name:                 "content-length exactly at limit is allowed",
			maxBytes:             10,
			body:                 "1234567890",
			contentLength:        "10",
			expectBodyRead:       true,
			expectedStatus:       http.StatusOK,
			expectedBodyContains: "body was read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyRead := false

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodyRead = true
				// Try to read the body
				_, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Write([]byte("body was read"))
			})

			wrapped := MaxBytesHandler(tt.maxBytes)(handler)

			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			if tt.contentLength != "" {
				req.Header.Set("Content-Length", tt.contentLength)
			}

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.expectedBodyContains)
			assert.Equal(t, tt.expectBodyRead, bodyRead, "body read expectation mismatch")
		})
	}
}

// trackingReader wraps an io.Reader to track if it was accessed
type trackingReader struct {
	r        io.Reader
	accessed bool
}

func (tr *trackingReader) Read(p []byte) (n int, err error) {
	tr.accessed = true
	return tr.r.Read(p)
}

func TestMaxBytesHandlerNoUnnecessaryReads(t *testing.T) {
	// Test with large Content-Length that should be rejected early
	tr := &trackingReader{r: strings.NewReader("large body content")}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This should never be called for oversized requests
		t.Error("Handler should not be called for oversized Content-Length")
	})

	wrapped := MaxBytesHandler(10)(handler)

	req := httptest.NewRequest("POST", "/", tr)
	req.ContentLength = 1000 // Much larger than limit
	req.Header.Set("Content-Length", "1000")

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.False(t, tr.accessed, "Body should not be accessed when Content-Length exceeds limit")
}

func TestMaxBytesHandlerMissingContentLength(t *testing.T) {
	// When Content-Length is missing, the handler should still enforce limits
	// but needs to allow the request to proceed with MaxBytesReader protection

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Attempt to read more than the limit
		data := make([]byte, 100)
		n, err := io.ReadFull(r.Body, data)

		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			// MaxBytesReader should have limited the read
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}

		w.Write([]byte("read successful"))
		w.Write(data[:n])
	})

	wrapped := MaxBytesHandler(10)(handler)

	// Create request without Content-Length header
	body := strings.NewReader("this is a very long body that exceeds the limit")
	req := httptest.NewRequest("POST", "/", body)
	req.ContentLength = -1 // Explicitly set to -1 (unknown length)
	// Don't set Content-Length header

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// The response should indicate the body was limited
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), "Request body too large")
}

func TestMaxBytesHandlerFlushSupport(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		require.True(t, ok, "ResponseWriter should implement http.Flusher for streaming support")

		w.Write([]byte("chunk1"))
		flusher.Flush()
		w.Write([]byte("chunk2"))
		flusher.Flush()
	})

	wrapped := MaxBytesHandler(1024)(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.True(t, rec.Flushed, "Handler should have flushed the response")
	assert.Equal(t, "chunk1chunk2", rec.Body.String())
}
