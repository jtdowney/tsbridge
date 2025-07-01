package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/middleware"
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr || len(s) > len(substr) && contains(s[1:], substr)
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
			expectError:      true, // Still error if no services configured
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
			if err != nil {
				t.Fatalf("failed to create tailscale server: %v", err)
			}

			// Create registry
			registry := NewRegistry(cfg, tsServer)

			// Start services
			err = registry.StartServices()

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if len(registry.services) != tt.expectedServices {
				t.Errorf("expected %d services, got %d", tt.expectedServices, len(registry.services))
			}
		})
	}
}

func TestServiceRegistryErrorTypes(t *testing.T) {
	t.Run("no services started returns internal error", func(t *testing.T) {
		// Create a minimal config with no services
		cfg := &config.Config{
			Global:   config.Global{},
			Services: []config.Service{}, // Empty services
		}

		// Create a dummy tailscale server (will not be used with empty services)
		registry := NewRegistry(cfg, nil)

		err := registry.StartServices()
		if err == nil {
			t.Fatal("expected error when no services configured")
		}

		// Should be an internal error
		if !errors.IsInternal(err) {
			t.Errorf("expected internal error, got %v", err)
		}
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
		if err != nil {
			t.Fatal(err)
		}
		defer tsServer.Close()

		registry := NewRegistry(cfg, tsServer)

		err = registry.StartServices()
		if err == nil {
			t.Fatal("expected error when listener creation fails")
		}

		// Should be a ServiceStartupError
		startupErr, ok := errors.AsServiceStartupError(err)
		if !ok {
			t.Errorf("expected ServiceStartupError, got %v", err)
		}

		// Should have 1 failure (service1) and 1 success (service2)
		if startupErr.Failed != 1 {
			t.Errorf("expected 1 failed service, got %d", startupErr.Failed)
		}
		if startupErr.Successful != 1 {
			t.Errorf("expected 1 successful service, got %d", startupErr.Successful)
		}

		// Error should mention listener creation
		if _, ok := startupErr.Failures["service1"]; !ok {
			t.Error("expected failure for service1")
		}
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
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	svc.handler = handler

	handler = svc.Handler()

	// Just verify we get a handler
	if handler == nil {
		t.Error("expected handler, got nil")
	}
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
			if err != nil {
				t.Fatalf("failed to create handler: %v", err)
			}
			svc.handler = handler

			// We can't directly test the timeout value in the handler,
			// but we can verify the handler is created successfully
			handler = svc.Handler()
			if handler == nil {
				t.Error("expected handler, got nil")
			}
		})
	}
}
func TestService_HandlerWithMetrics(t *testing.T) {
	// Create metrics collector
	collector := metrics.NewCollector()
	reg := prometheus.NewRegistry()
	err := collector.Register(reg)
	if err != nil {
		t.Fatalf("failed to register metrics: %v", err)
	}

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
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}
	svc.handler = handler

	handler = svc.Handler()

	// Just verify we get a handler
	if handler == nil {
		t.Error("expected handler, got nil")
	}
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
			if err != nil {
				t.Fatalf("failed to create handler: %v", err)
			}
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
			if w.Code != http.StatusOK {
				t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
			}

			// Check headers
			for header, want := range tt.wantHeaders {
				got := w.Header().Get(header)
				if got != want {
					t.Errorf("header %s = %q, want %q", header, got, want)
				}
			}

			// Check that no headers are present when whois is disabled
			if !tt.whoisEnabled {
				for _, header := range []string{"Echo-X-Tailscale-User", "Echo-X-Tailscale-Name", "Echo-X-Tailscale-Login"} {
					if got := w.Header().Get(header); got != "" {
						t.Errorf("header %s = %q, want empty (whois disabled)", header, got)
					}
				}
			}
		})
	}
}

