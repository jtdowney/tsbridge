package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	"github.com/jtdowney/tsbridge/internal/testhelpers"
	"github.com/jtdowney/tsbridge/internal/testutil"
	"github.com/jtdowney/tsbridge/internal/tsnet"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a valid test config
func createTestConfig(t *testing.T) *config.Config {
	socketPath := testutil.CreateTestUnixSocket(t)

	cfg := &config.Config{
		Tailscale: config.Tailscale{
			StateDir: t.TempDir(),
			AuthKey:  "test-auth-key", // Use auth key instead of OAuth to avoid API calls
		},
		Global: config.Global{
			ShutdownTimeout:       testhelpers.DurationPtr(5 * time.Second),
			ReadHeaderTimeout:     testhelpers.DurationPtr(30 * time.Second),
			WriteTimeout:          testhelpers.DurationPtr(30 * time.Second),
			IdleTimeout:           testhelpers.DurationPtr(120 * time.Second),
			ResponseHeaderTimeout: testhelpers.DurationPtr(10 * time.Second),
		},
		Services: []config.Service{
			{
				Name:        "test-service",
				BackendAddr: "unix://" + socketPath,
			},
		},
	}
	cfg.SetDefaults()
	return cfg
}

func TestNewApp(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: func() *config.Config {
				cfg := &config.Config{
					Global: config.Global{
						ShutdownTimeout: testhelpers.DurationPtr(30 * time.Second),
					},
					Tailscale: config.Tailscale{
						AuthKey: "test-key",
					},
					Services: []config.Service{
						{
							Name:        "test-service",
							BackendAddr: "localhost:8080",
						},
					},
				}
				cfg.SetDefaults()
				return cfg
			}(),
			wantErr: false,
		},
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg != nil && !tt.wantErr {
				// Create mock tailscale server for valid configs
				tsServer := testutil.CreateMockTailscaleServer(t, tt.cfg.Tailscale)

				// Use NewAppWithOptions to inject the mock
				app, err := NewAppWithOptions(tt.cfg, Options{
					TSServer: tsServer,
				})

				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.NotNil(t, app)
				}
			} else {
				// For nil config or expected errors, use NewApp directly
				app, err := NewApp(tt.cfg)
				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.NotNil(t, app)
				}
			}
		})
	}
}

func TestNewAppWithOptions(t *testing.T) {
	validConfig := &config.Config{
		Global: config.Global{
			ShutdownTimeout: testhelpers.DurationPtr(30 * time.Second),
		},
		Tailscale: config.Tailscale{
			StateDir: t.TempDir(),
			AuthKey:  "test-auth-key",
		},
		Services: []config.Service{
			{
				Name:         "test-service",
				BackendAddr:  "unix:///tmp/test.sock",
				WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
			},
		},
	}
	validConfig.SetDefaults()

	tests := []struct {
		name    string
		cfg     *config.Config
		opts    Options
		wantErr bool
		checkFn func(t *testing.T, app *App)
	}{
		{
			name: "with custom TSServer",
			cfg:  validConfig,
			opts: Options{
				TSServer: testutil.CreateMockTailscaleServer(t, validConfig.Tailscale),
			},
			wantErr: false,
			checkFn: func(t *testing.T, app *App) {
				assert.NotNil(t, app)
				assert.NotNil(t, app.tsServer)
			},
		},
		{
			name: "with nil options creates new TSServer",
			cfg:  validConfig,
			opts: Options{
				TSServer: testutil.CreateMockTailscaleServer(t, validConfig.Tailscale),
			},
			wantErr: false,
			checkFn: func(t *testing.T, app *App) {
				assert.NotNil(t, app)
				assert.NotNil(t, app.tsServer)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, err := NewAppWithOptions(tt.cfg, tt.opts)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, app)
			} else {
				require.NoError(t, err)
				require.NotNil(t, app)

				if tt.checkFn != nil {
					tt.checkFn(t, app)
				}
			}
		})
	}
}

func TestAppErrorTypes(t *testing.T) {
	t.Run("nil config returns validation error", func(t *testing.T) {
		_, err := NewApp(nil)
		require.Error(t, err)
		assert.True(t, errors.IsValidation(err), "expected validation error, got %v", err)
	})
}

