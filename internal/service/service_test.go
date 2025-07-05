package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/middleware"
	"github.com/jtdowney/tsbridge/internal/proxy"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	"github.com/jtdowney/tsbridge/internal/tsnet"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func boolPtr(b bool) *bool {
	return &b
}

// testTailscaleServerFactory creates a tailscale server for testing
func testTailscaleServerFactory() (*tailscale.Server, error) {
	// Create a factory that returns mock tsnet servers
	factory := func() tsnet.TSNetServer {
		return tsnet.NewMockTSNetServer()
	}

	cfg := config.Tailscale{
		AuthKey: "test-key",
	}

	return tailscale.NewServerWithFactory(cfg, factory)
}

// MockTsnetServer simulates a tsnet.Server
type MockTsnetServer struct {
	whoisFunc func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

// MockWhoisAdapter adapts our mock to the WhoisClient interface
type MockWhoisAdapter struct {
	server *MockTsnetServer
}

func (m *MockWhoisAdapter) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if m.server.whoisFunc != nil {
		return m.server.whoisFunc(ctx, remoteAddr)
	}
	return nil, nil
}

func TestRegistry_StartServices(t *testing.T) {
	// Start a mock backend server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	backendAddr := listener.Addr().String()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Create config with services
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Services: []config.Service{
			{
				Name:        "test-service-1",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
			{
				Name:        "test-service-2",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
		},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	// Create tailscale server using the test factory
	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	// Create registry
	registry := NewRegistry(cfg, tsServer)

	// Add metrics collector
	collector := metrics.NewCollector()
	registry.SetMetricsCollector(collector)

	// Start services
	err = registry.StartServices()
	require.NoError(t, err)

	// Verify services were created
	assert.Len(t, registry.services, 2)

	// Verify service properties
	for _, svc := range registry.services {
		assert.NotNil(t, svc.server)
		assert.NotNil(t, svc.listener)
		assert.Equal(t, backendAddr, svc.Config.BackendAddr)
	}

	// Shutdown
	shutdownCtx := context.Background()
	err = registry.Shutdown(shutdownCtx)
	assert.NoError(t, err)
}

func TestRegistry_StartServices_WithBackendHealthCheck(t *testing.T) {
	// With lazy connections, services always start successfully
	// Backend connectivity is only checked when requests come in
	tests := []struct {
		name             string
		services         []config.Service
		expectedServices int
		expectError      bool
	}{
		{
			name: "all services start regardless of backend availability",
			services: []config.Service{
				{Name: "service1", BackendAddr: "localhost:9001"},
				{Name: "service2", BackendAddr: "localhost:9999"}, // Non-existent backend
			},
			expectedServices: 2,
			expectError:      false, // No error expected with lazy connections
		},
		{
			name: "unix socket service starts without backend",
			services: []config.Service{
				{Name: "service1", BackendAddr: "unix:///tmp/nonexistent.sock"},
			},
			expectedServices: 1,
			expectError:      false,
		},
		{
			name:             "no services configured",
			services:         []config.Service{},
			expectedServices: 0,
			expectError:      false, // No error when no services configured
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test config
			cfg := &config.Config{
				Tailscale: config.Tailscale{
					AuthKey: "test-auth-key",
				},
				Global: config.Global{
					ReadHeaderTimeout: config.Duration{Duration: 30 * time.Second},
					WriteTimeout:      config.Duration{Duration: 30 * time.Second},
					IdleTimeout:       config.Duration{Duration: 120 * time.Second},
					ShutdownTimeout:   config.Duration{Duration: 10 * time.Second},
				},
				Services: tt.services,
			}

			// Set defaults
			cfg.SetDefaults()

			// Create tsnet server using the test factory
			tsServer, err := testTailscaleServerFactory()
			require.NoError(t, err, "failed to create tailscale server")

			// Create registry
			registry := NewRegistry(cfg, tsServer)

			// Start services
			err = registry.StartServices()

			if tt.expectError {
				require.Error(t, err, "expected error, got nil")
			} else {
				assert.NoError(t, err, "unexpected error")
			}

			assert.Len(t, registry.services, tt.expectedServices)
		})
	}
}

func TestServiceRegistryErrorTypes(t *testing.T) {
	t.Run("no services started succeeds with zero services", func(t *testing.T) {
		// Create a minimal config with no services
		cfg := &config.Config{
			Global:   config.Global{},
			Services: []config.Service{}, // Empty services
		}

		// Create a dummy tailscale server (will not be used with empty services)
		registry := NewRegistry(cfg, nil)

		err := registry.StartServices()
		require.NoError(t, err, "expected no error when no services configured")

		// Should have zero services
		assert.Len(t, registry.services, 0)
	})

	t.Run("docker provider zero services scenario", func(t *testing.T) {
		// This test verifies the Docker provider use case where tsbridge
		// starts with zero services and dynamically adds them as containers
		// with tsbridge labels are started.
		cfg := &config.Config{
			Global:   config.Global{},
			Services: []config.Service{}, // No initial services
		}

		registry := NewRegistry(cfg, nil)

		// Start services should succeed with zero services
		err := registry.StartServices()
		require.NoError(t, err, "Docker provider should start successfully with zero services")

		// Verify registry has zero services
		assert.Len(t, registry.services, 0, "registry should have zero services")
	})
}

func TestServiceStartupPartialFailures(t *testing.T) {
	// With lazy connections, all services now start successfully
	// These tests are kept for listener creation failures which can still happen

	t.Run("listener creation failure is tracked", func(t *testing.T) {
		cfg := &config.Config{
			Global: config.Global{},
			Services: []config.Service{
				{Name: "service1", BackendAddr: "localhost:8080", TLSMode: "off"},
				{Name: "service2", BackendAddr: "localhost:8081", TLSMode: "off"},
			},
		}

		// For this test, we need a custom factory that will fail listener creation for service1
		serviceCount := 0
		failService1Factory := func() tsnet.TSNetServer {
			serviceCount++
			mock := tsnet.NewMockTSNetServer()
			// Only fail for the first service (service1)
			if serviceCount == 1 {
				mock.ListenFunc = func(network, addr string) (net.Listener, error) {
					return nil, errors.NewResourceError("mock error for service1")
				}
			}
			return mock
		}

		tsServerCfg := config.Tailscale{
			AuthKey: "test-key",
		}
		tsServer, err := tailscale.NewServerWithFactory(tsServerCfg, failService1Factory)
		require.NoError(t, err)
		defer tsServer.Close()

		registry := NewRegistry(cfg, tsServer)

		err = registry.StartServices()
		require.Error(t, err, "expected error when listener creation fails")

		// Should be a ServiceStartupError
		startupErr, ok := errors.AsServiceStartupError(err)
		assert.True(t, ok, "expected ServiceStartupError, got %v", err)

		// Should have 1 failure (service1) and 1 success (service2)
		assert.Equal(t, 1, startupErr.Failed, "expected 1 failed service")
		assert.Equal(t, 1, startupErr.Successful, "expected 1 successful service")

		// Error should mention listener creation
		_, ok = startupErr.Failures["service1"]
		assert.True(t, ok, "expected failure for service1")
	})
}

func TestService_Handler(t *testing.T) {
	// Test that the service handler returns a proxy handler
	svc := &Service{
		Config: config.Service{
			Name:        "test-service",
			BackendAddr: "localhost:8080",
		},
	}

	// Initialize the handler
	handler, err := svc.CreateHandler()
	require.NoError(t, err, "failed to create handler")
	svc.handler = handler

	handler = svc.Handler()

	// Just verify we get a handler
	assert.NotNil(t, handler, "expected handler, got nil")
}

func TestService_HandlerWithResponseHeaderTimeout(t *testing.T) {
	tests := []struct {
		name            string
		globalTimeout   time.Duration
		serviceTimeout  time.Duration
		expectedTimeout time.Duration
	}{
		{
			name:            "use default when no config",
			globalTimeout:   0,
			serviceTimeout:  0,
			expectedTimeout: 30 * time.Second,
		},
		{
			name:            "use global timeout",
			globalTimeout:   45 * time.Second,
			serviceTimeout:  0,
			expectedTimeout: 45 * time.Second,
		},
		{
			name:            "service override global",
			globalTimeout:   45 * time.Second,
			serviceTimeout:  60 * time.Second,
			expectedTimeout: 60 * time.Second,
		},
		{
			name:            "service override with no global",
			globalTimeout:   0,
			serviceTimeout:  15 * time.Second,
			expectedTimeout: 15 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create service with config
			svc := &Service{
				Config: config.Service{
					Name:                  "test-service",
					BackendAddr:           "localhost:8080",
					ResponseHeaderTimeout: config.Duration{Duration: tt.serviceTimeout},
				},
				globalConfig: &config.Config{
					Global: config.Global{
						ResponseHeaderTimeout: config.Duration{Duration: tt.globalTimeout},
					},
				},
			}

			// Initialize the handler
			handler, err := svc.CreateHandler()
			require.NoError(t, err, "failed to create handler")
			svc.handler = handler

			// We can't directly test the timeout value in the handler,
			// but we can verify the handler is created successfully
			handler = svc.Handler()
			assert.NotNil(t, handler, "expected handler, got nil")
		})
	}
}
func TestService_HandlerWithMetrics(t *testing.T) {
	// Create metrics collector
	collector := metrics.NewCollector()
	reg := prometheus.NewRegistry()
	err := collector.Register(reg)
	require.NoError(t, err, "failed to register metrics")

	// Test that the service handler integrates metrics middleware
	svc := &Service{
		Config: config.Service{
			Name:        "test-service",
			BackendAddr: "localhost:8080",
		},
		metricsCollector: collector,
	}

	// Initialize the handler
	handler, err := svc.CreateHandler()
	require.NoError(t, err, "failed to create handler")
	svc.handler = handler

	handler = svc.Handler()

	// Just verify we get a handler
	assert.NotNil(t, handler, "expected handler, got nil")
}

