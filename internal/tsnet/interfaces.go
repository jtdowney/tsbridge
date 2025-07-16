// Package tsnet provides interfaces and implementations for Tailscale tsnet integration.
package tsnet

import (
	"context"
	"log/slog"
	"net"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
)

// TSNetServer is an interface that abstracts tsnet.Server functionality
// to enable testing without real network connections.
type TSNetServer interface {
	// Listen creates a listener on the Tailscale network.
	Listen(network, addr string) (net.Listener, error)

	// ListenTLS creates a TLS listener on the Tailscale network with
	// automatic certificate management.
	ListenTLS(network, addr string) (net.Listener, error)

	// ListenFunnel creates a listener for serving on Tailscale Funnel.
	ListenFunnel(network, addr string) (net.Listener, error)

	// Close shuts down the server.
	Close() error

	// Start initializes the server connection to Tailscale.
	Start() error

	// LocalClient returns a LocalClient for this server.
	LocalClient() (LocalClient, error)

	// SetHostname sets the hostname for this server.
	SetHostname(hostname string)

	// SetDir sets the state directory for this server.
	SetDir(dir string)

	// SetAuthKey sets the auth key for this server.
	SetAuthKey(authKey string)

	// SetEphemeral sets whether this server should be ephemeral.
	SetEphemeral(ephemeral bool)

	// SetControlURL sets the control server URL.
	SetControlURL(controlURL string)
}

// LocalClient is an interface for tailscale LocalClient operations.
type LocalClient interface {
	// WhoIs returns information about the connecting client.
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	// StatusWithoutPeers returns the status without peer information.
	StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error)
}

// RealTSNetServer wraps a real tsnet.Server to implement TSNetServer.
type RealTSNetServer struct {
	tsnet.Server
}

// NewRealTSNetServer creates a new RealTSNetServer instance.
func NewRealTSNetServer() *RealTSNetServer {
	return &RealTSNetServer{}
}

// Listen implements TSNetServer.
func (s *RealTSNetServer) Listen(network, addr string) (net.Listener, error) {
	start := time.Now()
	slog.Debug("tsnet Listen() called",
		"hostname", s.Hostname,
		"network", network,
		"addr", addr,
	)

	listener, err := s.Server.Listen(network, addr)

	if err != nil {
		slog.Debug("tsnet Listen() failed",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"error", err,
		)
	} else {
		slog.Debug("tsnet Listen() succeeded",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"listener_addr", listener.Addr(),
		)
	}

	return listener, err
}

// ListenTLS implements TSNetServer.
func (s *RealTSNetServer) ListenTLS(network, addr string) (net.Listener, error) {
	start := time.Now()
	slog.Debug("tsnet ListenTLS() called",
		"hostname", s.Hostname,
		"network", network,
		"addr", addr,
	)

	listener, err := s.Server.ListenTLS(network, addr)

	if err != nil {
		slog.Debug("tsnet ListenTLS() failed",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"error", err,
		)
	} else {
		slog.Debug("tsnet ListenTLS() succeeded",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"listener_addr", listener.Addr(),
		)
	}

	return listener, err
}

// ListenFunnel implements TSNetServer.
func (s *RealTSNetServer) ListenFunnel(network, addr string) (net.Listener, error) {
	start := time.Now()
	slog.Debug("tsnet ListenFunnel() called",
		"hostname", s.Hostname,
		"network", network,
		"addr", addr,
	)

	listener, err := s.Server.ListenFunnel(network, addr)

	if err != nil {
		slog.Debug("tsnet ListenFunnel() failed",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"error", err,
		)
	} else {
		slog.Debug("tsnet ListenFunnel() succeeded",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"listener_addr", listener.Addr(),
		)
	}

	return listener, err
}

// Close implements TSNetServer.
func (s *RealTSNetServer) Close() error {
	return s.Server.Close()
}

// Start implements TSNetServer.
func (s *RealTSNetServer) Start() error {
	start := time.Now()
	slog.Debug("tsnet server Start() called",
		"hostname", s.Hostname,
		"ephemeral", s.Ephemeral,
		"dir", s.Dir,
		"has_auth_key", s.AuthKey != "",
	)

	err := s.Server.Start()

	if err != nil {
		slog.Debug("tsnet server Start() failed",
			"hostname", s.Hostname,
			"duration", time.Since(start),
			"error", err,
		)
	} else {
		slog.Debug("tsnet server Start() succeeded",
			"hostname", s.Hostname,
			"duration", time.Since(start),
		)
	}

	return err
}

// LocalClient implements TSNetServer.
func (s *RealTSNetServer) LocalClient() (LocalClient, error) {
	lc, err := s.Server.LocalClient()
	if err != nil {
		return nil, err
	}
	return &RealLocalClient{lc: lc}, nil
}

// SetHostname implements TSNetServer.
func (s *RealTSNetServer) SetHostname(hostname string) {
	s.Hostname = hostname
}

// SetDir implements TSNetServer.
func (s *RealTSNetServer) SetDir(dir string) {
	s.Dir = dir
}

// SetAuthKey implements TSNetServer.
func (s *RealTSNetServer) SetAuthKey(authKey string) {
	s.AuthKey = authKey
}

