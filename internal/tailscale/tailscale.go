// Package tailscale manages Tailscale server instances and lifecycle.
package tailscale

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	tserrors "github.com/jtdowney/tsbridge/internal/errors"
	tsnetpkg "github.com/jtdowney/tsbridge/internal/tsnet"
)

// Server wraps a tsnet.Server with tsbridge-specific functionality
type Server struct {
	config config.Tailscale
	// serviceServers holds the tsnet.Server instance for each service
	serviceServers map[string]tsnetpkg.TSNetServer
	// serverFactory creates new TSNetServer instances
	serverFactory tsnetpkg.TSNetServerFactory
	// mu protects serviceServers map
	mu sync.Mutex
}

// NewServerWithFactory creates a new tailscale server instance with a custom TSNetServer factory
func NewServerWithFactory(cfg config.Tailscale, factory tsnetpkg.TSNetServerFactory) (*Server, error) {
	// Validate OAuth credentials if provided - both must be present or neither
	if (cfg.OAuthClientID != "" && cfg.OAuthClientSecret == "") ||
		(cfg.OAuthClientID == "" && cfg.OAuthClientSecret != "") {
		if cfg.OAuthClientID == "" {
			return nil, tserrors.NewConfigError("OAuth client secret provided without client ID")
		}
		return nil, tserrors.NewConfigError("OAuth client ID provided without client secret")
	}

	return &Server{
		config:         cfg,
		serviceServers: make(map[string]tsnetpkg.TSNetServer),
		serverFactory:  factory,
	}, nil
}

// NewServer creates a new tailscale server instance
func NewServer(cfg config.Tailscale) (*Server, error) {
	// Default factory creates real TSNet servers
	factory := func(serviceName string) tsnetpkg.TSNetServer {
		return tsnetpkg.NewRealTSNetServer(serviceName)
	}

	return NewServerWithFactory(cfg, factory)
}

// Listen creates a listener for a specific service using its full configuration
func (s *Server) Listen(svc config.Service, tlsMode string, funnelEnabled bool) (net.Listener, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	listenStart := time.Now()
	slog.Debug("starting listener creation for service",
		"service", svc.Name,
		"tls_mode", tlsMode,
		"funnel_enabled", funnelEnabled,
		"ephemeral", svc.Ephemeral,
		"tags", svc.Tags,
	)

	serviceServer := s.serverFactory(svc.Name)
	serviceServer.SetHostname(svc.Name)
	serviceServer.SetEphemeral(svc.Ephemeral)

	if s.config.ControlURL != "" {
		serviceServer.SetControlURL(s.config.ControlURL)
		slog.Debug("control URL set for service", "service", svc.Name, "control_url", s.config.ControlURL)
	}

	// Resolve base state directory and set service-specific path
	baseStateDir, stateDirSource := s.resolveBaseStateDir()
	serviceStateDir := filepath.Join(baseStateDir, svc.Name)
	serviceServer.SetDir(serviceStateDir)
	slog.Debug("state directory resolved for service",
		"service", svc.Name,
		"state_dir", serviceStateDir,
		"source", stateDirSource,
	)

	// Prepare auth key based on service type and existing state
	if err := s.prepareServiceAuth(serviceServer, svc, baseStateDir); err != nil {
		return nil, err
	}

	// Store the service server for later operations
	s.serviceServers[svc.Name] = serviceServer

	// Start the service server before listening
	if err := s.startServiceServer(serviceServer, svc.Name); err != nil {
		delete(s.serviceServers, svc.Name)
		if closeErr := serviceServer.Close(); closeErr != nil {
			slog.Debug("failed to close server after start failure", "service", svc.Name, "error", closeErr)
		}
		return nil, err
	}

	// Create the appropriate listener (funnel, TLS, or plain)
	listener, err := s.createServiceListener(serviceServer, svc, tlsMode, funnelEnabled, listenStart)
	if err != nil {
		slog.Debug("listener creation failed", "service", svc.Name, "error", err)
		delete(s.serviceServers, svc.Name)
		if closeErr := serviceServer.Close(); closeErr != nil {
			slog.Debug("failed to close server after listener failure", "service", svc.Name, "error", closeErr)
		}
		return nil, err
	}
	slog.Debug("listener created successfully", "service", svc.Name, "total_duration", time.Since(listenStart))

	return listener, nil
}

