// Package service provides service registry and management capabilities.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	tserrors "github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/middleware"
	"github.com/jtdowney/tsbridge/internal/proxy"
	"github.com/jtdowney/tsbridge/internal/tailscale"
)

// Registry manages all services
type Registry struct {
	config           *config.Config
	tsServer         *tailscale.Server
	services         map[string]*Service
	metricsCollector *metrics.Collector
	mu               sync.RWMutex
}

// Service represents a single service instance
type Service struct {
	Name             string
	Config           config.Service
	globalConfig     *config.Config
	listener         net.Listener
	server           *http.Server
	tsServer         *tailscale.Server // Reference to Tailscale server for WhoIs
	metricsCollector *metrics.Collector
	handler          http.Handler // Pre-created handler to catch config errors early
}

// handlerWithClose wraps an http.Handler and preserves the Close method from the underlying proxy.Handler
type handlerWithClose struct {
	http.Handler
	closeHandler proxy.Handler
}

// Close delegates to the underlying proxy handler's Close method
func (h *handlerWithClose) Close() error {
	if h.closeHandler != nil {
		return h.closeHandler.Close()
	}
	return nil
}

// NewRegistry creates a new service registry
func NewRegistry(cfg *config.Config, tsServer *tailscale.Server) *Registry {
	return &Registry{
		config:   cfg,
		tsServer: tsServer,
		services: make(map[string]*Service, len(cfg.Services)),
	}
}

// SetMetricsCollector sets the metrics collector for the registry
func (r *Registry) SetMetricsCollector(collector *metrics.Collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metricsCollector = collector
}

// GetMetricsCollector returns the metrics collector for the registry
func (r *Registry) GetMetricsCollector() *metrics.Collector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metricsCollector
}

// GetService returns a service by name
func (r *Registry) GetService(name string) (*Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, exists := r.services[name]
	return svc, exists
}

// StartServices starts all configured services
func (r *Registry) StartServices() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	startTime := time.Now()
	totalServices := len(r.config.Services)
	failedServices := make(map[string]error)
	successfulCount := 0

	slog.Debug("starting all configured services",
		"total_services", totalServices,
	)

	for i, svcCfg := range r.config.Services {
		serviceStart := time.Now()
		slog.Debug("starting service",
			"service", svcCfg.Name,
			"index", i+1,
			"of", totalServices,
		)

		svc, err := r.startService(svcCfg)
		if err != nil {
			slog.Debug("service start failed",
				"service", svcCfg.Name,
				"duration", time.Since(serviceStart),
				"error", err,
			)
			failedServices[svcCfg.Name] = err
			continue // Skip failed services as per spec
		}
		r.services[svcCfg.Name] = svc
		slog.Info("started service", "service", svcCfg.Name)
		slog.Debug("service started successfully",
			"service", svcCfg.Name,
			"duration", time.Since(serviceStart),
		)
		successfulCount++
	}

	// Update active services count if metrics collector is available
	if r.metricsCollector != nil {
		r.metricsCollector.SetActiveServices(len(r.services))
	}

	// Create ServiceStartupError if any services failed
	failedCount := len(failedServices)
	slog.Debug("service startup complete",
		"total_services", totalServices,
		"successful", successfulCount,
		"failed", failedCount,
		"total_duration", time.Since(startTime),
	)

	if failedCount > 0 {
		return tserrors.NewServiceStartupError(totalServices, successfulCount, failedCount, failedServices)
	}

	return nil
}