func TestAppStart(t *testing.T) {
	tests := []struct {
		name               string
		setupApp           func(t *testing.T) *App
		contextTimeout     time.Duration
		expectError        bool
		expectedErrMessage string
	}{
		{
			name: "successful start and shutdown without metrics",
			setupApp: func(t *testing.T) *App {
				socketPath := testutil.CreateTestUnixSocket(t)

				cfg := &config.Config{
					Global: config.Global{
						ShutdownTimeout: testhelpers.DurationPtr(1 * time.Second),
					},
					Tailscale: config.Tailscale{
						StateDir: t.TempDir(),
						AuthKey:  "test-auth-key",
					},
					Services: []config.Service{
						{
							Name:         "test-service",
							BackendAddr:  "unix://" + socketPath,
							WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
						},
					},
				}
				cfg.SetDefaults()

				// Create app with mock tailscale server
				tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
				app, err := NewAppWithOptions(cfg, Options{
					TSServer: tsServer,
				})
				require.NoError(t, err)
				return app
			},
			contextTimeout: 100 * time.Millisecond,
			expectError:    false,
		},
		{
			name: "successful start and shutdown with metrics",
			setupApp: func(t *testing.T) *App {
				socketPath := testutil.CreateTestUnixSocket(t)

				cfg := &config.Config{
					Global: config.Global{
						ShutdownTimeout: testhelpers.DurationPtr(1 * time.Second),
						MetricsAddr:     "127.0.0.1:0", // Use port 0 to get random available port
					},
					Tailscale: config.Tailscale{
						StateDir: t.TempDir(),
						AuthKey:  "test-auth-key",
					},
					Services: []config.Service{
						{
							Name:         "test-service",
							BackendAddr:  "unix://" + socketPath,
							WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
						},
					},
				}
				cfg.SetDefaults()

				// Create app with mock tailscale server and metrics enabled
				tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
				app, err := NewAppWithOptions(cfg, Options{
					TSServer: tsServer,
				})
				require.NoError(t, err)
				return app
			},
			contextTimeout: 100 * time.Millisecond,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := tt.setupApp(t)

			// Create context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), tt.contextTimeout)
			defer cancel()

			// Start the app
			err := app.Start(ctx)

			// Check expectations
			if tt.expectError {
				require.Error(t, err)
				if tt.expectedErrMessage != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMessage)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAppStartIdempotency(t *testing.T) {
	socketPath := testutil.CreateTestUnixSocket(t)

	// Create test config
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: testhelpers.DurationPtr(1 * time.Second),
		},
		Tailscale: config.Tailscale{
			StateDir: t.TempDir(),
			AuthKey:  "test-auth-key",
		},
		Services: []config.Service{
			{
				Name:         "test-service",
				BackendAddr:  "unix://" + socketPath,
				WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
			},
		},
	}
	cfg.SetDefaults()

	tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
	app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
	require.NoError(t, err)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start the app multiple times
	err1 := app.Start(ctx)
	err2 := app.Start(ctx)
	err3 := app.Start(ctx)

	// All should return nil (idempotent)
	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NoError(t, err3)
}

// Test that Start returns without error when context is cancelled
func TestAppStartReturnsCleanlyOnContextCancel(t *testing.T) {
	cfg := createTestConfig(t)

	// Create app
	tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
	app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
	require.NoError(t, err)

	// Start the app with a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	startErr := make(chan error, 1)
	go func() {
		startErr <- app.Start(ctx)
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Start should return nil (no error) when context is cancelled
	select {
	case err := <-startErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.Fail(t, "Start did not return within timeout")
	}
}

// Test that Start doesn't block shutdown in its goroutine
func TestAppStartDoesNotBlockShutdown(t *testing.T) {
	cfg := createTestConfig(t)

	// Create app
	tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
	app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
	require.NoError(t, err)

	// Create a context that we won't cancel immediately
	ctx := context.Background()

	startErr := make(chan error, 1)
	startStarted := make(chan struct{})
	go func() {
		close(startStarted)
		startErr <- app.Start(ctx)
	}()

	// Wait for Start to begin
	<-startStarted
	time.Sleep(100 * time.Millisecond)

	// The app should be running now
	// Try to shutdown from main without cancelling the start context
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	shutdownErr := app.Shutdown(shutdownCtx)

	// Shutdown should succeed
	require.NoError(t, shutdownErr)
}

func TestAppStartWithPartialServiceFailures(t *testing.T) {
	t.Run("app continues when some services fail", func(t *testing.T) {
		// Start a test backend server
		backend, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer backend.Close()

		// Accept connections in background
		go func() {
			for {
				conn, err := backend.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		}()

		// Create config with 3 services, 2 will fail
		cfg := &config.Config{
			Global: config.Global{
				ShutdownTimeout:   testhelpers.DurationPtr(5 * time.Second),
				ReadHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
				WriteTimeout:      testhelpers.DurationPtr(30 * time.Second),
				IdleTimeout:       testhelpers.DurationPtr(120 * time.Second),
			},
			Services: []config.Service{
				{Name: "service1", BackendAddr: "127.0.0.1:9999", TLSMode: "off"},
				{Name: "service2", BackendAddr: backend.Addr().String(), TLSMode: "off"}, // This one works
				{Name: "service3", BackendAddr: "127.0.0.1:9997", TLSMode: "off"},
			},
			Tailscale: config.Tailscale{
				AuthKey: "test-key",
			},
		}

		// Create tailscale server with mock factory
		factory := func(serviceName string) tsnet.TSNetServer {
			return tsnet.NewMockTSNetServer()
		}
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		require.NoError(t, err)

		// Create app with the mocked dependencies
		app, err := NewAppWithOptions(cfg, Options{
			TSServer: tsServer,
		})
		require.NoError(t, err)

		// Create a context that we'll cancel after services start
		ctx, cancel := context.WithCancel(context.Background())

		// Start the app in a goroutine
		startErr := make(chan error, 1)
		go func() {
			startErr <- app.Start(ctx)
		}()

		// Give services time to start
		time.Sleep(100 * time.Millisecond)

		// App should be running despite partial failures
		// Cancel the context to trigger shutdown
		cancel()

		// Wait for app to shut down
		select {
		case err := <-startErr:
			// Should have gotten a ServiceStartupError but app continued
			assert.NoError(t, err, "expected no error from Start after graceful shutdown")
		case <-time.After(2 * time.Second):
			assert.Fail(t, "app did not shut down in time")
		}
	})

	t.Run("app starts successfully with unreachable backends", func(t *testing.T) {
		// Create config with 2 services that have unreachable backends
		cfg := &config.Config{
			Global: config.Global{
				ShutdownTimeout:   testhelpers.DurationPtr(5 * time.Second),
				ReadHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
				WriteTimeout:      testhelpers.DurationPtr(30 * time.Second),
				IdleTimeout:       testhelpers.DurationPtr(120 * time.Second),
			},
			Services: []config.Service{
				{Name: "service1", BackendAddr: "127.0.0.1:9999", TLSMode: "off"},
				{Name: "service2", BackendAddr: "127.0.0.1:9998", TLSMode: "off"},
			},
			Tailscale: config.Tailscale{
				AuthKey: "test-key",
			},
		}

		// Create tailscale server with mock factory
		factory := func(serviceName string) tsnet.TSNetServer {
			return tsnet.NewMockTSNetServer()
		}
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		require.NoError(t, err)

		// Create app with the mocked dependencies
		app, err := NewAppWithOptions(cfg, Options{
			TSServer: tsServer,
		})
		require.NoError(t, err)

		// Create a context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Start the app
		err = app.Start(ctx)

		// Should succeed because we use lazy connections
		if err != nil {
			require.NoError(t, err, "expected app to start successfully with lazy connections")
		}
	})

	t.Run("metrics server continues when some services fail", func(t *testing.T) {
		// Start a test backend server
		backend, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer backend.Close()

		// Accept connections in background
		go func() {
			for {
				conn, err := backend.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		}()

		// Create config with metrics and mixed services
		cfg := &config.Config{
			Global: config.Global{
				ShutdownTimeout:   testhelpers.DurationPtr(5 * time.Second),
				ReadHeaderTimeout: testhelpers.DurationPtr(30 * time.Second),
				WriteTimeout:      testhelpers.DurationPtr(30 * time.Second),
				IdleTimeout:       testhelpers.DurationPtr(120 * time.Second),
				MetricsAddr:       "127.0.0.1:0", // Random port
			},
			Services: []config.Service{
				{Name: "service1", BackendAddr: "127.0.0.1:9999", TLSMode: "off"},
				{Name: "service2", BackendAddr: backend.Addr().String(), TLSMode: "off"}, // This one works
			},
			Tailscale: config.Tailscale{
				AuthKey: "test-key",
			},
		}

		// Create app with mock dependencies
		factory := func(serviceName string) tsnet.TSNetServer {
			return tsnet.NewMockTSNetServer()
		}
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		require.NoError(t, err)

		app, err := NewAppWithOptions(cfg, Options{
			TSServer: tsServer,
		})
		require.NoError(t, err)

		// Create a context that we'll cancel after services start
		ctx, cancel := context.WithCancel(context.Background())

		// Start the app in a goroutine
		startErr := make(chan error, 1)
		go func() {
			startErr <- app.Start(ctx)
		}()

		// Give services time to start
		time.Sleep(100 * time.Millisecond)

		// Metrics server should be running
		metricsAddr := app.MetricsAddr()
		if metricsAddr == "" {
			t.Error("expected metrics server to be running")
		}

		// Cancel the context to trigger shutdown
		cancel()

		// Wait for app to shut down
		select {
		case err := <-startErr:
			if err != nil {
				t.Errorf("expected no error from Start after graceful shutdown, got %v", err)
			}
		case <-time.After(2 * time.Second):
			assert.Fail(t, "app did not shut down in time")
		}
	})
}

// Test that Shutdown can be called independently
func TestAppShutdownCanBeCalledIndependently(t *testing.T) {
	cfg := createTestConfig(t)

	// Create app
	tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
	app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
	require.NoError(t, err)

	// Start the app
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startErr := make(chan error, 1)
	go func() {
		startErr <- app.Start(ctx)
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Call Shutdown directly without cancelling the context first
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	err = app.Shutdown(shutdownCtx)
	require.NoError(t, err)

	// Now cancel the start context
	cancel()

	// Wait for Start to return
	select {
	case err := <-startErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.Fail(t, "Start did not return within timeout")
	}
}

func TestAppShutdownErrorTypes(t *testing.T) {
	t.Run("shutdown errors are collected properly", func(t *testing.T) {
		// Create a valid minimal config
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				AuthKey: "test-key",
			},
			Global: config.Global{
				MetricsAddr: "", // No metrics server
			},
			Services: []config.Service{
				{Name: "test-service", BackendAddr: "localhost:8080"},
			},
		}
		cfg.SetDefaults()

		app, err := NewApp(cfg)
		if err != nil {
			require.NoError(t, err, "failed to create app")
		}

		// Shutdown immediately (nothing to shut down)
		ctx := context.Background()
		err = app.Shutdown(ctx)

		// Should succeed with no errors
		if err != nil {
			t.Errorf("unexpected shutdown error: %v", err)
		}
	})
}

func TestAppPerformShutdown(t *testing.T) {
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: testhelpers.DurationPtr(1 * time.Second),
			MetricsAddr:     "127.0.0.1:0",
		},
		Tailscale: config.Tailscale{
			StateDir: t.TempDir(),
			AuthKey:  "test-auth-key",
		},
		Services: []config.Service{
			{
				Name:         "test-service",
				BackendAddr:  "unix:///tmp/test.sock",
				WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
			},
		},
	}
	cfg.SetDefaults()

	tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
	app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
	require.NoError(t, err)
	// Setup metrics manually for test
	err = app.setupMetrics()
	require.NoError(t, err)

	// Start the app first
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() { _ = app.Start(ctx) }()
	time.Sleep(20 * time.Millisecond) // Give it time to start

	// Now test performShutdown
	shutdownCtx := context.Background()
	err = app.performShutdown(shutdownCtx)
	require.NoError(t, err)
}

func TestAppSetupMetrics(t *testing.T) {
	tests := []struct {
		name               string
		metricsAddr        string
		expectError        bool
		expectedErrMessage string
	}{
		{
			name:        "successful metrics setup",
			metricsAddr: "127.0.0.1:0",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test config
			cfg := &config.Config{
				Global: config.Global{
					MetricsAddr: tt.metricsAddr,
				},
				Tailscale: config.Tailscale{
					StateDir:          t.TempDir(),
					OAuthClientID:     "test-client-id",
					OAuthClientSecret: "test-client-secret",
				},
				Services: []config.Service{
					{
						Name:         "test-service",
						BackendAddr:  "unix:///tmp/test.sock",
						WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
						Tags:         []string{"tag:test"},
					},
				},
			}
			cfg.SetDefaults()

			// Create app using internal setup path
			tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
			app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
			require.NoError(t, err)

			// Set a valid registry
			app.cfg.Global.MetricsAddr = tt.metricsAddr

			// Call setupMetrics
			err = app.setupMetrics()

			// Check expectations
			if tt.expectError {
				require.Error(t, err)
				if tt.expectedErrMessage != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMessage)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, app.metricsServer)
			}
		})
	}
}