// SetEphemeral implements TSNetServer.
func (s *RealTSNetServer) SetEphemeral(ephemeral bool) {
	s.Ephemeral = ephemeral
}

// SetControlURL implements TSNetServer.
func (s *RealTSNetServer) SetControlURL(controlURL string) {
	s.ControlURL = controlURL
}

// RealLocalClient wraps a real local.Client.
type RealLocalClient struct {
	lc *local.Client
}

// WhoIs implements LocalClient.
func (c *RealLocalClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	return c.lc.WhoIs(ctx, remoteAddr)
}

// StatusWithoutPeers implements LocalClient.
func (c *RealLocalClient) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	return c.lc.StatusWithoutPeers(ctx)
}

// MockTSNetServer is a mock implementation of TSNetServer for testing.
type MockTSNetServer struct {
	Hostname  string
	Dir       string
	AuthKey   string
	Ephemeral bool
	Logf      logger.Logf

	ListenFunc       func(network, addr string) (net.Listener, error)
	ListenTLSFunc    func(network, addr string) (net.Listener, error)
	ListenFunnelFunc func(network, addr string) (net.Listener, error)
	CloseFunc        func() error
	StartFunc        func() error
	LocalClientFunc  func() (LocalClient, error)
}

// NewMockTSNetServer creates a new MockTSNetServer instance.
func NewMockTSNetServer() *MockTSNetServer {
	return &MockTSNetServer{
		ListenFunc: func(network, addr string) (net.Listener, error) {
			// Return a mock listener
			return &mockListener{addr: addr}, nil
		},
		ListenTLSFunc: func(network, addr string) (net.Listener, error) {
			// Return a mock listener for TLS
			return &mockListener{addr: addr}, nil
		},
		ListenFunnelFunc: func(network, addr string) (net.Listener, error) {
			// Return a mock listener for Funnel
			return &mockListener{addr: addr}, nil
		},
		CloseFunc: func() error {
			return nil
		},
		StartFunc: func() error {
			return nil
		},
		LocalClientFunc: func() (LocalClient, error) {
			return &MockLocalClient{}, nil
		},
	}
}

// mockListener is a simple mock implementation of net.Listener
type mockListener struct {
	addr string
}

func (m *mockListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (m *mockListener) Close() error {
	return nil
}

func (m *mockListener) Addr() net.Addr {
	return &mockAddr{addr: m.addr}
}

// mockAddr is a simple mock implementation of net.Addr
type mockAddr struct {
	addr string
}

func (m *mockAddr) Network() string {
	return "tcp"
}

func (m *mockAddr) String() string {
	return m.addr
}

// Listen implements TSNetServer.
func (m *MockTSNetServer) Listen(network, addr string) (net.Listener, error) {
	return m.ListenFunc(network, addr)
}

// ListenTLS implements TSNetServer.
func (m *MockTSNetServer) ListenTLS(network, addr string) (net.Listener, error) {
	return m.ListenTLSFunc(network, addr)
}

// ListenFunnel implements TSNetServer.
func (m *MockTSNetServer) ListenFunnel(network, addr string) (net.Listener, error) {
	if m.ListenFunnelFunc != nil {
		return m.ListenFunnelFunc(network, addr)
	}
	return &mockListener{addr: addr}, nil
}

// Close implements TSNetServer.
func (m *MockTSNetServer) Close() error {
	return m.CloseFunc()
}

// Start implements TSNetServer.
func (m *MockTSNetServer) Start() error {
	return m.StartFunc()
}

// LocalClient implements TSNetServer.
func (m *MockTSNetServer) LocalClient() (LocalClient, error) {
	return m.LocalClientFunc()
}

// SetHostname implements TSNetServer.
func (m *MockTSNetServer) SetHostname(hostname string) {
	m.Hostname = hostname
}

// SetDir implements TSNetServer.
func (m *MockTSNetServer) SetDir(dir string) {
	m.Dir = dir
}

// SetAuthKey implements TSNetServer.
func (m *MockTSNetServer) SetAuthKey(authKey string) {
	m.AuthKey = authKey
}

// SetEphemeral implements TSNetServer.
func (m *MockTSNetServer) SetEphemeral(ephemeral bool) {
	m.Ephemeral = ephemeral
}

// SetControlURL implements TSNetServer.
func (m *MockTSNetServer) SetControlURL(controlURL string) {
	// No-op for mock
}

// MockLocalClient is a mock implementation of LocalClient for testing.
type MockLocalClient struct {
	WhoIsFunc              func(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	StatusWithoutPeersFunc func(ctx context.Context) (*ipnstate.Status, error)
}

// WhoIs implements LocalClient.
func (m *MockLocalClient) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if m.WhoIsFunc != nil {
		return m.WhoIsFunc(ctx, remoteAddr)
	}
	return nil, nil
}

// StatusWithoutPeers implements LocalClient.
func (m *MockLocalClient) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	if m.StatusWithoutPeersFunc != nil {
		return m.StatusWithoutPeersFunc(ctx)
	}
	return nil, nil
}

// TSNetServerFactory is a function that creates new TSNetServer instances.
type TSNetServerFactory func() TSNetServer
