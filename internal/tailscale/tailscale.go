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
	// Config package has already resolved all secrets, so we can use them directly
	authKey := cfg.AuthKey
	clientID := cfg.OAuthClientID
	clientSecret := cfg.OAuthClientSecret

	// Validate we have either AuthKey or OAuth credentials
	if authKey == "" && (clientID == "" || clientSecret == "") {
		// Provide more specific error message
		switch {
		case clientID == "" && clientSecret == "":
			return nil, tserrors.NewConfigError("either auth key or OAuth credentials (client ID and secret) must be provided")
		case clientID == "":
			return nil, tserrors.NewConfigError("OAuth client ID is required when using OAuth authentication")
		default:
			return nil, tserrors.NewConfigError("OAuth client secret is required when using OAuth authentication")
		}
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
	factory := func() tsnetpkg.TSNetServer {
		return tsnetpkg.NewRealTSNetServer()
	}

	return NewServerWithFactory(cfg, factory)
}

// Listen creates a listener for a specific service
func (s *Server) Listen(serviceName string, tlsMode string, funnelEnabled bool) (net.Listener, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a new server for this service
	serviceServer := s.serverFactory()

	// Configure the service server
	serviceServer.SetHostname(serviceName)

	// Priority for state directory
	stateDir := s.config.StateDir
	if stateDir == "" {
		stateDir = os.Getenv("TSBRIDGE_STATE_DIR")
	}
	if stateDir == "" {
		// Use XDG data directory as default
		stateDir = getDefaultStateDir()
	}
	// Each service needs its own unique state directory to avoid conflicts
	// when multiple tsnet.Server instances write to the same directory
	serviceStateDir := filepath.Join(stateDir, serviceName)
	serviceServer.SetDir(serviceStateDir)

	// Check if this service already has state
	// If state exists, tsnet will use it and doesn't need an auth key
	if !hasExistingState(stateDir, serviceName) {
		// Generate or resolve auth key with OAuth support only for new nodes
		cfg := config.Config{
			Tailscale: s.config,
		}
		authKey, err := generateOrResolveAuthKey(cfg)
		if err != nil {
			return nil, tserrors.WrapConfig(err, fmt.Sprintf("resolving auth key for service %q", serviceName))
		}
		serviceServer.SetAuthKey(authKey)
	}
	// If state exists, we don't set an auth key - tsnet will use the stored state

	// Store the service server for later operations
	s.serviceServers[serviceName] = serviceServer

	// Start the service server before listening
	if err := serviceServer.Start(); err != nil {
		return nil, tserrors.WrapResource(err, fmt.Sprintf("starting tsnet server for service %q", serviceName))
	}

	// Choose the appropriate listener based on TLS mode and funnel settings
	if funnelEnabled {
		// Funnel requires HTTPS on port 443
		return serviceServer.ListenFunnel("tcp", ":443")
	}

	switch tlsMode {
	case "auto":
		// Use ListenTLS for automatic TLS certificate provisioning
		return serviceServer.ListenTLS("tcp", ":443")
	case "off":
		// Use plain Listen without TLS (traffic still encrypted via WireGuard)
		return serviceServer.Listen("tcp", ":80")
	default:
		return nil, tserrors.NewValidationError(fmt.Sprintf("invalid TLS mode: %q", tlsMode))
	}
}