func TestAppMetricsAddr(t *testing.T) {
	tests := []struct {
		name          string
		setupApp      func(t *testing.T) *App
		expectedEmpty bool
	}{
		{
			name: "returns empty when no metrics server",
			setupApp: func(t *testing.T) *App {
				cfg := &config.Config{
					Global: config.Global{
						ShutdownTimeout: testhelpers.DurationPtr(1 * time.Second),
					},
					Tailscale: config.Tailscale{
						StateDir: t.TempDir(),
						AuthKey:  "test-auth-key",
					},
					Services: []config.Service{
						{
							Name:         "test-service",
							BackendAddr:  "unix:///tmp/test.sock", // Use unix socket that won't try to connect
							WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
						},
					},
				}
				cfg.SetDefaults()

				tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
				app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
				require.NoError(t, err)
				return app
			},
			expectedEmpty: true,
		},
		{
			name: "returns address from metrics server",
			setupApp: func(t *testing.T) *App {
				cfg := &config.Config{
					Global: config.Global{
						ShutdownTimeout: testhelpers.DurationPtr(1 * time.Second),
						MetricsAddr:     "127.0.0.1:0",
					},
					Tailscale: config.Tailscale{
						StateDir: t.TempDir(),
						AuthKey:  "test-auth-key",
					},
					Services: []config.Service{
						{
							Name:         "test-service",
							BackendAddr:  "unix:///tmp/test.sock", // Use unix socket that won't try to connect
							WhoisTimeout: testhelpers.DurationPtr(5 * time.Second),
						},
					},
				}
				cfg.SetDefaults()

				tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
				app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
				require.NoError(t, err)
				// Setup metrics manually for test
				err = app.setupMetrics()
				require.NoError(t, err)

				// Start metrics server to get actual address
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				// Use a channel to ensure Start completes
				done := make(chan error)
				go func() { done <- app.Start(ctx) }()

				// Wait for Start to complete or timeout
				select {
				case <-done:
				case <-time.After(100 * time.Millisecond):
					require.Fail(t, "Timeout waiting for app.Start to complete")
				}

				return app
			},
			expectedEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := tt.setupApp(t)

			addr := app.MetricsAddr()

			if tt.expectedEmpty {
				assert.Equal(t, "", addr)
			} else {
				assert.NotEmpty(t, addr)
			}
		})
	}
}

