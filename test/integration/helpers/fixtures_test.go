package helpers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewTestFixture(t *testing.T) {
	fixture := NewTestFixture(t)
	cfg := fixture.Build()

	// Verify defaults
	assert.Equal(t, "tskey-auth-test123", cfg.Tailscale.AuthKey.Value())
	assert.Equal(t, "localhost:0", cfg.Global.MetricsAddr)
	assert.Equal(t, 1, len(cfg.Services))
	assert.Equal(t, "test-service", cfg.Services[0].Name)
	assert.Equal(t, "localhost:8080", cfg.Services[0].BackendAddr)
}

func TestTestFixtureWithService(t *testing.T) {
	fixture := NewTestFixture(t)

	// Test updating existing service
	cfg := fixture.WithService("test-service", "localhost:9090").Build()
	assert.Equal(t, 1, len(cfg.Services))
	assert.Equal(t, "localhost:9090", cfg.Services[0].BackendAddr)

	// Test adding new service
	cfg = fixture.WithService("another-service", "localhost:9091").Build()
	assert.Equal(t, 2, len(cfg.Services))
	assert.Equal(t, "another-service", cfg.Services[1].Name)
	assert.Equal(t, "localhost:9091", cfg.Services[1].BackendAddr)
}

func TestTestFixtureWithOAuth(t *testing.T) {
	fixture := NewTestFixture(t)
	cfg := fixture.WithOAuth("client-id", "client-secret").Build()

	// Verify OAuth is configured and AuthKey is cleared
	assert.Equal(t, "", cfg.Tailscale.AuthKey.Value())
	assert.Equal(t, "client-id", cfg.Tailscale.OAuthClientID)
	assert.Equal(t, "client-secret", cfg.Tailscale.OAuthClientSecret.Value())
}

func TestTestFixtureWithTimeout(t *testing.T) {
	fixture := NewTestFixture(t)
	cfg := fixture.
		WithTimeout("read", 5*time.Second).
		WithTimeout("write", 10*time.Second).
		WithTimeout("idle", 15*time.Second).
		WithTimeout("shutdown", 20*time.Second).
		Build()

	assert.Equal(t, 5*time.Second, cfg.Global.ReadHeaderTimeout.Duration)
	assert.Equal(t, 10*time.Second, cfg.Global.WriteTimeout.Duration)
	assert.Equal(t, 15*time.Second, cfg.Global.IdleTimeout.Duration)
	assert.Equal(t, 20*time.Second, cfg.Global.ShutdownTimeout.Duration)
}