// ListenWithService creates a listener for a specific service using its full configuration
func (s *Server) ListenWithService(svc config.Service, tlsMode string, funnelEnabled bool) (net.Listener, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a new server for this service
	serviceServer := s.serverFactory()

	// Configure the service server
	serviceServer.SetHostname(svc.Name)
	serviceServer.SetEphemeral(svc.Ephemeral)

	// Priority for state directory
	stateDir := s.config.StateDir
	if stateDir == "" {
		stateDir = os.Getenv("TSBRIDGE_STATE_DIR")
	}
	if stateDir == "" {
		// Use XDG data directory as default
		stateDir = getDefaultStateDir()
	}
	// Each service needs its own unique state directory to avoid conflicts
	// when multiple tsnet.Server instances write to the same directory
	serviceStateDir := filepath.Join(stateDir, svc.Name)
	serviceServer.SetDir(serviceStateDir)

	// Check if this service already has state
	// If state exists, tsnet will use it and doesn't need an auth key
	if !hasExistingState(stateDir, svc.Name) {
		// Generate or resolve auth key with OAuth support only for new nodes
		cfg := config.Config{
			Tailscale: s.config,
		}
		authKey, err := generateOrResolveAuthKey(cfg)
		if err != nil {
			return nil, tserrors.WrapConfig(err, fmt.Sprintf("resolving auth key for service %q", svc.Name))
		}
		serviceServer.SetAuthKey(authKey)
	}
	// If state exists, we don't set an auth key - tsnet will use the stored state

	// Store the service server for later operations
	s.serviceServers[svc.Name] = serviceServer

	// Start the service server before listening
	if err := serviceServer.Start(); err != nil {
		return nil, tserrors.WrapResource(err, fmt.Sprintf("starting tsnet server for service %q", svc.Name))
	}

	// Choose the appropriate listener based on TLS mode and funnel settings
	var listener net.Listener
	var err error

	if funnelEnabled {
		// Funnel requires HTTPS on port 443
		listener, err = serviceServer.ListenFunnel("tcp", ":443")
		if err != nil {
			return nil, err
		}
		// Note: Funnel already handles certificates, no priming needed
		return listener, nil
	}

	switch tlsMode {
	case "auto":
		// Use ListenTLS for automatic TLS certificate provisioning
		listener, err = serviceServer.ListenTLS("tcp", ":443")
		if err != nil {
			return nil, err
		}

		// Prime the TLS certificate by making a request to ourselves
		go s.primeCertificate(serviceServer, svc.Name)

	case "off":
		// Use plain Listen without TLS (traffic still encrypted via WireGuard)
		listener, err = serviceServer.Listen("tcp", ":80")
		if err != nil {
			return nil, err
		}

	default:
		return nil, tserrors.NewValidationError(fmt.Sprintf("invalid TLS mode: %q", tlsMode))
	}

	return listener, nil
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
	defer s.mu.Unlock()

	var closeErrors []error

	// Close all service servers
	for serviceName, server := range s.serviceServers {
		if err := server.Close(); err != nil {
			closeErrors = append(closeErrors, tserrors.WrapResource(err, fmt.Sprintf("closing service %q", serviceName)))
		}
	}

	// Clear the map after closing
	s.serviceServers = make(map[string]tsnetpkg.TSNetServer)

	// Combine errors if any occurred
	if len(closeErrors) > 0 {
		return errors.Join(closeErrors...)
	}

	return nil
}

// ValidateTailscaleSecrets validates that either auth key or OAuth credentials are present.
// The actual validation and resolution is done by the config package.
func ValidateTailscaleSecrets(cfg config.Tailscale) error {
	// Config package has already resolved all secrets, so we just check if they exist
	if cfg.AuthKey != "" {
		return nil // Auth key is available, no need for OAuth
	}

	// Check if OAuth credentials are available
	if cfg.OAuthClientID != "" && cfg.OAuthClientSecret != "" {
		return nil
	}

	// If neither auth key nor complete OAuth credentials are available, return error
	if cfg.OAuthClientID == "" && cfg.OAuthClientSecret == "" {
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

// primeCertificate makes an HTTPS request to the service to trigger certificate provisioning
func (s *Server) primeCertificate(serviceServer tsnetpkg.TSNetServer, serviceName string) {
	// Wait longer for the service to fully start and be reachable
	// This is especially important in Docker environments
	time.Sleep(5 * time.Second)

	// Get the LocalClient to fetch status
	lc, err := serviceServer.LocalClient()
	if err != nil {
		slog.Warn("failed to get LocalClient for certificate priming",
			"service", serviceName,
			"error", err)
		return
	}

	// Get status to find our FQDN
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		slog.Warn("failed to get status for certificate priming",
			"service", serviceName,
			"error", err)
		return
	}

	if status == nil || status.Self == nil {
		slog.Warn("no self peer in status for certificate priming",
			"service", serviceName)
		return
	}

	// Get the FQDN (DNSName includes trailing dot, so remove it)
	fqdn := strings.TrimSuffix(status.Self.DNSName, ".")
	if fqdn == "" {
		slog.Warn("no DNS name found for certificate priming",
			"service", serviceName)
		return
	}

	// Get the Tailscale IP address
	if len(status.Self.TailscaleIPs) == 0 {
		slog.Warn("no Tailscale IP found for certificate priming",
			"service", serviceName)
		return
	}

	tsIP := status.Self.TailscaleIPs[0].String()

	// Create a custom HTTP client with a short timeout
	// Always use the IP address with SNI set to the FQDN
	client := &http.Client{
		Timeout: 30 * time.Second,
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

	// Make the request - we don't care about the response
	resp, err := client.Get(url)
	if err != nil {
		// This is expected if the backend isn't ready yet
		slog.Info("certificate priming request completed (certificate will be provisioned on first request)",
			"service", serviceName,
			"url", url,
			"sni", fqdn,
			"error", err)
		return
	}
	resp.Body.Close()

	slog.Info("TLS certificate primed successfully",
		"service", serviceName,
		"url", url,
		"sni", fqdn)
}