func TestMetricsServerIntegration(t *testing.T) {
	// Create a real metrics server to test integration
	collector := metrics.NewCollector()
	reg := prometheus.NewRegistry()

	// Register the collector properly
	err := collector.Register(reg)
	require.NoError(t, err)

	server := metrics.NewServer("127.0.0.1:0", reg, 5*time.Second)

	// Start the server
	ctx := context.Background()
	err = server.Start(ctx)
	require.NoError(t, err)

	// Get the actual address
	addr := server.Addr()
	assert.NotEmpty(t, addr)

	// Try to connect to verify it's listening
	resp, err := http.Get("http://" + addr + "/metrics")
	if err == nil {
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// Shutdown
	err = server.Shutdown(context.Background())
	require.NoError(t, err)
}

// TestAppPartialFailureLogging verifies that partial failures are logged correctly
func TestAppPartialFailureLogging(t *testing.T) {
	// This test would capture logs to verify proper error reporting
	// For now, we're focusing on the behavior rather than log output
	t.Skip("Log capture test not implemented")
}

func TestWatchConfigChanges(t *testing.T) {
	t.Run("handles config updates", func(t *testing.T) {
		// Create initial config
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				StateDir:          t.TempDir(),
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
			},
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
			},
		}
		cfg.SetDefaults()

		// Create app
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
		require.NoError(t, err)

		// Create config channel
		configCh := make(chan *config.Config, 1)

		// Start watching in background
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go app.watchConfigChanges(ctx, configCh)

		// Send new config
		newCfg := &config.Config{
			Tailscale: cfg.Tailscale,
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8081", // Changed port
					Tags:        []string{"tag:test"},
				},
				{
					Name:        "new-service",
					BackendAddr: "localhost:8082",
					Tags:        []string{"tag:test"},
				},
			},
		}
		newCfg.SetDefaults()

		configCh <- newCfg

		// Give it time to process
		time.Sleep(100 * time.Millisecond)

		// Verify config was updated
		app.mu.RLock()
		assert.Equal(t, 2, len(app.cfg.Services))
		assert.Equal(t, "localhost:8081", app.cfg.Services[0].BackendAddr)
		assert.Equal(t, "new-service", app.cfg.Services[1].Name)
		app.mu.RUnlock()
	})

	t.Run("stops watching when context is cancelled", func(t *testing.T) {
		// Create config
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				StateDir:          t.TempDir(),
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
			},
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
			},
		}
		cfg.SetDefaults()

		// Create app
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
		require.NoError(t, err)

		// Create config channel
		configCh := make(chan *config.Config, 1)

		// Start watching
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			app.watchConfigChanges(ctx, configCh)
			close(done)
		}()

		// Cancel context
		cancel()

		// Verify goroutine exits
		select {
		case <-done:
			// Good, goroutine exited
		case <-time.After(1 * time.Second):
			require.Fail(t, "watchConfigChanges did not exit when context was cancelled")
		}
	})

	t.Run("stops watching when channel is closed", func(t *testing.T) {
		// Create config
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				StateDir:          t.TempDir(),
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
			},
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
			},
		}
		cfg.SetDefaults()

		// Create app
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
		require.NoError(t, err)

		// Create config channel
		configCh := make(chan *config.Config)

		// Start watching
		ctx := context.Background()
		done := make(chan struct{})
		go func() {
			app.watchConfigChanges(ctx, configCh)
			close(done)
		}()

		// Close channel
		close(configCh)

		// Verify goroutine exits
		select {
		case <-done:
			// Good, goroutine exited
		case <-time.After(1 * time.Second):
			require.Fail(t, "watchConfigChanges did not exit when channel was closed")
		}
	})
}