func TestService_isAccessLogEnabled(t *testing.T) {
	tests := []struct {
		name        string
		serviceLog  *bool
		globalLog   *bool
		wantEnabled bool
	}{
		{
			name:        "default to true when nothing specified",
			serviceLog:  nil,
			globalLog:   nil,
			wantEnabled: true,
		},
		{
			name:        "service override true",
			serviceLog:  boolPtr(true),
			globalLog:   boolPtr(false),
			wantEnabled: true,
		},
		{
			name:        "service override false",
			serviceLog:  boolPtr(false),
			globalLog:   boolPtr(true),
			wantEnabled: false,
		},
		{
			name:        "global true, no service override",
			serviceLog:  nil,
			globalLog:   boolPtr(true),
			wantEnabled: true,
		},
		{
			name:        "global false, no service override",
			serviceLog:  nil,
			globalLog:   boolPtr(false),
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{
				Config: config.Service{
					AccessLog: tt.serviceLog,
				},
				globalConfig: &config.Config{
					Global: config.Global{
						AccessLog: tt.globalLog,
					},
				},
			}

			assert.Equal(t, tt.wantEnabled, s.isAccessLogEnabled())
		})
	}
}

func TestService_isAccessLogEnabled_NoGlobalConfig(t *testing.T) {
	// Test when globalConfig is nil
	s := &Service{
		Config: config.Service{
			AccessLog: nil,
		},
		globalConfig: nil,
	}

	// Should default to true
	assert.True(t, s.isAccessLogEnabled())
}

