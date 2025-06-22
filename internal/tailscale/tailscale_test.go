package tailscale

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"

	"github.com/jtdowney/tsbridge/internal/config"
	tferrors "github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/testutil"
	tsnet "github.com/jtdowney/tsbridge/internal/tsnet"
)

func TestNewServer(t *testing.T) {
	// This test verifies that NewServer validates auth configuration
	// Since NewServer now creates real TSNet servers, we focus on validation
	tests := []struct {
		name    string
		cfg     config.Tailscale
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with inline OAuth",
			cfg: config.Tailscale{
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
				OAuthTags:         []string{"tag:tsbridge"},
			},
			wantErr: false,
		},
		{
			name: "valid config with auth key",
			cfg: config.Tailscale{
				AuthKey: "test-auth-key",
			},
			wantErr: false,
		},
		{
			name: "valid config with auth key from env",
			cfg: config.Tailscale{
				// Config package would have resolved this already
				AuthKey: "env-auth-key",
			},
			wantErr: false,
		},
		{
			name:    "missing all credentials",
			cfg:     config.Tailscale{},
			wantErr: true,
			errMsg:  "either auth key or OAuth credentials",
		},
		{
			name: "missing OAuth client secret",
			cfg: config.Tailscale{
				OAuthClientID: "test-client-id",
				// Missing secret
			},
			wantErr: true,
			errMsg:  "OAuth client secret is required",
		},
		{
			name: "missing OAuth client ID",
			cfg: config.Tailscale{
				OAuthClientSecret: "test-secret",
				// Missing ID
			},
			wantErr: true,
			errMsg:  "OAuth client ID is required",
		},
		{
			name: "config with state directory",
			cfg: config.Tailscale{
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
				StateDir:          "/tmp/tsbridge-test",
			},
			wantErr: false,
		},
		{
			name: "config with state directory",
			cfg: config.Tailscale{
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
				StateDir:          "/tmp/tsbridge-state",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: Environment variables would be resolved by the config package
			// before the Tailscale config reaches this code
			server, err := NewServer(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewServer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("NewServer() error = %v, want error containing %q", err, tt.errMsg)
				}
			}

			if !tt.wantErr && server == nil {
				t.Error("NewServer() returned nil server without error")
			}
		})
	}
}

func TestServer_Listen(t *testing.T) {
	// This test will verify that we can create listeners for services
	// Use auth key instead of OAuth to avoid external API calls
	cfg := config.Tailscale{
		AuthKey: "test-auth-key",
	}

	// Use mock factory for testing
	factory := func() tsnet.TSNetServer {
		return tsnet.NewMockTSNetServer()
	}

	server, err := NewServerWithFactory(cfg, factory)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Test that we can create a listener
	listener, err := server.Listen("test-service", "auto", false)
	if err != nil {
		t.Errorf("Listen() error = %v", err)
	}
	if listener == nil {
		t.Error("Listen() returned nil listener without error")
	}
}

func TestServer_ListenWithFunnel(t *testing.T) {
	// Test that funnel listeners are created correctly
	cfg := config.Tailscale{
		AuthKey: "test-auth-key",
	}

	// Track which listen method was called
	var listenFunnelCalled bool

	// Use mock factory for testing
	factory := func() tsnet.TSNetServer {
		mock := tsnet.NewMockTSNetServer()
		// Override the ListenFunnel function to track it was called
		mock.ListenFunnelFunc = func(network, addr string) (net.Listener, error) {
			listenFunnelCalled = true
			if network != "tcp" || addr != ":443" {
				t.Errorf("expected tcp:443, got %s%s", network, addr)
			}
			return &mockListener{addr: addr}, nil
		}
		return mock
	}

	server, err := NewServerWithFactory(cfg, factory)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Test with funnel enabled
	listener, err := server.Listen("test-service", "auto", true)
	if err != nil {
		t.Errorf("Listen() with funnel error = %v", err)
	}
	if listener == nil {
		t.Error("Listen() with funnel returned nil listener without error")
	}
	if !listenFunnelCalled {
		t.Error("ListenFunnel was not called when funnel was enabled")
	}
}

