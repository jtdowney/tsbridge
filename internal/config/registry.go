package config

import (
	"fmt"
	"sync"
)

// ProviderFactory is a function that creates a Provider from options
type ProviderFactory func(opts interface{}) (Provider, error)

// ProviderRegistry manages provider factories
type ProviderRegistry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
}

// NewProviderRegistry creates a new provider registry
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		factories: make(map[string]ProviderFactory),
	}
}

// Register adds a provider factory to the registry
func (r *ProviderRegistry) Register(name string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// Get creates a provider using the registered factory
func (r *ProviderRegistry) Get(name string, opts interface{}) (Provider, error) {
	r.mu.RLock()
	factory, exists := r.factories[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider not registered: %s", name)
	}

	return factory(opts)
}

// List returns the names of all registered providers
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// FileProviderOptions contains options for the file provider
type FileProviderOptions struct {
	Path string
}

// FileProviderFactory creates a file provider
func FileProviderFactory(opts interface{}) (Provider, error) {
	fileOpts, ok := opts.(FileProviderOptions)
	if !ok {
		return nil, fmt.Errorf("invalid options type for file provider: expected FileProviderOptions, got %T", opts)
	}
	return NewFileProvider(fileOpts.Path), nil
}

// DockerProviderCreator is the function type for creating docker providers
type DockerProviderCreator func(opts DockerProviderOptions) (Provider, error)

// DockerProviderFactory creates a factory function for docker providers
func DockerProviderFactory(creator DockerProviderCreator) ProviderFactory {
	return func(opts interface{}) (Provider, error) {
		dockerOpts, ok := opts.(DockerProviderOptions)
		if !ok {
			return nil, fmt.Errorf("invalid options type for docker provider: expected DockerProviderOptions, got %T", opts)
		}
		return creator(dockerOpts)
	}
}