func TestServiceWithWhoisMiddleware(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back the whois headers if present
		for _, header := range []string{"X-Tailscale-User", "X-Tailscale-Name", "X-Tailscale-Login", "X-Tailscale-Addresses"} {
			if value := r.Header.Get(header); value != "" {
				w.Header().Set("Echo-"+header, value)
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	tests := []struct {
		name         string
		whoisEnabled bool
		whoisFunc    func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
		wantHeaders  map[string]string
	}{
		{
			name:         "whois_disabled",
			whoisEnabled: false,
			whoisFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				return &apitype.WhoIsResponse{
					UserProfile: &tailcfg.UserProfile{
						LoginName: "user@example.com",
					},
				}, nil
			},
			wantHeaders: map[string]string{}, // No headers should be added
		},
		{
			name:         "whois_enabled_with_user_info",
			whoisEnabled: true,
			whoisFunc: func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
				return &apitype.WhoIsResponse{
					UserProfile: &tailcfg.UserProfile{
						LoginName:   "user@example.com",
						DisplayName: "Test User",
					},
				}, nil
			},
			wantHeaders: map[string]string{
				"Echo-X-Tailscale-User":  "user@example.com",
				"Echo-X-Tailscale-Name":  "Test User",
				"Echo-X-Tailscale-Login": "user@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create service config
			svcConfig := config.Service{
				Name:         "test-service",
				BackendAddr:  backend.URL,
				WhoisEnabled: &tt.whoisEnabled,
				WhoisTimeout: config.Duration{Duration: 100 * time.Millisecond},
			}

			// Create mock tsnet server
			mockTsnetServer := &MockTsnetServer{
				whoisFunc: tt.whoisFunc,
			}

			// Create service with handler
			svc := &Service{
				Config: svcConfig,
			}

			// Initialize the handler
			handler, err := svc.CreateHandler()
			require.NoError(t, err, "failed to create handler")
			svc.handler = handler

			// Create the handler manually to simulate what would happen
			handler = svc.Handler()

			// If whois is enabled, wrap with middleware
			if tt.whoisEnabled {
				whoisAdapter := &MockWhoisAdapter{server: mockTsnetServer}
				handler = middleware.Whois(whoisAdapter, true, 100*time.Millisecond, nil)(handler)
			}

			// Create test request
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "100.64.1.2:12345"
			w := httptest.NewRecorder()

			// Execute request
			handler.ServeHTTP(w, req)

			// Check status
			assert.Equal(t, http.StatusOK, w.Code)

			// Check headers
			for header, want := range tt.wantHeaders {
				got := w.Header().Get(header)
				assert.Equal(t, want, got, "header %s", header)
			}

			// Check that no headers are present when whois is disabled
			if !tt.whoisEnabled {
				for _, header := range []string{"Echo-X-Tailscale-User", "Echo-X-Tailscale-Name", "Echo-X-Tailscale-Login"} {
					got := w.Header().Get(header)
					assert.Empty(t, got, "header %s should be empty (whois disabled)", header)
				}
			}
		})
	}
}

func TestRegistry_Shutdown(t *testing.T) {

	// Start a mock backend server
	backend, err := net.Listen("tcp", "localhost:8081")
	require.NoError(t, err, "failed to create backend listener")
	defer func() {
		if err := backend.Close(); err != nil {
			t.Logf("failed to close backend: %v", err)
		}
	}()

	// Accept connections in background
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	// Create a test config
	cfg := &config.Config{
		Tailscale: config.Tailscale{
			AuthKey: "test-auth-key",
		},
		Global: config.Global{
			ReadHeaderTimeout: config.Duration{Duration: 30 * 1000000000}, // 30s
			WriteTimeout:      config.Duration{Duration: 30 * 1000000000},
			IdleTimeout:       config.Duration{Duration: 120 * 1000000000},
			ShutdownTimeout:   config.Duration{Duration: 10 * 1000000000},
		},
		Services: []config.Service{
			{
				Name:        "test-service",
				BackendAddr: "localhost:8081",
			},
		},
	}

	// Set defaults
	cfg.SetDefaults()

	// Create tsnet server using the test factory
	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err, "failed to create tailscale server")

	// Create registry and start services
	registry := NewRegistry(cfg, tsServer)
	err = registry.StartServices()
	require.NoError(t, err, "failed to start services")

	// Test graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Global.ShutdownTimeout.Duration)
	defer cancel()

	err = registry.Shutdown(ctx)
	assert.NoError(t, err, "shutdown failed")
}

// TestShutdownWithInflightRequests verifies that shutdown waits for in-flight
// requests to complete, up to the shutdown timeout
func TestShutdownWithInflightRequests(t *testing.T) {
	tests := []struct {
		name                  string
		shutdownTimeout       time.Duration
		requestDuration       time.Duration
		expectRequestComplete bool
	}{
		{
			name:                  "request completes before shutdown timeout",
			shutdownTimeout:       2 * time.Second,
			requestDuration:       500 * time.Millisecond,
			expectRequestComplete: true,
		},
		{
			name:                  "request exceeds shutdown timeout",
			shutdownTimeout:       500 * time.Millisecond,
			requestDuration:       2 * time.Second,
			expectRequestComplete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Track request completion
			var mu sync.Mutex
			var requestCompleted bool
			var activeRequests int32

			// Create a handler that simulates slow processing
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				activeRequests++
				mu.Unlock()
				defer func() {
					mu.Lock()
					activeRequests--
					mu.Unlock()
				}()

				// Use a select to handle both completion and cancellation
				timer := time.NewTimer(tt.requestDuration)
				defer timer.Stop()

				select {
				case <-timer.C:
					// Request completed normally
					mu.Lock()
					requestCompleted = true
					mu.Unlock()
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("OK"))
				case <-r.Context().Done():
					// Request was cancelled due to shutdown
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			})

			// Create an HTTP server
			server := &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 30 * time.Second,
				WriteTimeout:      30 * time.Second,
			}

			// Start server on a random port
			listener, err := net.Listen("tcp", "localhost:0")
			require.NoError(t, err, "failed to create listener")
			defer func() {
				if err := listener.Close(); err != nil {
					t.Logf("failed to close listener: %v", err)
				}
			}()

			// Start server
			var serverErr error
			serverDone := make(chan struct{})
			go func() {
				serverErr = server.Serve(listener)
				close(serverDone)
			}()

			// Wait for server to start
			time.Sleep(50 * time.Millisecond)

			// Start a request in the background
			requestDone := make(chan error, 1)
			go func() {
				client := &http.Client{
					Timeout: 10 * time.Second,
				}
				resp, err := client.Get("http://" + listener.Addr().String())
				if err != nil {
					requestDone <- err
					return
				}
				_ = resp.Body.Close()
				requestDone <- nil
			}()

			// Wait for request to start processing
			time.Sleep(100 * time.Millisecond)

			// Verify request is active
			mu.Lock()
			currentActive := activeRequests
			mu.Unlock()
			assert.Equal(t, int32(1), currentActive, "expected 1 active request")

			// Initiate shutdown
			shutdownStart := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), tt.shutdownTimeout)
			defer cancel()

			shutdownErr := server.Shutdown(ctx)
			shutdownDuration := time.Since(shutdownStart)

			// Wait for server to finish
			<-serverDone

			// Check if server error is expected (ErrServerClosed)
			assert.Equal(t, http.ErrServerClosed, serverErr, "expected ErrServerClosed")

			// Wait for request to complete or timeout
			select {
			case <-requestDone:
				// Request finished (either completed or failed)
			case <-time.After(3 * time.Second):
				require.Fail(t, "request did not finish in time")
			}

			// Verify no active requests remain
			mu.Lock()
			finalActive := activeRequests
			mu.Unlock()
			assert.Equal(t, int32(0), finalActive, "expected 0 active requests after shutdown")

			// Check shutdown duration
			if tt.expectRequestComplete {
				// Shutdown should have waited for request
				assert.GreaterOrEqual(t, shutdownDuration, tt.requestDuration-100*time.Millisecond, "shutdown completed too quickly")
				mu.Lock()
				completed := requestCompleted
				mu.Unlock()
				assert.True(t, completed, "expected request to complete")
				assert.NoError(t, shutdownErr, "expected clean shutdown")
			} else {
				// Shutdown should have timed out
				assert.GreaterOrEqual(t, shutdownDuration, tt.shutdownTimeout-100*time.Millisecond, "shutdown completed too quickly")
				assert.LessOrEqual(t, shutdownDuration, tt.shutdownTimeout+500*time.Millisecond, "shutdown took too long")
				// Context deadline exceeded error is expected
				assert.Equal(t, context.DeadlineExceeded, shutdownErr, "expected context.DeadlineExceeded")
			}
		})
	}
}