// startService starts a single service
func (r *Registry) startService(svcCfg config.Service) (*Service, error) {
	phaseStart := time.Now()
	slog.Debug("creating listener for service",
		"service", svcCfg.Name,
		"tls_mode", svcCfg.TLSMode,
		"funnel_enabled", svcCfg.FunnelEnabled != nil && *svcCfg.FunnelEnabled,
	)

	// Create listener for this service
	listener, err := r.tsServer.Listen(svcCfg, svcCfg.TLSMode, svcCfg.FunnelEnabled != nil && *svcCfg.FunnelEnabled)
	if err != nil {
		return nil, tserrors.WrapResource(err, "creating listener")
	}
	slog.Debug("listener created for service",
		"service", svcCfg.Name,
		"duration", time.Since(phaseStart),
		"address", listener.Addr(),
	)

	// Create service instance
	svc := &Service{
		Name:             svcCfg.Name,
		Config:           svcCfg,
		globalConfig:     r.config,
		listener:         listener,
		tsServer:         r.tsServer,
		metricsCollector: r.metricsCollector,
	}

	// Create handler early to catch configuration errors
	handlerStart := time.Now()
	slog.Debug("creating handler for service",
		"service", svcCfg.Name,
		"backend_addr", svcCfg.BackendAddr,
	)
	handler, err := svc.CreateHandler()
	if err != nil {
		_ = listener.Close()
		if r.tsServer != nil {
			if closeErr := r.tsServer.CloseService(svcCfg.Name); closeErr != nil {
				slog.Debug("failed to close tsnet server after handler failure",
					"service", svcCfg.Name,
					"error", closeErr,
				)
			}
		}
		slog.Debug("handler creation failed",
			"service", svcCfg.Name,
			"duration", time.Since(handlerStart),
			"error", err,
		)
		return nil, err
	}
	svc.handler = handler
	slog.Debug("handler created for service",
		"service", svcCfg.Name,
		"duration", time.Since(handlerStart),
	)

	// Create HTTP server with timeouts
	svc.server = &http.Server{
		Handler:           svc.handler,
		ReadHeaderTimeout: constants.DefaultReadHeaderTimeout, // Set default to satisfy linter
	}

	// Override with configured values if provided
	if svcCfg.ReadHeaderTimeout != nil {
		svc.server.ReadHeaderTimeout = *svcCfg.ReadHeaderTimeout
	}
	if svcCfg.WriteTimeout != nil {
		svc.server.WriteTimeout = *svcCfg.WriteTimeout
	}
	if svcCfg.IdleTimeout != nil {
		svc.server.IdleTimeout = *svcCfg.IdleTimeout
	}

	slog.Debug("HTTP server configured",
		"service", svcCfg.Name,
		"read_header_timeout", svc.server.ReadHeaderTimeout,
		"write_timeout", svc.server.WriteTimeout,
		"idle_timeout", svc.server.IdleTimeout,
	)

	// Start serving in background
	go func() {
		slog.Debug("service listening", "service", svcCfg.Name, "address", listener.Addr())
		if err := svc.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("service serve error", "service", svcCfg.Name, "error", err)
		}
	}()

	return svc, nil
}

// Handler returns the HTTP handler for this service
func (s *Service) Handler() http.Handler {
	return s.handler
}

// SetHandler sets the handler for the service (used for testing)
func (s *Service) SetHandler(h http.Handler) {
	s.handler = h
}