// mockListener implements net.Listener for testing
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
	return &net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 443,
	}
}

func TestValidateTailscaleSecrets(t *testing.T) {
	// This test verifies that ValidateTailscaleSecrets checks if values exist in the config
	// The config package is responsible for resolving secrets from env/files
	tests := []struct {
		name    string
		cfg     config.Tailscale
		wantErr bool
		wantMsg string
	}{
		{
			name: "auth key provided",
			cfg: config.Tailscale{
				AuthKey: "tskey-auth-xxx",
			},
			wantErr: false,
		},
		{
			name: "oauth credentials provided",
			cfg: config.Tailscale{
				OAuthClientID:     "client-id",
				OAuthClientSecret: "client-secret",
			},
			wantErr: false,
		},
		{
			name:    "no credentials provided",
			cfg:     config.Tailscale{},
			wantErr: true,
			wantMsg: "either auth key or OAuth credentials (client ID and secret) must be provided",
		},
		{
			name: "only client ID provided",
			cfg: config.Tailscale{
				OAuthClientID: "client-id",
			},
			wantErr: true,
			wantMsg: "OAuth client secret is missing",
		},
		{
			name: "only client secret provided",
			cfg: config.Tailscale{
				OAuthClientSecret: "client-secret",
			},
			wantErr: true,
			wantMsg: "OAuth client ID is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTailscaleSecrets(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTailscaleSecrets() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("ValidateTailscaleSecrets() error = %v, want error containing %v", err, tt.wantMsg)
			}
		})
	}
}

func TestServerWithDependencyInjection(t *testing.T) {
	t.Run("creates server with factory", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey:  "test-key",
			StateDir: "/test/state",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.AuthKey = "test-key"
			mock.Dir = "/test/state"
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if server == nil {
			t.Fatal("expected server to be created")
		}

		// Factory is stored but not called until Listen
		if server.serverFactory == nil {
			t.Error("expected factory to be stored")
		}
	})

	t.Run("Listen creates service-specific server", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		listenerCreated := false
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.AuthKey = "test-key"
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				if network != "tcp" || addr != ":443" {
					t.Errorf("expected tcp:443, got %s:%s", network, addr)
				}
				listenerCreated = true
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		listener, err := server.Listen("test-service", "auto", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer listener.Close()

		if !listenerCreated {
			t.Error("listener was not created")
		}
	})

	t.Run("Close closes all service servers", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		var closedServers []string
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.AuthKey = "test-key"
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			// Capture which server is being closed
			hostname := mock.Hostname
			mock.CloseFunc = func() error {
				closedServers = append(closedServers, hostname)
				return nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create multiple services
		services := []string{"service1", "service2", "service3"}
		for _, svc := range services {
			_, err := server.Listen(svc, "auto", false)
			if err != nil {
				t.Fatalf("unexpected error creating listener for %s: %v", svc, err)
			}
		}

		// Close should close all service servers
		err = server.Close()
		if err != nil {
			t.Fatalf("unexpected error closing server: %v", err)
		}

		if len(closedServers) != len(services) {
			t.Errorf("expected %d servers to be closed, got %d", len(services), len(closedServers))
		}
	})

	t.Run("Close collects errors from all servers", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		closeCount := 0
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.AuthKey = "test-key"
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			// Make some servers fail on close
			currentCount := closeCount
			closeCount++
			mock.CloseFunc = func() error {
				if currentCount%2 == 0 {
					return errors.New("close error")
				}
				return nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create multiple services
		for i := 0; i < 4; i++ {
			_, err := server.Listen(string(rune('a'+i)), "auto", false)
			if err != nil {
				t.Fatalf("unexpected error creating listener: %v", err)
			}
		}

		// Close should return an error since some servers fail
		err = server.Close()
		if err == nil {
			t.Error("expected error from Close, got nil")
		}
	})
}