// TestConcurrentShutdown verifies that multiple services can shut down concurrently
func TestConcurrentShutdown(t *testing.T) {
	// Create test config
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 2 * time.Second},
		},
	}

	// Create a registry
	registry := &Registry{
		config:   cfg,
		services: make(map[string]*Service),
	}

	// Create multiple mock services with servers
	numServices := 5
	for i := 0; i < numServices; i++ {
		serviceName := "test-service-" + string(rune('a'+i))
		svc := &Service{
			Name: serviceName,
			Config: config.Service{
				Name: serviceName,
			},
			server: &http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Simulate some work
					time.Sleep(100 * time.Millisecond)
					w.WriteHeader(http.StatusOK)
				}),
				ReadHeaderTimeout: 3 * time.Second,
			},
		}

		// Start each service
		listener, err := net.Listen("tcp", "localhost:0")
		require.NoError(t, err, "failed to create listener for service %d", i)
		defer func() {
			if err := listener.Close(); err != nil {
				t.Logf("failed to close listener: %v", err)
			}
		}()

		go func() { _ = svc.server.Serve(listener) }()
		registry.services[serviceName] = svc
	}

	// Wait for all services to start
	time.Sleep(100 * time.Millisecond)

	// Measure concurrent shutdown
	shutdownStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Global.ShutdownTimeout.Duration)
	defer cancel()

	err := registry.Shutdown(ctx)
	shutdownDuration := time.Since(shutdownStart)

	assert.NoError(t, err, "unexpected shutdown error")

	// Verify shutdown was concurrent (should be much less than numServices * 100ms)
	assert.LessOrEqual(t, shutdownDuration, 500*time.Millisecond, "shutdown took too long for concurrent operation")
}

// TestShutdownErrorHandling verifies that shutdown collects and returns all errors
func TestShutdownErrorHandling(t *testing.T) {
	// Create a registry with services that will fail to shutdown
	registry := &Registry{
		config: &config.Config{
			Global: config.Global{
				ShutdownTimeout: config.Duration{Duration: 100 * time.Millisecond},
			},
		},
		services: make(map[string]*Service),
	}

	// Add a service with a handler that never completes
	svc := &Service{
		Name: "hanging-service",
		Config: config.Service{
			Name: "hanging-service",
		},
		server: &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Block forever
				<-r.Context().Done()
			}),
			ReadHeaderTimeout: 3 * time.Second,
		},
	}

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err, "failed to create listener")
	defer func() {
		if err := listener.Close(); err != nil {
			t.Logf("failed to close listener: %v", err)
		}
	}()

	go func() { _ = svc.server.Serve(listener) }()
	registry.services[svc.Config.Name] = svc

	// Make a request that will hang
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		_, _ = client.Get("http://" + listener.Addr().String())
	}()

	// Wait for request to start
	time.Sleep(50 * time.Millisecond)

	// Try to shutdown with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = registry.Shutdown(ctx)

	// Should get an error due to timeout
	assert.Error(t, err, "expected shutdown error due to timeout")

	if err != nil {
		assert.Contains(t, err.Error(), "hanging-service", "error should mention the service name")
	}
}

// TestServiceWithRealProxy tests the service handler with a real proxy
func TestServiceWithRealProxy(t *testing.T) {
	// Create a test backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Response", "true")
		fmt.Fprintf(w, "backend says hello from %s", r.URL.Path)
	}))
	defer backend.Close()

	// Create config
	cfg := &config.Config{
		Global: config.Global{
			ReadHeaderTimeout: config.Duration{Duration: 30 * time.Second},
			WriteTimeout:      config.Duration{Duration: 30 * time.Second},
			IdleTimeout:       config.Duration{Duration: 120 * time.Second},
			ShutdownTimeout:   config.Duration{Duration: 10 * time.Second},
		},
		Services: []config.Service{
			{
				Name:        "test-api",
				BackendAddr: backend.Listener.Addr().String(),
			},
		},
	}

	// Get the service handler directly
	svc := &Service{
		Config: cfg.Services[0],
	}

	// Initialize the handler
	handler, err := svc.CreateHandler()
	require.NoError(t, err)
	svc.SetHandler(handler)

	handler = svc.Handler()

	// Test the proxy
	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		expectedStatus int
		checkResponse  func(t *testing.T, resp *http.Response, body string)
	}{
		{
			name:           "GET request through proxy",
			method:         "GET",
			path:           "/api/users",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp *http.Response, body string) {
				assert.Equal(t, "backend says hello from /api/users", body)
				assert.Equal(t, "true", resp.Header.Get("X-Backend-Response"))
			},
		},
		{
			name:           "POST request through proxy",
			method:         "POST",
			path:           "/api/users",
			body:           `{"name": "test"}`,
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp *http.Response, body string) {
				assert.Equal(t, "backend says hello from /api/users", body)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			// Create response recorder
			rr := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(rr, req)

			// Check status
			assert.Equal(t, tt.expectedStatus, rr.Code)

			// Create response for checking
			resp := rr.Result()
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)

			// Run custom checks
			if tt.checkResponse != nil {
				tt.checkResponse(t, resp, string(body))
			}
		})
	}
}

