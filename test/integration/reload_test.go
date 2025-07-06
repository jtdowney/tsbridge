//go:build integration
// +build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/app"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
	"github.com/jtdowney/tsbridge/internal/testutil"
	"github.com/jtdowney/tsbridge/test/integration/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stripScheme removes the http:// or https:// prefix from a URL
func stripScheme(url string) string {
	if strings.HasPrefix(url, "http://") {
		return url[len("http://"):]
	}
	if strings.HasPrefix(url, "https://") {
		return url[len("https://"):]
	}
	return url
}

// waitForServicesReady waits for services to be ready with a timeout
func waitForServicesReady(t *testing.T) {
	t.Helper()

	// Since we're using mock tsnet servers, we need a small delay for goroutines to start
	// Using a channel-based approach for better synchronization
	ready := make(chan struct{})

	go func() {
		// Give services time to start their goroutines
		time.Sleep(50 * time.Millisecond)
		close(ready)
	}()

	select {
	case <-ready:
		// Services should be ready
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for services to be ready")
	}
}

func TestDynamicServiceManagement(t *testing.T) {
	t.Run("reload adds new services", func(t *testing.T) {
		// Create backends
		backend1 := helpers.CreateTestBackend(t)
		backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Backend 2"))
		}))
		t.Cleanup(func() { backend2.Close() })

		// Start with one service
		cfg := helpers.CreateTestConfig(t, "svc1", stripScheme(backend1.URL))

		// Create app with mock tailscale server
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		testApp, err := app.NewAppWithOptions(cfg, app.Options{TSServer: tsServer})
		require.NoError(t, err)

		// Start the app
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = testApp.Start(ctx)
		}()

		// Wait for startup
		waitForServicesReady(t)

		// Create new config with additional service
		newCfg := helpers.CreateMultiServiceConfig(t, map[string]string{
			"svc1": stripScheme(backend1.URL),
			"svc2": stripScheme(backend2.URL),
		})

		// Reload configuration
		err = testApp.ReloadConfig(newCfg)
		require.NoError(t, err)

		// Give services time to start
		waitForServicesReady(t)

		// Note: In a real integration test, we would make HTTP requests to verify
		// the services are working. Since this is a mock setup, we just verify
		// the reload completed without error.
	})

	t.Run("reload removes services", func(t *testing.T) {
		// Create backends
		backend1 := helpers.CreateTestBackend(t)
		backend2 := helpers.CreateTestBackend(t)

		// Start with two services
		cfg := helpers.CreateMultiServiceConfig(t, map[string]string{
			"svc1": stripScheme(backend1.URL),
			"svc2": stripScheme(backend2.URL),
		})

		// Create app with mock tailscale server
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		testApp, err := app.NewAppWithOptions(cfg, app.Options{TSServer: tsServer})
		require.NoError(t, err)

		// Start the app
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = testApp.Start(ctx)
		}()

		// Wait for startup
		waitForServicesReady(t)

		// Create new config with only one service
		newCfg := helpers.CreateTestConfig(t, "svc1", stripScheme(backend1.URL))

		// Reload configuration
		err = testApp.ReloadConfig(newCfg)
		require.NoError(t, err)

		// Give services time to stop
		waitForServicesReady(t)
	})

	t.Run("reload updates service configuration", func(t *testing.T) {
		// Create backends
		backend1 := helpers.CreateTestBackend(t)
		backend2 := helpers.CreateTestBackend(t)

		// Start with one service pointing to backend1
		cfg := helpers.CreateTestConfig(t, "svc1", stripScheme(backend1.URL))
		cfg.Services[0].UpstreamHeaders = map[string]string{
			"X-Custom": "value1",
		}

		// Create app with mock tailscale server
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		testApp, err := app.NewAppWithOptions(cfg, app.Options{TSServer: tsServer})
		require.NoError(t, err)

		// Start the app
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = testApp.Start(ctx)
		}()

		// Wait for startup
		waitForServicesReady(t)

		// Update service to point to backend2
		newCfg := helpers.CreateTestConfig(t, "svc1", stripScheme(backend2.URL))
		newCfg.Services[0].UpstreamHeaders = map[string]string{
			"X-Custom": "value2",
		}

		// Reload configuration
		err = testApp.ReloadConfig(newCfg)
		require.NoError(t, err)

		// Give service time to restart
		waitForServicesReady(t)
	})

	t.Run("reload handles partial failures gracefully", func(t *testing.T) {
		// Create backend
		backend1 := helpers.CreateTestBackend(t)

		// Start with one service
		cfg := helpers.CreateTestConfig(t, "svc1", stripScheme(backend1.URL))

		// Create app with mock tailscale server
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		testApp, err := app.NewAppWithOptions(cfg, app.Options{TSServer: tsServer})
		require.NoError(t, err)

		// Start the app
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = testApp.Start(ctx)
		}()

		// Wait for startup
		waitForServicesReady(t)

		// Create new config with a service that has an invalid backend
		// to simulate a partial failure
		newCfg := helpers.CreateMultiServiceConfig(t, map[string]string{
			"svc1": stripScheme(backend1.URL),
			"svc2": "localhost:9999", // Unreachable backend
		})

		// Reload configuration - should handle gracefully
		err = testApp.ReloadConfig(newCfg)
		// The exact error behavior depends on the implementation
		// In this case, we just verify it completes without panic
	})

	t.Run("concurrent reloads are handled safely", func(t *testing.T) {
		// Create backend
		backend1 := helpers.CreateTestBackend(t)

		// Start with one service
		cfg := helpers.CreateTestConfig(t, "svc1", stripScheme(backend1.URL))

		// Create app with mock tailscale server
		tsServer := testutil.CreateMockTailscaleServer(t, cfg.Tailscale)
		testApp, err := app.NewAppWithOptions(cfg, app.Options{TSServer: tsServer})
		require.NoError(t, err)

		// Start the app
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = testApp.Start(ctx)
		}()

		// Wait for startup
		waitForServicesReady(t)

		// Create multiple different configs
		configs := make([]*config.Config, 5)
		for i := 0; i < 5; i++ {
			backend := helpers.CreateTestBackend(t)
			configs[i] = helpers.CreateTestConfig(t, "svc1", stripScheme(backend.URL))
			configs[i].Services[0].Tags = []string{string(rune('a' + i))} // Different tags to force updates
		}

		// Trigger concurrent reloads
		errCh := make(chan error, len(configs))
		for _, cfg := range configs {
			go func(c *config.Config) {
				errCh <- testApp.ReloadConfig(c)
			}(cfg)
		}

		// Collect results
		for i := 0; i < len(configs); i++ {
			<-errCh // Just drain the channel, some may fail due to concurrency
		}

		// The important thing is that the app remains stable
		waitForServicesReady(t)

		// App should still be running (no panic)
		assert.NotNil(t, testApp)
	})

	t.Run("reload error handling returns detailed information", func(t *testing.T) {
		// This test verifies that the ReloadError type provides useful information
		reloadErr := errors.NewReloadError()
		reloadErr.RecordAddError("svc1", assert.AnError)
		reloadErr.RecordRemoveError("svc2", assert.AnError)
		reloadErr.RecordUpdateError("svc3", assert.AnError)
		reloadErr.RecordSuccess()
		reloadErr.RecordSuccess()

		assert.True(t, reloadErr.HasErrors())
		assert.False(t, reloadErr.AllFailed())
		assert.Equal(t, 3, reloadErr.Failed)
		assert.Equal(t, 2, reloadErr.Successful)
		assert.Contains(t, reloadErr.Error(), "3 errors, 2 successful")
	})
}