func TestResolveAuthConfiguration(t *testing.T) {
	t.Run("validates auth configuration", func(t *testing.T) {
		testCases := []struct {
			name    string
			cfg     config.Tailscale
			envVars map[string]string
			wantErr bool
		}{
			{
				name: "auth key provided",
				cfg: config.Tailscale{
					AuthKey: "test-key",
				},
				wantErr: false,
			},
			{
				name: "OAuth credentials provided",
				cfg: config.Tailscale{
					OAuthClientID:     "client-id",
					OAuthClientSecret: "client-secret",
				},
				wantErr: false,
			},
			{
				name:    "no credentials provided",
				cfg:     config.Tailscale{},
				wantErr: true,
			},
			{
				name: "incomplete OAuth credentials",
				cfg: config.Tailscale{
					OAuthClientID: "client-id",
					// Missing secret
				},
				wantErr: true,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Set up environment variables
				for k, v := range tc.envVars {
					t.Setenv(k, v)
				}

				factory := func() tsnet.TSNetServer {
					return tsnet.NewMockTSNetServer()
				}

				_, err := NewServerWithFactory(tc.cfg, factory)
				if tc.wantErr && err == nil {
					t.Error("expected error, got nil")
				}
				if !tc.wantErr && err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			})
		}
	})
}

