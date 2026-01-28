package web

import (
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockApp implements the Application interface for testing
type mockApp struct {
	config   *config.Config
	registry *service.Registry
}

func (m *mockApp) GetConfig() *config.Config {
	return m.config
}

func (m *mockApp) GetRegistry() *service.Registry {
	return m.registry
}

func TestNewServer(t *testing.T) {
	app := &mockApp{
		config: &config.Config{
			Services: []config.Service{
				{
					Name:        "test-service",
					BackendAddr: "localhost:8080",
					TLSMode:     "auto",
					Tags:        []string{"test"},
				},
			},
		},
	}

	server, err := NewServer(":8080", app)
	require.NoError(t, err)
	assert.NotNil(t, server)
	assert.Equal(t, ":8080", server.addr)
	assert.Equal(t, app, server.app)
}

func TestGetServicesInfo(t *testing.T) {
	app := &mockApp{
		config: &config.Config{
			Services: []config.Service{
				{
					Name:         "test-service",
					BackendAddr:  "localhost:8080",
					TLSMode:      "auto",
					WhoisEnabled: &[]bool{true}[0], // pointer to true
					Tags:         []string{"test", "api"},
				},
				{
					Name:        "web-service",
					BackendAddr: "localhost:3000",
					TLSMode:     "off",
					Tags:        []string{"web"},
				},
			},
		},
	}

	server, err := NewServer(":8080", app)
	require.NoError(t, err)

	services := server.getServicesInfo()
	assert.Len(t, services, 2)

	// Check first service
	assert.Equal(t, "test-service", services[0].Name)
	assert.Equal(t, "localhost:8080", services[0].Backend)
	assert.Equal(t, "auto", services[0].TLSMode)
	assert.True(t, services[0].WhoisEnabled)
	assert.Equal(t, []string{"test", "api"}, services[0].Tags)
	assert.Equal(t, "running", services[0].Status)

	// Check second service
	assert.Equal(t, "web-service", services[1].Name)
	assert.Equal(t, "localhost:3000", services[1].Backend)
	assert.Equal(t, "off", services[1].TLSMode)
	assert.Equal(t, []string{"web"}, services[1].Tags)
}

func TestGetMetricsSummary(t *testing.T) {
	app := &mockApp{
		config: &config.Config{
			Services: []config.Service{
				{Name: "service1", BackendAddr: "localhost:8080"},
				{Name: "service2", BackendAddr: "localhost:8081"},
			},
		},
	}

	server, err := NewServer(":8080", app)
	require.NoError(t, err)

	metrics := server.getMetricsSummary()
	assert.Equal(t, 2, metrics.TotalServices)
	assert.Equal(t, 2, metrics.ActiveServices) // All services assumed running in mock
	assert.True(t, metrics.LastUpdated.After(time.Now().Add(-time.Second)))
}
