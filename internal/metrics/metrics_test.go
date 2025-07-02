package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCollector(t *testing.T) {
	t.Run("creates all required metrics", func(t *testing.T) {
		c := NewCollector()

		if c.RequestsTotal == nil {
			t.Error("RequestsTotal metric not initialized")
		}
		if c.RequestDuration == nil {
			t.Error("RequestDuration metric not initialized")
		}
		if c.ErrorsTotal == nil {
			t.Error("ErrorsTotal metric not initialized")
		}
	})
}

func TestCollectorRegistration(t *testing.T) {
	t.Run("registers metrics with prometheus", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		c := NewCollector()

		err := c.Register(reg)
		if err != nil {
			t.Fatalf("failed to register metrics: %v", err)
		}

		// Increment metrics to ensure they show up in gather
		c.RequestsTotal.WithLabelValues("test", "200").Inc()
		c.RequestDuration.WithLabelValues("test").Observe(0.1)
		c.ErrorsTotal.WithLabelValues("test", "backend").Inc()

		// Try to gather metrics - should not panic
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatalf("failed to gather metrics: %v", err)
		}

		// Should have at least 3 metric families
		if len(mfs) < 3 {
			t.Errorf("expected at least 3 metric families, got %d", len(mfs))
		}

		// Verify metric names
		metricNames := make(map[string]bool)
		for _, mf := range mfs {
			metricNames[mf.GetName()] = true
		}

		expectedMetrics := []string{
			"tsbridge_requests_total",
			"tsbridge_request_duration_seconds",
			"tsbridge_errors_total",
		}

		for _, expected := range expectedMetrics {
			if !metricNames[expected] {
				t.Errorf("metric %s not found in registry", expected)
			}
		}
	})
}

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		serviceName   string
		handlerStatus int
		handlerErr    bool
		wantStatus    int
		wantLabels    map[string]string
	}{
		{
			name:          "successful request",
			serviceName:   "web",
			handlerStatus: http.StatusOK,
			handlerErr:    false,
			wantStatus:    http.StatusOK,
			wantLabels: map[string]string{
				"service": "web",
				"status":  "200",
			},
		},
		{
			name:          "client error",
			serviceName:   "api",
			handlerStatus: http.StatusNotFound,
			handlerErr:    false,
			wantStatus:    http.StatusNotFound,
			wantLabels: map[string]string{
				"service": "api",
				"status":  "404",
			},
		},
		{
			name:          "server error",
			serviceName:   "backend",
			handlerStatus: http.StatusInternalServerError,
			handlerErr:    false,
			wantStatus:    http.StatusInternalServerError,
			wantLabels: map[string]string{
				"service": "backend",
				"status":  "500",
			},
		},
		{
			name:          "handler panic",
			serviceName:   "crash",
			handlerStatus: 0,
			handlerErr:    true,
			wantStatus:    http.StatusInternalServerError,
			wantLabels: map[string]string{
				"service": "crash",
				"status":  "500",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test registry and collector
			reg := prometheus.NewRegistry()
			c := NewCollector()
			err := c.Register(reg)
			if err != nil {
				t.Fatalf("failed to register metrics: %v", err)
			}

			// Create test handler
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.handlerErr {
					panic("test panic")
				}
				w.WriteHeader(tt.handlerStatus)
			})

			// Wrap with middleware
			wrapped := c.Middleware(tt.serviceName, handler)

			// Make request
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			// Check response
			if rec.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", rec.Code, tt.wantStatus)
			}

			// Check metrics were recorded
			mfs, err := reg.Gather()
			if err != nil {
				t.Fatalf("failed to gather metrics: %v", err)
			}

			// Verify request counter was incremented
			var foundRequestMetric bool
			for _, mf := range mfs {
				if mf.GetName() == "tsbridge_requests_total" {
					foundRequestMetric = true
					metrics := mf.GetMetric()
					if len(metrics) == 0 {
						t.Error("no metrics recorded for requests_total")
					}
				}
			}
			if !foundRequestMetric {
				t.Error("tsbridge_requests_total metric not found")
			}
		})
	}
}