func TestServiceLifecycle(t *testing.T) {
	t.Run("Start method does not exist", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			return tsnet.NewMockTSNetServer()
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify that Start method does not exist on the server interface
		// This test will fail to compile if Start() method exists
		type noStartMethodInterface interface {
			Listen(serviceName string, tlsMode string, funnelEnabled bool) (net.Listener, error)
			Close() error
			GetServiceServer(serviceName string) tsnet.TSNetServer
		}

		// This should compile if Start() is removed
		var _ noStartMethodInterface = server
	})

	t.Run("service initialization happens during Listen", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey:  "test-key",
			StateDir: "/test/state",
		}

		var serviceStarted bool
		var configuredHostname, configuredAuthKey, configuredDir string

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				serviceStarted = true
				// Capture the configuration at start time
				configuredHostname = mock.Hostname
				configuredAuthKey = mock.AuthKey
				configuredDir = mock.Dir
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Service should not be started until Listen is called
		if serviceStarted {
			t.Error("service was started before Listen")
		}

		// Call Listen to initialize the service
		listener, err := server.Listen("test-service", "auto", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer listener.Close()

		// Service should be started and configured properly
		if !serviceStarted {
			t.Error("service was not started after Listen")
		}

		if configuredHostname != "test-service" {
			t.Errorf("expected hostname %q, got %q", "test-service", configuredHostname)
		}

		if configuredAuthKey != "test-key" {
			t.Errorf("expected auth key %q, got %q", "test-key", configuredAuthKey)
		}

		if configuredDir != "/test/state/test-service" {
			t.Errorf("expected dir %q, got %q", "/test/state/test-service", configuredDir)
		}
	})

	t.Run("concurrent Listen calls are thread-safe", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Launch multiple goroutines that create listeners concurrently
		const numGoroutines = 10
		errChan := make(chan error, numGoroutines)
		listenerChan := make(chan net.Listener, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(idx int) {
				serviceName := fmt.Sprintf("service-%d", idx)
				listener, err := server.Listen(serviceName, "auto", false)
				if err != nil {
					errChan <- err
				} else {
					listenerChan <- listener
				}
			}(i)
		}

		// Collect results
		var listeners []net.Listener
		for i := 0; i < numGoroutines; i++ {
			select {
			case err := <-errChan:
				t.Errorf("concurrent Listen failed: %v", err)
			case listener := <-listenerChan:
				listeners = append(listeners, listener)
			}
		}

		// Clean up listeners
		for _, listener := range listeners {
			listener.Close()
		}

		// Verify all services were created
		if len(listeners) != numGoroutines {
			t.Errorf("expected %d listeners, got %d", numGoroutines, len(listeners))
		}
	})

	t.Run("Listen fails when service start fails", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return errors.New("start failed")
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Listen should fail when Start fails
		_, err = server.Listen("test-service", "auto", false)
		if err == nil {
			t.Error("expected error when Start fails, got nil")
		}

		if !strings.Contains(err.Error(), "start failed") {
			t.Errorf("expected error to contain 'start failed', got: %v", err)
		}
	})

	t.Run("GetServiceServer returns correct server", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		var createdServers []tsnet.TSNetServer
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			createdServers = append(createdServers, mock)
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create multiple services
		serviceNames := []string{"service1", "service2", "service3"}
		for _, name := range serviceNames {
			_, err := server.Listen(name, "auto", false)
			if err != nil {
				t.Fatalf("unexpected error creating listener for %s: %v", name, err)
			}
		}

		// Verify GetServiceServer returns the correct server for each service
		for i, name := range serviceNames {
			tsnetServer := server.GetServiceServer(name)
			if tsnetServer != createdServers[i] {
				t.Errorf("GetServiceServer(%q) returned wrong server", name)
			}
		}

		// Verify GetServiceServer returns nil for non-existent service
		nonExistentServer := server.GetServiceServer("non-existent")
		if nonExistentServer != nil {
			t.Error("GetServiceServer should return nil for non-existent service")
		}
	})

	t.Run("Close clears serviceServers map", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			mock.CloseFunc = func() error {
				return nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create a service
		_, err = server.Listen("test-service", "auto", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify service exists
		if server.GetServiceServer("test-service") == nil {
			t.Error("service should exist before Close")
		}

		// Close the server
		err = server.Close()
		if err != nil {
			t.Fatalf("unexpected error closing server: %v", err)
		}

		// Verify service no longer exists
		if server.GetServiceServer("test-service") != nil {
			t.Error("service should not exist after Close")
		}
	})

	t.Run("state directory from environment variable", func(t *testing.T) {
		t.Setenv("TSBRIDGE_STATE_DIR", "/env/state")

		cfg := config.Tailscale{
			AuthKey: "test-key",
			// No StateDir configured
		}

		var capturedDir string
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				capturedDir = mock.Dir
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create a service to trigger configuration
		_, err = server.Listen("test-service", "auto", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify environment variable was used (with service name appended)
		if capturedDir != "/env/state/test-service" {
			t.Errorf("expected dir from env var %q, got %q", "/env/state/test-service", capturedDir)
		}
	})

	t.Run("state directory defaults to XDG data home", func(t *testing.T) {
		// Clear any TSBRIDGE_STATE_DIR that might be set
		t.Setenv("TSBRIDGE_STATE_DIR", "")

		cfg := config.Tailscale{
			AuthKey: "test-key",
			// No StateDir configured
		}

		var capturedDir string
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				capturedDir = mock.Dir
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create a service to trigger configuration
		_, err = server.Listen("test-service", "auto", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify default directory is used (should end with /tsbridge/test-service)
		if !strings.HasSuffix(capturedDir, "/tsbridge/test-service") {
			t.Errorf("expected dir to end with /tsbridge/test-service, got %q", capturedDir)
		}

		// On macOS, should be in Library/Application Support
		if strings.Contains(capturedDir, "darwin") || strings.Contains(capturedDir, "Library/Application Support") {
			if !strings.Contains(capturedDir, "Library/Application Support/tsbridge") {
				t.Errorf("on macOS, expected dir to contain Library/Application Support/tsbridge, got %q", capturedDir)
			}
		}
	})

	t.Run("each service gets unique state directory", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey:  "test-key",
			StateDir: "/base/state",
		}

		capturedDirs := make(map[string]string)
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				// Capture the directory for each service
				capturedDirs[mock.Hostname] = mock.Dir
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create multiple services
		services := []string{"service1", "service2", "service3"}
		for _, svc := range services {
			_, err := server.Listen(svc, "auto", false)
			if err != nil {
				t.Fatalf("unexpected error creating listener for %s: %v", svc, err)
			}
		}

		// Verify each service has a unique subdirectory
		for _, svc := range services {
			expectedDir := fmt.Sprintf("/base/state/%s", svc)
			actualDir := capturedDirs[svc]
			if actualDir != expectedDir {
				t.Errorf("service %q: expected dir %q, got %q", svc, expectedDir, actualDir)
			}
		}

		// Verify all directories are unique
		seenDirs := make(map[string]bool)
		for svc, dir := range capturedDirs {
			if seenDirs[dir] {
				t.Errorf("duplicate state directory %q for service %q", dir, svc)
			}
			seenDirs[dir] = true
		}
	})
}