// TestShutdownIdempotency verifies shutdown can be called multiple times safely
func TestShutdownIdempotency(t *testing.T) {
	registry := &Registry{
		config: &config.Config{
			Global: config.Global{
				ShutdownTimeout: config.Duration{Duration: 1 * time.Second},
			},
		},
		services: make(map[string]*Service),
		mu:       sync.RWMutex{},
	}

	// Add a simple service
	svc := &Service{
		Name: "test-service",
		Config: config.Service{
			Name: "test-service",
		},
		server: &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
			ReadHeaderTimeout: 3 * time.Second,
		},
	}

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err, "failed to create listener")
	defer func() {
		if err := listener.Close(); err != nil {
			t.Logf("failed to close listener: %v", err)
		}
	}()

	go func() { _ = svc.server.Serve(listener) }()
	registry.services[svc.Config.Name] = svc

	// First shutdown
	ctx1, cancel1 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel1()
	err1 := registry.Shutdown(ctx1)
	assert.NoError(t, err1, "first shutdown failed")

	// Second shutdown (should be safe)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()
	err2 := registry.Shutdown(ctx2)
	assert.NoError(t, err2, "second shutdown failed")
}

// TestService_NameField verifies that Service struct has a Name field that is properly set
func TestService_NameField(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
	}{
		{
			name:        "service has name field set from config",
			serviceName: "test-service-1",
		},
		{
			name:        "service with different name",
			serviceName: "api-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Service{
				Name:        tt.serviceName,
				BackendAddr: "localhost:8080",
			}

			svc := &Service{
				Name:   cfg.Name,
				Config: cfg,
			}

			// Test that Service has a Name field
			assert.Equal(t, tt.serviceName, svc.Name, "Service.Name should be set from config")
		})
	}
}

// TestRegistry_StartServices_SetsServiceName verifies that startService sets the Name field
func TestRegistry_StartServices_SetsServiceName(t *testing.T) {
	// Start a mock backend server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	backendAddr := listener.Addr().String()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Create config with services
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Services: []config.Service{
			{
				Name:        "test-service-1",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
			{
				Name:        "test-service-2",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
		},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	// Create tailscale server using the test factory
	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	// Create registry
	registry := NewRegistry(cfg, tsServer)

	// Start services
	err = registry.StartServices()
	require.NoError(t, err)

	// Verify services have Name field set
	assert.Len(t, registry.services, 2)

	// Check each service by name
	for _, expectedSvc := range cfg.Services {
		svc, exists := registry.services[expectedSvc.Name]
		assert.True(t, exists, "Service %s should exist", expectedSvc.Name)
		assert.Equal(t, expectedSvc.Name, svc.Name, "Service.Name should match config")
	}

	// Shutdown
	shutdownCtx := context.Background()
	err = registry.Shutdown(shutdownCtx)
	assert.NoError(t, err)
}

// TestRegistry_ServicesAsMap verifies that Registry stores services in a map
func TestRegistry_ServicesAsMap(t *testing.T) {
	// Start a mock backend server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	backendAddr := listener.Addr().String()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Create config with services
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Services: []config.Service{
			{
				Name:        "test-service-1",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
			{
				Name:        "test-service-2",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
		},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	// Create tailscale server using the test factory
	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	// Create registry
	registry := NewRegistry(cfg, tsServer)

	// Start services
	err = registry.StartServices()
	require.NoError(t, err)

	// Verify services are stored in a map
	registry.mu.Lock()
	defer registry.mu.Unlock()

	// Check that services map exists and has correct entries
	assert.NotNil(t, registry.services)
	assert.Len(t, registry.services, 2)

	// Verify each service is accessible by name
	svc1, exists := registry.services["test-service-1"]
	assert.True(t, exists, "test-service-1 should exist in map")
	assert.NotNil(t, svc1)
	assert.Equal(t, "test-service-1", svc1.Name)

	svc2, exists := registry.services["test-service-2"]
	assert.True(t, exists, "test-service-2 should exist in map")
	assert.NotNil(t, svc2)
	assert.Equal(t, "test-service-2", svc2.Name)
}

// TestRegistry_GetService verifies the GetService method
func TestRegistry_GetService(t *testing.T) {
	// Start a mock backend server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	backendAddr := listener.Addr().String()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Create config with services
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Services: []config.Service{
			{
				Name:        "test-service-1",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
			{
				Name:        "test-service-2",
				BackendAddr: backendAddr,
				TLSMode:     "off",
			},
		},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	// Create tailscale server using the test factory
	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	// Create registry
	registry := NewRegistry(cfg, tsServer)

	// Start services
	err = registry.StartServices()
	require.NoError(t, err)

	// Test GetService for existing services
	svc1, exists := registry.GetService("test-service-1")
	assert.True(t, exists, "test-service-1 should exist")
	assert.NotNil(t, svc1)
	assert.Equal(t, "test-service-1", svc1.Name)

	svc2, exists := registry.GetService("test-service-2")
	assert.True(t, exists, "test-service-2 should exist")
	assert.NotNil(t, svc2)
	assert.Equal(t, "test-service-2", svc2.Name)

	// Test GetService for non-existent service
	svc3, exists := registry.GetService("non-existent-service")
	assert.False(t, exists, "non-existent-service should not exist")
	assert.Nil(t, svc3)

	// Test that GetService is thread-safe
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc, _ := registry.GetService("test-service-1")
			assert.NotNil(t, svc)
		}()
	}
	wg.Wait()
}

// TestRegistry_AddService verifies the AddService method
func TestRegistry_AddService(t *testing.T) {
	tests := []struct {
		name             string
		existingServices []config.Service
		newService       config.Service
		expectError      bool
		errorContains    string
	}{
		{
			name:             "add new service successfully",
			existingServices: []config.Service{},
			newService: config.Service{
				Name:        "new-service",
				BackendAddr: "localhost:9000",
				TLSMode:     "off",
			},
			expectError: false,
		},
		{
			name: "add service when others exist",
			existingServices: []config.Service{
				{Name: "existing-1", BackendAddr: "localhost:8001", TLSMode: "off"},
				{Name: "existing-2", BackendAddr: "localhost:8002", TLSMode: "off"},
			},
			newService: config.Service{
				Name:        "new-service",
				BackendAddr: "localhost:9000",
				TLSMode:     "off",
			},
			expectError: false,
		},
		{
			name: "fail when service already exists",
			existingServices: []config.Service{
				{Name: "existing-service", BackendAddr: "localhost:8001", TLSMode: "off"},
			},
			newService: config.Service{
				Name:        "existing-service",
				BackendAddr: "localhost:9000",
				TLSMode:     "off",
			},
			expectError:   true,
			errorContains: "already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create config with existing services
			cfg := &config.Config{
				Global: config.Global{
					ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
				},
				Services: tt.existingServices,
				Tailscale: config.Tailscale{
					AuthKey: "test-key",
				},
			}

			// Create tailscale server
			tsServer, err := testTailscaleServerFactory()
			require.NoError(t, err)

			// Create registry
			registry := NewRegistry(cfg, tsServer)

			// Start existing services if any
			if len(tt.existingServices) > 0 {
				err = registry.StartServices()
				require.NoError(t, err)
			}

			// Add the new service
			err = registry.AddService(tt.newService)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)

				// Verify service was added
				svc, exists := registry.GetService(tt.newService.Name)
				assert.True(t, exists)
				assert.NotNil(t, svc)
				assert.Equal(t, tt.newService.Name, svc.Name)
				assert.NotNil(t, svc.server)
				assert.NotNil(t, svc.listener)
			}

			// Cleanup
			ctx := context.Background()
			_ = registry.Shutdown(ctx)
		})
	}
}

