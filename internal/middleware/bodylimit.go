// Package middleware implements HTTP middleware for request processing.
package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// MaxBytesHandler returns a middleware that limits the size of request bodies.
// If maxBytes is negative, no limit is applied.
func MaxBytesHandler(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if maxBytes < 0 {
			// No limit
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check Content-Length header first for efficiency
			if r.ContentLength > maxBytes {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}

			// Wrap the body with MaxBytesReader
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

			// Use a custom response writer to catch MaxBytesError
			rw := &maxBytesResponseWriter{
				ResponseWriter: w,
				written:        false,
			}

			next.ServeHTTP(rw, r)

			// If MaxBytesReader detected oversized body, it calls our Error method
			// Check if we need to send 413 response
			if rw.oversized && !rw.written {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			}
		})
	}
}

// maxBytesResponseWriter wraps http.ResponseWriter to intercept MaxBytesReader errors
type maxBytesResponseWriter struct {
	http.ResponseWriter
	written   bool
	oversized bool
}

func (w *maxBytesResponseWriter) Write(b []byte) (int, error) {
	w.written = true
	return w.ResponseWriter.Write(b)
}

func (w *maxBytesResponseWriter) WriteHeader(statusCode int) {
	w.written = true
	w.ResponseWriter.WriteHeader(statusCode)
}

// Error is called by MaxBytesReader when the body exceeds the limit
func (w *maxBytesResponseWriter) Error(msg string, code int) {
	if code == http.StatusRequestEntityTooLarge {
		w.oversized = true
		// Don't write the error here, let our middleware handle it
		return
	}
	// For other errors, use default behavior
	http.Error(w, msg, code)
}

// Hijack implements the http.Hijacker interface
func (w *maxBytesResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

// Flush implements the http.Flusher interface for streaming support
func (w *maxBytesResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