// CreateHandler creates the HTTP handler for the service, returning an error if configuration is invalid
func (s *Service) CreateHandler() (http.Handler, error) {
	// Create transport config from global settings
	transportConfig := &proxy.TransportConfig{}
	if s.Config.ResponseHeaderTimeout != nil {
		transportConfig.ResponseHeaderTimeout = *s.Config.ResponseHeaderTimeout
	}

	// Get trusted proxies from global config
	var trustedProxies []string
	if s.globalConfig != nil {
		trustedProxies = s.globalConfig.Global.TrustedProxies
		// Set transport timeouts from global config
		if s.globalConfig.Global.DialTimeout != nil {
			transportConfig.DialTimeout = *s.globalConfig.Global.DialTimeout
		}
		if s.globalConfig.Global.KeepAliveTimeout != nil {
			transportConfig.KeepAliveTimeout = *s.globalConfig.Global.KeepAliveTimeout
		}
		if s.globalConfig.Global.IdleConnTimeout != nil {
			transportConfig.IdleConnTimeout = *s.globalConfig.Global.IdleConnTimeout
		}
		if s.globalConfig.Global.TLSHandshakeTimeout != nil {
			transportConfig.TLSHandshakeTimeout = *s.globalConfig.Global.TLSHandshakeTimeout
		}
		if s.globalConfig.Global.ExpectContinueTimeout != nil {
			transportConfig.ExpectContinueTimeout = *s.globalConfig.Global.ExpectContinueTimeout
		}
	}

	// Create proxy handler with unified configuration
	handler, err := proxy.NewHandler(&proxy.HandlerConfig{
		BackendAddr:        s.Config.BackendAddr,
		TransportConfig:    transportConfig,
		TrustedProxies:     trustedProxies,
		MetricsCollector:   s.metricsCollector,
		ServiceName:        s.Config.Name,
		UpstreamHeaders:    s.Config.UpstreamHeaders,
		DownstreamHeaders:  s.Config.DownstreamHeaders,
		RemoveUpstream:     s.Config.RemoveUpstream,
		RemoveDownstream:   s.Config.RemoveDownstream,
		FlushInterval:      s.Config.FlushInterval,
		InsecureSkipVerify: s.Config.InsecureSkipVerify != nil && *s.Config.InsecureSkipVerify,
	})
	if err != nil {
		return nil, err
	}

	// Log security warning if TLS verification is disabled
	if s.Config.InsecureSkipVerify != nil && *s.Config.InsecureSkipVerify {
		slog.Warn("TLS certificate verification disabled for service connection to backend",
			"service", s.Config.Name,
			"backend", s.Config.BackendAddr)
	}

	// Wrap with middleware - convert to http.Handler for middleware chaining
	var httpHandler http.Handler = handler

	// Wrap with request ID middleware - this should be early in the chain
	httpHandler = middleware.RequestID(httpHandler)

	// Apply request body size limit
	maxBodySize := s.getMaxRequestBodySize()
	if maxBodySize > 0 {
		httpHandler = middleware.MaxBytesHandler(maxBodySize)(httpHandler)
	}

	// Wrap with whois middleware if enabled
	whoisEnabled := s.Config.WhoisEnabled != nil && *s.Config.WhoisEnabled
	if whoisEnabled && s.tsServer != nil {
		// Get the tsnet server instance for this service
		serviceServer := s.tsServer.GetServiceServer(s.Config.Name)
		if serviceServer != nil {
			var whoisTimeout time.Duration
			if s.Config.WhoisTimeout != nil {
				whoisTimeout = *s.Config.WhoisTimeout
			} else {
				whoisTimeout = constants.DefaultWhoisTimeout
			}
			// Create a whois client adapter for the tsnet server
			whoisClient := tailscale.NewWhoisClientAdapter(serviceServer)
			// Use the whois middleware with internalized cache
			httpHandler = middleware.Whois(whoisClient, whoisEnabled, whoisTimeout, constants.DefaultWhoisCacheSize, constants.DefaultWhoisCacheTTL)(httpHandler)
		}
	}

	// Wrap with metrics middleware if collector is available
	if s.metricsCollector != nil {
		httpHandler = s.metricsCollector.Middleware(s.Config.Name, httpHandler)
	}

	// Wrap with access logging middleware if enabled
	if s.isAccessLogEnabled() {
		httpHandler = middleware.AccessLog(slog.Default(), s.Config.Name)(httpHandler)
	}

	// Create a wrapper that preserves the Close method from the original handler
	return &handlerWithClose{
		Handler:      httpHandler,
		closeHandler: handler,
	}, nil
}

// isAccessLogEnabled returns whether access logging is enabled for this service
func (s *Service) isAccessLogEnabled() bool {
	// First check service-specific setting
	if s.Config.AccessLog != nil {
		return *s.Config.AccessLog
	}
	// Then check global setting
	if s.globalConfig != nil && s.globalConfig.Global.AccessLog != nil {
		return *s.globalConfig.Global.AccessLog
	}
	// Default to true
	return true
}

// getMaxRequestBodySize returns the max request body size for this service
// It returns the service-specific override if set, otherwise the global value
// A negative value means no limit should be applied
func (s *Service) getMaxRequestBodySize() int64 {
	if s.Config.MaxRequestBodySize != nil {
		// If explicitly set, use the value as-is. 0 is a valid explicit limit.
		return *s.Config.MaxRequestBodySize
	}
	if s.globalConfig != nil && s.globalConfig.Global.MaxRequestBodySize != nil {
		// If explicitly set globally, use the value as-is.
		return *s.globalConfig.Global.MaxRequestBodySize
	}
	// Default if no config available or not explicitly set anywhere
	return constants.DefaultMaxRequestBodySize
}

