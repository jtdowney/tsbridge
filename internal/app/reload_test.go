package app

import (
	"context"
	"errors"
	"testing"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockServiceRegistry mocks the service registry for testing
type mockServiceRegistry struct {
	services           map[string]*config.Service
	addServiceError    error
	removeServiceError error
	updateServiceError error
	addServiceCalls    []config.Service
	removeServiceCalls []string
	updateServiceCalls []updateCall
}

type updateCall struct {
	name   string
	config config.Service
}

func newMockServiceRegistry() *mockServiceRegistry {
	return &mockServiceRegistry{
		services:           make(map[string]*config.Service),
		addServiceCalls:    []config.Service{},
		removeServiceCalls: []string{},
		updateServiceCalls: []updateCall{},
	}
}

func (m *mockServiceRegistry) AddService(svcCfg config.Service) error {
	m.addServiceCalls = append(m.addServiceCalls, svcCfg)
	if m.addServiceError != nil {
		return m.addServiceError
	}
	m.services[svcCfg.Name] = &svcCfg
	return nil
}

func (m *mockServiceRegistry) RemoveService(name string) error {
	m.removeServiceCalls = append(m.removeServiceCalls, name)
	if m.removeServiceError != nil {
		return m.removeServiceError
	}
	delete(m.services, name)
	return nil
}

func (m *mockServiceRegistry) UpdateService(name string, newCfg config.Service) error {
	m.updateServiceCalls = append(m.updateServiceCalls, updateCall{name: name, config: newCfg})
	if m.updateServiceError != nil {
		return m.updateServiceError
	}
	m.services[name] = &newCfg
	return nil
}

func (m *mockServiceRegistry) Shutdown(ctx context.Context) error {
	return nil
}

func TestReloadConfigWithRegistry_WithErrors(t *testing.T) {
	tests := []struct {
		name               string
		oldCfg             *config.Config
		newCfg             *config.Config
		addServiceError    error
		removeServiceError error
		updateServiceError error
		expectError        bool
		expectAddCalls     int
		expectRemoveCalls  int
		expectUpdateCalls  int
	}{
		{
			name: "successful reload with all operations",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"},
					{Name: "svc2", BackendAddr: "http://localhost:8002"},
					{Name: "svc3", BackendAddr: "http://localhost:8003"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"}, // unchanged
					{Name: "svc2", BackendAddr: "http://localhost:8022"}, // updated
					{Name: "svc4", BackendAddr: "http://localhost:8004"}, // added
				},
			},
			expectError:       false,
			expectAddCalls:    1,
			expectRemoveCalls: 1,
			expectUpdateCalls: 1,
		},
		{
			name: "add service failure",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"},
					{Name: "svc2", BackendAddr: "http://localhost:8002"},
				},
			},
			addServiceError:   errors.New("failed to add service"),
			expectError:       true,
			expectAddCalls:    1,
			expectRemoveCalls: 0,
			expectUpdateCalls: 0,
		},
		{
			name: "remove service failure",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"},
					{Name: "svc2", BackendAddr: "http://localhost:8002"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"},
				},
			},
			removeServiceError: errors.New("failed to remove service"),
			expectError:        true,
			expectAddCalls:     0,
			expectRemoveCalls:  1,
			expectUpdateCalls:  0,
		},
		{
			name: "update service failure",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8002"},
				},
			},
			updateServiceError: errors.New("failed to update service"),
			expectError:        true,
			expectAddCalls:     0,
			expectRemoveCalls:  0,
			expectUpdateCalls:  1,
		},
		{
			name: "multiple failures continues processing",
			oldCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc1", BackendAddr: "http://localhost:8001"},
					{Name: "svc2", BackendAddr: "http://localhost:8002"},
					{Name: "svc3", BackendAddr: "http://localhost:8003"},
				},
			},
			newCfg: &config.Config{
				Services: []config.Service{
					{Name: "svc2", BackendAddr: "http://localhost:8022"}, // update
					{Name: "svc4", BackendAddr: "http://localhost:8004"}, // add
				},
			},
			addServiceError:    errors.New("add failed"),
			removeServiceError: errors.New("remove failed"),
			updateServiceError: errors.New("update failed"),
			expectError:        true,
			expectAddCalls:     1,
			expectRemoveCalls:  2, // svc1 and svc3
			expectUpdateCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRegistry := newMockServiceRegistry()
			mockRegistry.addServiceError = tt.addServiceError
			mockRegistry.removeServiceError = tt.removeServiceError
			mockRegistry.updateServiceError = tt.updateServiceError

			// Pre-populate services for old config
			for _, svc := range tt.oldCfg.Services {
				mockRegistry.services[svc.Name] = &svc
			}

			err := reloadConfigWithRegistry(tt.oldCfg, tt.newCfg, mockRegistry)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify expected calls were made
			assert.Len(t, mockRegistry.addServiceCalls, tt.expectAddCalls, "unexpected number of AddService calls")
			assert.Len(t, mockRegistry.removeServiceCalls, tt.expectRemoveCalls, "unexpected number of RemoveService calls")
			assert.Len(t, mockRegistry.updateServiceCalls, tt.expectUpdateCalls, "unexpected number of UpdateService calls")

		})
	}
}