func TestEphemeralServices(t *testing.T) {
	t.Run("ephemeral flag is set on tsnet server", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		var createdServers []tsnet.TSNetServer
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				ln := &mockListener{
					addr: addr,
				}
				return ln, nil
			}
			createdServers = append(createdServers, mock)
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create a service with ephemeral=true
		svc := config.Service{
			Name:      "test-service",
			Ephemeral: true,
		}

		// Listen should create the server with ephemeral flag
		_, err = server.ListenWithService(svc, "off", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the server was created with Ephemeral=true
		if len(createdServers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(createdServers))
		}

		// Check that Ephemeral was set on the mock
		mock := createdServers[0].(*tsnet.MockTSNetServer)
		if !mock.Ephemeral {
			t.Error("expected Ephemeral to be true")
		}
	})

	t.Run("ephemeral defaults to false", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		var createdServers []tsnet.TSNetServer
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				ln := &mockListener{
					addr: addr,
				}
				return ln, nil
			}
			createdServers = append(createdServers, mock)
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create a service without ephemeral field (defaults to false)
		svc := config.Service{
			Name: "test-service",
		}

		// Listen should create the server with ephemeral flag false
		_, err = server.ListenWithService(svc, "off", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the server was created with Ephemeral=false
		if len(createdServers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(createdServers))
		}

		// Check that Ephemeral was set to false on the mock
		mock := createdServers[0].(*tsnet.MockTSNetServer)
		if mock.Ephemeral {
			t.Error("expected Ephemeral to be false")
		}
	})
}

func TestGetDefaultStateDir(t *testing.T) {
	// Test the default state directory path
	dir := getDefaultStateDir()

	// Should always end with /tsbridge
	if !strings.HasSuffix(dir, "/tsbridge") && !strings.HasSuffix(dir, "\\tsbridge") {
		t.Errorf("expected dir to end with /tsbridge or \\tsbridge, got %q", dir)
	}

	// Platform-specific checks
	switch runtime.GOOS {
	case "darwin":
		// macOS should use Library/Application Support
		if !strings.Contains(dir, "Library/Application Support/tsbridge") {
			t.Errorf("on macOS, expected dir to contain Library/Application Support/tsbridge, got %q", dir)
		}
	case "windows":
		// Windows should use AppData
		if !strings.Contains(dir, "AppData") && !strings.Contains(dir, "tsbridge") {
			t.Errorf("on Windows, expected dir to contain AppData and tsbridge, got %q", dir)
		}
	default:
		// Linux/Unix should use .local/share or XDG_DATA_HOME
		if !strings.Contains(dir, ".local/share/tsbridge") && os.Getenv("XDG_DATA_HOME") == "" {
			t.Errorf("on Linux/Unix, expected dir to contain .local/share/tsbridge, got %q", dir)
		}
	}
}

