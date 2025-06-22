// Package docker provides Docker label-based configuration for tsbridge.
package docker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
)

const (
	// DefaultLabelPrefix is the default label prefix for tsbridge configuration
	DefaultLabelPrefix = "tsbridge"

	// DefaultDockerEndpoint is the default Docker socket endpoint
	DefaultDockerEndpoint = "unix:///var/run/docker.sock"

	// watchInterval is the interval for polling Docker for changes
	watchInterval = 5 * time.Second
)

// Provider implements config.Provider for Docker label-based configuration
type Provider struct {
	client      *client.Client
	labelPrefix string
	socketPath  string
	mu          sync.RWMutex
	lastConfig  *config.Config
}

// Options contains configuration options for the Docker provider
type Options struct {
	// DockerEndpoint is the Docker socket endpoint (default: unix:///var/run/docker.sock)
	DockerEndpoint string
	// LabelPrefix is the prefix for tsbridge labels (default: tsbridge)
	LabelPrefix string
}

// osStat is a variable to allow mocking in tests
var osStat = os.Stat

// validateDockerAccess checks if the Docker socket is accessible
func validateDockerAccess(socketPath string) error {
	// Skip validation for non-unix sockets (TCP, HTTP)
	if strings.HasPrefix(socketPath, "tcp://") ||
		strings.HasPrefix(socketPath, "http://") ||
		strings.HasPrefix(socketPath, "https://") {
		return nil
	}

	// Extract the actual file path from unix:// URLs
	path := socketPath
	if strings.HasPrefix(socketPath, "unix://") {
		path = strings.TrimPrefix(socketPath, "unix://")
	}

	// Check if the socket exists and is accessible
	info, err := osStat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.NewResourceError(
				fmt.Sprintf("Docker socket not found at %s. Ensure Docker is installed and running.", path),
			)
		}
		if os.IsPermission(err) {
			return errors.NewResourceError(
				fmt.Sprintf("permission denied accessing Docker socket at %s. Try running with appropriate permissions or adding the user to the docker group.", path),
			)
		}
		return errors.WrapProviderError(err, "docker", errors.ErrTypeResource, "failed to access Docker socket")
	}

	// Verify it's actually a socket
	if info.Mode()&os.ModeSocket == 0 {
		return errors.NewResourceError(
			fmt.Sprintf("path %s exists but is not a socket", path),
		)
	}

	return nil
}

// NewProvider creates a new Docker configuration provider
func NewProvider(opts Options) (*Provider, error) {
	if opts.DockerEndpoint == "" {
		opts.DockerEndpoint = DefaultDockerEndpoint
	}
	if opts.LabelPrefix == "" {
		opts.LabelPrefix = DefaultLabelPrefix
	}

	// Validate Docker socket access before creating client
	if err := validateDockerAccess(opts.DockerEndpoint); err != nil {
		return nil, err
	}

	// Create Docker client
	dockerClient, err := client.NewClientWithOpts(
		client.WithHost(opts.DockerEndpoint),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, errors.WrapProviderError(err, "docker", errors.ErrTypeResource, "creating Docker client")
	}

	// Verify Docker connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := dockerClient.Ping(ctx); err != nil {
		return nil, errors.WrapProviderError(err, "docker", errors.ErrTypeResource, "connecting to Docker")
	}

	return &Provider{
		client:      dockerClient,
		labelPrefix: opts.LabelPrefix,
		socketPath:  opts.DockerEndpoint,
	}, nil
}