// resolveBaseStateDir determines the base state directory and its source.
func (s *Server) resolveBaseStateDir() (string, string) {
	stateDir := s.config.StateDir
	stateDirSource := "config"
	if stateDir == "" {
		stateDir = os.Getenv("STATE_DIRECTORY")
		if stateDir != "" && strings.Contains(stateDir, ":") {
			stateDir = strings.Split(stateDir, ":")[0]
		}
		if stateDir != "" {
			stateDirSource = "STATE_DIRECTORY env"
		}
	}
	if stateDir == "" {
		stateDir = os.Getenv("TSBRIDGE_STATE_DIR")
		if stateDir != "" {
			stateDirSource = "TSBRIDGE_STATE_DIR env"
		}
	}
	if stateDir == "" {
		stateDir = getDefaultStateDir()
		stateDirSource = "XDG default"
	}
	return stateDir, stateDirSource
}

// prepareServiceAuth handles auth key generation/resolution based on service type and existing state.
func (s *Server) prepareServiceAuth(serviceServer tsnetpkg.TSNetServer, svc config.Service, baseStateDir string) error {
	var needsAuthKey bool
	var authKeyReason string

	if svc.Ephemeral {
		needsAuthKey = true
		authKeyReason = "ephemeral service"
		slog.Debug("skipping state check for ephemeral service", "service", svc.Name)
	} else {
		hasState := hasExistingState(baseStateDir, svc.Name)
		needsAuthKey = !hasState
		if needsAuthKey {
			authKeyReason = "no existing state found"
		}
		slog.Debug("checking for existing state",
			"service", svc.Name,
			"has_existing_state", hasState,
			"state_dir", baseStateDir,
		)
	}

	if needsAuthKey {
		// Now we actually need auth, so validate that we have OAuth or authkey
		if err := ValidateTailscaleSecrets(s.config); err != nil {
			return tserrors.WrapConfig(err, fmt.Sprintf("service %q needs authentication but %s", svc.Name, err.Error()))
		}

		slog.Debug("generating auth key", "service", svc.Name, "reason", authKeyReason)
		cfg := config.Config{Tailscale: s.config}
		authKey, err := generateOrResolveAuthKey(cfg, svc)
		if err != nil {
			return tserrors.WrapConfig(err, fmt.Sprintf("resolving auth key for service %q", svc.Name))
		}
		serviceServer.SetAuthKey(authKey)
		slog.Debug("auth key set for service", "service", svc.Name)
	} else {
		slog.Debug("using existing state, no auth key needed", "service", svc.Name)
	}
	return nil
}

// startServiceServer starts the tsnet server for a service.
func (s *Server) startServiceServer(serviceServer tsnetpkg.TSNetServer, serviceName string) error {
	startTime := time.Now()
	slog.Debug("starting tsnet server", "service", serviceName)
	if err := serviceServer.Start(); err != nil {
		slog.Debug("tsnet server start failed",
			"service", serviceName,
			"duration", time.Since(startTime),
			"error", err,
		)
		return tserrors.WrapResource(err, fmt.Sprintf("starting tsnet server for service %q", serviceName))
	}
	slog.Debug("tsnet server started successfully",
		"service", serviceName,
		"duration", time.Since(startTime),
	)
	return nil
}

// createServiceListener creates the appropriate net.Listener based on TLS mode and funnel settings.
func (s *Server) createServiceListener(serviceServer tsnetpkg.TSNetServer, svc config.Service, tlsMode string, funnelEnabled bool, listenStart time.Time) (net.Listener, error) {
	if funnelEnabled {
		return s.createFunnelListener(serviceServer, svc.Name, listenStart)
	}

	listenAddr := s.determineListenAddr(svc, tlsMode)

	switch tlsMode {
	case constants.TLSModeAuto:
		return s.createTLSListener(serviceServer, svc.Name, listenAddr, listenStart)
	case constants.TLSModeOff:
		return s.createPlainListener(serviceServer, svc.Name, listenAddr, listenStart)
	default:
		return nil, tserrors.NewValidationError(fmt.Sprintf("invalid TLS mode: %q", tlsMode))
	}
}