// TestRegistry_AddService_Concurrent verifies thread safety of AddService
func TestRegistry_AddService_Concurrent(t *testing.T) {
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	registry := NewRegistry(cfg, tsServer)

	// Try to add multiple services concurrently
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			svc := config.Service{
				Name:        fmt.Sprintf("service-%d", idx),
				BackendAddr: fmt.Sprintf("localhost:900%d", idx),
				TLSMode:     "off",
			}
			if err := registry.AddService(svc); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("concurrent add failed: %v", err)
	}

	// Verify all services were added
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("service-%d", i)
		svc, exists := registry.GetService(name)
		assert.True(t, exists, "service %s should exist", name)
		assert.NotNil(t, svc)
	}

	// Cleanup
	ctx := context.Background()
	_ = registry.Shutdown(ctx)
}

// TestRegistry_RemoveService verifies the RemoveService method
func TestRegistry_RemoveService(t *testing.T) {
	tests := []struct {
		name            string
		initialServices []config.Service
		removeService   string
		expectError     bool
		errorContains   string
	}{
		{
			name: "remove existing service",
			initialServices: []config.Service{
				{Name: "service-1", BackendAddr: "localhost:8001", TLSMode: "off"},
				{Name: "service-2", BackendAddr: "localhost:8002", TLSMode: "off"},
			},
			removeService: "service-1",
			expectError:   false,
		},
		{
			name: "remove last remaining service",
			initialServices: []config.Service{
				{Name: "only-service", BackendAddr: "localhost:8001", TLSMode: "off"},
			},
			removeService: "only-service",
			expectError:   false,
		},
		{
			name: "fail when service doesn't exist",
			initialServices: []config.Service{
				{Name: "service-1", BackendAddr: "localhost:8001", TLSMode: "off"},
			},
			removeService: "non-existent",
			expectError:   true,
			errorContains: "not found",
		},
		{
			name:            "fail when no services exist",
			initialServices: []config.Service{},
			removeService:   "any-service",
			expectError:     true,
			errorContains:   "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create config
			cfg := &config.Config{
				Global: config.Global{
					ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
				},
				Services: tt.initialServices,
				Tailscale: config.Tailscale{
					AuthKey: "test-key",
				},
			}

			// Create tailscale server
			tsServer, err := testTailscaleServerFactory()
			require.NoError(t, err)

			// Create registry
			registry := NewRegistry(cfg, tsServer)

			// Start initial services
			if len(tt.initialServices) > 0 {
				err = registry.StartServices()
				require.NoError(t, err)
			}

			// Remove the service
			err = registry.RemoveService(tt.removeService)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)

				// Verify service was removed
				svc, exists := registry.GetService(tt.removeService)
				assert.False(t, exists, "service should not exist after removal")
				assert.Nil(t, svc)

				// Verify other services still exist
				for _, initialSvc := range tt.initialServices {
					if initialSvc.Name != tt.removeService {
						svc, exists := registry.GetService(initialSvc.Name)
						assert.True(t, exists, "service %s should still exist", initialSvc.Name)
						assert.NotNil(t, svc)
					}
				}
			}

			// Cleanup remaining services
			ctx := context.Background()
			_ = registry.Shutdown(ctx)
		})
	}
}

// TestRegistry_RemoveService_VerifyStop verifies that RemoveService properly stops the service
func TestRegistry_RemoveService_VerifyStop(t *testing.T) {
	// Create a channel to verify server shutdown
	serverStopped := make(chan struct{})
	listenerClosed := make(chan struct{})

	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Services: []config.Service{
			{Name: "test-service", BackendAddr: "localhost:8001", TLSMode: "off"},
		},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	registry := NewRegistry(cfg, tsServer)

	// Start service
	err = registry.StartServices()
	require.NoError(t, err)

	// Get the service to verify it exists
	svc, exists := registry.GetService("test-service")
	require.True(t, exists)
	require.NotNil(t, svc)
	require.NotNil(t, svc.server)
	require.NotNil(t, svc.listener)

	// Store references to verify they're properly closed
	originalListener := svc.listener

	// Replace the server's Shutdown method to detect when it's called
	// Create a wrapper goroutine to monitor the original Serve goroutine
	go func() {
		// Wait for the original Serve goroutine to exit
		// This happens when server.Shutdown is called
		for {
			time.Sleep(10 * time.Millisecond)
			// Check if the listener is closed
			conn, err := net.Dial("tcp", originalListener.Addr().String())
			if err != nil {
				// Listener is closed
				close(listenerClosed)
				break
			}
			conn.Close()
		}
	}()

	// Monitor server state
	go func() {
		// The server.Shutdown will be called by RemoveService
		// We can't directly intercept it, but we know it will close the listener
		<-listenerClosed
		close(serverStopped)
	}()

	// Remove the service
	err = registry.RemoveService("test-service")
	require.NoError(t, err)

	// Verify the server was stopped
	select {
	case <-serverStopped:
		// Good, server was stopped
	case <-time.After(1 * time.Second):
		t.Fatal("server was not stopped within timeout")
	}

	// Verify service is removed from registry
	svc, exists = registry.GetService("test-service")
	assert.False(t, exists)
	assert.Nil(t, svc)

	// Verify we can't connect to the original listener
	_, err = net.Dial("tcp", originalListener.Addr().String())
	assert.Error(t, err, "should not be able to connect to closed listener")
}