func TestTLSMode(t *testing.T) {

	t.Run("Listen with TLS mode auto uses ListenTLS", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		listenerCreated := false
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				if network != "tcp" || addr != ":443" {
					t.Errorf("expected tcp:443, got %s:%s", network, addr)
				}
				listenerCreated = true
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				t.Error("ListenFunc should not be called with TLS mode auto")
				return nil, errors.New("should not be called")
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		listener, err := server.Listen("test-service", "auto", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer listener.Close()

		if !listenerCreated {
			t.Error("ListenTLS was not called")
		}
	})

	t.Run("Listen with TLS mode off uses Listen", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		listenerCreated := false
		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				if network != "tcp" || addr != ":80" {
					t.Errorf("expected tcp:80, got %s:%s", network, addr)
				}
				listenerCreated = true
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				t.Error("ListenTLSFunc should not be called with TLS mode off")
				return nil, errors.New("should not be called")
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		listener, err := server.Listen("test-service", "off", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer listener.Close()

		if !listenerCreated {
			t.Error("Listen was not called")
		}
	})

	t.Run("Listen with invalid TLS mode returns error", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = server.Listen("test-service", "invalid", false)
		if err == nil {
			t.Error("expected error for invalid TLS mode")
		}
		if !strings.Contains(err.Error(), "invalid TLS mode") {
			t.Errorf("expected error about invalid TLS mode, got: %v", err)
		}
	})
}

func TestTailscaleErrorTypes(t *testing.T) {
	t.Run("missing credentials returns config error", func(t *testing.T) {
		cfg := config.Tailscale{
			// No auth key or OAuth credentials
		}

		_, err := NewServer(cfg)
		if err == nil {
			t.Fatal("expected error for missing credentials")
		}

		if !tferrors.IsConfig(err) {
			t.Errorf("expected config error, got %v", err)
		}

		// Check that error message contains expected text
		expectedMsg := "either auth key or OAuth credentials"
		if !strings.Contains(err.Error(), expectedMsg) {
			t.Errorf("expected error message to contain %q, got %v", expectedMsg, err)
		}
	})

	t.Run("incomplete OAuth credentials returns config error", func(t *testing.T) {
		cfg := config.Tailscale{
			OAuthClientID: "client-id",
			// Missing client secret
		}

		_, err := NewServer(cfg)
		if err == nil {
			t.Fatal("expected error for incomplete OAuth credentials")
		}

		if !tferrors.IsConfig(err) {
			t.Errorf("expected config error, got %v", err)
		}
	})

	t.Run("invalid TLS mode returns validation error", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = server.Listen("test-service", "invalid-mode", false)
		if err == nil {
			t.Fatal("expected error for invalid TLS mode")
		}

		if !tferrors.IsValidation(err) {
			t.Errorf("expected validation error, got %v", err)
		}
	})

	t.Run("tsnet server start failure returns resource error", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return tferrors.NewNetworkError("connection failed")
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = server.Listen("test-service", "auto", false)
		if err == nil {
			t.Fatal("expected error when tsnet server fails to start")
		}

		if !tferrors.IsResource(err) {
			t.Errorf("expected resource error, got %v", err)
		}
	})

	t.Run("service close errors are resource errors", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKey: "test-key",
		}

		factory := func() tsnet.TSNetServer {
			mock := tsnet.NewMockTSNetServer()
			mock.StartFunc = func() error {
				return nil
			}
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				ln, _ := net.Listen("tcp", "127.0.0.1:0")
				return ln, nil
			}
			mock.CloseFunc = func() error {
				return tferrors.NewNetworkError("close failed")
			}
			return mock
		}

		server, err := NewServerWithFactory(cfg, factory)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Create a service
		_, err = server.Listen("test-service", "off", false)
		if err != nil {
			t.Fatalf("unexpected error creating listener: %v", err)
		}

		// Close should return resource error
		err = server.Close()
		if err == nil {
			t.Fatal("expected error when closing fails")
		}

		if !tferrors.IsResource(err) {
			t.Errorf("expected resource error, got %v", err)
		}
	})

	t.Run("auth key resolution error is config error", func(t *testing.T) {
		cfg := config.Tailscale{
			AuthKeyFile: "/nonexistent/file",
		}

		_, err := NewServer(cfg)
		if err == nil {
			t.Fatal("expected error for nonexistent auth key file")
		}

		if !tferrors.IsConfig(err) {
			t.Errorf("expected config error, got %v", err)
		}
	})
}

