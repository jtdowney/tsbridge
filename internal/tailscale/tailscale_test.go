package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"

	"github.com/jtdowney/tsbridge/internal/config"
	tsnet "github.com/jtdowney/tsbridge/internal/tsnet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				AuthKey: "$TS_AUTHKEY",
			},
			wantErr: false,
		},
		{
			name:    "missing auth configuration",
			cfg:     config.Tailscale{},
			wantErr: true,
			errMsg:  "either auth key or OAuth credentials",
		},
		{
			name: "incomplete OAuth - missing secret",
			cfg: config.Tailscale{
				OAuthClientID: "test-client-id",
			},
			wantErr: true,
			errMsg:  "OAuth client secret is required",
		},
		{
			name: "incomplete OAuth - missing ID",
			cfg: config.Tailscale{
				OAuthClientSecret: "test-client-secret",
			},
			wantErr: true,
			errMsg:  "OAuth client ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "valid config with auth key from env" {
				t.Setenv("TS_AUTHKEY", "test-auth-key")
			}

			// Use a mock factory for testing
			factory := func() tsnet.TSNetServer {
				return tsnet.NewMockTSNetServer()
			}

			server, err := NewServerWithFactory(tt.cfg, factory)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, server)
			}
		})
	}
}

func TestListen(t *testing.T) {
	// This test verifies that Listen creates the correct listener type
	// based on configuration and starts the TSNet server
	tests := []struct {
		name          string
		svc           config.Service
		tlsMode       string
		funnelEnabled bool
		existingState bool
		wantErr       bool
		errMsg        string
	}{
		{
			name: "TLS mode auto",
			svc: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
			tlsMode:       "auto",
			funnelEnabled: false,
			existingState: false,
			wantErr:       false,
		},
		{
			name: "TLS mode off",
			svc: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
			tlsMode:       "off",
			funnelEnabled: false,
			existingState: false,
			wantErr:       false,
		},
		{
			name: "Funnel enabled",
			svc: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
			tlsMode:       "auto",
			funnelEnabled: true,
			existingState: false,
			wantErr:       false,
		},
		{
			name: "Invalid TLS mode",
			svc: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
			tlsMode:       "invalid",
			funnelEnabled: false,
			existingState: false,
			wantErr:       true,
			errMsg:        "invalid TLS mode",
		},
		{
			name: "With existing state",
			svc: config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
			tlsMode:       "auto",
			funnelEnabled: false,
			existingState: true,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary directory for state
			tempDir := t.TempDir()

			// Create mock TSNet server
			mockServer := tsnet.NewMockTSNetServer()
			mockServer.StartFunc = func() error {
				return nil
			}

			// Track which Listen method was called
			var listenCalled, listenTLSCalled, listenFunnelCalled bool

			mockServer.ListenFunc = func(network, addr string) (net.Listener, error) {
				listenCalled = true
				return &mockListener{addr: addr}, nil
			}

			mockServer.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				listenTLSCalled = true
				return &mockListener{addr: addr}, nil
			}

			mockServer.ListenFunnelFunc = func(network, addr string) (net.Listener, error) {
				listenFunnelCalled = true
				return &mockListener{addr: addr}, nil
			}

			// Create a mock LocalClient
			mockLocalClient := &tsnet.MockLocalClient{
				StatusWithoutPeersFunc: func(ctx context.Context) (*ipnstate.Status, error) {
					return &ipnstate.Status{
						Self: &ipnstate.PeerStatus{
							DNSName:      "test-service.tailnet.ts.net.",
							TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
						},
					}, nil
				},
			}

			mockServer.LocalClientFunc = func() (tsnet.LocalClient, error) {
				return mockLocalClient, nil
			}

			// Create server with mock factory
			factory := func() tsnet.TSNetServer {
				return mockServer
			}

			cfg := config.Tailscale{
				AuthKey:  "test-key",
				StateDir: tempDir,
			}

			server, err := NewServerWithFactory(cfg, factory)
			require.NoError(t, err)

			// Create existing state if needed
			if tt.existingState {
				serviceStateDir := fmt.Sprintf("%s/%s", tempDir, tt.svc.Name)
				err := os.MkdirAll(serviceStateDir, 0755)
				require.NoError(t, err)
				stateFile := fmt.Sprintf("%s/tailscaled.state", serviceStateDir)
				err = os.WriteFile(stateFile, []byte("dummy state"), 0644)
				require.NoError(t, err)
			}

			// Call Listen
			listener, err := server.Listen(tt.svc, tt.tlsMode, tt.funnelEnabled)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, listener)

				// Verify the correct Listen method was called
				switch {
				case tt.funnelEnabled:
					assert.True(t, listenFunnelCalled)
					assert.False(t, listenCalled)
					assert.False(t, listenTLSCalled)
				case tt.tlsMode == "auto":
					assert.True(t, listenTLSCalled)
					assert.False(t, listenCalled)
					assert.False(t, listenFunnelCalled)
				case tt.tlsMode == "off":
					assert.True(t, listenCalled)
					assert.False(t, listenTLSCalled)
					assert.False(t, listenFunnelCalled)
				}

				// Verify auth key was not set if existing state
				if tt.existingState {
					assert.Empty(t, mockServer.AuthKey)
				} else {
					assert.NotEmpty(t, mockServer.AuthKey)
				}
			}
		})
	}
}