// TestRegistry_UpdateService verifies the UpdateService method
func TestRegistry_UpdateService(t *testing.T) {
	tests := []struct {
		name           string
		initialService config.Service
		updatedService config.Service
		expectError    bool
		errorContains  string
	}{
		{
			name: "update existing service",
			initialService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8001",
				TLSMode:     "off",
			},
			updatedService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:9001",
				TLSMode:     "off",
			},
			expectError: false,
		},
		{
			name: "update service with new headers",
			initialService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8001",
				TLSMode:     "off",
			},
			updatedService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8001",
				TLSMode:     "off",
				UpstreamHeaders: map[string]string{
					"X-Custom-Header": "value",
				},
			},
			expectError: false,
		},
		{
			name: "fail when service doesn't exist",
			initialService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8001",
				TLSMode:     "off",
			},
			updatedService: config.Service{
				Name:        "non-existent-service",
				BackendAddr: "localhost:9001",
				TLSMode:     "off",
			},
			expectError:   true,
			errorContains: "not found",
		},
		{
			name: "fail early on invalid backend address",
			initialService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8001",
				TLSMode:     "off",
			},
			updatedService: config.Service{
				Name:        "test-service",
				BackendAddr: "", // empty backend address
				TLSMode:     "off",
			},
			expectError:   true,
			errorContains: "backend address is required",
		},
		{
			name: "fail early on invalid TLS mode",
			initialService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8001",
				TLSMode:     "off",
			},
			updatedService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:9001",
				TLSMode:     "invalid", // invalid TLS mode
			},
			expectError:   true,
			errorContains: "invalid TLS mode",
		},
		{
			name: "fail early on invalid unix socket path",
			initialService: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8001",
				TLSMode:     "off",
			},
			updatedService: config.Service{
				Name:        "test-service",
				BackendAddr: "unix://relative/path/socket", // relative path
				TLSMode:     "off",
			},
			expectError:   true,
			errorContains: "unix socket path must be absolute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create config
			cfg := &config.Config{
				Global: config.Global{
					ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
				},
				Services: []config.Service{tt.initialService},
				Tailscale: config.Tailscale{
					AuthKey: "test-key",
				},
			}

			// Create tailscale server
			tsServer, err := testTailscaleServerFactory()
			require.NoError(t, err)

			// Create registry
			registry := NewRegistry(cfg, tsServer)

			// Start initial service
			err = registry.StartServices()
			require.NoError(t, err)

			// Update the service
			err = registry.UpdateService(tt.updatedService.Name, tt.updatedService)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)

				// Verify service was updated
				svc, exists := registry.GetService(tt.updatedService.Name)
				assert.True(t, exists)
				assert.NotNil(t, svc)

				// Verify the service has new configuration
				assert.Equal(t, tt.updatedService.BackendAddr, svc.Config.BackendAddr)
				if len(tt.updatedService.UpstreamHeaders) > 0 {
					assert.Equal(t, tt.updatedService.UpstreamHeaders, svc.Config.UpstreamHeaders)
				}
			}

			// Cleanup
			ctx := context.Background()
			_ = registry.Shutdown(ctx)
		})
	}
}

// TestRegistry_UpdateService_ValidationFailureKeepsOldService verifies that when
// a service update fails due to configuration validation, the old service continues running
func TestRegistry_UpdateService_ValidationFailureKeepsOldService(t *testing.T) {
	// Create initial config
	initialService := config.Service{
		Name:        "test-service",
		BackendAddr: "localhost:8001",
		TLSMode:     "off",
	}

	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Services: []config.Service{initialService},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	// Create tailscale server
	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	// Create registry and start service
	registry := NewRegistry(cfg, tsServer)
	err = registry.StartServices()
	require.NoError(t, err)

	// Verify initial service is running
	svc, exists := registry.GetService("test-service")
	require.True(t, exists)
	require.NotNil(t, svc)
	assert.Equal(t, "localhost:8001", svc.Config.BackendAddr)

	// Attempt to update with invalid configuration
	invalidUpdate := config.Service{
		Name:        "test-service",
		BackendAddr: "", // Invalid: empty backend address
		TLSMode:     "off",
	}

	// Update should fail with validation error
	err = registry.UpdateService("test-service", invalidUpdate)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend address is required")

	// Verify original service is still in the registry and unchanged
	svc, exists = registry.GetService("test-service")
	require.True(t, exists)
	require.NotNil(t, svc)
	assert.Equal(t, "localhost:8001", svc.Config.BackendAddr) // Original config preserved
}

// TestRegistry_UpdateService_Concurrent verifies concurrent updates don't cause issues
func TestRegistry_UpdateService_Concurrent(t *testing.T) {
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
		},
		Services: []config.Service{
			{Name: "service-1", BackendAddr: "localhost:8001", TLSMode: "off"},
			{Name: "service-2", BackendAddr: "localhost:8002", TLSMode: "off"},
		},
		Tailscale: config.Tailscale{
			AuthKey: "test-key",
		},
	}

	tsServer, err := testTailscaleServerFactory()
	require.NoError(t, err)

	registry := NewRegistry(cfg, tsServer)

	// Start services
	err = registry.StartServices()
	require.NoError(t, err)

	// Update services concurrently
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 5; i++ {
		wg.Add(2)

		// Update service-1
		go func(idx int) {
			defer wg.Done()
			updated := config.Service{
				Name:        "service-1",
				BackendAddr: fmt.Sprintf("localhost:900%d", idx),
				TLSMode:     "off",
			}
			if err := registry.UpdateService("service-1", updated); err != nil {
				errors <- err
			}
		}(i)

		// Update service-2
		go func(idx int) {
			defer wg.Done()
			updated := config.Service{
				Name:        "service-2",
				BackendAddr: fmt.Sprintf("localhost:901%d", idx),
				TLSMode:     "off",
			}
			if err := registry.UpdateService("service-2", updated); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		errorCount++
		t.Logf("concurrent update error: %v", err)
	}
	assert.Equal(t, 0, errorCount, "no errors expected in concurrent updates")

	// Verify services still exist
	svc1, exists := registry.GetService("service-1")
	assert.True(t, exists)
	assert.NotNil(t, svc1)

	svc2, exists := registry.GetService("service-2")
	assert.True(t, exists)
	assert.NotNil(t, svc2)

	// Cleanup
	ctx := context.Background()
	_ = registry.Shutdown(ctx)
}

