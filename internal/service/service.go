// Package service provides service registry and management capabilities.
package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

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
	services         []*Service
	metricsCollector *metrics.Collector
	mu               sync.Mutex
}

// Service represents a single service instance
type Service struct {
	Config           config.Service
	globalConfig     *config.Config
	listener         net.Listener
	server           *http.Server
	tsServer         *tailscale.Server // Reference to Tailscale server for WhoIs
	metricsCollector *metrics.Collector
	handler          http.Handler // Pre-created handler to catch config errors early
}

// NewRegistry creates a new service registry
func NewRegistry(cfg *config.Config, tsServer *tailscale.Server) *Registry {
	return &Registry{
		config:   cfg,
		tsServer: tsServer,
		services: make([]*Service, 0, len(cfg.Services)),
	}
}

// SetMetricsCollector sets the metrics collector for the registry
func (r *Registry) SetMetricsCollector(collector *metrics.Collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metricsCollector = collector
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
		r.services = append(r.services, svc)
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
	listener, err := r.tsServer.ListenWithService(svcCfg, svcCfg.TLSMode, svcCfg.FunnelEnabled != nil && *svcCfg.FunnelEnabled)
	if err != nil {
		return nil, tserrors.WrapResource(err, "creating listener")
	}

	// Create service instance
	svc := &Service{
		Config:           svcCfg,
		globalConfig:     r.config,
		listener:         listener,
		tsServer:         r.tsServer,
		metricsCollector: r.metricsCollector,
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
		Handler:      svc.handler,
		ReadTimeout:  svcCfg.ReadTimeout.Duration,
		WriteTimeout: svcCfg.WriteTimeout.Duration,
		IdleTimeout:  svcCfg.IdleTimeout.Duration,
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

	handler, err := proxy.NewHandlerWithHeaders(
		s.Config.BackendAddr,
		transportConfig,
		trustedProxies,
		s.Config.UpstreamHeaders,
		s.Config.DownstreamHeaders,
		s.Config.RemoveUpstream,
		s.Config.RemoveDownstream,
	)
	if err != nil {
		return nil, err
	}

	// Wrap with request ID middleware - this should be early in the chain
	handler = middleware.RequestID(handler)

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
			handler = middleware.Whois(whoisClient, whoisEnabled, whoisTimeout)(handler)
		}
	}

	// Wrap with metrics middleware if collector is available
	if s.metricsCollector != nil {
		handler = s.metricsCollector.Middleware(s.Config.Name, handler)
	}

	// Wrap with access logging middleware if enabled
	if s.isAccessLogEnabled() {
		handler = middleware.AccessLog(slog.Default(), s.Config.Name)(handler)
	}

	return handler, nil
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