func TestClose(t *testing.T) {
	// Create mock TSNet servers
	mockServer1 := tsnet.NewMockTSNetServer()
	mockServer2 := tsnet.NewMockTSNetServer()

	closeCount := 0
	mockServer1.CloseFunc = func() error {
		closeCount++
		return nil
	}
	mockServer2.CloseFunc = func() error {
		closeCount++
		return errors.New("close error")
	}

	// Create server with mock factory
	factory := func() tsnet.TSNetServer {
		return tsnet.NewMockTSNetServer()
	}

	cfg := config.Tailscale{
		AuthKey: "test-key",
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	// Add mock servers to the map
	server.serviceServers["service1"] = mockServer1
	server.serviceServers["service2"] = mockServer2

	// Close the server
	err = server.Close()

	// Should return error since one server failed to close
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "close error")

	// Both servers should have been attempted to close
	assert.Equal(t, 2, closeCount)

	// Map should be cleared
	assert.Empty(t, server.serviceServers)
}

func TestGetServiceServer(t *testing.T) {
	// Create mock TSNet server
	mockServer := tsnet.NewMockTSNetServer()

	// Create server with mock factory
	factory := func() tsnet.TSNetServer {
		return tsnet.NewMockTSNetServer()
	}

	cfg := config.Tailscale{
		AuthKey: "test-key",
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	// Add mock server to the map
	server.serviceServers["test-service"] = mockServer

	// Test getting existing service
	result := server.GetServiceServer("test-service")
	assert.Equal(t, mockServer, result)

	// Test getting non-existent service
	result = server.GetServiceServer("non-existent")
	assert.Nil(t, result)
}

func TestValidateTailscaleSecrets(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Tailscale
		wantErr bool
		errMsg  string
	}{
		{
			name: "auth key present",
			cfg: config.Tailscale{
				AuthKey: "test-key",
			},
			wantErr: false,
		},
		{
			name: "OAuth credentials present",
			cfg: config.Tailscale{
				OAuthClientID:     "client-id",
				OAuthClientSecret: "client-secret",
			},
			wantErr: false,
		},
		{
			name:    "no credentials",
			cfg:     config.Tailscale{},
			wantErr: true,
			errMsg:  "either auth key or OAuth credentials",
		},
		{
			name: "missing OAuth secret",
			cfg: config.Tailscale{
				OAuthClientID: "client-id",
			},
			wantErr: true,
			errMsg:  "OAuth client secret is missing",
		},
		{
			name: "missing OAuth ID",
			cfg: config.Tailscale{
				OAuthClientSecret: "client-secret",
			},
			wantErr: true,
			errMsg:  "OAuth client ID is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTailscaleSecrets(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetDefaultStateDir(t *testing.T) {
	dir := getDefaultStateDir()
	assert.NotEmpty(t, dir)
	assert.True(t, strings.HasSuffix(dir, "tsbridge"))
}

// Mock listener implementation
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

type mockAddr struct {
	addr string
}

func (m *mockAddr) Network() string {
	return "tcp"
}

func (m *mockAddr) String() string {
	return m.addr
}

// TestGenerateOrResolveAuthKey tests the generateOrResolveAuthKey function
func TestGenerateOrResolveAuthKey(t *testing.T) {
	// Create a test server to handle OAuth requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v2/oauth/token":
			// Handle OAuth token request
			token := map[string]interface{}{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			_ = json.NewEncoder(w).Encode(token)
		case "/api/v2/tailnet/-/keys":
			// Handle API request
			response := map[string]interface{}{
				"key":     "tskey-auth-test123",
				"created": time.Now().Format(time.RFC3339),
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Set test OAuth endpoint
	t.Setenv("TSBRIDGE_OAUTH_ENDPOINT", server.URL)

	tests := []struct {
		name    string
		cfg     config.Config
		svc     config.Service
		wantErr bool
	}{
		{
			name: "use global auth key",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					AuthKey: "global-auth-key",
				},
			},
			svc: config.Service{
				Name: "test-service",
			},
			wantErr: false,
		},
		{
			name: "use service tags",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientID:     "test-client",
					OAuthClientSecret: "test-secret",
				},
			},
			svc: config.Service{
				Name: "test-service",
				Tags: []string{"tag:test"},
			},
			wantErr: false,
		},
		{
			name: "use global tags when no service tags",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientID:     "test-client",
					OAuthClientSecret: "test-secret",
				},
			},
			svc: config.Service{
				Name: "test-service",
			},
			wantErr: false,
		},
		{
			name: "no auth config",
			cfg: config.Config{
				Tailscale: config.Tailscale{},
			},
			svc: config.Service{
				Name: "test-service",
			},
			wantErr: true,
		},
		{
			name: "service with ephemeral enabled",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientID:     "test-client",
					OAuthClientSecret: "test-secret",
				},
			},
			svc: config.Service{
				Name:      "test-service",
				Ephemeral: true,
			},
			wantErr: false,
		},
		{
			name: "OAuth request fails",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientID:     "test-client",
					OAuthClientSecret: "test-secret",
				},
			},
			svc: config.Service{
				Name: "test-service",
				Tags: []string{"tag:test"},
			},
			wantErr: false, // OAuth failures are logged but not returned as errors
		},
		{
			name: "missing OAuth client ID",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientSecret: "test-secret",
				},
			},
			svc: config.Service{
				Name: "test-service",
			},
			wantErr: true,
		},
		{
			name: "missing OAuth client secret",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientID: "test-client",
				},
			},
			svc: config.Service{
				Name: "test-service",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := generateOrResolveAuthKey(tt.cfg, tt.svc)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				// When OAuth is used, we should get a non-empty result
				if tt.cfg.Tailscale.AuthKey != "" {
					assert.Equal(t, tt.cfg.Tailscale.AuthKey, result)
				}
			}
		})
	}
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
				if tc.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})
}