// Stop gracefully stops the service
func (s *Service) Stop(ctx context.Context) error {
	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil {
			return err
		}
	}

	// Close the listener (may already be closed by server.Shutdown)
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			// Only log if it's not an "already closed" error
			if !errors.Is(err, net.ErrClosed) {
				slog.Warn("failed to close listener", "service", s.Config.Name, "error", err)
			}
		}
	}

	// Close the handler if it implements Close
	if s.handler != nil {
		if closer, ok := s.handler.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				slog.Warn("failed to close handler", "service", s.Config.Name, "error", err)
			}
		}
	}

	return nil
}

// Shutdown gracefully shuts down all services
func (r *Registry) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var wg sync.WaitGroup
	errCh := make(chan error, len(r.services))

	for _, svc := range r.services {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			if err := s.Stop(ctx); err != nil {
				errCh <- tserrors.WrapInternal(err, fmt.Sprintf("shutting down service %q", s.Config.Name))
			}
		}(svc)
	}

	wg.Wait()
	close(errCh)

	// Collect any errors
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// AddService dynamically starts and registers a new service.
// Returns an error if the service already exists or fails to start.
// Thread-safe.
func (r *Registry) AddService(svcCfg config.Service) error {
	start := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if service already exists
	if _, exists := r.services[svcCfg.Name]; exists {
		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("add", false, time.Since(start))
		}
		return fmt.Errorf("service %s already exists", svcCfg.Name)
	}

	// Start the service
	svc, err := r.startService(svcCfg)
	if err != nil {
		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("add", false, time.Since(start))
		}
		return fmt.Errorf("failed to start service %s: %w", svcCfg.Name, err)
	}

	// Add to registry
	r.services[svcCfg.Name] = svc

	// Record metrics
	if r.metricsCollector != nil {
		r.metricsCollector.RecordServiceOperation("add", true, time.Since(start))
		r.metricsCollector.SetActiveServices(len(r.services))
	}

	slog.Info("added service", "service", svcCfg.Name)
	return nil
}

// RemoveService stops and removes a service from the registry.
// Returns an error if the service is not found or fails to stop.
func (r *Registry) RemoveService(name string) error {
	start := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	svc, exists := r.services[name]
	if !exists {
		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("remove", false, time.Since(start))
		}
		return fmt.Errorf("service %s not found", name)
	}

	// Stop the service (this will close listener and handler)
	ctx, cancel := context.WithTimeout(context.Background(), constants.ServiceStopTimeout)
	defer cancel()

	if err := svc.Stop(ctx); err != nil {
		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("remove", false, time.Since(start))
		}
		return fmt.Errorf("failed to stop service %s: %w", name, err)
	}

	// Close the tsnet server for this service
	if r.tsServer != nil {
		if err := r.tsServer.CloseService(name); err != nil {
			slog.Error("failed to close tsnet server for service", "service", name, "error", err)
			// Continue with removal even if tsnet close fails
		}
	}

	// Remove from registry
	delete(r.services, name)

	// Record metrics
	if r.metricsCollector != nil {
		r.metricsCollector.RecordServiceOperation("remove", true, time.Since(start))
		r.metricsCollector.SetActiveServices(len(r.services))
	}

	slog.Info("removed service", "service", name)
	return nil
}