func TestRegistry_Shutdown(t *testing.T) {

	// Start a mock backend server
	backend, err := net.Listen("tcp", "localhost:8081")
	if err != nil {
		t.Fatalf("failed to create backend listener: %v", err)
	}
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
	if err != nil {
		t.Fatalf("failed to create tailscale server: %v", err)
	}

	// Create registry and start services
	registry := NewRegistry(cfg, tsServer)
	err = registry.StartServices()
	if err != nil {
		t.Fatalf("failed to start services: %v", err)
	}

	// Test graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Global.ShutdownTimeout.Duration)
	defer cancel()

	err = registry.Shutdown(ctx)
	if err != nil {
		t.Errorf("shutdown failed: %v", err)
	}
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
			if err != nil {
				t.Fatalf("failed to create listener: %v", err)
			}
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
			if currentActive != 1 {
				t.Errorf("expected 1 active request, got %d", currentActive)
			}

			// Initiate shutdown
			shutdownStart := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), tt.shutdownTimeout)
			defer cancel()

			shutdownErr := server.Shutdown(ctx)
			shutdownDuration := time.Since(shutdownStart)

			// Wait for server to finish
			<-serverDone

			// Check if server error is expected (ErrServerClosed)
			if serverErr != http.ErrServerClosed {
				t.Errorf("expected ErrServerClosed, got %v", serverErr)
			}

			// Wait for request to complete or timeout
			select {
			case <-requestDone:
				// Request finished (either completed or failed)
			case <-time.After(3 * time.Second):
				t.Fatal("request did not finish in time")
			}

			// Verify no active requests remain
			mu.Lock()
			finalActive := activeRequests
			mu.Unlock()
			if finalActive != 0 {
				t.Errorf("expected 0 active requests after shutdown, got %d", finalActive)
			}

			// Check shutdown duration
			if tt.expectRequestComplete {
				// Shutdown should have waited for request
				if shutdownDuration < tt.requestDuration-100*time.Millisecond {
					t.Errorf("shutdown completed too quickly: %v < %v", shutdownDuration, tt.requestDuration)
				}
				mu.Lock()
				completed := requestCompleted
				mu.Unlock()
				if !completed {
					t.Error("expected request to complete, but it didn't")
				}
				if shutdownErr != nil {
					t.Errorf("expected clean shutdown, got error: %v", shutdownErr)
				}
			} else {
				// Shutdown should have timed out
				if shutdownDuration < tt.shutdownTimeout-100*time.Millisecond {
					t.Errorf("shutdown completed too quickly: %v < %v", shutdownDuration, tt.shutdownTimeout)
				}
				if shutdownDuration > tt.shutdownTimeout+500*time.Millisecond {
					t.Errorf("shutdown took too long: %v > %v", shutdownDuration, tt.shutdownTimeout)
				}
				// Context deadline exceeded error is expected
				if shutdownErr == nil || shutdownErr != context.DeadlineExceeded {
					t.Errorf("expected context.DeadlineExceeded, got %v", shutdownErr)
				}
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
		services: make([]*Service, 0),
	}

	// Create multiple mock services with servers
	numServices := 5
	for i := 0; i < numServices; i++ {
		svc := &Service{
			Config: config.Service{
				Name: "test-service-" + string(rune('a'+i)),
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
		if err != nil {
			t.Fatalf("failed to create listener for service %d: %v", i, err)
		}
		defer func() {
			if err := listener.Close(); err != nil {
				t.Logf("failed to close listener: %v", err)
			}
		}()

		go func() { _ = svc.server.Serve(listener) }()
		registry.services = append(registry.services, svc)
	}

	// Wait for all services to start
	time.Sleep(100 * time.Millisecond)

	// Measure concurrent shutdown
	shutdownStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Global.ShutdownTimeout.Duration)
	defer cancel()

	err := registry.Shutdown(ctx)
	shutdownDuration := time.Since(shutdownStart)

	if err != nil {
		t.Errorf("unexpected shutdown error: %v", err)
	}

	// Verify shutdown was concurrent (should be much less than numServices * 100ms)
	if shutdownDuration > 500*time.Millisecond {
		t.Errorf("shutdown took too long for concurrent operation: %v", shutdownDuration)
	}
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
		services: make([]*Service, 0),
	}

	// Add a service with a handler that never completes
	svc := &Service{
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
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Logf("failed to close listener: %v", err)
		}
	}()

	go func() { _ = svc.server.Serve(listener) }()
	registry.services = append(registry.services, svc)

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
	if err == nil {
		t.Error("expected shutdown error due to timeout, got nil")
	}

	if err != nil && !contains(err.Error(), "hanging-service") {
		t.Errorf("error should mention the service name, got: %v", err)
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
		services: make([]*Service, 0),
		mu:       sync.Mutex{},
	}

	// Add a simple service
	svc := &Service{
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
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Logf("failed to close listener: %v", err)
		}
	}()

	go func() { _ = svc.server.Serve(listener) }()
	registry.services = append(registry.services, svc)

	// First shutdown
	ctx1, cancel1 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel1()
	err1 := registry.Shutdown(ctx1)
	if err1 != nil {
		t.Errorf("first shutdown failed: %v", err1)
	}

	// Second shutdown (should be safe)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()
	err2 := registry.Shutdown(ctx2)
	if err2 != nil {
		t.Errorf("second shutdown failed: %v", err2)
	}
}