// createFunnelListener creates a funnel listener.
func (s *Server) createFunnelListener(serviceServer tsnetpkg.TSNetServer, serviceName string, listenStart time.Time) (net.Listener, error) {
	listenerStart := time.Now()
	slog.Debug("creating funnel listener", "service", serviceName, "address", ":443")
	listener, err := serviceServer.ListenFunnel("tcp", ":443")
	if err != nil {
		slog.Debug("funnel listener creation failed",
			"service", serviceName,
			"duration", time.Since(listenerStart),
			"error", err,
		)
		return nil, err
	}
	slog.Debug("funnel listener created successfully",
		"service", serviceName,
		"duration", time.Since(listenerStart),
		"total_duration", time.Since(listenStart),
	)
	return listener, nil
}

// createTLSListener creates a TLS listener with certificate priming.
func (s *Server) createTLSListener(serviceServer tsnetpkg.TSNetServer, serviceName, listenAddr string, listenStart time.Time) (net.Listener, error) {
	listenerStart := time.Now()
	slog.Debug("creating TLS listener", "service", serviceName, "address", listenAddr)
	listener, err := serviceServer.ListenTLS("tcp", listenAddr)
	if err != nil {
		slog.Debug("TLS listener creation failed",
			"service", serviceName,
			"duration", time.Since(listenerStart),
			"error", err,
		)
		return nil, err
	}
	slog.Debug("TLS listener created successfully",
		"service", serviceName,
		"duration", time.Since(listenerStart),
		"total_duration", time.Since(listenStart),
	)

	// Prime the TLS certificate asynchronously
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), constants.CertificatePrimingTimeout)
		defer cancel()
		start := time.Now()
		if err := s.primeCertificate(ctx, serviceServer, serviceName); err != nil {
			slog.Warn("certificate priming failed", "service", serviceName, "error", err, "duration", time.Since(start))
		} else {
			slog.Debug("certificate primed successfully", "service", serviceName, "duration", time.Since(start))
		}
	}()

	return listener, nil
}

// createPlainListener creates a plain (non-TLS) listener.
func (s *Server) createPlainListener(serviceServer tsnetpkg.TSNetServer, serviceName, listenAddr string, listenStart time.Time) (net.Listener, error) {
	listenerStart := time.Now()
	slog.Debug("creating plain listener", "service", serviceName, "address", listenAddr)
	listener, err := serviceServer.Listen("tcp", listenAddr)
	if err != nil {
		slog.Debug("plain listener creation failed",
			"service", serviceName,
			"duration", time.Since(listenerStart),
			"error", err,
		)
		return nil, err
	}
	slog.Debug("plain listener created successfully",
		"service", serviceName,
		"duration", time.Since(listenerStart),
		"total_duration", time.Since(listenStart),
	)
	return listener, nil
}

// determineListenAddr returns the address to listen on based on service config and TLS mode
func (s *Server) determineListenAddr(svc config.Service, tlsMode string) string {
	// Use ListenAddr if set
	if svc.ListenAddr != "" {
		return svc.ListenAddr
	}

	// Default ports based on TLS mode
	if tlsMode == "off" {
		return ":80"
	}
	return ":443"
}

// GetServiceServer returns the TSNetServer for a specific service
func (s *Server) GetServiceServer(serviceName string) tsnetpkg.TSNetServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serviceServers[serviceName]
}

// Close shuts down the server and all service servers
func (s *Server) Close() error {
	s.mu.Lock()
	servers := s.serviceServers
	s.serviceServers = make(map[string]tsnetpkg.TSNetServer)
	s.mu.Unlock()

	var closeErrors []error

	// Close all service servers with timeout
	for serviceName, server := range servers {
		slog.Debug("closing tsnet server", "service", serviceName)
		if err := s.closeServerWithTimeout(server, serviceName, constants.TsnetServerCloseTimeout); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}

	// Combine errors if any occurred
	if len(closeErrors) > 0 {
		return errors.Join(closeErrors...)
	}

	return nil
}

// closeServerWithTimeout closes a tsnet server with a timeout to prevent hanging
func (s *Server) closeServerWithTimeout(server tsnetpkg.TSNetServer, serviceName string, timeout time.Duration) error {
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- server.Close()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			return tserrors.WrapResource(err, fmt.Sprintf("closing service %q", serviceName))
		}
		slog.Debug("tsnet server closed successfully", "service", serviceName, "duration", time.Since(start))
		return nil
	case <-timer.C:
		slog.Warn("tsnet server close timed out, forcing shutdown", "service", serviceName, "timeout", timeout, "duration", time.Since(start))
		return tserrors.NewTimeoutError(fmt.Sprintf("closing service %q", serviceName), timeout)
	}
}

