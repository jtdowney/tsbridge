// Package service provides service registry and management capabilities.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	tserrors "github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/metrics"
	"github.com/jtdowney/tsbridge/internal/middleware"
	"github.com/jtdowney/tsbridge/internal/proxy"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	"log/slog"
)

// Registry manages all services
type Registry struct {
	config           *config.Config
	tsServer         *tailscale.Server
	services         map[string]*Service
	metricsCollector *metrics.Collector
	whoisCache       *middleware.WhoisCache
	mu               sync.Mutex
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
	whoisCache       *middleware.WhoisCache
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
		config:     cfg,
		tsServer:   tsServer,
		services:   make(map[string]*Service, len(cfg.Services)),
		whoisCache: middleware.NewWhoisCache(1000, 5*time.Minute),
	}
}

// SetMetricsCollector sets the metrics collector for the registry
func (r *Registry) SetMetricsCollector(collector *metrics.Collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metricsCollector = collector
}

// GetService returns a service by name
func (r *Registry) GetService(name string) (*Service, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	svc, exists := r.services[name]
	return svc, exists
}

// StartServices starts all configured services
func (r *Registry) StartServices() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	totalServices := len(r.config.Services)
	failedServices := make(map[string]error)
	successfulCount := 0

	for _, svcCfg := range r.config.Services {
		svc, err := r.startService(svcCfg)
		if err != nil {
			slog.Error("failed to start service", "service", svcCfg.Name, "error", err)
			failedServices[svcCfg.Name] = err
			continue // Skip failed services as per spec
		}
		r.services[svcCfg.Name] = svc
		slog.Info("started service", "service", svcCfg.Name)
		successfulCount++
	}

	// If no services were configured, return a simple error
	if totalServices == 0 {
		return tserrors.NewInternalError("no services configured")
	}

	// Create ServiceStartupError if any services failed
	failedCount := len(failedServices)
	if failedCount > 0 {
		return tserrors.NewServiceStartupError(totalServices, successfulCount, failedCount, failedServices)
	}

	return nil
}