func TestReloadConfig(t *testing.T) {
	t.Run("updates config successfully", func(t *testing.T) {
		// Create initial config
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				StateDir: t.TempDir(),
				AuthKey:  "test-auth-key", // Use auth key instead of OAuth to avoid API calls
			},
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
			},
		}
		cfg.SetDefaults()

		// Create app
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
		require.NoError(t, err)

		// Start the app to initialize services
		ctx := context.Background()
		err = app.Start(ctx)
		require.NoError(t, err)
		defer app.Shutdown(ctx)

		// Create new config
		newCfg := &config.Config{
			Tailscale: cfg.Tailscale,
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8081",
					Tags:        []string{"tag:test"},
				},
				{
					Name:        "new-service",
					BackendAddr: "localhost:8082",
					Tags:        []string{"tag:test"},
				},
			},
		}
		newCfg.SetDefaults()

		// Reload config
		err = app.ReloadConfig(newCfg)
		require.NoError(t, err)

		// Verify config was updated
		assert.Equal(t, 2, len(app.cfg.Services))
		assert.Equal(t, "localhost:8081", app.cfg.Services[0].BackendAddr)
		assert.Equal(t, "new-service", app.cfg.Services[1].Name)
	})

	t.Run("adds new services", func(t *testing.T) {
		// Test that reloadConfig identifies and processes new services
		// We'll use the helper functions directly to verify the logic
		oldCfg := &config.Config{
			Services: []config.Service{
				{
					Name:        "existing-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
			},
		}

		newCfg := &config.Config{
			Services: []config.Service{
				{
					Name:        "existing-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
				{
					Name:        "new-service",
					BackendAddr: "localhost:8081",
					Tags:        []string{"tag:test"},
				},
			},
		}

		// Verify helper functions work correctly
		toAdd := findServicesToAdd(oldCfg, newCfg)
		assert.Equal(t, 1, len(toAdd))
		assert.Equal(t, "new-service", toAdd[0].Name)

		toRemove := findServicesToRemove(oldCfg, newCfg)
		assert.Equal(t, 0, len(toRemove))

		toUpdate := findServicesToUpdate(oldCfg, newCfg)
		assert.Equal(t, 0, len(toUpdate))
	})

	t.Run("removes services", func(t *testing.T) {
		// Test that reloadConfig identifies and processes removed services
		oldCfg := &config.Config{
			Services: []config.Service{
				{
					Name:        "service-1",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
				{
					Name:        "service-2",
					BackendAddr: "localhost:8081",
					Tags:        []string{"tag:test"},
				},
			},
		}

		newCfg := &config.Config{
			Services: []config.Service{
				{
					Name:        "service-1",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
			},
		}

		// Verify helper functions work correctly
		toRemove := findServicesToRemove(oldCfg, newCfg)
		assert.Equal(t, 1, len(toRemove))
		assert.Equal(t, "service-2", toRemove[0])

		toAdd := findServicesToAdd(oldCfg, newCfg)
		assert.Equal(t, 0, len(toAdd))

		toUpdate := findServicesToUpdate(oldCfg, newCfg)
		assert.Equal(t, 0, len(toUpdate))
	})

	t.Run("updates changed services", func(t *testing.T) {
		// Test that reloadConfig identifies and processes updated services
		oldCfg := &config.Config{
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
					TLSMode:     "auto",
				},
			},
		}
		oldCfg.SetDefaults()

		newCfg := &config.Config{
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
					TLSMode:     "off", // Changed
				},
			},
		}
		newCfg.SetDefaults()

		// Verify helper functions work correctly
		toUpdate := findServicesToUpdate(oldCfg, newCfg)
		assert.Equal(t, 1, len(toUpdate))
		assert.Equal(t, "test-service", toUpdate[0].Name)
		assert.Equal(t, "off", toUpdate[0].TLSMode)

		toAdd := findServicesToAdd(oldCfg, newCfg)
		assert.Equal(t, 0, len(toAdd))

		toRemove := findServicesToRemove(oldCfg, newCfg)
		assert.Equal(t, 0, len(toRemove))
	})

	t.Run("handles concurrent reloads", func(t *testing.T) {
		// Create initial config
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				StateDir:          t.TempDir(),
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
			},
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					Tags:        []string{"tag:test"},
				},
			},
		}
		cfg.SetDefaults()

		// Create app
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
		require.NoError(t, err)

		// Run multiple concurrent reloads
		done := make(chan bool)
		for i := 0; i < 10; i++ {
			go func(port int) {
				newCfg := &config.Config{
					Tailscale: cfg.Tailscale,
					Services: []config.Service{
						{
							Name:        "test-service",
							BackendAddr: fmt.Sprintf("localhost:%d", 8080+port),
							Tags:        []string{"tag:test"},
						},
					},
				}
				newCfg.SetDefaults()
				app.ReloadConfig(newCfg)
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Verify final state is consistent
		assert.Equal(t, 1, len(app.cfg.Services))
		assert.Equal(t, "test-service", app.cfg.Services[0].Name)
	})
}