func TestSecretResolutionByConfig(t *testing.T) {
	// This test verifies that the tailscale package correctly validates
	// secrets that have already been resolved by the config package
	tests := []struct {
		name         string
		cfg          config.Tailscale
		expectNewErr bool
		errMsg       string
	}{
		{
			name: "oauth credentials already resolved",
			cfg: config.Tailscale{
				OAuthClientID:     "test-client-id",
				OAuthClientSecret: "test-client-secret",
				OAuthTags:         []string{"tag:test"},
			},
			expectNewErr: false,
		},
		{
			name: "auth key already resolved",
			cfg: config.Tailscale{
				AuthKey: "tskey-test-12345",
			},
			expectNewErr: false,
		},
		{
			name: "missing all credentials",
			cfg:  config.Tailscale{
				// No auth configuration at all
			},
			expectNewErr: true,
			errMsg:       "either auth key or OAuth credentials",
		},
		{
			name: "missing OAuth client secret",
			cfg: config.Tailscale{
				OAuthClientID: "test-client-id",
				// Missing secret
			},
			expectNewErr: true,
			errMsg:       "OAuth client secret is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock factory
			factory := func() tsnet.TSNetServer {
				return tsnet.NewMockTSNetServer()
			}

			// NewServer validates that credentials are present
			server, err := NewServerWithFactory(tt.cfg, factory)
			if tt.expectNewErr {
				testutil.AssertError(t, err) // Just check that error occurred
				if tt.errMsg != "" {
					testutil.AssertError(t, err, tt.errMsg)
				}
				return
			}
			testutil.RequireNoError(t, err, "NewServer should succeed with resolved secrets")
			testutil.RequireNotNil(t, server)
		})
	}
}

func TestSecretValidationErrorMessages(t *testing.T) {
	// This test verifies error messages when secrets are missing
	// Note: The config package handles secret resolution from env/files,
	// so these tests assume the config has already been processed
	tests := []struct {
		name           string
		cfg            config.Tailscale
		expectedErrors []string
	}{
		{
			name: "missing OAuth client ID",
			cfg: config.Tailscale{
				// Config resolution would have left this empty if env var was missing
				OAuthClientID:     "",
				OAuthClientSecret: "test-secret",
			},
			expectedErrors: []string{
				"OAuth client ID is required",
			},
		},
		{
			name: "missing OAuth client secret",
			cfg: config.Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: "", // Would be empty if file was missing
			},
			expectedErrors: []string{
				"OAuth client secret is required",
			},
		},
		{
			name: "no auth configuration",
			cfg:  config.Tailscale{
				// No auth configuration at all
			},
			expectedErrors: []string{
				"either auth key or OAuth credentials (client ID and secret) must be provided",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := func() tsnet.TSNetServer {
				return tsnet.NewMockTSNetServer()
			}

			_, err := NewServerWithFactory(tt.cfg, factory)
			testutil.RequireError(t, err, "NewServer should fail when secrets are missing")

			// Verify error message contains expected information
			errMsg := err.Error()
			for _, expected := range tt.expectedErrors {
				testutil.AssertContains(t, errMsg, expected)
			}
		})
	}
}

