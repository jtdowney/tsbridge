package middleware

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// accessLogResponseWriter wraps http.ResponseWriter to capture response details
type accessLogResponseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

// WriteHeader captures the status code
func (w *accessLogResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write captures the response size
func (w *accessLogResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

// Hijack implements the http.Hijacker interface for WebSocket support
func (w *accessLogResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("ResponseWriter does not support hijacking")
}

// Flush implements the http.Flusher interface for streaming support
func (w *accessLogResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// AccessLog returns a middleware that logs HTTP requests
func AccessLog(logger *slog.Logger, serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap the response writer to capture status and size
			wrapped := &accessLogResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK, // Default to 200
			}

			// Process request
			next.ServeHTTP(wrapped, r)

			// Calculate duration
			duration := time.Since(start)

			// Build log attributes
			attrs := []slog.Attr{
				slog.String("service", serviceName),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", wrapped.statusCode),
				slog.Int("size", wrapped.size),
				slog.Float64("duration_ms", float64(duration.Microseconds())/1000.0),
			}

			// Add request ID if available (from context or header)
			requestID := GetRequestID(r.Context())
			if requestID == "" {
				// Fallback to header if not in context
				requestID = r.Header.Get("X-Request-ID")
			}
			if requestID != "" {
				attrs = append(attrs, slog.String("request_id", requestID))
			}

			// Add user agent if present
			if ua := r.Header.Get("User-Agent"); ua != "" {
				attrs = append(attrs, slog.String("user_agent", ua))
			}

			// Add remote address
			attrs = append(attrs, slog.String("remote_addr", r.RemoteAddr))

			// Log the request
			logger.LogAttrs(r.Context(), slog.LevelInfo, "HTTP request", attrs...)
		})
	}
}