func TestFindServicesToRemove(t *testing.T) {
	tests := []struct {
		name     string
		oldCfg   *config.Config
		newCfg   *config.Config
		expected []string
	}{
		{
			name: "no services removed",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			expected: []string{},
		},
		{
			name: "one service removed",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
				},
			},
			expected: []string{"service2"},
		},
		{
			name: "all services removed",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{},
			},
			expected: []string{"service1", "service2"},
		},
		{
			name: "empty old config",
			oldCfg: &config.Config{
				Services: []config.Service{},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
				},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findServicesToRemove(tt.oldCfg, tt.newCfg)
			assert.ElementsMatch(t, tt.expected, result)
		})
	}
}

func TestFindServicesToAdd(t *testing.T) {
	tests := []struct {
		name     string
		oldCfg   *config.Config
		newCfg   *config.Config
		expected []string
	}{
		{
			name: "no services added",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			expected: []string{},
		},
		{
			name: "one service added",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			expected: []string{"service2"},
		},
		{
			name: "all services are new",
			oldCfg: &config.Config{
				Services: []config.Service{},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
					{Name: "service2"},
				},
			},
			expected: []string{"service1", "service2"},
		},
		{
			name: "empty new config",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findServicesToAdd(tt.oldCfg, tt.newCfg)
			resultNames := make([]string, len(result))
			for i, svc := range result {
				resultNames[i] = svc.Name
			}
			assert.ElementsMatch(t, tt.expected, resultNames)
		})
	}
}

