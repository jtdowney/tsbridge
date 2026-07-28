// Package app provides the main application lifecycle management for tsbridge.
package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	tserrors "github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/service"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	"github.com/jtdowney/tsbridge/internal/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// webServer interface for web server operations
type webServer interface {
	Start() error
	Stop(ctx context.Context) error
}

// App encapsulates the tsbridge application lifecycle
type App struct {
	cfg           *config.Config
	provider      config.Provider
	tsServer      *tailscale.Server
	registry      *service.Registry
	metricsServer *metrics.Server
	webServer     webServer
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
	providerName := ""
	if opts.Provider != nil {
		providerName = opts.Provider.Name()
	}
	if err := cfg.Validate(providerName); err != nil {
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

	// Setup web server if configured
	if cfg.Global.WebAddr != "" {
		if err := app.setupWebServer(); err != nil {
			// Clean up if web server setup fails
			if opts.TSServer == nil {
				tsServer.Close()
			}
			return nil, tserrors.WrapResource(err, "failed to setup web server")
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
	var metricsTimeout time.Duration
	if a.cfg.Global.MetricsReadHeaderTimeout != nil {
		metricsTimeout = *a.cfg.Global.MetricsReadHeaderTimeout
	}
	a.metricsServer = metrics.NewServer(a.cfg.Global.MetricsAddr, reg, metricsTimeout)

	return nil
}

// setupWebServer initializes the web server.
func (a *App) setupWebServer() error {
	slog.Debug("creating web server")
	webSrv, err := web.NewServer(a.cfg.Global.WebAddr, a)
	if err != nil {
		return tserrors.WrapResource(err, "failed to create web server")
	}
	a.webServer = webSrv
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

		// Start web server if configured
		if a.webServer != nil {
			slog.Debug("starting web server", "address", a.cfg.Global.WebAddr)
			go func() {
				if err := a.webServer.Start(); err != nil {
					slog.Error("web server error", "error", err)
				}
			}()
			slog.Info("web server listening", "address", a.cfg.Global.WebAddr)
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
					"total", startupErr.Total,
					"failures", startupErr.Failures)
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
		slog.Error("failed to shutdown services", "error", err)
		errs = append(errs, err)
	}

	// Shutdown metrics server if running
	if a.metricsServer != nil {
		if err := a.metricsServer.Shutdown(ctx); err != nil {
			wrappedErr := tserrors.WrapInternal(err, "failed to shutdown metrics server")
			slog.Error("failed to shutdown metrics server", "error", err)
			errs = append(errs, wrappedErr)
		}
	}

	// Shutdown web server if running
	if a.webServer != nil {
		if err := a.webServer.Stop(ctx); err != nil {
			wrappedErr := tserrors.WrapInternal(err, "failed to shutdown web server")
			slog.Error("failed to shutdown web server", "error", err)
			errs = append(errs, wrappedErr)
		}
	}

	// Close tailscale server
	if err := a.tsServer.Close(); err != nil {
		// Check if it's a timeout error - log but don't fail shutdown
		if tserrors.IsTimeout(err) {
			slog.Warn("tailscale server close timed out but continuing shutdown", "error", err)
		} else {
			wrappedErr := tserrors.WrapResource(err, "failed to close tailscale server")
			slog.Error("failed to close tailscale server", "error", err)
			errs = append(errs, wrappedErr)
		}
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
			if err := a.ReloadConfig(newCfg); err != nil {
				slog.Error("failed to reload configuration", "error", err)
			}
		}
	}
}

// ReloadConfig reloads the configuration and restarts affected services
// This method is exported for testing purposes
func (a *App) ReloadConfig(newCfg *config.Config) error {
	start := time.Now()

	a.mu.Lock()
	defer a.mu.Unlock()

	oldCfg := a.cfg

	// Use the extracted reload logic
	err := reloadConfigWithRegistry(oldCfg, newCfg, a.registry)

	// Record reload metrics if collector is available
	if a.registry != nil {
		if collector := a.registry.GetMetricsCollector(); collector != nil {
			success := err == nil
			collector.RecordConfigReload(success, time.Since(start))
		}
	}

	// Always update the config, even if some operations failed
	// This ensures we're working with the latest intended configuration
	a.cfg = newCfg

	return err
}

// findServicesToRemove returns names of services in old config not present in new config.
func findServicesToRemove(old, new *config.Config) []string {
	newServices := make(map[string]bool)
	for _, svc := range new.Services {
		newServices[svc.Name] = true
	}

	var toRemove []string
	for _, svc := range old.Services {
		if !newServices[svc.Name] {
			toRemove = append(toRemove, svc.Name)
		}
	}
	return toRemove
}

// findServicesToAdd returns services in new config but not in old.
func findServicesToAdd(old, new *config.Config) []config.Service {
	oldServices := make(map[string]bool)
	for _, svc := range old.Services {
		oldServices[svc.Name] = true
	}

	var toAdd []config.Service
	for _, svc := range new.Services {
		if !oldServices[svc.Name] {
			toAdd = append(toAdd, svc)
		}
	}
	return toAdd
}

// findServicesToUpdate returns services present in both configs with changed configuration.
func findServicesToUpdate(old, new *config.Config) []config.Service {
	oldServices := make(map[string]config.Service)
	for _, svc := range old.Services {
		oldServices[svc.Name] = svc
	}

	var toUpdate []config.Service
	for _, newSvc := range new.Services {
		if oldSvc, exists := oldServices[newSvc.Name]; exists {
			// Compare configurations
			if !config.ServiceConfigEqual(oldSvc, newSvc) {
				toUpdate = append(toUpdate, newSvc)
			}
		}
	}
	return toUpdate
}

// MetricsAddr returns the actual address the metrics server is listening on.
// Returns empty string if metrics server is not running.
func (a *App) MetricsAddr() string {
	if a.metricsServer == nil {
		return ""
	}
	return a.metricsServer.Addr()
}

// GetConfig returns the current configuration.
func (a *App) GetConfig() *config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

// GetRegistry returns the service registry.
func (a *App) GetRegistry() *service.Registry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.registry
}