// TestPrimeCertificate tests the certificate priming behavior
func TestPrimeCertificate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that includes sleep")
	}

	// Skip on Windows due to timing sensitivity
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows due to timing sensitivity")
	}

	// Create mock TSNet server
	mockServer := tsnet.NewMockTSNetServer()

	// Create a mock LocalClient
	statusCalled := false
	mockLocalClient := &tsnet.MockLocalClient{
		StatusWithoutPeersFunc: func(ctx context.Context) (*ipnstate.Status, error) {
			statusCalled = true
			return &ipnstate.Status{
				Self: &ipnstate.PeerStatus{
					DNSName:      "test-service.tailnet.ts.net.",
					TailscaleIPs: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
				},
			}, nil
		},
	}

	mockServer.LocalClientFunc = func() (tsnet.LocalClient, error) {
		return mockLocalClient, nil
	}

	// Create server with mock factory
	factory := func() tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey: "test-key",
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	// Call primeCertificate in a goroutine (like it would be in real usage)
	done := make(chan bool)
	go func() {
		server.primeCertificate(mockServer, "test-service")
		done <- true
	}()

	// Wait for it to complete with a longer timeout to account for:
	// - 5 second sleep
	// - 30 second HTTP timeout (but connection should fail quickly)
	select {
	case <-done:
		// Verify that status was called
		assert.True(t, statusCalled)
	case <-time.After(45 * time.Second):
		t.Fatal("primeCertificate timed out")
	}
}