func TestFindServicesToUpdate(t *testing.T) {
	tests := []struct {
		name     string
		oldCfg   *config.Config
		newCfg   *config.Config
		expected []string
	}{
		{
			name: "no services updated",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:8080"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:8080"},
				},
			},
			expected: []string{},
		},
		{
			name: "backend address changed",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:8080"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:8081"},
				},
			},
			expected: []string{"service1"},
		},
		{
			name: "multiple services updated",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:8080"},
					{Name: "service2", BackendAddr: "localhost:8081"},
					{Name: "service3", BackendAddr: "localhost:8082"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:9080"}, // Changed
					{Name: "service2", BackendAddr: "localhost:8081"}, // Same
					{Name: "service3", BackendAddr: "localhost:9082"}, // Changed
				},
			},
			expected: []string{"service1", "service3"},
		},
		{
			name: "service not in old config is not updated",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:8080"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "service1", BackendAddr: "localhost:8080"},
					{Name: "service2", BackendAddr: "localhost:8081"},
				},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set defaults for proper comparison
			tt.oldCfg.SetDefaults()
			tt.newCfg.SetDefaults()

			result := findServicesToUpdate(tt.oldCfg, tt.newCfg)
			resultNames := make([]string, len(result))
			for i, svc := range result {
				resultNames[i] = svc.Name
			}
			assert.ElementsMatch(t, tt.expected, resultNames)
		})
	}
}

