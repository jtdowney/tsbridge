// Package app provides the main application lifecycle management for tsbridge.
package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/jtdowney/tsbridge/internal/config"
	tserrors "github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/service"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// App encapsulates the tsbridge application lifecycle
type App struct {
	cfg           *config.Config
	provider      config.Provider
	tsServer      *tailscale.Server
	registry      *service.Registry
	metricsServer *metrics.Server
	startOnce     sync.Once
	stopOnce      sync.Once
	configWatcher context.CancelFunc
	mu            sync.RWMutex
}

// Options allows customizing App creation for testing
type Options struct {
	TSServer *tailscale.Server
	Registry *service.Registry
	Provider config.Provider
}

// NewApp creates a new App instance with the given configuration
func NewApp(cfg *config.Config) (*App, error) {
	return NewAppWithOptions(cfg, Options{})
}

// NewAppWithOptions creates a new App instance with custom options
func NewAppWithOptions(cfg *config.Config, opts Options) (*App, error) {
	// If a provider is given, use it to load config
	if opts.Provider != nil {
		ctx := context.Background()
		loadedCfg, err := opts.Provider.Load(ctx)
		if err != nil {
			return nil, tserrors.WrapConfig(err, "failed to load config from provider")
		}
		cfg = loadedCfg
	}

	if cfg == nil {
		return nil, tserrors.NewValidationError("config cannot be nil")
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		// The error from Validate is already a validation error
		return nil, err
	}

	var tsServer *tailscale.Server
	var registry *service.Registry
	var err error

	// Use provided tsServer or create new one
	if opts.TSServer != nil {
		tsServer = opts.TSServer
	} else {
		// Create tailscale server
		slog.Debug("creating tailscale server")
		tsServer, err = tailscale.NewServer(cfg.Tailscale)
		if err != nil {
			return nil, tserrors.WrapResource(err, "failed to create tailscale server")
		}
	}

	// Use provided registry or create new one
	if opts.Registry != nil {
		registry = opts.Registry
	} else {
		// Create service registry
		registry = service.NewRegistry(cfg, tsServer)
	}

	app := &App{
		cfg:      cfg,
		provider: opts.Provider,
		tsServer: tsServer,
		registry: registry,
	}

	// Setup metrics if configured
	if cfg.Global.MetricsAddr != "" {
		if err := app.setupMetrics(); err != nil {
			// Clean up tsServer if metrics setup fails and we created it
			if opts.TSServer == nil {
				tsServer.Close()
			}
			return nil, tserrors.WrapResource(err, "failed to setup metrics")
		}
	}

	return app, nil
}

// setupMetrics initializes the metrics server and collector
func (a *App) setupMetrics() error {
	slog.Debug("creating metrics collector")
	collector := metrics.NewCollector()
	reg := prometheus.NewRegistry()

	// Register default collectors (Go runtime and process metrics)
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	if err := collector.Register(reg); err != nil {
		return tserrors.WrapResource(err, "failed to register metrics")
	}

	// Set metrics collector on registry
	a.registry.SetMetricsCollector(collector)

	// Create metrics server (but don't start it yet)
	a.metricsServer = metrics.NewServer(a.cfg.Global.MetricsAddr, reg, a.cfg.Global.MetricsReadHeaderTimeout.Duration)

	return nil
}

// Start starts the application and all its services
func (a *App) Start(ctx context.Context) error {
	var startErr error
	a.startOnce.Do(func() {
		// Start watching for configuration changes if provider supports it
		if a.provider != nil {
			watchCtx, cancel := context.WithCancel(ctx)
			a.configWatcher = cancel

			configCh, err := a.provider.Watch(watchCtx)
			if err != nil {
				slog.Warn("failed to start config watcher", "error", err)
			} else if configCh != nil {
				go a.watchConfigChanges(watchCtx, configCh)
			}
		}

		// Start metrics server if configured
		if a.metricsServer != nil {
			slog.Debug("starting metrics server", "address", a.cfg.Global.MetricsAddr)
			if err := a.metricsServer.Start(ctx); err != nil {
				startErr = tserrors.WrapResource(err, "failed to start metrics server")
				return
			}
			slog.Info("metrics server listening", "address", a.metricsServer.Addr())
		}

		// Start services
		slog.Info("starting services")
		if err := a.registry.StartServices(); err != nil {
			// Check if this is a partial failure
			if startupErr, ok := tserrors.AsServiceStartupError(err); ok && !startupErr.AllFailed() {
				// Some services started successfully, log the failures but continue
				slog.Warn("some services failed to start",
					"successful", startupErr.Successful,
					"failed", startupErr.Failed,
					"total", startupErr.Total)
				for service, serviceErr := range startupErr.Failures {
					slog.Error("service startup failed",
						"service", service,
						"error", serviceErr)
				}
				// Continue running with partial services
			} else {
				// All services failed or other error type
				startErr = err
				// If all services fail, shut down metrics server
				if a.metricsServer != nil {
					if shutdownErr := a.metricsServer.Shutdown(context.Background()); shutdownErr != nil {
						slog.Error("failed to shutdown metrics server", "error", shutdownErr)
					}
				}
				return
			}
		}
	})

	return startErr
}

// Shutdown gracefully shuts down the application
func (a *App) Shutdown(ctx context.Context) error {
	var shutdownErr error
	a.stopOnce.Do(func() {
		shutdownErr = a.performShutdown(ctx)
	})
	return shutdownErr
}

// performShutdown performs the actual shutdown sequence
func (a *App) performShutdown(ctx context.Context) error {
	var errs []error

	// Stop config watcher if running
	if a.configWatcher != nil {
		a.configWatcher()
	}

	// Shutdown services
	if err := a.registry.Shutdown(ctx); err != nil {
		// The error from Shutdown is already typed
		errs = append(errs, err)
	}

	// Shutdown metrics server if running
	if a.metricsServer != nil {
		if err := a.metricsServer.Shutdown(ctx); err != nil {
			errs = append(errs, tserrors.WrapInternal(err, "failed to shutdown metrics server"))
		}
	}

	// Close tailscale server
	if err := a.tsServer.Close(); err != nil {
		errs = append(errs, tserrors.WrapResource(err, "failed to close tailscale server"))
	}

	if len(errs) == 0 {
		slog.Info("shutdown complete")
		return nil
	}

	// Combine all errors using errors.Join
	return errors.Join(errs...)
}

// watchConfigChanges monitors for configuration changes from the provider
func (a *App) watchConfigChanges(ctx context.Context, configCh <-chan *config.Config) {
	for {
		select {
		case <-ctx.Done():
			return
		case newCfg, ok := <-configCh:
			if !ok {
				return
			}

			slog.Info("configuration changed, reloading services")
			if err := a.reloadConfig(newCfg); err != nil {
				slog.Error("failed to reload configuration", "error", err)
			}
		}
	}
}

// reloadConfig reloads the configuration and restarts affected services
func (a *App) reloadConfig(newCfg *config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// For now, we just log the change. Full reload implementation would:
	// 1. Compare old and new configs to find changes
	// 2. Stop removed services
	// 3. Start new services
	// 4. Restart modified services
	slog.Info("configuration reload requested", "services", len(newCfg.Services))

	// Update the config
	a.cfg = newCfg

	return nil
}

// MetricsAddr returns the actual address the metrics server is listening on.
// Returns empty string if metrics server is not running.
func (a *App) MetricsAddr() string {
	if a.metricsServer == nil {
		return ""
	}
	return a.metricsServer.Addr()
}
