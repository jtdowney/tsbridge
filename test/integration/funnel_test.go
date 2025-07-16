package integration_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/service"
	"github.com/jtdowney/tsbridge/internal/tailscale"
	tsnetpkg "github.com/jtdowney/tsbridge/internal/tsnet"
	"github.com/jtdowney/tsbridge/test/integration/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// stripScheme removes the http:// or https:// prefix from a URL
func stripScheme(url string) string {
	if strings.HasPrefix(url, "http://") {
		return strings.TrimPrefix(url, "http://")
	}
	if strings.HasPrefix(url, "https://") {
		return strings.TrimPrefix(url, "https://")
	}
	return url
}

func TestFunnelIntegration(t *testing.T) {
	t.Run("service with funnel enabled uses ListenFunnel", func(t *testing.T) {
		// Create test backend
		backend := helpers.CreateTestBackend(t)
		defer backend.Close()

		// Track which listen method was called
		var listenFunnelCalled bool
		var listenTLSCalled bool
		var listenCalled bool

		// Create mock factory that tracks which listen method is called
		factory := func(serviceName string) tsnetpkg.TSNetServer {
			mock := tsnetpkg.NewMockTSNetServer()

			// Override listen functions to track calls
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				listenCalled = true
				return &mockListener{addr: addr}, nil
			}

			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				listenTLSCalled = true
				return &mockListener{addr: addr}, nil
			}

			mock.ListenFunnelFunc = func(network, addr string) (net.Listener, error) {
				listenFunnelCalled = true
				assert.Equal(t, "tcp", network)
				assert.Equal(t, ":443", addr)
				return &mockListener{addr: addr}, nil
			}

			return mock
		}

		// Create config with funnel enabled
		funnelTrue := true
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				AuthKey: "test-key",
			},
			Services: []config.Service{
				{
					Name:          "funnel-service",
					BackendAddr:   stripScheme(backend.URL),
					FunnelEnabled: &funnelTrue,
					TLSMode:       "auto", // Should be ignored when funnel is enabled
				},
			},
		}

		// Set defaults and normalize
		cfg.SetDefaults()
		cfg.Normalize()

		// Create tailscale server with mock factory
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		require.NoError(t, err)
		defer tsServer.Close()

		// Create and start service registry
		registry := service.NewRegistry(cfg, tsServer)
		err = registry.StartServices()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		defer registry.Shutdown(ctx)

		// Verify that ListenFunnel was called and others were not
		assert.True(t, listenFunnelCalled)
		assert.False(t, listenTLSCalled)
		assert.False(t, listenCalled)
	})

	t.Run("service without funnel uses TLS mode", func(t *testing.T) {
		// Create test backend
		backend := helpers.CreateTestBackend(t)
		defer backend.Close()

		// Track which listen method was called
		var listenFunnelCalled bool
		var listenTLSCalled bool
		var listenCalled bool

		// Create mock factory that tracks which listen method is called
		factory := func(serviceName string) tsnetpkg.TSNetServer {
			mock := tsnetpkg.NewMockTSNetServer()

			// Override listen functions to track calls
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				listenCalled = true
				return &mockListener{addr: addr}, nil
			}

			mock.ListenTLSFunc = func(network, addr string) (net.Listener, error) {
				listenTLSCalled = true
				assert.Equal(t, "tcp", network)
				assert.Equal(t, ":443", addr)
				return &mockListener{addr: addr}, nil
			}

			mock.ListenFunnelFunc = func(network, addr string) (net.Listener, error) {
				listenFunnelCalled = true
				return &mockListener{addr: addr}, nil
			}

			return mock
		}

		// Create config without funnel (nil)
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				AuthKey: "test-key",
			},
			Services: []config.Service{
				{
					Name:          "normal-service",
					BackendAddr:   stripScheme(backend.URL),
					FunnelEnabled: nil, // Not specified
					TLSMode:       "auto",
				},
			},
		}

		// Set defaults and normalize
		cfg.SetDefaults()
		cfg.Normalize()

		// Create tailscale server with mock factory
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		require.NoError(t, err)
		defer tsServer.Close()

		// Create and start service registry
		registry := service.NewRegistry(cfg, tsServer)
		err = registry.StartServices()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		defer registry.Shutdown(ctx)

		// Verify that ListenTLS was called for auto mode
		assert.False(t, listenFunnelCalled)
		assert.True(t, listenTLSCalled)
		assert.False(t, listenCalled)
	})

	t.Run("service with funnel disabled uses TLS mode", func(t *testing.T) {
		// Create test backend
		backend := helpers.CreateTestBackend(t)
		defer backend.Close()

		// Track which listen method was called
		var listenFunnelCalled bool
		var listenCalled bool

		// Create mock factory that tracks which listen method is called
		factory := func(serviceName string) tsnetpkg.TSNetServer {
			mock := tsnetpkg.NewMockTSNetServer()

			// Override listen functions to track calls
			mock.ListenFunc = func(network, addr string) (net.Listener, error) {
				listenCalled = true
				assert.Equal(t, "tcp", network)
				assert.Equal(t, ":80", addr)
				return &mockListener{addr: addr}, nil
			}

			mock.ListenFunnelFunc = func(network, addr string) (net.Listener, error) {
				listenFunnelCalled = true
				return &mockListener{addr: addr}, nil
			}

			return mock
		}

		// Create config with funnel explicitly disabled
		funnelFalse := false
		cfg := &config.Config{
			Tailscale: config.Tailscale{
				AuthKey: "test-key",
			},
			Services: []config.Service{
				{
					Name:          "no-funnel-service",
					BackendAddr:   stripScheme(backend.URL),
					FunnelEnabled: &funnelFalse,
					TLSMode:       "off", // Should use plain Listen
				},
			},
		}

		// Set defaults and normalize
		cfg.SetDefaults()
		cfg.Normalize()

		// Create tailscale server with mock factory
		tsServer, err := tailscale.NewServerWithFactory(cfg.Tailscale, factory)
		require.NoError(t, err)
		defer tsServer.Close()

		// Create and start service registry
		registry := service.NewRegistry(cfg, tsServer)
		err = registry.StartServices()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		defer registry.Shutdown(ctx)

		// Verify that Listen was called (not ListenFunnel)
		assert.False(t, listenFunnelCalled)
		assert.True(t, listenCalled)
	})
}
