// Package metrics handles Prometheus metrics collection and exposition.
package metrics

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector holds all prometheus metrics for tsbridge
type Collector struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	ErrorsTotal     *prometheus.CounterVec

	// Enhanced metrics
	ConnectionCount      *prometheus.GaugeVec
	WhoisDuration        *prometheus.HistogramVec
	OAuthRefreshTotal    *prometheus.CounterVec
	BackendHealth        *prometheus.GaugeVec
	ConnectionPoolActive *prometheus.GaugeVec
	ConnectionPoolIdle   *prometheus.GaugeVec
	ConnectionPoolWait   *prometheus.GaugeVec
}

// NewCollector creates a new metrics collector with all required metrics
func NewCollector() *Collector {
	return &Collector{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tsbridge_requests_total",
				Help: "Total number of requests processed",
			},
			[]string{"service", "status"},
		),
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "tsbridge_request_duration_seconds",
				Help:    "Request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"service"},
		),
		ErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tsbridge_errors_total",
				Help: "Total number of errors",
			},
			[]string{"service", "type"},
		),
		ConnectionCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tsbridge_connections_active",
				Help: "Number of active connections per service",
			},
			[]string{"service"},
		),
		WhoisDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "tsbridge_whois_duration_seconds",
				Help:    "Whois lookup duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"service"},
		),
		OAuthRefreshTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tsbridge_oauth_refresh_total",
				Help: "Total number of OAuth token refreshes",
			},
			[]string{"status"},
		),
		BackendHealth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tsbridge_backend_health",
				Help: "Backend health status (1 = healthy, 0 = unhealthy)",
			},
			[]string{"service"},
		),
		ConnectionPoolActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tsbridge_connection_pool_active",
				Help: "Number of active connections in the pool",
			},
			[]string{"service"},
		),
		ConnectionPoolIdle: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tsbridge_connection_pool_idle",
				Help: "Number of idle connections in the pool",
			},
			[]string{"service"},
		),
		ConnectionPoolWait: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tsbridge_connection_pool_wait",
				Help: "Number of requests waiting for a connection",
			},
			[]string{"service"},
		),
	}
}

// Register registers all metrics with the provided registry
func (c *Collector) Register(reg prometheus.Registerer) error {
	collectors := []prometheus.Collector{
		c.RequestsTotal,
		c.RequestDuration,
		c.ErrorsTotal,
		c.ConnectionCount,
		c.WhoisDuration,
		c.OAuthRefreshTotal,
		c.BackendHealth,
		c.ConnectionPoolActive,
		c.ConnectionPoolIdle,
		c.ConnectionPoolWait,
	}

	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			return errors.WrapResource(err, "failed to register collector")
		}
	}

	return nil
}

// RecordError increments the error counter for a service and error type
func (c *Collector) RecordError(service, errorType string) {
	c.ErrorsTotal.WithLabelValues(service, errorType).Inc()
}

// RecordWhoisDuration records the duration of a whois lookup
func (c *Collector) RecordWhoisDuration(service string, duration time.Duration) {
	c.WhoisDuration.WithLabelValues(service).Observe(duration.Seconds())
}

// SetBackendHealth sets the health status of a backend
func (c *Collector) SetBackendHealth(service string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	c.BackendHealth.WithLabelValues(service).Set(value)
}

// UpdateConnectionPoolMetrics updates connection pool metrics for a service
func (c *Collector) UpdateConnectionPoolMetrics(service string, active, idle, wait int) {
	c.ConnectionPoolActive.WithLabelValues(service).Set(float64(active))
	c.ConnectionPoolIdle.WithLabelValues(service).Set(float64(idle))
	c.ConnectionPoolWait.WithLabelValues(service).Set(float64(wait))
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// Hijack implements the http.Hijacker interface for WebSocket support
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("ResponseWriter does not support hijacking")
}

// Flush implements the http.Flusher interface for streaming support
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Middleware returns HTTP middleware that records metrics for requests
func (c *Collector) Middleware(serviceName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Recover from panics
		defer func() {
			if err := recover(); err != nil {
				// Write error response if not already written
				if !wrapped.written {
					wrapped.WriteHeader(http.StatusInternalServerError)
				}
				// Record error
				c.RecordError(serviceName, "panic")
			}

			// Record metrics
			duration := time.Since(start)
			c.RequestDuration.WithLabelValues(serviceName).Observe(duration.Seconds())
			c.RequestsTotal.WithLabelValues(serviceName, strconv.Itoa(wrapped.statusCode)).Inc()
		}()

		// Call next handler
		next.ServeHTTP(wrapped, r)
	})
}

// Server represents a metrics HTTP server
type Server struct {
	addr              string
	server            *http.Server
	listener          net.Listener
	registry          *prometheus.Registry
	readHeaderTimeout time.Duration
}

// NewServer creates a new metrics server with a custom registry
func NewServer(addr string, registry *prometheus.Registry, readHeaderTimeout time.Duration) *Server {
	return &Server{
		addr:              addr,
		registry:          registry,
		readHeaderTimeout: readHeaderTimeout,
	}
}

// Start starts the metrics server
func (s *Server) Start(ctx context.Context) error {
	// Create prometheus handler
	handler := promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})

	// Create listener
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return errors.WrapResource(err, fmt.Sprintf("failed to listen on %s", s.addr))
	}
	s.listener = listener

	// Create server
	timeout := s.readHeaderTimeout
	if timeout == 0 {
		timeout = 5 * time.Second // Default if not set
	}
	s.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: timeout,
	}

	// Start serving in background
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash
			slog.Error("metrics server error", "error", err)
		}
	}()

	return nil
}

// Addr returns the actual address the server is listening on
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Shutdown gracefully shuts down the metrics server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