func TestServer(t *testing.T) {
	t.Run("starts metrics server", func(t *testing.T) {
		// Create server
		s := NewServer(":0") // Use random port

		// Start server
		ctx := context.Background()
		err := s.Start(ctx)
		if err != nil {
			t.Fatalf("failed to start server: %v", err)
		}
		defer s.Shutdown(context.Background())

		// Get the actual address
		addr := s.Addr()
		if addr == "" {
			t.Fatal("server address is empty")
		}

		// Make request to /metrics
		resp, err := http.Get("http://" + addr + "/metrics")
		if err != nil {
			t.Fatalf("failed to get metrics: %v", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				t.Logf("failed to close response body: %v", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("got status %d, want %d", resp.StatusCode, http.StatusOK)
		}

		// Read response
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("failed to read response: %v", err)
		}

		// Should contain prometheus metrics
		if !strings.Contains(string(body), "# HELP") {
			t.Error("response doesn't look like prometheus metrics")
		}
	})

	t.Run("graceful shutdown", func(t *testing.T) {
		s := NewServer(":0")

		ctx := context.Background()
		err := s.Start(ctx)
		if err != nil {
			t.Fatalf("failed to start server: %v", err)
		}

		// Shutdown with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err = s.Shutdown(shutdownCtx)
		if err != nil {
			t.Errorf("shutdown failed: %v", err)
		}
	})
}

func TestRecordError(t *testing.T) {
	tests := []struct {
		name      string
		service   string
		errorType string
	}{
		{
			name:      "backend error",
			service:   "web",
			errorType: "backend",
		},
		{
			name:      "whois error",
			service:   "api",
			errorType: "whois",
		},
		{
			name:      "config error",
			service:   "admin",
			errorType: "config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			c := NewCollector()
			err := c.Register(reg)
			if err != nil {
				t.Fatalf("failed to register metrics: %v", err)
			}

			// Record error
			c.RecordError(tt.service, tt.errorType)

			// Gather metrics
			mfs, err := reg.Gather()
			if err != nil {
				t.Fatalf("failed to gather metrics: %v", err)
			}

			// Find errors_total metric
			var found bool
			for _, mf := range mfs {
				if mf.GetName() == "tsbridge_errors_total" {
					found = true
					metrics := mf.GetMetric()
					if len(metrics) == 0 {
						t.Error("no error metrics recorded")
					}
				}
			}
			if !found {
				t.Error("tsbridge_errors_total metric not found")
			}
		})
	}
}

func TestEnhancedMetrics(t *testing.T) {
	t.Run("ConnectionCountGauge", func(t *testing.T) {
		collector := NewCollector()
		require.NotNil(t, collector.ConnectionCount)

		// Test incrementing connections
		collector.ConnectionCount.WithLabelValues("test-service").Inc()
		assert.Equal(t, float64(1), promtestutil.ToFloat64(collector.ConnectionCount.WithLabelValues("test-service")))

		// Test decrementing connections
		collector.ConnectionCount.WithLabelValues("test-service").Dec()
		assert.Equal(t, float64(0), promtestutil.ToFloat64(collector.ConnectionCount.WithLabelValues("test-service")))
	})

	t.Run("WhoisDurationHistogram", func(t *testing.T) {
		collector := NewCollector()
		require.NotNil(t, collector.WhoisDuration)

		// Test recording whois lookup duration
		start := time.Now()
		time.Sleep(10 * time.Millisecond)
		collector.RecordWhoisDuration("test-service", time.Since(start))

		// Verify histogram was updated
		metric := &dto.Metric{}
		err := collector.WhoisDuration.WithLabelValues("test-service").(prometheus.Histogram).Write(metric)
		require.NoError(t, err)
		assert.Greater(t, metric.Histogram.GetSampleCount(), uint64(0))
	})

	t.Run("OAuthRefreshCounter", func(t *testing.T) {
		collector := NewCollector()
		require.NotNil(t, collector.OAuthRefreshTotal)

		// Test counting OAuth refreshes
		collector.OAuthRefreshTotal.WithLabelValues("success").Inc()
		assert.Equal(t, float64(1), promtestutil.ToFloat64(collector.OAuthRefreshTotal.WithLabelValues("success")))

		collector.OAuthRefreshTotal.WithLabelValues("failure").Inc()
		assert.Equal(t, float64(1), promtestutil.ToFloat64(collector.OAuthRefreshTotal.WithLabelValues("failure")))
	})

	t.Run("BackendHealthGauge", func(t *testing.T) {
		collector := NewCollector()
		require.NotNil(t, collector.BackendHealth)

		// Test setting backend health status
		collector.SetBackendHealth("test-service", true)
		assert.Equal(t, float64(1), promtestutil.ToFloat64(collector.BackendHealth.WithLabelValues("test-service")))

		collector.SetBackendHealth("test-service", false)
		assert.Equal(t, float64(0), promtestutil.ToFloat64(collector.BackendHealth.WithLabelValues("test-service")))
	})

	t.Run("ConnectionPoolMetrics", func(t *testing.T) {
		collector := NewCollector()
		require.NotNil(t, collector.ConnectionPoolActive)
		require.NotNil(t, collector.ConnectionPoolIdle)
		require.NotNil(t, collector.ConnectionPoolWait)

		// Test updating connection pool metrics
		collector.UpdateConnectionPoolMetrics("test-service", 5, 3, 2)

		assert.Equal(t, float64(5), promtestutil.ToFloat64(collector.ConnectionPoolActive.WithLabelValues("test-service")))
		assert.Equal(t, float64(3), promtestutil.ToFloat64(collector.ConnectionPoolIdle.WithLabelValues("test-service")))
		assert.Equal(t, float64(2), promtestutil.ToFloat64(collector.ConnectionPoolWait.WithLabelValues("test-service")))
	})

	t.Run("MetricsRegistration", func(t *testing.T) {
		collector := NewCollector()
		registry := prometheus.NewRegistry()

		// All new metrics should register successfully
		err := collector.Register(registry)
		require.NoError(t, err)
	})
}

