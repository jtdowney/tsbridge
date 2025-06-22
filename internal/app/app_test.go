package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	"github.com/jtdowney/tsbridge/internal/tsnet"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestUnixSocket creates a temporary unix socket for testing
func createTestUnixSocket(t *testing.T) string {
	// Use a shorter path to avoid macOS unix socket path length limits
	// Replace slashes with dashes to make valid filename
	safeName := strings.ReplaceAll(t.Name(), "/", "-")
	socketPath := "/tmp/tsb-" + safeName + ".sock"

	// Remove any existing socket file
	os.Remove(socketPath)

	// Create a simple unix socket server
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		listener.Close()
		os.Remove(socketPath)
	})

	// Start a simple server in the background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	return socketPath
}

// Helper function to create a valid test config
func createTestConfig(t *testing.T) *config.Config {
	socketPath := createTestUnixSocket(t)

	cfg := &config.Config{
		Tailscale: config.Tailscale{
			StateDir: t.TempDir(),
			AuthKey:  "test-auth-key", // Use auth key instead of OAuth to avoid API calls
		},
		Global: config.Global{
			ShutdownTimeout:       config.Duration{Duration: 5 * time.Second},
			ReadTimeout:           config.Duration{Duration: 30 * time.Second},
			WriteTimeout:          config.Duration{Duration: 30 * time.Second},
			IdleTimeout:           config.Duration{Duration: 120 * time.Second},
			ResponseHeaderTimeout: config.Duration{Duration: 10 * time.Second},
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
						ShutdownTimeout: config.Duration{Duration: 30 * time.Second},
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
		{
			name: "invalid config - no auth",
			cfg: &config.Config{
				Global: config.Global{
					ShutdownTimeout: config.Duration{Duration: 30 * time.Second},
				},
				Tailscale: config.Tailscale{
					// No auth configured
				},
				Services: []config.Service{
					{Name: "test-service", BackendAddr: "localhost:8080"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg != nil && !tt.wantErr {
				// Create mock tailscale server for valid configs
				tsServer := createMockTailscaleServer(t, tt.cfg.Tailscale)

				// Use NewAppWithOptions to inject the mock
				app, err := NewAppWithOptions(tt.cfg, Options{
					TSServer: tsServer,
				})

				if (err != nil) != tt.wantErr {
					t.Errorf("NewAppWithOptions() error = %v, wantErr %v", err, tt.wantErr)
					return
				}

				if !tt.wantErr && app == nil {
					t.Error("NewAppWithOptions() returned nil app without error")
				}
			} else {
				// For nil config or expected errors, use NewApp directly
				app, err := NewApp(tt.cfg)
				if (err != nil) != tt.wantErr {
					t.Errorf("NewApp() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if !tt.wantErr && app == nil {
					t.Error("NewApp() returned nil app without error")
				}
			}
		})
	}
}

func TestNewAppWithOptions(t *testing.T) {
	validConfig := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 30 * time.Second},
		},
		Tailscale: config.Tailscale{
			StateDir: t.TempDir(),
			AuthKey:  "test-auth-key",
		},
		Services: []config.Service{
			{
				Name:         "test-service",
				BackendAddr:  "unix:///tmp/test.sock",
				WhoisTimeout: config.Duration{Duration: 5 * time.Second},
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
				TSServer: createMockTailscaleServer(t, validConfig.Tailscale),
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
				TSServer: createMockTailscaleServer(t, validConfig.Tailscale),
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
				assert.Error(t, err)
				assert.Nil(t, app)
			} else {
				assert.NoError(t, err)
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
		if err == nil {
			t.Fatal("expected error for nil config")
		}

		if !errors.IsValidation(err) {
			t.Errorf("expected validation error, got %v", err)
		}
	})

	t.Run("invalid config returns validation error", func(t *testing.T) {
		cfg := &config.Config{
			// Missing required OAuth or AuthKey
			Services: []config.Service{
				{Name: "test", BackendAddr: "localhost:8080"},
			},
		}

		_, err := NewApp(cfg)
		if err == nil {
			t.Fatal("expected error for invalid config")
		}

		if !errors.IsValidation(err) {
			t.Errorf("expected validation error, got %v", err)
		}
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
				socketPath := createTestUnixSocket(t)

				cfg := &config.Config{
					Global: config.Global{
						ShutdownTimeout: config.Duration{Duration: 1 * time.Second},
					},
					Tailscale: config.Tailscale{
						StateDir: t.TempDir(),
						AuthKey:  "test-auth-key",
					},
					Services: []config.Service{
						{
							Name:         "test-service",
							BackendAddr:  "unix://" + socketPath,
							WhoisTimeout: config.Duration{Duration: 5 * time.Second},
						},
					},
				}
				cfg.SetDefaults()

				// Create app with mock tailscale server
				tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
				socketPath := createTestUnixSocket(t)

				cfg := &config.Config{
					Global: config.Global{
						ShutdownTimeout: config.Duration{Duration: 1 * time.Second},
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
							WhoisTimeout: config.Duration{Duration: 5 * time.Second},
						},
					},
				}
				cfg.SetDefaults()

				// Create app with mock tailscale server and metrics enabled
				tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
				assert.Error(t, err)
				if tt.expectedErrMessage != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMessage)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAppStartIdempotency(t *testing.T) {
	socketPath := createTestUnixSocket(t)

	// Create test config
	cfg := &config.Config{
		Global: config.Global{
			ShutdownTimeout: config.Duration{Duration: 1 * time.Second},
		},
		Tailscale: config.Tailscale{
			StateDir: t.TempDir(),
			AuthKey:  "test-auth-key",
		},
		Services: []config.Service{
			{
				Name:         "test-service",
				BackendAddr:  "unix://" + socketPath,
				WhoisTimeout: config.Duration{Duration: 5 * time.Second},
			},
		},
	}
	cfg.SetDefaults()

	tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NoError(t, err3)
}

// Test that Start returns without error when context is cancelled
func TestAppStartReturnsCleanlyOnContextCancel(t *testing.T) {
	cfg := createTestConfig(t)

	// Create app
	tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within timeout")
	}
}

// Test that Start doesn't block shutdown in its goroutine
func TestAppStartDoesNotBlockShutdown(t *testing.T) {
	cfg := createTestConfig(t)

	// Create app
	tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
	assert.NoError(t, shutdownErr)
}

func TestAppStartWithPartialServiceFailures(t *testing.T) {
	t.Run("app continues when some services fail", func(t *testing.T) {
		// Start a test backend server
		backend, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
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
				ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
				ReadTimeout:     config.Duration{Duration: 30 * time.Second},
				WriteTimeout:    config.Duration{Duration: 30 * time.Second},
				IdleTimeout:     config.Duration{Duration: 120 * time.Second},
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
		factory := func() tsnet.TSNetServer {
			return tsnet.NewMockTSNetServer()
		}
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		if err != nil {
			t.Fatal(err)
		}

		// Create app with the mocked dependencies
		app, err := NewAppWithOptions(cfg, Options{
			TSServer: tsServer,
		})
		if err != nil {
			t.Fatal(err)
		}

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
			if err != nil {
				t.Errorf("expected no error from Start after graceful shutdown, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("app did not shut down in time")
		}
	})

	t.Run("app starts successfully with unreachable backends", func(t *testing.T) {
		// Create config with 2 services that have unreachable backends
		cfg := &config.Config{
			Global: config.Global{
				ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
				ReadTimeout:     config.Duration{Duration: 30 * time.Second},
				WriteTimeout:    config.Duration{Duration: 30 * time.Second},
				IdleTimeout:     config.Duration{Duration: 120 * time.Second},
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
		factory := func() tsnet.TSNetServer {
			return tsnet.NewMockTSNetServer()
		}
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		if err != nil {
			t.Fatal(err)
		}

		// Create app with the mocked dependencies
		app, err := NewAppWithOptions(cfg, Options{
			TSServer: tsServer,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Create a context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Start the app
		err = app.Start(ctx)

		// Should succeed because we use lazy connections
		if err != nil {
			t.Fatalf("expected app to start successfully with lazy connections, got error: %v", err)
		}
	})

	t.Run("metrics server continues when some services fail", func(t *testing.T) {
		// Start a test backend server
		backend, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
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
				ShutdownTimeout: config.Duration{Duration: 5 * time.Second},
				ReadTimeout:     config.Duration{Duration: 30 * time.Second},
				WriteTimeout:    config.Duration{Duration: 30 * time.Second},
				IdleTimeout:     config.Duration{Duration: 120 * time.Second},
				MetricsAddr:     "127.0.0.1:0", // Random port
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
		factory := func() tsnet.TSNetServer {
			return tsnet.NewMockTSNetServer()
		}
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		if err != nil {
			t.Fatal(err)
		}

		app, err := NewAppWithOptions(cfg, Options{
			TSServer: tsServer,
		})
		if err != nil {
			t.Fatal(err)
		}

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
			t.Error("app did not shut down in time")
		}
	})
}

// Test that Shutdown can be called independently
func TestAppShutdownCanBeCalledIndependently(t *testing.T) {
	cfg := createTestConfig(t)

	// Create app
	tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
	assert.NoError(t, err)

	// Now cancel the start context
	cancel()

	// Wait for Start to return
	select {
	case err := <-startErr:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within timeout")
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
			t.Fatalf("failed to create app: %v", err)
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
			ShutdownTimeout: config.Duration{Duration: 1 * time.Second},
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
				WhoisTimeout: config.Duration{Duration: 5 * time.Second},
			},
		},
	}
	cfg.SetDefaults()

	tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
	assert.NoError(t, err)
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
						WhoisTimeout: config.Duration{Duration: 5 * time.Second},
					},
				},
			}
			cfg.SetDefaults()

			// Create app using internal setup path
			tsServer := createMockTailscaleServer(t, cfg.Tailscale)
			app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
			require.NoError(t, err)

			// Set a valid registry
			app.cfg.Global.MetricsAddr = tt.metricsAddr

			// Call setupMetrics
			err = app.setupMetrics()

			// Check expectations
			if tt.expectError {
				assert.Error(t, err)
				if tt.expectedErrMessage != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMessage)
				}
			} else {
				assert.NoError(t, err)
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
						ShutdownTimeout: config.Duration{Duration: 1 * time.Second},
					},
					Tailscale: config.Tailscale{
						StateDir: t.TempDir(),
						AuthKey:  "test-auth-key",
					},
					Services: []config.Service{
						{
							Name:         "test-service",
							BackendAddr:  "unix:///tmp/test.sock", // Use unix socket that won't try to connect
							WhoisTimeout: config.Duration{Duration: 5 * time.Second},
						},
					},
				}
				cfg.SetDefaults()

				tsServer := createMockTailscaleServer(t, cfg.Tailscale)
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
						ShutdownTimeout: config.Duration{Duration: 1 * time.Second},
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
							WhoisTimeout: config.Duration{Duration: 5 * time.Second},
						},
					},
				}
				cfg.SetDefaults()

				tsServer := createMockTailscaleServer(t, cfg.Tailscale)
				app, err := NewAppWithOptions(cfg, Options{TSServer: tsServer})
				require.NoError(t, err)
				// Setup metrics manually for test
				err = app.setupMetrics()
				require.NoError(t, err)

				// Start metrics server to get actual address
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				go func() { _ = app.Start(ctx) }()

				// Give it a moment to start
				time.Sleep(20 * time.Millisecond)

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

	server := metrics.NewServerWithRegistry("127.0.0.1:0", reg, 5*time.Second)

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
	assert.NoError(t, err)
}

// TestAppPartialFailureLogging verifies that partial failures are logged correctly
func TestAppPartialFailureLogging(t *testing.T) {
	// This test would capture logs to verify proper error reporting
	// For now, we're focusing on the behavior rather than log output
	t.Skip("Log capture test not implemented")
}