// startService starts a single service
func (r *Registry) startService(svcCfg config.Service) (*Service, error) {

	// Create listener for this service
	listener, err := r.tsServer.Listen(svcCfg, svcCfg.TLSMode, svcCfg.FunnelEnabled != nil && *svcCfg.FunnelEnabled)
	if err != nil {
		return nil, tserrors.WrapResource(err, "creating listener")
	}

	// Create service instance
	svc := &Service{
		Name:             svcCfg.Name,
		Config:           svcCfg,
		globalConfig:     r.config,
		listener:         listener,
		tsServer:         r.tsServer,
		metricsCollector: r.metricsCollector,
		whoisCache:       r.whoisCache,
	}

	// Create handler early to catch configuration errors
	handler, err := svc.CreateHandler()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	svc.handler = handler

	// Create HTTP server with timeouts
	svc.server = &http.Server{
		Handler:           svc.handler,
		ReadHeaderTimeout: svcCfg.ReadHeaderTimeout.Duration,
		WriteTimeout:      svcCfg.WriteTimeout.Duration,
		IdleTimeout:       svcCfg.IdleTimeout.Duration,
	}

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
	transportConfig := &proxy.TransportConfig{
		ResponseHeaderTimeout: s.Config.ResponseHeaderTimeout.Duration,
	}

	// Get trusted proxies from global config
	var trustedProxies []string
	if s.globalConfig != nil {
		trustedProxies = s.globalConfig.Global.TrustedProxies
		// Set transport timeouts from global config
		transportConfig.DialTimeout = s.globalConfig.Global.DialTimeout.Duration
		transportConfig.KeepAliveTimeout = s.globalConfig.Global.KeepAliveTimeout.Duration
		transportConfig.IdleConnTimeout = s.globalConfig.Global.IdleConnTimeout.Duration
		transportConfig.TLSHandshakeTimeout = s.globalConfig.Global.TLSHandshakeTimeout.Duration
		transportConfig.ExpectContinueTimeout = s.globalConfig.Global.ExpectContinueTimeout.Duration
	}

	// Create proxy handler with unified configuration
	handler, err := proxy.NewHandler(&proxy.HandlerConfig{
		BackendAddr:       s.Config.BackendAddr,
		TransportConfig:   transportConfig,
		TrustedProxies:    trustedProxies,
		MetricsCollector:  s.metricsCollector,
		ServiceName:       s.Config.Name,
		UpstreamHeaders:   s.Config.UpstreamHeaders,
		DownstreamHeaders: s.Config.DownstreamHeaders,
		RemoveUpstream:    s.Config.RemoveUpstream,
		RemoveDownstream:  s.Config.RemoveDownstream,
		FlushInterval:     &s.Config.FlushInterval.Duration,
	})
	if err != nil {
		return nil, err
	}

	// Wrap with middleware - convert to http.Handler for middleware chaining
	var httpHandler http.Handler = handler

	// Wrap with request ID middleware - this should be early in the chain
	httpHandler = middleware.RequestID(httpHandler)

	// Wrap with whois middleware if enabled
	whoisEnabled := s.Config.WhoisEnabled != nil && *s.Config.WhoisEnabled
	if whoisEnabled && s.tsServer != nil {
		// Get the tsnet server instance for this service
		serviceServer := s.tsServer.GetServiceServer(s.Config.Name)
		if serviceServer != nil {
			whoisTimeout := s.Config.WhoisTimeout.Duration
			if whoisTimeout == 0 {
				whoisTimeout = constants.DefaultWhoisTimeout
			}
			// Create a whois client adapter for the tsnet server
			whoisClient := tailscale.NewWhoisClientAdapter(serviceServer)
			// Use the whois middleware with cache
			httpHandler = middleware.Whois(whoisClient, whoisEnabled, whoisTimeout, s.whoisCache)(httpHandler)
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

// Stop gracefully stops the service
func (s *Service) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
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
			if err := s.server.Shutdown(ctx); err != nil {
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

// AddService starts a new service and adds it to the registry
func (r *Registry) AddService(svcCfg config.Service) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if service already exists
	if _, exists := r.services[svcCfg.Name]; exists {
		return fmt.Errorf("service %s already exists", svcCfg.Name)
	}

	// Start the service
	svc, err := r.startService(svcCfg)
	if err != nil {
		return fmt.Errorf("failed to start service %s: %w", svcCfg.Name, err)
	}

	// Add to registry
	r.services[svcCfg.Name] = svc
	slog.Info("added service", "service", svcCfg.Name)
	return nil
}

// RemoveService stops and removes a service from the registry
func (r *Registry) RemoveService(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	svc, exists := r.services[name]
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	// Stop the service
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := svc.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop service %s: %w", name, err)
	}

	// Close the listener
	if svc.listener != nil {
		if err := svc.listener.Close(); err != nil {
			slog.Warn("failed to close listener", "service", name, "error", err)
		}
	}

	// Close the handler if it implements Close
	if svc.handler != nil {
		if closer, ok := svc.handler.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				slog.Warn("failed to close handler", "service", name, "error", err)
			}
		}
	}

	// Remove from registry
	delete(r.services, name)
	slog.Info("removed service", "service", name)
	return nil
}

// UpdateService updates an existing service with new configuration
func (r *Registry) UpdateService(name string, newCfg config.Service) error {
	// For now, implement as remove + add
	// Later can optimize to avoid downtime if needed

	// First check if the service exists
	r.mu.Lock()
	_, exists := r.services[name]
	r.mu.Unlock()

	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	// Remove the old service
	if err := r.RemoveService(name); err != nil {
		return fmt.Errorf("failed to remove old service: %w", err)
	}

	// Add the new service
	if err := r.AddService(newCfg); err != nil {
		return fmt.Errorf("failed to add new service: %w", err)
	}

	slog.Info("updated service", "service", name)
	return nil
}