// CloseService closes and removes the tsnet server for a specific service
func (s *Server) CloseService(serviceName string) error {
	s.mu.Lock()
	server, exists := s.serviceServers[serviceName]
	if !exists {
		s.mu.Unlock()
		// Service not found, nothing to do
		return nil
	}

	// Remove from the map immediately to prevent returning it to new callers
	delete(s.serviceServers, serviceName)
	s.mu.Unlock()

	// Close the tsnet server with timeout to avoid hangs
	if err := s.closeServerWithTimeout(server, serviceName, constants.TsnetServerCloseTimeout); err != nil {
		return err
	}

	return nil
}

// ValidateTailscaleSecrets validates that either auth key or OAuth credentials are present.
// The actual validation and resolution is done by the config package.
func ValidateTailscaleSecrets(cfg config.Tailscale) error {
	// Config package has already resolved all secrets, so we just check if they exist
	if cfg.AuthKey.Value() != "" {
		return nil // Auth key is available, no need for OAuth
	}

	// Check if OAuth credentials are available
	if cfg.OAuthClientID != "" && cfg.OAuthClientSecret.Value() != "" {
		return nil
	}

	// If neither auth key nor complete OAuth credentials are available, return error
	if cfg.OAuthClientID == "" && cfg.OAuthClientSecret.Value() == "" {
		return tserrors.NewConfigError("either auth key or OAuth credentials (client ID and secret) must be provided")
	}

	// One OAuth credential is missing
	if cfg.OAuthClientID == "" {
		return tserrors.NewConfigError("OAuth client ID is missing")
	}
	return tserrors.NewConfigError("OAuth client secret is missing")
}

// getDefaultStateDir returns the default state directory using platform-specific paths
func getDefaultStateDir() string {
	// Use XDG data directory which handles cross-platform paths correctly
	return filepath.Join(xdg.DataHome, "tsbridge")
}

// primeCertificate makes an HTTPS request to the service to trigger certificate provisioning with timeout
func (s *Server) primeCertificate(ctx context.Context, serviceServer tsnetpkg.TSNetServer, serviceName string) error {
	// Wait longer for the service to fully start and be reachable
	// This is especially important in Docker environments
	select {
	case <-time.After(constants.TsnetServerStartTimeout):
	case <-ctx.Done():
		return fmt.Errorf("context cancelled during initial wait: %w", ctx.Err())
	}

	// Get the LocalClient to fetch status
	lc, err := serviceServer.LocalClient()
	if err != nil {
		return fmt.Errorf("failed to get LocalClient for certificate priming: %w", err)
	}

	// Get status to find our FQDN using the provided context
	status, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get status for certificate priming: %w", err)
	}

	if status == nil || status.Self == nil {
		return fmt.Errorf("no self peer in status for certificate priming")
	}

	// Get the FQDN (DNSName includes trailing dot, so remove it)
	fqdn := strings.TrimSuffix(status.Self.DNSName, ".")
	if fqdn == "" {
		return fmt.Errorf("no DNS name found for certificate priming")
	}

	// Get the Tailscale IP address
	if len(status.Self.TailscaleIPs) == 0 {
		return fmt.Errorf("no Tailscale IP found for certificate priming")
	}

	tsIP := status.Self.TailscaleIPs[0].String()

	// Create a custom HTTP client that respects the context
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				// Skip verification since we're just priming the cert
				InsecureSkipVerify: true, // #nosec G402 - connecting to ourselves to prime certificate
				ServerName:         fqdn, // Use FQDN for SNI to get the correct certificate
			},
		},
	}

	// Always use the Tailscale IP to avoid DNS resolution issues
	url := fmt.Sprintf("https://%s", tsIP)

	slog.Info("priming TLS certificate",
		"service", serviceName,
		"url", url,
		"sni", fqdn)

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for certificate priming: %w", err)
	}

	// Make the request - we don't care about the response
	resp, err := client.Do(req)
	if err != nil {
		// This is expected if the backend isn't ready yet
		slog.Info("certificate priming request completed (certificate will be provisioned on first request)",
			"service", serviceName,
			"url", url,
			"sni", fqdn,
			"error", err)
		return nil // Don't return error for expected connection failures
	}
	resp.Body.Close()

	slog.Info("TLS certificate primed successfully",
		"service", serviceName,
		"url", url,
		"sni", fqdn)
	return nil
}