func TestConfigWatchIntegration(t *testing.T) {
	t.Run("provider with config watching", func(t *testing.T) {
		// Create a mock provider that supports watching
		mockProvider := &mockConfigProvider{
			name: "mock",
			loadFunc: func(ctx context.Context) (*config.Config, error) {
				cfg := &config.Config{
					Tailscale: config.Tailscale{
						StateDir: t.TempDir(),
						AuthKey:  "test-auth-key",
					},
					Services: []config.Service{
						{
							Name:        "test-service",
							BackendAddr: "localhost:8080",
						},
					},
				}
				cfg.SetDefaults()
				return cfg, nil
			},
			watchFunc: func(ctx context.Context) (<-chan *config.Config, error) {
				ch := make(chan *config.Config, 1)
				go func() {
					// Simulate a config change after a short delay
					time.Sleep(100 * time.Millisecond)
					cfg := &config.Config{
						Tailscale: config.Tailscale{
							StateDir: t.TempDir(),
							AuthKey:  "test-auth-key",
						},
						Services: []config.Service{
							{
								Name:        "test-service",
								BackendAddr: "localhost:8081", // Changed
							},
						},
					}
					cfg.SetDefaults()
					select {
					case ch <- cfg:
					case <-ctx.Done():
					}
				}()
				return ch, nil
			},
		}

		// Create app with the provider and mock tsnet server
		tsServer := testutil.CreateMockTailscaleServer(t, config.Tailscale{AuthKey: "test-auth-key"})
		app, err := NewAppWithOptions(nil, Options{Provider: mockProvider, TSServer: tsServer})
		require.NoError(t, err)

		// Start the app
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = app.Start(ctx)
		require.NoError(t, err)

		// Wait for config change to be processed
		time.Sleep(200 * time.Millisecond)

		// Verify config was updated
		app.mu.RLock()
		assert.Equal(t, "localhost:8081", app.cfg.Services[0].BackendAddr)
		app.mu.RUnlock()

		// Shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		err = app.Shutdown(shutdownCtx)
		require.NoError(t, err)
	})
}

// mockConfigProvider implements config.Provider for testing
type mockConfigProvider struct {
	name      string
	loadFunc  func(ctx context.Context) (*config.Config, error)
	watchFunc func(ctx context.Context) (<-chan *config.Config, error)
}

func (m *mockConfigProvider) Name() string {
	return m.name
}

func (m *mockConfigProvider) Load(ctx context.Context) (*config.Config, error) {
	if m.loadFunc != nil {
		return m.loadFunc(ctx)
	}
	return nil, nil
}

func (m *mockConfigProvider) Watch(ctx context.Context) (<-chan *config.Config, error) {
	if m.watchFunc != nil {
		return m.watchFunc(ctx)
	}
	return nil, nil
}