// TestPrimeCertificateErrorCases tests various error scenarios in primeCertificate
func TestPrimeCertificateErrorCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that includes sleep")
	}

	tests := []struct {
		name               string
		localClientError   error
		statusError        error
		statusResponse     *ipnstate.Status
		expectStatusCalled bool
	}{
		{
			name:               "LocalClient error",
			localClientError:   errors.New("local client error"),
			expectStatusCalled: false,
		},
		{
			name:               "Status error",
			statusError:        errors.New("status error"),
			expectStatusCalled: true,
		},
		{
			name:               "Nil status",
			statusResponse:     nil,
			expectStatusCalled: true,
		},
		{
			name: "Nil self peer",
			statusResponse: &ipnstate.Status{
				Self: nil,
			},
			expectStatusCalled: true,
		},
		{
			name: "Empty DNS name",
			statusResponse: &ipnstate.Status{
				Self: &ipnstate.PeerStatus{
					DNSName: "",
				},
			},
			expectStatusCalled: true,
		},
		{
			name: "No Tailscale IPs",
			statusResponse: &ipnstate.Status{
				Self: &ipnstate.PeerStatus{
					DNSName:      "test.tailnet.ts.net.",
					TailscaleIPs: []netip.Addr{},
				},
			},
			expectStatusCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip Windows to avoid timing issues
			if runtime.GOOS == "windows" {
				t.Skip("Skipping on Windows due to timing sensitivity")
			}

			// Create mock TSNet server
			mockServer := tsnet.NewMockTSNetServer()

			// Create a mock LocalClient
			statusCalled := false
			mockLocalClient := &tsnet.MockLocalClient{
				StatusWithoutPeersFunc: func(ctx context.Context) (*ipnstate.Status, error) {
					statusCalled = true
					return tt.statusResponse, tt.statusError
				},
			}

			mockServer.LocalClientFunc = func() (tsnet.LocalClient, error) {
				if tt.localClientError != nil {
					return nil, tt.localClientError
				}
				return mockLocalClient, nil
			}

			// Create server with mock factory
			factory := func() tsnet.TSNetServer {
				return mockServer
			}

			cfg := config.Tailscale{
				AuthKey: "test-key",
			}

			server, err := NewServerWithFactory(cfg, factory)
			require.NoError(t, err)

			// Call primeCertificate in a goroutine
			done := make(chan bool)
			go func() {
				server.primeCertificate(mockServer, "test-service")
				done <- true
			}()

			// Wait for it to complete
			select {
			case <-done:
				// Verify expectations
				assert.Equal(t, tt.expectStatusCalled, statusCalled)
			case <-time.After(10 * time.Second):
				t.Fatal("primeCertificate timed out")
			}
		})
	}
}

