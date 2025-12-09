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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"

	"github.com/jtdowney/tsbridge/internal/config"
	tserrors "github.com/jtdowney/tsbridge/internal/errors"
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
				OAuthClientSecret: config.RedactedString("test-client-secret"),
			},
			wantErr: false,
		},
		{
			name: "valid config with auth key",
			cfg: config.Tailscale{
				AuthKey: config.RedactedString("test-auth-key"),
			},
			wantErr: false,
		},
		{
			name: "valid config with auth key from env",
			cfg: config.Tailscale{
				AuthKey: config.RedactedString("$TS_AUTHKEY"),
			},
			wantErr: false,
		},
		{
			name:    "missing auth configuration is now allowed",
			cfg:     config.Tailscale{},
			wantErr: false,
		},
		{
			name: "incomplete OAuth - missing secret is still allowed",
			cfg: config.Tailscale{
				OAuthClientID: "test-client-id",
			},
			wantErr: true,
		},
		{
			name: "incomplete OAuth - missing ID is still allowed",
			cfg: config.Tailscale{
				OAuthClientSecret: config.RedactedString("test-client-secret"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "valid config with auth key from env" {
				t.Setenv("TS_AUTHKEY", "test-auth-key")
			}

			// Use a mock factory for testing
			factory := func(serviceName string) tsnet.TSNetServer {
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
			factory := func(serviceName string) tsnet.TSNetServer {
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

func TestListen_EphemeralServices(t *testing.T) {
	tests := []struct {
		name             string
		svc              config.Service
		existingState    bool
		expectAuthKeySet bool
		setupOAuth       bool
	}{
		{
			name: "ephemeral service with existing state should generate auth key",
			svc: config.Service{
				Name:        "test-ephemeral",
				BackendAddr: "localhost:8080",
				Ephemeral:   true,
				Tags:        []string{"tag:test"},
			},
			existingState:    true,
			expectAuthKeySet: true,
			setupOAuth:       true,
		},
		{
			name: "non-ephemeral service with existing state should NOT generate auth key",
			svc: config.Service{
				Name:        "test-persistent",
				BackendAddr: "localhost:8080",
				Ephemeral:   false,
			},
			existingState:    true,
			expectAuthKeySet: false,
			setupOAuth:       false,
		},
		{
			name: "ephemeral service without state should generate auth key",
			svc: config.Service{
				Name:        "test-ephemeral-new",
				BackendAddr: "localhost:8080",
				Ephemeral:   true,
				Tags:        []string{"tag:test"},
			},
			existingState:    false,
			expectAuthKeySet: true,
			setupOAuth:       true,
		},
		{
			name: "non-ephemeral service without state should generate auth key",
			svc: config.Service{
				Name:        "test-persistent-new",
				BackendAddr: "localhost:8080",
				Ephemeral:   false,
				Tags:        []string{"tag:test"},
			},
			existingState:    false,
			expectAuthKeySet: true,
			setupOAuth:       true,
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
			mockServer.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				return &mockListener{addr: addr}, nil
			}

			// Create a mock LocalClient for certificate priming
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
			factory := func(serviceName string) tsnet.TSNetServer {
				return mockServer
			}

			var cfg config.Tailscale
			if tt.setupOAuth {
				// Setup OAuth mock server
				oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v2/oauth/token":
						response := map[string]interface{}{
							"access_token": "test-access-token",
							"token_type":   "Bearer",
							"expires_in":   3600,
						}
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(response)
					case "/api/v2/tailnet/-/keys":
						response := map[string]interface{}{
							"key":     "tskey-auth-test123",
							"created": time.Now(),
						}
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(response)
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer oauthServer.Close()

				// Set test endpoint
				t.Setenv("TSBRIDGE_OAUTH_ENDPOINT", oauthServer.URL)

				cfg = config.Tailscale{
					OAuthClientID:     "test-client-id",
					OAuthClientSecret: config.RedactedString("test-client-secret"),
					StateDir:          tempDir,
				}
			} else {
				cfg = config.Tailscale{
					AuthKey:  config.RedactedString("test-auth-key"),
					StateDir: tempDir,
				}
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
			_, err = server.Listen(tt.svc, "auto", false)
			require.NoError(t, err)

			// Verify auth key was set or not based on expectation
			if tt.expectAuthKeySet {
				assert.NotEmpty(t, mockServer.AuthKey, "expected auth key to be set")
			} else {
				assert.Empty(t, mockServer.AuthKey, "expected auth key NOT to be set")
			}

			// Verify ephemeral flag is always set correctly
			assert.Equal(t, tt.svc.Ephemeral, mockServer.Ephemeral)
		})
	}
}

func TestPrepareServiceAuth(t *testing.T) {
	// Create a mock OAuth server for tests that need OAuth
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			token := map[string]interface{}{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			_ = json.NewEncoder(w).Encode(token)
		case "/api/v2/tailnet/-/keys":
			w.Header().Set("Content-Type", "application/json")
			key := map[string]interface{}{
				"id":          "key123",
				"key":         "tskey-test-generated",
				"created":     "2023-01-01T00:00:00Z",
				"expires":     "2023-01-02T00:00:00Z",
				"description": "generated for test-service",
			}
			_ = json.NewEncoder(w).Encode(key)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer oauthServer.Close()

	// Set test OAuth endpoint
	t.Setenv("TSBRIDGE_OAUTH_ENDPOINT", oauthServer.URL)

	tests := []struct {
		name            string
		cfg             config.Tailscale
		svc             config.Service
		setupStateDir   bool
		wantErr         bool
		wantErrContains string
	}{
		{
			name: "ephemeral service with OAuth credentials",
			cfg: config.Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: config.RedactedString("test-secret"),
			},
			svc: config.Service{
				Name:      "test-service",
				Ephemeral: true,
				Tags:      []string{"tag:test"},
			},
			wantErr: false,
		},
		{
			name: "ephemeral service without credentials",
			cfg:  config.Tailscale{},
			svc: config.Service{
				Name:      "test-service",
				Ephemeral: true,
			},
			wantErr:         true,
			wantErrContains: "needs authentication",
		},
		{
			name: "non-ephemeral service with existing state",
			cfg:  config.Tailscale{},
			svc: config.Service{
				Name: "test-service",
			},
			setupStateDir: true,
			wantErr:       false,
		},
		{
			name: "non-ephemeral service without existing state and without credentials",
			cfg:  config.Tailscale{},
			svc: config.Service{
				Name: "test-service",
			},
			setupStateDir:   false,
			wantErr:         true,
			wantErrContains: "needs authentication",
		},
		{
			name: "non-ephemeral service without existing state but with OAuth",
			cfg: config.Tailscale{
				OAuthClientID:     "test-id",
				OAuthClientSecret: config.RedactedString("test-secret"),
			},
			svc: config.Service{
				Name: "test-service",
				Tags: []string{"tag:test"},
			},
			setupStateDir: false,
			wantErr:       false,
		},
		{
			name: "non-ephemeral service without existing state but with authkey",
			cfg: config.Tailscale{
				AuthKey: config.RedactedString("tskey-auth-123"),
			},
			svc: config.Service{
				Name: "test-service",
			},
			setupStateDir: false,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary directory for state
			tmpDir := t.TempDir()

			// If test requires existing state, create it
			if tt.setupStateDir {
				serviceStateDir := filepath.Join(tmpDir, tt.svc.Name)
				err := os.MkdirAll(serviceStateDir, 0700)
				require.NoError(t, err)

				// Create a mock state file
				stateFile := filepath.Join(serviceStateDir, "tailscaled.state")
				err = os.WriteFile(stateFile, []byte("mock state"), 0600)
				require.NoError(t, err)
			}

			// Create mock TSNetServer
			mockServer := tsnet.NewMockTSNetServer()

			// Create server with mock factory
			factory := func(serviceName string) tsnet.TSNetServer {
				return mockServer
			}
			server, err := NewServerWithFactory(tt.cfg, factory)
			require.NoError(t, err)

			// Test prepareServiceAuth
			err = server.prepareServiceAuth(mockServer, tt.svc, tmpDir)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
			} else {
				assert.NoError(t, err)
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
	factory := func(serviceName string) tsnet.TSNetServer {
		return tsnet.NewMockTSNetServer()
	}

	cfg := config.Tailscale{
		AuthKey: config.RedactedString("test-key"),
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
	factory := func(serviceName string) tsnet.TSNetServer {
		return tsnet.NewMockTSNetServer()
	}

	cfg := config.Tailscale{
		AuthKey: config.RedactedString("test-key"),
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
				AuthKey: config.RedactedString("test-key"),
			},
			wantErr: false,
		},
		{
			name: "OAuth credentials present",
			cfg: config.Tailscale{
				OAuthClientID:     "client-id",
				OAuthClientSecret: config.RedactedString("client-secret"),
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
				OAuthClientSecret: config.RedactedString("client-secret"),
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

func TestStateDirResolution(t *testing.T) {
	tests := []struct {
		name           string
		configStateDir string
		stateDirEnv    string
		envVars        map[string]string
		expectedDir    string
		serviceName    string
	}{
		{
			name:           "config state_dir takes highest priority",
			configStateDir: "/custom/config/dir",
			stateDirEnv:    "STATE_DIR_ENV_VAR",
			envVars: map[string]string{
				"STATE_DIR_ENV_VAR":  "/env/specified/dir",
				"STATE_DIRECTORY":    "/systemd/state",
				"TSBRIDGE_STATE_DIR": "/tsbridge/state",
			},
			expectedDir: "/custom/config/dir/test-service",
			serviceName: "test-service",
		},
		{
			name:           "state_dir with resolved env takes priority over STATE_DIRECTORY",
			configStateDir: "/custom/env/state", // This simulates state_dir_env being resolved during config loading
			envVars: map[string]string{
				"STATE_DIRECTORY":    "/systemd/state",
				"TSBRIDGE_STATE_DIR": "/tsbridge/state",
			},
			expectedDir: "/custom/env/state/test-service",
			serviceName: "test-service",
		},
		{
			name: "STATE_DIRECTORY is used when no config or state_dir_env",
			envVars: map[string]string{
				"STATE_DIRECTORY":    "/var/lib/tsbridge",
				"TSBRIDGE_STATE_DIR": "/tsbridge/state",
			},
			expectedDir: "/var/lib/tsbridge/test-service",
			serviceName: "test-service",
		},
		{
			name: "TSBRIDGE_STATE_DIR is used when STATE_DIRECTORY is not set",
			envVars: map[string]string{
				"TSBRIDGE_STATE_DIR": "/tsbridge/custom/state",
			},
			expectedDir: "/tsbridge/custom/state/test-service",
			serviceName: "test-service",
		},
		{
			name:        "XDG default is used when no env vars are set",
			expectedDir: getDefaultStateDir() + "/test-service",
			serviceName: "test-service",
		},
		{
			name: "STATE_DIRECTORY with multiple paths uses first one",
			envVars: map[string]string{
				"STATE_DIRECTORY": "/var/lib/tsbridge:/var/lib/tsbridge2",
			},
			expectedDir: "/var/lib/tsbridge/test-service",
			serviceName: "test-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all environment variables first
			t.Setenv("STATE_DIRECTORY", "")
			t.Setenv("TSBRIDGE_STATE_DIR", "")

			// Set test environment variables
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			// Create mock TSNet server
			mockServer := tsnet.NewMockTSNetServer()
			mockServer.StartFunc = func() error { return nil }
			mockServer.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				return &mockListener{addr: addr}, nil
			}

			// Create mock LocalClient for certificate priming
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
			factory := func(serviceName string) tsnet.TSNetServer {
				return mockServer
			}

			cfg := config.Tailscale{
				AuthKey:     config.RedactedString("test-key"),
				StateDir:    tt.configStateDir,
				StateDirEnv: tt.stateDirEnv,
			}

			server, err := NewServerWithFactory(cfg, factory)
			require.NoError(t, err)

			// Create service config
			svc := config.Service{
				Name:        tt.serviceName,
				BackendAddr: "localhost:8080",
			}

			// Call Listen to trigger state directory resolution
			_, err = server.Listen(svc, "auto", false)
			require.NoError(t, err)

			// Verify the correct directory was set
			assert.Equal(t, tt.expectedDir, mockServer.Dir)
		})
	}
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
					AuthKey: config.RedactedString("global-auth-key"),
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
					OAuthClientSecret: config.RedactedString("test-secret"),
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
					OAuthClientSecret: config.RedactedString("test-secret"),
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
					OAuthClientSecret: config.RedactedString("test-secret"),
				},
			},
			svc: config.Service{
				Name:      "test-service",
				Ephemeral: true,
			},
			wantErr: false,
		},
		{
			name: "OAuth generates key successfully",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientID:     "test-client",
					OAuthClientSecret: config.RedactedString("test-secret"),
				},
			},
			svc: config.Service{
				Name: "test-service",
				Tags: []string{"tag:test"},
			},
			wantErr: false,
		},
		{
			name: "missing OAuth client ID",
			cfg: config.Config{
				Tailscale: config.Tailscale{
					OAuthClientSecret: config.RedactedString("test-secret"),
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
				if tt.cfg.Tailscale.AuthKey.Value() != "" {
					assert.Equal(t, tt.cfg.Tailscale.AuthKey.Value(), result)
				}
			}
		})
	}
}

// TestGenerateOrResolveAuthKeyOAuthFailure tests OAuth API failure handling
func TestGenerateOrResolveAuthKeyOAuthFailure(t *testing.T) {
	// Create a test server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/oauth/token":
			// Successfully return OAuth token
			w.Header().Set("Content-Type", "application/json")
			token := map[string]interface{}{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			_ = json.NewEncoder(w).Encode(token)
		case "/api/v2/tailnet/-/keys":
			// Return API error for key generation
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": "internal server error",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Set test OAuth endpoint
	t.Setenv("TSBRIDGE_OAUTH_ENDPOINT", server.URL)

	cfg := config.Config{
		Tailscale: config.Tailscale{
			OAuthClientID:     "test-client",
			OAuthClientSecret: config.RedactedString("test-secret"),
		},
	}
	svc := config.Service{
		Name: "test-service",
		Tags: []string{"tag:test"},
	}

	// OAuth failures should return an error
	result, err := generateOrResolveAuthKey(cfg, svc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "internal server error")
	assert.Empty(t, result, "should return empty key on OAuth failure")
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
					AuthKey: config.RedactedString("test-key"),
				},
				wantErr: false,
			},
			{
				name: "OAuth credentials provided",
				cfg: config.Tailscale{
					OAuthClientID:     "client-id",
					OAuthClientSecret: config.RedactedString("client-secret"),
				},
				wantErr: false,
			},
			{
				name:    "no credentials provided is now allowed",
				cfg:     config.Tailscale{},
				wantErr: false,
			},
			{
				name: "incomplete OAuth credentials is now allowed",
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

				factory := func(serviceName string) tsnet.TSNetServer {
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
	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey: config.RedactedString("test-key"),
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	// Call primeCertificate in a goroutine (like it would be in real usage)
	done := make(chan bool)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		server.primeCertificate(ctx, mockServer, "test-service")
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
			factory := func(serviceName string) tsnet.TSNetServer {
				return mockServer
			}

			cfg := config.Tailscale{
				AuthKey: config.RedactedString("test-key"),
			}

			server, err := NewServerWithFactory(cfg, factory)
			require.NoError(t, err)

			// Call primeCertificate in a goroutine
			done := make(chan bool)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				server.primeCertificate(ctx, mockServer, "test-service")
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

// TestAsyncCertificatePriming tests that Listen returns immediately without waiting for certificate priming
func TestAsyncCertificatePriming(t *testing.T) {
	// Create a mock TSNet server that will simulate slow priming
	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		return nil
	}

	mockServer.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
		return &mockListener{addr: addr}, nil
	}

	// Create a slow LocalClient to simulate slow priming
	primingStarted := make(chan bool)
	mockLocalClient := &tsnet.MockLocalClient{
		StatusWithoutPeersFunc: func(ctx context.Context) (*ipnstate.Status, error) {
			// Signal that priming has started
			select {
			case primingStarted <- true:
			default:
			}
			// Simulate slow priming by sleeping
			time.Sleep(2 * time.Second)
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
	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey: config.RedactedString("test-key"),
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	// Create service config
	svc := config.Service{
		Name:        "test-service",
		BackendAddr: "localhost:8080",
	}

	// Call Listen and measure time
	start := time.Now()
	listener, err := server.Listen(svc, "auto", false)
	elapsed := time.Since(start)

	// Verify Listen returned immediately (within 100ms)
	assert.NoError(t, err)
	assert.NotNil(t, listener)
	assert.Less(t, elapsed, 100*time.Millisecond, "Listen should return immediately")

	// Wait for priming to start (but not complete)
	select {
	case <-primingStarted:
		// Priming started in background
	case <-time.After(6 * time.Second):
		t.Fatal("Certificate priming should have started")
	}
}

// TestPrimeCertificateTimeout tests that certificate priming respects context timeout
func TestPrimeCertificateTimeout(t *testing.T) {
	// Create a mock TSNet server that will timeout
	mockServer := tsnet.NewMockTSNetServer()

	timeoutReached := make(chan bool, 1)
	mockLocalClient := &tsnet.MockLocalClient{
		StatusWithoutPeersFunc: func(ctx context.Context) (*ipnstate.Status, error) {
			// Check if context timeout works
			select {
			case <-ctx.Done():
				timeoutReached <- true
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
				// Should not reach here if timeout is working
				return &ipnstate.Status{
					Self: &ipnstate.PeerStatus{
						DNSName:      "test.tailnet.ts.net.",
						TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
					},
				}, nil
			}
		},
	}

	mockServer.LocalClientFunc = func() (tsnet.LocalClient, error) {
		return mockLocalClient, nil
	}

	// Create server with mock factory
	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey: config.RedactedString("test-key"),
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	// Use a timeout that's longer than the initial 5s wait but still reasonable for testing
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	err = server.primeCertificate(ctx, mockServer, "test-service")
	elapsed := time.Since(start)

	// Should timeout during initial wait and return error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled during initial wait")
	assert.Less(t, elapsed, 4*time.Second, "Should timeout within 4 seconds")
	assert.Greater(t, elapsed, 2*time.Second, "Should timeout after at least 3 seconds")

	// The timeout should occur during the initial wait, so StatusWithoutPeers shouldn't be called
	select {
	case <-timeoutReached:
		t.Fatal("StatusWithoutPeers should not have been called due to early timeout")
	default:
		// Expected - timeout during initial wait
	}
}

// TestListenWithControlURL tests that control URL is set on the TSNet server
func TestListenWithControlURL(t *testing.T) {
	// Track whether SetControlURL was called with the expected value
	controlURLSet := false
	expectedControlURL := "https://headscale.example.com"

	// Create factory that verifies control URL is set
	factory := func(serviceName string) tsnet.TSNetServer {
		return &mockTSNetServerWithControlURL{
			MockTSNetServer: tsnet.NewMockTSNetServer(),
			onSetControlURL: func(url string) {
				if url == expectedControlURL {
					controlURLSet = true
				}
			},
		}
	}

	// Create state directory with existing state to avoid auth key generation
	stateDir := t.TempDir()
	serviceStateDir := filepath.Join(stateDir, "test-service")
	require.NoError(t, os.MkdirAll(serviceStateDir, 0755))
	// Create a dummy state file
	stateFile := filepath.Join(serviceStateDir, "tailscaled.state")
	require.NoError(t, os.WriteFile(stateFile, []byte("dummy state"), 0600))

	cfg := config.Tailscale{
		OAuthClientID:     "test-client-id",
		OAuthClientSecret: config.RedactedString("test-client-secret"),
		ControlURL:        expectedControlURL,
		StateDir:          stateDir,
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)
	defer server.Close()

	// Create a service
	svc := config.Service{
		Name:        "test-service",
		BackendAddr: "localhost:8080",
		Tags:        []string{"tag:test"},
	}

	// Call Listen
	_, err = server.Listen(svc, "off", false)
	require.NoError(t, err)

	// Verify control URL was set
	assert.True(t, controlURLSet, "SetControlURL was not called with the expected URL")
}

// mockTSNetServerWithControlURL wraps MockTSNetServer to capture SetControlURL calls
type mockTSNetServerWithControlURL struct {
	*tsnet.MockTSNetServer
	onSetControlURL func(string)
}

func (m *mockTSNetServerWithControlURL) SetControlURL(url string) {
	if m.onSetControlURL != nil {
		m.onSetControlURL(url)
	}
}

func (m *mockTSNetServerWithControlURL) Start() error {
	if m.StartFunc != nil {
		return m.StartFunc()
	}
	return nil
}

func (m *mockTSNetServerWithControlURL) Listen(network, addr string) (net.Listener, error) {
	if m.ListenFunc != nil {
		return m.ListenFunc(network, addr)
	}
	return &mockListener{addr: addr}, nil
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
			factory := func(serviceName string) tsnet.TSNetServer {
				return mockServer
			}

			cfg := config.Tailscale{
				AuthKey: config.RedactedString("test-key"),
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
			factory := func(serviceName string) tsnet.TSNetServer {
				return tsnet.NewMockTSNetServer()
			}

			cfg := config.Tailscale{
				AuthKey: config.RedactedString("test-key"),
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

func TestDetermineListenAddr(t *testing.T) {
	tests := []struct {
		name         string
		service      config.Service
		tlsMode      string
		expectedPort string
	}{
		{
			name: "TLS auto with no custom port uses default 443",
			service: config.Service{
				Name: "test",
			},
			tlsMode:      "auto",
			expectedPort: ":443",
		},
		{
			name: "TLS off with no custom port uses default 80",
			service: config.Service{
				Name: "test",
			},
			tlsMode:      "off",
			expectedPort: ":80",
		},
		{
			name: "Empty TLS mode defaults to auto behavior",
			service: config.Service{
				Name: "test",
			},
			tlsMode:      "",
			expectedPort: ":443",
		},
		{
			name: "ListenAddr with just port",
			service: config.Service{
				Name:       "test",
				ListenAddr: ":7070",
			},
			tlsMode:      "auto",
			expectedPort: ":7070",
		},
		{
			name: "ListenAddr with full address",
			service: config.Service{
				Name:       "test",
				ListenAddr: "0.0.0.0:8888",
			},
			tlsMode:      "off",
			expectedPort: "0.0.0.0:8888",
		},
		{
			name: "ListenAddr with IPv6 address",
			service: config.Service{
				Name:       "test",
				ListenAddr: "[::1]:9090",
			},
			tlsMode:      "auto",
			expectedPort: "[::1]:9090",
		},
	}

	// Create a server instance to test the method
	factory := func(serviceName string) tsnet.TSNetServer {
		return &tsnet.MockTSNetServer{}
	}
	cfg := config.Tailscale{
		OAuthClientID:     "test-client",
		OAuthClientSecret: config.RedactedString("test-secret"),
	}
	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.determineListenAddr(tt.service, tt.tlsMode)
			assert.Equal(t, tt.expectedPort, result)
		})
	}
}

func TestListen_CleansUpServerOnStartFailure(t *testing.T) {
	tempDir := t.TempDir()

	closeCalled := false

	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		return errors.New("simulated start failure")
	}
	mockServer.CloseFunc = func() error {
		closeCalled = true
		return nil
	}

	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey:  "test-key",
		StateDir: tempDir,
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	svc := config.Service{
		Name:        "test-service",
		BackendAddr: "localhost:8080",
	}

	_, err = server.Listen(svc, "off", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting tsnet server")

	assert.Nil(t, server.GetServiceServer("test-service"), "server should be removed from map after start failure")

	assert.True(t, closeCalled, "Close() should be called when Start() fails")
}

func TestListen_CleansUpServerOnListenerFailure(t *testing.T) {
	tempDir := t.TempDir()

	closeCalled := false
	startCalled := false

	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		startCalled = true
		return nil
	}
	mockServer.ListenFunc = func(network, addr string) (net.Listener, error) {
		return nil, errors.New("simulated listener failure")
	}
	mockServer.CloseFunc = func() error {
		closeCalled = true
		return nil
	}

	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey:  "test-key",
		StateDir: tempDir,
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	svc := config.Service{
		Name:        "test-service",
		BackendAddr: "localhost:8080",
	}

	_, err = server.Listen(svc, "off", false)

	require.Error(t, err)

	assert.True(t, startCalled, "Start() should be called before listener creation")

	assert.Nil(t, server.GetServiceServer("test-service"), "server should be removed from map after listener failure")

	assert.True(t, closeCalled, "Close() should be called when listener creation fails")
}

func TestListen_CleansUpServerOnTLSListenerFailure(t *testing.T) {
	tempDir := t.TempDir()

	closeCalled := false

	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		return nil
	}
	mockServer.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
		return nil, errors.New("simulated TLS listener failure")
	}
	mockServer.CloseFunc = func() error {
		closeCalled = true
		return nil
	}

	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey:  "test-key",
		StateDir: tempDir,
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	svc := config.Service{
		Name:        "test-service",
		BackendAddr: "localhost:8080",
	}

	_, err = server.Listen(svc, "auto", false)

	require.Error(t, err)
	assert.Nil(t, server.GetServiceServer("test-service"), "server should be removed from map after TLS listener failure")
	assert.True(t, closeCalled, "Close() should be called when TLS listener creation fails")
}