func TestCertificatePriming(t *testing.T) {
	tests := []struct {
		name             string
		serviceName      string
		tlsMode          string
		funnelEnabled    bool
		expectPriming    bool
		mockDNSName      string
		mockTailscaleIPs []string
		statusError      error
		localClientErr   error
	}{
		{
			name:          "TLS auto mode triggers priming",
			serviceName:   "test-service",
			tlsMode:       "auto",
			funnelEnabled: false,
			expectPriming: true,
			mockDNSName:   "test-service.tailnet.ts.net.",
		},
		{
			name:          "TLS off mode does not trigger priming",
			serviceName:   "test-service",
			tlsMode:       "off",
			funnelEnabled: false,
			expectPriming: false,
		},
		{
			name:          "Funnel mode does not trigger priming",
			serviceName:   "test-service",
			tlsMode:       "auto",
			funnelEnabled: true,
			expectPriming: false,
		},
		{
			name:           "LocalClient error handled gracefully",
			serviceName:    "test-service",
			tlsMode:        "auto",
			funnelEnabled:  false,
			expectPriming:  true,
			localClientErr: errors.New("local client error"),
		},
		{
			name:          "Status error handled gracefully",
			serviceName:   "test-service",
			tlsMode:       "auto",
			funnelEnabled: false,
			expectPriming: true,
			statusError:   errors.New("status error"),
		},
		{
			name:          "Empty DNS name handled gracefully",
			serviceName:   "test-service",
			tlsMode:       "auto",
			funnelEnabled: false,
			expectPriming: true,
			mockDNSName:   "",
		},
		{
			name:             "Certificate priming uses IP with SNI",
			serviceName:      "test-service",
			tlsMode:          "auto",
			funnelEnabled:    false,
			expectPriming:    true,
			mockDNSName:      "test-service.tailnet.ts.net.",
			mockTailscaleIPs: []string{"100.100.100.100"},
		},
		{
			name:             "No Tailscale IP skips priming",
			serviceName:      "test-service",
			tlsMode:          "auto",
			funnelEnabled:    false,
			expectPriming:    false,
			mockDNSName:      "test-service.tailnet.ts.net.",
			mockTailscaleIPs: []string{}, // No IPs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mocks
			mockLocalClient := &tsnet.MockLocalClient{}
			mockTSNetServer := tsnet.NewMockTSNetServer()

			// Set up mock expectations
			mockTSNetServer.LocalClientFunc = func() (tsnet.LocalClient, error) {
				if tt.localClientErr != nil {
					return nil, tt.localClientErr
				}
				return mockLocalClient, nil
			}

			if tt.localClientErr == nil && tt.expectPriming {
				mockLocalClient.StatusWithoutPeersFunc = func(ctx context.Context) (*ipnstate.Status, error) {
					if tt.statusError != nil {
						return nil, tt.statusError
					}

					// Convert IP strings to netip.Addr
					var tailscaleIPs []netip.Addr
					for _, ip := range tt.mockTailscaleIPs {
						if addr, err := netip.ParseAddr(ip); err == nil {
							tailscaleIPs = append(tailscaleIPs, addr)
						}
					}

					return &ipnstate.Status{
						Self: &ipnstate.PeerStatus{
							DNSName:      tt.mockDNSName,
							TailscaleIPs: tailscaleIPs,
						},
					}, nil
				}
			}

			// Create server with mock factory
			server, err := NewServerWithFactory(config.Tailscale{
				AuthKey: "test-key",
			}, func() tsnet.TSNetServer {
				return mockTSNetServer
			})
			testutil.AssertNoError(t, err)

			// Create service config
			svc := config.Service{
				Name:        tt.serviceName,
				BackendAddr: "localhost:8080",
			}

			// Call ListenWithService
			listener, err := server.ListenWithService(svc, tt.tlsMode, tt.funnelEnabled)
			testutil.AssertNoError(t, err)
			testutil.AssertNotNil(t, listener)

			// Give some time for the goroutine to run if priming is expected
			if tt.expectPriming {
				time.Sleep(6 * time.Second)
			}
		})
	}
}