func TestRecordWhoisDuration(t *testing.T) {
	collector := NewCollector()

	// Record a duration
	collector.RecordWhoisDuration("test-service", 100*time.Millisecond)

	// Verify the histogram has a sample
	metric := &dto.Metric{}
	err := collector.WhoisDuration.WithLabelValues("test-service").(prometheus.Histogram).Write(metric)
	require.NoError(t, err)

	assert.Equal(t, uint64(1), metric.GetHistogram().GetSampleCount())
	assert.InDelta(t, 0.1, metric.GetHistogram().GetSampleSum(), 0.001)
}

func TestSetBackendHealth(t *testing.T) {
	collector := NewCollector()

	tests := []struct {
		name     string
		service  string
		healthy  bool
		expected float64
	}{
		{"healthy backend", "service1", true, 1},
		{"unhealthy backend", "service2", false, 0},
		{"toggle health", "service3", true, 1},
		{"toggle health back", "service3", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector.SetBackendHealth(tt.service, tt.healthy)
			assert.Equal(t, tt.expected, promtestutil.ToFloat64(collector.BackendHealth.WithLabelValues(tt.service)))
		})
	}

	t.Run("server startup failure returns startup error", func(t *testing.T) {
		server1 := NewServer("invalid:address:format") // Invalid address
		err := server1.Start(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to listen on")
		defer server1.Shutdown(context.Background())
	})

	t.Run("graceful shutdown", func(t *testing.T) {
		server2 := NewServer(":0")
		err := server2.Start(context.Background())
		require.NoError(t, err)

		// Ensure we have a valid address
		addr := server2.Addr()
		assert.NotEmpty(t, addr)

		// Shutdown with a valid context should succeed
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err = server2.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestUpdateConnectionPoolMetrics(t *testing.T) {
	collector := NewCollector()

	tests := []struct {
		name    string
		service string
		active  int
		idle    int
		wait    int
	}{
		{"initial metrics", "service1", 10, 5, 2},
		{"high load", "service2", 50, 0, 10},
		{"idle pool", "service3", 0, 20, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector.UpdateConnectionPoolMetrics(tt.service, tt.active, tt.idle, tt.wait)

			assert.Equal(t, float64(tt.active), promtestutil.ToFloat64(collector.ConnectionPoolActive.WithLabelValues(tt.service)))
			assert.Equal(t, float64(tt.idle), promtestutil.ToFloat64(collector.ConnectionPoolIdle.WithLabelValues(tt.service)))
			assert.Equal(t, float64(tt.wait), promtestutil.ToFloat64(collector.ConnectionPoolWait.WithLabelValues(tt.service)))
		})
	}
}

func TestMetricsErrorTypes(t *testing.T) {
	t.Run("duplicate registration returns resource error", func(t *testing.T) {
		// Create a collector and register it twice
		collector := NewCollector()
		registry := prometheus.NewRegistry()

		// First registration should succeed
		err := collector.Register(registry)
		if err != nil {
			t.Fatalf("first registration failed: %v", err)
		}

		// Second registration should fail with resource error
		err = collector.Register(registry)
		if err == nil {
			t.Fatal("expected error for duplicate registration")
		}

		if !errors.IsResource(err) {
			t.Errorf("expected resource error, got %v", err)
		}
	})

	t.Run("invalid address returns resource error", func(t *testing.T) {
		// Create server with invalid address
		server := NewServer("invalid:address:format")

		ctx := context.Background()
		err := server.Start(ctx)
		if err == nil {
			t.Fatal("expected error for invalid address")
		}

		if !errors.IsResource(err) {
			t.Errorf("expected resource error, got %v", err)
		}
	})

	t.Run("port already in use returns resource error", func(t *testing.T) {
		// Create first server
		server1 := NewServer("127.0.0.1:0") // Use port 0 to get random port
		ctx := context.Background()
		err := server1.Start(ctx)
		if err != nil {
			t.Fatalf("failed to start first server: %v", err)
		}
		defer server1.Shutdown(ctx)

		// Get the address the first server is listening on
		addr := server1.Addr()

		// Try to create second server on same port
		server2 := NewServer(addr)
		err = server2.Start(ctx)
		if err == nil {
			t.Fatal("expected error for port already in use")
		}

		if !errors.IsResource(err) {
			t.Errorf("expected resource error, got %v", err)
		}
	})
}

// TestCollectorRegisterError tests custom error types for collector registration
func TestCollectorRegisterError(t *testing.T) {
	// Create a custom registerer that always fails
	failingRegisterer := &failingRegisterer{
		err: fmt.Errorf("registration failed"),
	}

	collector := NewCollector()
	err := collector.Register(failingRegisterer)
	if err == nil {
		t.Fatal("expected error from failing registerer")
	}

	if !errors.IsResource(err) {
		t.Errorf("expected resource error, got %v", err)
	}
}

// failingRegisterer is a test implementation that always fails
type failingRegisterer struct {
	err error
}

func (f *failingRegisterer) Register(prometheus.Collector) error {
	return f.err
}

func (f *failingRegisterer) MustRegister(...prometheus.Collector) {
	panic("not implemented")
}

func (f *failingRegisterer) Unregister(prometheus.Collector) bool {
	return false
}

func TestMetricsEndpoint(t *testing.T) {
	// Create metrics collector and registry
	collector := NewCollector()
	reg := prometheus.NewRegistry()
	err := collector.Register(reg)
	require.NoError(t, err)

	// Add default Go metrics to ensure we have some output
	reg.MustRegister(collectors.NewGoCollector())

	// Start metrics server
	metricsServer := NewServerWithRegistry(":0", reg, 5*time.Second)
	err = metricsServer.Start(context.TODO())
	require.NoError(t, err)
	defer func() {
		_ = metricsServer.Shutdown(context.TODO())
	}()

	// Get metrics endpoint URL
	metricsURL := fmt.Sprintf("http://%s/metrics", metricsServer.Addr())

	// Test 1: Metrics endpoint should be accessible
	resp, err := http.Get(metricsURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Should contain Prometheus metrics format
	bodyStr := string(body)
	assert.Contains(t, bodyStr, "# HELP")

	// Test 2: Metrics should include our custom metrics (initially empty)
	// Record some test metrics
	collector.RequestsTotal.WithLabelValues("test-service", "200").Inc()
	collector.RequestDuration.WithLabelValues("test-service").Observe(0.5)
	collector.ErrorsTotal.WithLabelValues("test-service", "backend").Inc()

	// Fetch metrics again
	resp2, err := http.Get(metricsURL)
	require.NoError(t, err)
	defer resp2.Body.Close()

	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)

	bodyStr2 := string(body2)

	// Check for our custom metrics
	expectedMetrics := []string{
		"tsbridge_requests_total",
		"tsbridge_request_duration_seconds",
		"tsbridge_errors_total",
	}

	for _, metric := range expectedMetrics {
		assert.Contains(t, bodyStr2, metric)
	}

	// Should contain the test service label
	assert.Contains(t, bodyStr2, `service="test-service"`)
}

func TestMetricsMiddlewareIntegration(t *testing.T) {
	// Create a test backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	}))
	defer backend.Close()

	// Create metrics collector
	collector := NewCollector()
	reg := prometheus.NewRegistry()
	err := collector.Register(reg)
	require.NoError(t, err)

	// Create handler with middleware
	baseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Forward to backend
		resp, err := http.Get(backend.URL + r.URL.Path)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
	handler := collector.Middleware("test-api", baseHandler)

	// Make several requests
	testRequests := []struct {
		path           string
		expectedStatus int
	}{
		{"/", http.StatusOK},
		{"/users", http.StatusOK},
		{"/error", http.StatusInternalServerError},
		{"/api", http.StatusOK},
	}

	for _, tr := range testRequests {
		req := httptest.NewRequest("GET", tr.path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, tr.expectedStatus, rr.Code)
	}

	// Start metrics server to check the metrics
	metricsServer := NewServerWithRegistry(":0", reg, 5*time.Second)
	err = metricsServer.Start(context.TODO())
	require.NoError(t, err)
	defer func() {
		_ = metricsServer.Shutdown(context.TODO())
	}()

	// Get metrics
	metricsURL := fmt.Sprintf("http://%s/metrics", metricsServer.Addr())
	resp, err := http.Get(metricsURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)

	// Verify metrics were recorded
	// Should have 3 requests with status 200
	assert.Contains(t, bodyStr, `tsbridge_requests_total{service="test-api",status="200"} 3`)

	// Should have 1 request with status 500
	assert.Contains(t, bodyStr, `tsbridge_requests_total{service="test-api",status="500"} 1`)

	// Should have request duration metrics
	assert.Contains(t, bodyStr, `tsbridge_request_duration_seconds_count{service="test-api"} 4`)
}

func TestMetricsServerGracefulShutdown(t *testing.T) {
	// Create metrics server
	metricsServer := NewServer(":0")
	err := metricsServer.Start(context.TODO())
	require.NoError(t, err)

	// Verify it's running
	metricsURL := fmt.Sprintf("http://%s/metrics", metricsServer.Addr())
	resp, err := http.Get(metricsURL)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Shutdown the server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = metricsServer.Shutdown(shutdownCtx)
	assert.NoError(t, err)

	// Verify it's no longer accessible
	_, err = http.Get(metricsURL)
	assert.Error(t, err)
}

func TestMiddlewareWebSocketSupport(t *testing.T) {
	// Create a test handler that implements hijacking for WebSocket
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			hijacker, ok := w.(http.Hijacker)
			require.True(t, ok, "ResponseWriter should implement http.Hijacker for WebSocket support")

			conn, bufrw, err := hijacker.Hijack()
			require.NoError(t, err)
			defer conn.Close()

			// Write a simple WebSocket upgrade response
			response := "HTTP/1.1 101 Switching Protocols\r\n" +
				"Upgrade: websocket\r\n" +
				"Connection: Upgrade\r\n" +
				"\r\n"
			bufrw.WriteString(response)
			bufrw.Flush()
		} else {
			w.WriteHeader(http.StatusOK)
		}
	})

	// Create metrics collector
	collector := NewCollector()
	reg := prometheus.NewRegistry()
	err := collector.Register(reg)
	require.NoError(t, err)

	// Wrap handler with metrics middleware
	handler := collector.Middleware("websocket-test", testHandler)

	// Test regular HTTP request
	t.Run("regular HTTP request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	// Test WebSocket upgrade request
	t.Run("WebSocket upgrade request", func(t *testing.T) {
		server := httptest.NewServer(handler)
		defer server.Close()

		// Create WebSocket upgrade request
		req, err := http.NewRequest("GET", server.URL, nil)
		require.NoError(t, err)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

		// Make the request
		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should get 101 Switching Protocols
		assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
		assert.Equal(t, "websocket", resp.Header.Get("Upgrade"))
	})
}

func TestMiddlewareFlushSupport(t *testing.T) {
	// Create a test handler that uses http.Flusher for streaming
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		require.True(t, ok, "ResponseWriter should implement http.Flusher for streaming support")

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "chunk1")
		flusher.Flush()
		fmt.Fprint(w, "chunk2")
		flusher.Flush()
	})

	// Create metrics collector
	collector := NewCollector()
	reg := prometheus.NewRegistry()
	err := collector.Register(reg)
	require.NoError(t, err)

	// Wrap handler with metrics middleware
	handler := collector.Middleware("streaming-test", testHandler)

	// Test streaming response
	req := httptest.NewRequest("GET", "/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "chunk1chunk2", rec.Body.String())
}