// validateServiceConfig checks service config for common errors before updating.
//
// Validates:
//   - Non-empty service name and backend address
//   - Absolute unix socket paths (unix://)
//   - TLS mode is one of "off" or "auto"
//   - Non-negative timeouts
//
// Returns error if invalid.
func (r *Registry) validateServiceConfig(cfg config.Service) error {
	// Validate service name
	if cfg.Name == "" {
		return fmt.Errorf("service name is required")
	}

	// Validate backend address
	if cfg.BackendAddr == "" {
		return fmt.Errorf("backend address is required")
	}

	// Validate backend address format
	path, unix := strings.CutPrefix(cfg.BackendAddr, "unix://")
	if unix {
		// Unix socket path validation
		if !filepath.IsAbs(path) {
			return fmt.Errorf("unix socket path must be absolute: %s", path)
		}
	} else {
		// TCP address validation
		// Add scheme if missing, consistent with proxy.parseBackendURL
		addr := cfg.BackendAddr
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		if _, err := url.Parse(addr); err != nil {
			return fmt.Errorf("invalid backend address: %w", err)
		}
	}

	// Validate TLS mode
	switch cfg.TLSMode {
	case constants.TLSModeOff, constants.TLSModeAuto:
		// Valid modes
	default:
		return fmt.Errorf("invalid TLS mode: %s (must be '%s' or '%s')", cfg.TLSMode, constants.TLSModeOff, constants.TLSModeAuto)
	}

	// Validate timeout values
	if cfg.ReadHeaderTimeout != nil && *cfg.ReadHeaderTimeout < 0 {
		return fmt.Errorf("read header timeout must be non-negative")
	}
	if cfg.WriteTimeout != nil && *cfg.WriteTimeout < 0 {
		return fmt.Errorf("write timeout must be non-negative")
	}
	if cfg.IdleTimeout != nil && *cfg.IdleTimeout < 0 {
		return fmt.Errorf("idle timeout must be non-negative")
	}
	if cfg.ResponseHeaderTimeout != nil && *cfg.ResponseHeaderTimeout < 0 {
		return fmt.Errorf("response header timeout must be non-negative")
	}

	return nil
}

// UpdateService updates an existing service with new configuration at runtime.
// Minimizes downtime by validating config before stopping the old service.
// Thread-safe. Returns error if service not found, config invalid, stop/start fails.
func (r *Registry) UpdateService(name string, newCfg config.Service) error {
	start := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if service exists
	oldSvc, exists := r.services[name]
	if !exists {
		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("update", false, time.Since(start))
		}
		return fmt.Errorf("service %s not found", name)
	}

	// Validate the new configuration as much as possible before stopping the old service
	// This helps minimize downtime by catching configuration errors early
	if err := r.validateServiceConfig(newCfg); err != nil {
		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("update", false, time.Since(start))
		}
		return fmt.Errorf("invalid service configuration: %w", err)
	}

	// Store old service config for logging/debugging
	oldConfig := oldSvc.Config

	// Stop the old service
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := oldSvc.Stop(ctx); err != nil {
		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("update", false, time.Since(start))
		}
		return fmt.Errorf("failed to stop service %s: %w", name, err)
	}

	// Close the tsnet server for this service
	if r.tsServer != nil {
		if err := r.tsServer.CloseService(name); err != nil {
			slog.Error("failed to close tsnet server for service", "service", name, "error", err)
			// Continue with update even if tsnet close fails
		}
	}

	// Start the new service configuration
	newSvc, err := r.startService(newCfg)
	if err != nil {
		// Attempt rollback: try to restart the old service configuration
		slog.Warn("failed to start updated service, attempting rollback",
			"service", name,
			"error", err,
		)

		rollbackSvc, rollbackErr := r.startService(oldConfig)
		if rollbackErr != nil {
			// Both new service and rollback failed - remove from registry
			slog.Error("rollback failed after update failure",
				"service", name,
				"update_error", err,
				"rollback_error", rollbackErr,
			)
			delete(r.services, name)

			if r.metricsCollector != nil {
				r.metricsCollector.RecordServiceOperation("update", false, time.Since(start))
				r.metricsCollector.SetActiveServices(len(r.services))
			}

			return fmt.Errorf("update failed and rollback failed for %s: update error: %w, rollback error: %v",
				name, err, rollbackErr)
		}

		// Rollback succeeded - service is back to old config
		slog.Info("rollback succeeded after update failure",
			"service", name,
			"backend", oldConfig.BackendAddr,
		)
		r.services[name] = rollbackSvc

		if r.metricsCollector != nil {
			r.metricsCollector.RecordServiceOperation("update", false, time.Since(start))
		}

		return fmt.Errorf("failed to start updated service %s (rolled back to previous config): %w", name, err)
	}

	// Replace in registry
	r.services[name] = newSvc

	// Record success metric
	if r.metricsCollector != nil {
		r.metricsCollector.RecordServiceOperation("update", true, time.Since(start))
		// Active services count doesn't change on update
	}

	slog.Info("updated service",
		"service", name,
		"old_backend", oldConfig.BackendAddr,
		"new_backend", newCfg.BackendAddr,
	)
	return nil
}