func TestReloadConfigWithRegistry_PartialFailure(t *testing.T) {
	oldCfg := &config.Config{
		Services: []config.Service{
			{Name: "svc1", BackendAddr: "http://localhost:8001"},
			{Name: "svc2", BackendAddr: "http://localhost:8002"},
		},
	}

	newCfg := &config.Config{
		Services: []config.Service{
			{Name: "svc3", BackendAddr: "http://localhost:8003"},
			{Name: "svc4", BackendAddr: "http://localhost:8004"},
			{Name: "svc5", BackendAddr: "http://localhost:8005"},
		},
	}

	// Create a mock that fails only for svc4
	mockRegistry := &mockServiceRegistryWithConditions{
		mockServiceRegistry: mockServiceRegistry{
			services:           make(map[string]*config.Service),
			addServiceCalls:    []config.Service{},
			removeServiceCalls: []string{},
			updateServiceCalls: []updateCall{},
		},
		failOnServiceName: "svc4",
		failureError:      errors.New("simulated error"),
	}

	// Pre-populate services
	for _, svc := range oldCfg.Services {
		mockRegistry.services[svc.Name] = &svc
	}

	err := reloadConfigWithRegistry(oldCfg, newCfg, mockRegistry)
	require.Error(t, err)

	// Should have attempted all operations despite failures
	assert.Len(t, mockRegistry.removeServiceCalls, 2) // svc1, svc2
	assert.Len(t, mockRegistry.addServiceCalls, 3)    // svc3, svc4, svc5

	// Check that partial success occurred
	_, hasSvc3 := mockRegistry.services["svc3"]
	_, hasSvc4 := mockRegistry.services["svc4"]
	_, hasSvc5 := mockRegistry.services["svc5"]
	assert.True(t, hasSvc3, "svc3 should have been added")
	assert.False(t, hasSvc4, "svc4 should not have been added (failed)")
	assert.True(t, hasSvc5, "svc5 should have been added")
}

// mockServiceRegistryWithConditions extends mockServiceRegistry to allow conditional failures
type mockServiceRegistryWithConditions struct {
	mockServiceRegistry
	failOnServiceName string
	failureError      error
}

func (m *mockServiceRegistryWithConditions) AddService(svcCfg config.Service) error {
	m.addServiceCalls = append(m.addServiceCalls, svcCfg)
	if svcCfg.Name == m.failOnServiceName && m.failureError != nil {
		return m.failureError
	}
	m.services[svcCfg.Name] = &svcCfg
	return nil
}