func TestListen_CleansUpServerOnFunnelListenerFailure(t *testing.T) {
	tempDir := t.TempDir()

	closeCalled := false

	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		return nil
	}
	mockServer.ListenFunnelFunc = func(network, addr string) (net.Listener, error) {
		return nil, errors.New("simulated funnel listener failure")
	}
	mockServer.CloseFunc = func() error {
		closeCalled = true
		return nil
	}

	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey:  "test-key",
		StateDir: tempDir,
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	svc := config.Service{
		Name:        "test-service",
		BackendAddr: "localhost:8080",
	}

	_, err = server.Listen(svc, "auto", true)

	require.Error(t, err)
	assert.Nil(t, server.GetServiceServer("test-service"), "server should be removed from map after funnel listener failure")
	assert.True(t, closeCalled, "Close() should be called when funnel listener creation fails")
}

func TestStartServerWithTimeout_Success(t *testing.T) {
	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		return nil
	}

	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey: "test-key",
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	err = server.startServerWithTimeout(mockServer, "test-service", 5*time.Second)
	assert.NoError(t, err)
}

func TestStartServerWithTimeout_StartError(t *testing.T) {
	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		return errors.New("connection refused")
	}

	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey: "test-key",
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	err = server.startServerWithTimeout(mockServer, "test-service", 5*time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting tsnet server")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestStartServerWithTimeout_Timeout(t *testing.T) {
	startBlocking := make(chan struct{})
	closeCalled := false

	mockServer := tsnet.NewMockTSNetServer()
	mockServer.StartFunc = func() error {
		<-startBlocking
		return nil
	}
	mockServer.CloseFunc = func() error {
		closeCalled = true
		return nil
	}

	factory := func(serviceName string) tsnet.TSNetServer {
		return mockServer
	}

	cfg := config.Tailscale{
		AuthKey: "test-key",
	}

	server, err := NewServerWithFactory(cfg, factory)
	require.NoError(t, err)

	start := time.Now()
	err = server.startServerWithTimeout(mockServer, "test-service", 100*time.Millisecond)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.True(t, tserrors.IsTimeout(err), "expected timeout error")
	assert.Contains(t, err.Error(), "starting tsnet server")
	assert.Contains(t, err.Error(), "check network connectivity")

	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 200*time.Millisecond)

	// Give cleanup goroutine time to run
	time.Sleep(50 * time.Millisecond)
	assert.True(t, closeCalled, "Close() should be called on timeout")

	close(startBlocking)
}
