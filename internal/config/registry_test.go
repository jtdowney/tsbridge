package config

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRegistryProvider implements Provider interface for registry testing
type testRegistryProvider struct {
	name string
}

func (m *testRegistryProvider) Load(ctx context.Context) (*Config, error) {
	return nil, nil
}

func (m *testRegistryProvider) Watch(ctx context.Context) (<-chan *Config, error) {
	return nil, nil
}

func (m *testRegistryProvider) Name() string {
	return m.name
}

// testProviderFactory creates test providers
func testProviderFactory(name string) ProviderFactory {
	return func(opts interface{}) (Provider, error) {
		return &testRegistryProvider{name: name}, nil
	}
}

// errorProviderFactory returns an error
func errorProviderFactory(errMsg string) ProviderFactory {
	return func(opts interface{}) (Provider, error) {
		return nil, fmt.Errorf("%s", errMsg)
	}
}

func TestNewProviderRegistry(t *testing.T) {
	registry := NewProviderRegistry()
	require.NotNil(t, registry)
	assert.NotNil(t, registry.factories)
	assert.Empty(t, registry.List())
}

func TestProviderRegistry_Register(t *testing.T) {
	t.Run("register single provider", func(t *testing.T) {
		registry := NewProviderRegistry()
		registry.Register("test", testProviderFactory("test"))

		list := registry.List()
		assert.Len(t, list, 1)
		assert.Contains(t, list, "test")
	})

	t.Run("register multiple providers", func(t *testing.T) {
		registry := NewProviderRegistry()
		registry.Register("test1", testProviderFactory("test1"))
		registry.Register("test2", testProviderFactory("test2"))
		registry.Register("test3", testProviderFactory("test3"))

		list := registry.List()
		assert.Len(t, list, 3)
		assert.Contains(t, list, "test1")
		assert.Contains(t, list, "test2")
		assert.Contains(t, list, "test3")
	})

	t.Run("overwrite existing provider", func(t *testing.T) {
		registry := NewProviderRegistry()

		// Register first factory
		registry.Register("test", testProviderFactory("test1"))
		provider1, err := registry.Get("test", nil)
		require.NoError(t, err)
		assert.Equal(t, "test1", provider1.Name())

		// Overwrite with second factory
		registry.Register("test", testProviderFactory("test2"))
		provider2, err := registry.Get("test", nil)
		require.NoError(t, err)
		assert.Equal(t, "test2", provider2.Name())

		// Should still have only one provider
		list := registry.List()
		assert.Len(t, list, 1)
		assert.Contains(t, list, "test")
	})
}

func TestProviderRegistry_Get(t *testing.T) {
	tests := []struct {
		name         string
		registered   map[string]ProviderFactory
		getProvider  string
		opts         interface{}
		expectError  bool
		errorMessage string
	}{
		{
			name: "get existing provider",
			registered: map[string]ProviderFactory{
				"test": testProviderFactory("test"),
			},
			getProvider: "test",
			opts:        nil,
			expectError: false,
		},
		{
			name:         "get non-existent provider",
			registered:   map[string]ProviderFactory{},
			getProvider:  "missing",
			opts:         nil,
			expectError:  true,
			errorMessage: "provider not registered: missing",
		},
		{
			name: "provider factory returns error",
			registered: map[string]ProviderFactory{
				"error": errorProviderFactory("factory error"),
			},
			getProvider:  "error",
			opts:         nil,
			expectError:  true,
			errorMessage: "factory error",
		},
		{
			name: "get with options",
			registered: map[string]ProviderFactory{
				"test": func(opts interface{}) (Provider, error) {
					str, ok := opts.(string)
					if !ok {
						return nil, fmt.Errorf("expected string options")
					}
					return &testRegistryProvider{name: str}, nil
				},
			},
			getProvider: "test",
			opts:        "test-options",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewProviderRegistry()

			for name, factory := range tt.registered {
				registry.Register(name, factory)
			}

			provider, err := registry.Get(tt.getProvider, tt.opts)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMessage)
				assert.Nil(t, provider)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, provider)
			}
		})
	}
}

func TestProviderRegistry_List(t *testing.T) {
	tests := []struct {
		name       string
		registered []string
		expected   []string
	}{
		{
			name:       "empty registry",
			registered: []string{},
			expected:   []string{},
		},
		{
			name:       "single provider",
			registered: []string{"test"},
			expected:   []string{"test"},
		},
		{
			name:       "multiple providers",
			registered: []string{"test1", "test2", "test3"},
			expected:   []string{"test1", "test2", "test3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewProviderRegistry()

			for _, name := range tt.registered {
				registry.Register(name, testProviderFactory(name))
			}

			list := registry.List()
			assert.Len(t, list, len(tt.expected))

			// Check all expected items are in the list
			for _, expected := range tt.expected {
				assert.Contains(t, list, expected)
			}
		})
	}
}

func TestProviderRegistry_ThreadSafety(t *testing.T) {
	registry := NewProviderRegistry()

	// Number of concurrent operations
	numOps := 100
	numWorkers := 10

	var wg sync.WaitGroup
	wg.Add(numWorkers * 3) // 3 types of operations

	// Concurrent Register operations
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				name := fmt.Sprintf("provider_%d_%d", workerID, j)
				registry.Register(name, testProviderFactory(name))
			}
		}(i)
	}

	// Concurrent Get operations
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				// Try to get both existing and non-existing providers
				name := fmt.Sprintf("provider_%d_%d", workerID%numWorkers, j)
				_, _ = registry.Get(name, nil)
			}
		}(i)
	}

	// Concurrent List operations
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_ = registry.List()
			}
		}()
	}

	// Wait for all operations to complete
	wg.Wait()

	// Verify registry is still in a valid state
	list := registry.List()
	assert.NotNil(t, list)

	// Try some operations to ensure registry still works
	registry.Register("final_test", testProviderFactory("final"))
	provider, err := registry.Get("final_test", nil)
	assert.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestFileProviderFactory(t *testing.T) {
	tests := []struct {
		name        string
		opts        interface{}
		expectError bool
	}{
		{
			name:        "valid file provider options",
			opts:        FileProviderOptions{Path: "/test/path"},
			expectError: false,
		},
		{
			name:        "invalid options type",
			opts:        "invalid",
			expectError: true,
		},
		{
			name:        "nil options",
			opts:        nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := FileProviderFactory(tt.opts)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, provider)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, provider)
				assert.Equal(t, "file", provider.Name())
			}
		})
	}
}

func TestDockerProviderFactory(t *testing.T) {
	// Mock docker provider creator
	mockCreator := func(opts DockerProviderOptions) (Provider, error) {
		if opts.DockerEndpoint == "" {
			return nil, fmt.Errorf("docker endpoint required")
		}
		return &testRegistryProvider{name: "docker"}, nil
	}

	factory := DockerProviderFactory(mockCreator)

	tests := []struct {
		name        string
		opts        interface{}
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid docker provider options",
			opts: DockerProviderOptions{
				DockerEndpoint: "unix:///var/run/docker.sock",
				LabelPrefix:    "tsbridge",
			},
			expectError: false,
		},
		{
			name:        "invalid options type",
			opts:        "invalid",
			expectError: true,
			errorMsg:    "invalid options type for docker provider",
		},
		{
			name:        "empty docker endpoint",
			opts:        DockerProviderOptions{},
			expectError: true,
			errorMsg:    "docker endpoint required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := factory(tt.opts)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, provider)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, provider)
			}
		})
	}
}