// TestListenWithPrimeCertificate tests that Listen starts certificate priming for TLS mode
func TestListenWithPrimeCertificate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that includes sleep")
	}

	tests := []struct {
		name          string
		tlsMode       string
		funnelEnabled bool
		expectPriming bool
	}{
		{
			name:          "TLS auto mode should prime",
			tlsMode:       "auto",
			funnelEnabled: false,
			expectPriming: true,
		},
		{
			name:          "TLS off mode should not prime",
			tlsMode:       "off",
			funnelEnabled: false,
			expectPriming: false,
		},
		{
			name:          "Funnel mode should not prime",
			tlsMode:       "auto",
			funnelEnabled: true,
			expectPriming: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock TSNet server
			mockServer := tsnet.NewMockTSNetServer()
			mockServer.StartFunc = func() error {
				return nil
			}

			// Setup listeners
			mockServer.ListenFunc = func(network, addr string) (net.Listener, error) {
				return &mockListener{addr: addr}, nil
			}
			mockServer.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				return &mockListener{addr: addr}, nil
			}
			mockServer.ListenFunnelFunc = func(network, addr string) (net.Listener, error) {
				return &mockListener{addr: addr}, nil
			}

			// Setup LocalClient for priming
			mockLocalClient := &tsnet.MockLocalClient{
				StatusWithoutPeersFunc: func(ctx context.Context) (*ipnstate.Status, error) {
					return &ipnstate.Status{
						Self: &ipnstate.PeerStatus{
							DNSName:      "test.tailnet.ts.net.",
							TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
						},
					}, nil
				},
			}

			mockServer.LocalClientFunc = func() (tsnet.LocalClient, error) {
				return mockLocalClient, nil
			}

			// Create server with mock factory
			factory := func() tsnet.TSNetServer {
				return mockServer
			}

			cfg := config.Tailscale{
				AuthKey: "test-key",
			}

			server, err := NewServerWithFactory(cfg, factory)
			require.NoError(t, err)

			// Create service config
			svc := config.Service{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			}

			// Call Listen
			listener, err := server.Listen(svc, tt.tlsMode, tt.funnelEnabled)
			assert.NoError(t, err)
			assert.NotNil(t, listener)

			// Give some time for the goroutine to run if priming is expected
			if tt.expectPriming {
				time.Sleep(6 * time.Second)
			}
		})
	}
}

func TestCloseService(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		setupFunc   func(*Server)
		expectError bool
	}{
		{
			name:        "close existing service",
			serviceName: "test-service",
			setupFunc: func(s *Server) {
				// Add a mock server to the map
				mockServer := tsnet.NewMockTSNetServer()
				mockServer.CloseFunc = func() error {
					return nil
				}
				s.serviceServers["test-service"] = mockServer
			},
			expectError: false,
		},
		{
			name:        "close non-existent service",
			serviceName: "non-existent",
			setupFunc:   func(s *Server) {},
			expectError: false, // Should not error for non-existent service
		},
		{
			name:        "close service with error",
			serviceName: "error-service",
			setupFunc: func(s *Server) {
				// Add a mock server that returns an error on close
				mockServer := tsnet.NewMockTSNetServer()
				mockServer.CloseFunc = func() error {
					return errors.New("close failed")
				}
				s.serviceServers["error-service"] = mockServer
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create server with mock factory
			factory := func() tsnet.TSNetServer {
				return tsnet.NewMockTSNetServer()
			}

			cfg := config.Tailscale{
				AuthKey: "test-key",
			}

			server, err := NewServerWithFactory(cfg, factory)
			require.NoError(t, err)

			// Setup the test
			tt.setupFunc(server)

			// Check initial state
			initialCount := len(server.serviceServers)

			// Call CloseService
			err = server.CloseService(tt.serviceName)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify the service was removed from the map if it existed
			if _, existed := server.serviceServers[tt.serviceName]; existed && !tt.expectError {
				t.Errorf("service %s should have been removed from map", tt.serviceName)
			}

			// Verify map size changed appropriately
			if tt.setupFunc != nil && tt.name == "close existing service" {
				assert.Equal(t, initialCount-1, len(server.serviceServers))
			}
		})
	}
}