func TestMaxRequestBodySize(t *testing.T) {
	tests := []struct {
		name               string
		globalMaxBodySize  config.ByteSize
		serviceMaxBodySize *config.ByteSize
		requestBodySize    int
		expectedStatus     int
		expectBodyRead     bool
	}{
		{
			name:               "request within global limit",
			globalMaxBodySize:  config.ByteSize{Value: 1024, IsSet: true},
			serviceMaxBodySize: nil,
			requestBodySize:    512,
			expectedStatus:     http.StatusOK,
			expectBodyRead:     true,
		},
		{
			name:               "request exceeds global limit",
			globalMaxBodySize:  config.ByteSize{Value: 1024, IsSet: true},
			serviceMaxBodySize: nil,
			requestBodySize:    2048,
			expectedStatus:     http.StatusRequestEntityTooLarge,
			expectBodyRead:     false,
		},
		{
			name:               "service override allows larger request",
			globalMaxBodySize:  config.ByteSize{Value: 1024, IsSet: true},
			serviceMaxBodySize: &config.ByteSize{Value: 4096, IsSet: true},
			requestBodySize:    2048,
			expectedStatus:     http.StatusOK,
			expectBodyRead:     true,
		},
		{
			name:               "service override restricts to smaller limit",
			globalMaxBodySize:  config.ByteSize{Value: 4096, IsSet: true},
			serviceMaxBodySize: &config.ByteSize{Value: 1024, IsSet: true},
			requestBodySize:    2048,
			expectedStatus:     http.StatusRequestEntityTooLarge,
			expectBodyRead:     false,
		},
		{
			name:               "zero global limit uses default",
			globalMaxBodySize:  config.ByteSize{Value: 0, IsSet: false},
			serviceMaxBodySize: nil,
			requestBodySize:    100,
			expectedStatus:     http.StatusOK,
			expectBodyRead:     true,
		},
		{
			name:               "negative service limit disables check",
			globalMaxBodySize:  config.ByteSize{Value: 1024, IsSet: true},
			serviceMaxBodySize: &config.ByteSize{Value: -1, IsSet: true},
			requestBodySize:    10240,
			expectedStatus:     http.StatusOK,
			expectBodyRead:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test backend
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Read and echo the body size
				body, _ := io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(strconv.Itoa(len(body))))
			}))
			defer backend.Close()

			// Create service config
			svcConfig := config.Service{
				Name:               "test-service",
				BackendAddr:        backend.URL,
				MaxRequestBodySize: tt.serviceMaxBodySize,
			}

			// Create global config
			globalCfg := &config.Config{
				Global: config.Global{
					MaxRequestBodySize: tt.globalMaxBodySize,
				},
			}

			// Create metrics registry and collector
			registry := prometheus.NewRegistry()
			metricsCollector := metrics.NewCollector()
			metricsCollector.Register(registry)

			// Create proxy handler config
			handlerConfig := &proxy.HandlerConfig{
				BackendAddr:      backend.URL,
				ServiceName:      svcConfig.Name,
				MetricsCollector: metricsCollector,
				TransportConfig: &proxy.TransportConfig{
					DialTimeout:           30 * time.Second,
					KeepAliveTimeout:      30 * time.Second,
					IdleConnTimeout:       90 * time.Second,
					TLSHandshakeTimeout:   10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
					ResponseHeaderTimeout: 30 * time.Second,
				},
			}

			// Create proxy handler
			proxyHandler, err := proxy.NewHandler(handlerConfig)
			require.NoError(t, err)
			defer proxyHandler.Close()

			// Get the max body size limit
			var maxBodySize int64
			switch {
			case tt.serviceMaxBodySize != nil && tt.serviceMaxBodySize.IsSet:
				maxBodySize = tt.serviceMaxBodySize.Value
			case globalCfg != nil && globalCfg.Global.MaxRequestBodySize.IsSet:
				maxBodySize = globalCfg.Global.MaxRequestBodySize.Value
			default:
				maxBodySize = constants.DefaultMaxRequestBodySize
			}

			// Apply body limit middleware
			handler := middleware.MaxBytesHandler(maxBodySize)(proxyHandler)

			// Create test request
			body := bytes.Repeat([]byte("a"), tt.requestBodySize)
			req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
			req.Header.Set("Content-Length", strconv.Itoa(tt.requestBodySize))

			// Set request ID header
			req.Header.Set("X-Request-ID", "test-req-id")

			// Execute request
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			// Check response
			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectBodyRead {
				// Backend should have received the full body
				responseBody, _ := io.ReadAll(rec.Body)
				assert.Equal(t, strconv.Itoa(tt.requestBodySize), string(responseBody))
			}
		})
	}
}

func TestMaxRequestBodySizeConfiguration(t *testing.T) {
	tests := []struct {
		name               string
		globalCfg          *config.Config
		serviceMaxBodySize *config.ByteSize
		expectedLimit      int64
	}{
		{
			name:          "global default is applied when not specified",
			globalCfg:     nil,
			expectedLimit: constants.DefaultMaxRequestBodySize,
		},
		{
			name: "service inherits global setting when not specified",
			globalCfg: &config.Config{
				Global: config.Global{
					MaxRequestBodySize: config.ByteSize{Value: 5 * 1024 * 1024, IsSet: true},
				},
			},
			expectedLimit: 5 * 1024 * 1024,
		},
		{
			name: "service can override global setting",
			globalCfg: &config.Config{
				Global: config.Global{
					MaxRequestBodySize: config.ByteSize{Value: 5 * 1024 * 1024, IsSet: true},
				},
			},
			serviceMaxBodySize: &config.ByteSize{Value: 20 * 1024 * 1024, IsSet: true},
			expectedLimit:      20 * 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{
				Config: config.Service{
					MaxRequestBodySize: tt.serviceMaxBodySize,
				},
				globalConfig: tt.globalCfg,
			}

			limit := svc.getMaxRequestBodySize()
			assert.Equal(t, tt.expectedLimit, limit)
		})
	}
}