// Load retrieves configuration from Docker labels
// SECURITY: When logging configuration, always use cfg.Redacted() to prevent
// exposing sensitive values like OAuth secrets and auth keys in logs
func (p *Provider) Load(ctx context.Context) (*config.Config, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Find tsbridge container to get global configuration
	selfContainer, err := p.findSelfContainer(ctx)
	if err != nil {
		return nil, errors.WrapProviderError(err, "docker", errors.ErrTypeResource, "finding tsbridge container")
	}

	// Parse global configuration from tsbridge container labels
	cfg := &config.Config{}
	if err := p.parseGlobalConfig(selfContainer, cfg); err != nil {
		return nil, errors.WrapProviderError(err, "docker", errors.ErrTypeConfig, "parsing global configuration")
	}

	// Find all containers with tsbridge.enabled=true
	serviceContainers, err := p.findServiceContainers(ctx)
	if err != nil {
		return nil, errors.WrapProviderError(err, "docker", errors.ErrTypeResource, "finding service containers")
	}

	// Parse service configurations
	for _, container := range serviceContainers {
		svc, err := p.parseServiceConfig(container)
		if err != nil {
			slog.Warn("failed to parse service configuration",
				"container", container.Names[0],
				"error", err)
			continue
		}
		cfg.Services = append(cfg.Services, *svc)
	}

	// Apply standard configuration processing
	if err := config.ProcessLoadedConfigWithProvider(cfg, "docker"); err != nil {
		return nil, err
	}

	p.lastConfig = cfg
	return cfg, nil
}

// Watch monitors Docker for configuration changes
func (p *Provider) Watch(ctx context.Context) (<-chan *config.Config, error) {
	configCh := make(chan *config.Config)

	go func() {
		defer close(configCh)

		ticker := time.NewTicker(watchInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				newConfig, err := p.Load(ctx)
				if err != nil {
					slog.Error("failed to reload configuration from Docker", "error", err)
					continue
				}

				// Check if configuration has changed
				if !p.configEqual(p.getLastConfig(), newConfig) {
					slog.Info("Docker configuration changed, reloading")
					select {
					case configCh <- newConfig:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return configCh, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "docker"
}

// Close closes the Docker client connection
func (p *Provider) Close() error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

// getLastConfig returns the last configuration in a thread-safe manner
func (p *Provider) getLastConfig() *config.Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastConfig
}

// findSelfContainer finds the tsbridge container itself
func (p *Provider) findSelfContainer(ctx context.Context) (*types.Container, error) {
	// First try to find by hostname (which is the container ID in Docker)
	hostname, err := p.getHostname()
	if err == nil {
		container, err := p.getContainerByID(ctx, hostname)
		if err == nil {
			return container, nil
		}
	}

	// Fallback: find container with tsbridge labels
	opts := container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("%s.tailscale.oauth_client_id", p.labelPrefix)),
		),
	}

	containers, err := p.client.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	if len(containers) == 0 {
		return nil, errors.NewValidationError("no tsbridge container found with global configuration labels")
	}

	// Return the first one (there should only be one)
	return &containers[0], nil
}

// findServiceContainers finds all containers with tsbridge.enabled=true
func (p *Provider) findServiceContainers(ctx context.Context) ([]types.Container, error) {
	opts := container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("%s.enabled=true", p.labelPrefix)),
			filters.Arg("status", "running"),
		),
	}

	return p.client.ContainerList(ctx, opts)
}

// getContainerByID gets a container by ID
func (p *Provider) getContainerByID(ctx context.Context, id string) (*types.Container, error) {
	opts := container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("id", id),
		),
	}

	containers, err := p.client.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	if len(containers) == 0 {
		return nil, errors.NewValidationError("container not found")
	}

	return &containers[0], nil
}

// getHostname gets the container hostname
func (p *Provider) getHostname() (string, error) {
	// In a container, /etc/hostname contains the container ID
	data, err := readFile("/etc/hostname")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// configEqual compares two configurations for equality
func (p *Provider) configEqual(a, b *config.Config) bool {
	if a == nil || b == nil {
		return a == b
	}

	// Simple comparison - could be enhanced with deep equality check
	if len(a.Services) != len(b.Services) {
		return false
	}

	// Compare service names
	aNames := make(map[string]bool)
	for _, svc := range a.Services {
		aNames[svc.Name] = true
	}

	for _, svc := range b.Services {
		if !aNames[svc.Name] {
			return false
		}
	}

	return true
}

// readFile is a helper for testing
var readFile = os.ReadFile
