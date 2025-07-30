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
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/errors"
)

// DockerClient defines the methods required from a Docker client to be used by the provider
type DockerClient interface {
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	Ping(ctx context.Context) (types.Ping, error)
	Close() error
}

const (
	// DefaultLabelPrefix is the default label prefix for tsbridge configuration
	DefaultLabelPrefix = "tsbridge"

	// DefaultDockerEndpoint is the default Docker socket endpoint
	DefaultDockerEndpoint = "unix:///var/run/docker.sock"
)

// Provider implements config.Provider for Docker label-based configuration
type Provider struct {
	client        DockerClient
	labelPrefix   string
	socketPath    string
	mu            sync.RWMutex
	lastConfig    *config.Config
	debounceTimer *time.Timer
	debounceMu    sync.Mutex
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
	path, _ := strings.CutPrefix(socketPath, "unix://")

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
	slog.Debug("validating Docker socket access", "endpoint", opts.DockerEndpoint)
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
	ctx, cancel := context.WithTimeout(context.Background(), constants.DockerPingTimeout)
	defer cancel()

	pingInfo, err := dockerClient.Ping(ctx)
	if err != nil {
		return nil, errors.WrapProviderError(err, "docker", errors.ErrTypeResource, "connecting to Docker")
	}
	slog.Debug("Docker connection verified",
		"api_version", pingInfo.APIVersion,
		"os", pingInfo.OSType)

	slog.Info("Docker provider initialized successfully",
		"endpoint", opts.DockerEndpoint,
		"label_prefix", opts.LabelPrefix)

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
	slog.Debug("found service containers", "count", len(serviceContainers))

	// Parse service configurations
	for _, container := range serviceContainers {
		containerName := ""
		if len(container.Names) > 0 {
			containerName = container.Names[0]
		}

		svc, err := p.parseServiceConfig(container)
		if err != nil {
			slog.Warn("failed to parse service configuration",
				"container", containerName,
				"error", err)
			continue
		}
		cfg.Services = append(cfg.Services, *svc)
	}

	// Apply standard configuration processing
	// Docker provider allows zero services at startup
	if err := config.ProcessLoadedConfigWithProvider(cfg, "docker"); err != nil {
		return nil, err
	}

	slog.Info("Docker configuration loaded successfully",
		"services", len(cfg.Services),
		"label_prefix", p.labelPrefix)

	p.lastConfig = cfg
	return cfg, nil
}

// Watch monitors Docker events for container configuration changes
func (p *Provider) Watch(ctx context.Context) (<-chan *config.Config, error) {
	configCh := make(chan *config.Config)
	eventOptions := p.createEventOptions()

	slog.Info("starting Docker event watcher",
		"label_prefix", p.labelPrefix,
		"socket_path", p.socketPath)

	go func() {
		defer close(configCh)
		p.watchLoop(ctx, configCh, eventOptions)
	}()

	return configCh, nil
}

// createEventOptions creates the event filter options for Docker events
func (p *Provider) createEventOptions() events.ListOptions {
	eventFilters := filters.NewArgs()
	eventFilters.Add("type", "container")
	eventFilters.Add("event", "start")
	eventFilters.Add("event", "stop")
	eventFilters.Add("event", "die")
	eventFilters.Add("event", "pause")
	eventFilters.Add("event", "unpause")
	// Note: We don't filter by label here because Docker treats multiple
	// label filters as AND conditions. We'll check labels client-side
	// to support both "enabled" and "enable" labels.

	return events.ListOptions{
		Filters: eventFilters,
	}
}

// watchLoop runs the main event watching loop with reconnection
func (p *Provider) watchLoop(ctx context.Context, configCh chan<- *config.Config, eventOptions events.ListOptions) {
	backoff := time.Second
	const maxBackoff = constants.DockerMaxReconnectBackoff

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// processEventStream returns true if context is cancelled, false if stream closed
			cancelled, streamEstablished := p.processEventStream(ctx, configCh, eventOptions)
			if cancelled {
				return // Context cancelled
			}

			// Reset backoff if we successfully established a stream and received events
			if streamEstablished {
				backoff = time.Second
			}

			// Event stream closed, wait before reconnecting with backoff
			slog.Debug("Docker event stream closed, reconnecting...", "backoff", backoff)

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				// Increase backoff for next time, up to the max
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}
}

// processEventStream processes a single event stream connection
// Returns (cancelled, streamEstablished) where:
// - cancelled is true if context was cancelled
// - streamEstablished is true if we successfully received at least one event
func (p *Provider) processEventStream(ctx context.Context, configCh chan<- *config.Config, eventOptions events.ListOptions) (bool, bool) {
	events, errs := p.client.Events(ctx, eventOptions)
	streamEstablished := false

	for {
		select {
		case <-ctx.Done():
			return true, streamEstablished
		case err := <-errs:
			if err != nil {
				slog.Error("Docker events stream error", "error", err)
				return false, streamEstablished // Return to restart event stream
			}
		case event := <-events:
			// Mark stream as established after receiving first event
			streamEstablished = true

			if p.handleContainerEvent(ctx, configCh, event) {
				return true, streamEstablished // Context cancelled
			}
		}
	}
}

// handleContainerEvent processes a Docker container event.
// For stop/die, removes the associated service immediately to avoid race conditions.
// For other events, triggers a debounced config reload.
// Returns true if context was cancelled, false otherwise.
func (p *Provider) handleContainerEvent(ctx context.Context, configCh chan<- *config.Config, event events.Message) bool {
	if event.Type != "container" {
		return false
	}

	// Check if this container has tsbridge enabled (either "enabled" or "enable" label)
	enabledLabel := fmt.Sprintf("%s.enabled", p.labelPrefix)
	enableLabel := fmt.Sprintf("%s.enable", p.labelPrefix)

	// Docker events include labels in the Actor.Attributes map
	isEnabled := event.Actor.Attributes[enabledLabel] == "true" ||
		event.Actor.Attributes[enableLabel] == "true"

	if !isEnabled {
		// Not a tsbridge-enabled container, ignore this event
		return false
	}

	containerID := event.Actor.ID
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}

	slog.Debug("Docker container event received",
		"action", event.Action,
		"container_name", event.Actor.Attributes["name"],
		"container_id", containerID)

	// For critical events like stop/die, handle immediately to avoid race conditions
	// For other events, use debounced reload to batch rapid changes
	if event.Action == "stop" || event.Action == "die" {
		// Handle stop/die events immediately to avoid Docker API race conditions
		slog.Debug("Handling stop/die event immediately (no debouncing)",
			"action", event.Action,
			"container_name", event.Actor.Attributes["name"])

		// Save the old config before loading new one
		oldConfig := p.getLastConfig()

		// Load new configuration when container event occurs
		newConfig, err := p.Load(ctx)
		if err != nil {
			slog.Error("failed to reload configuration after Docker event", "error", err)
			return false
		}

		// Manually remove the service associated with the stopped container
		// This handles the Docker API race condition where a container might still appear as "running"
		// briefly after the stop event is received
		containerName := event.Actor.Attributes["name"]
		if containerName != "" {
			newConfig = p.removeServiceByContainerName(newConfig, containerName)
		}

		// Check if configuration has changed
		if !p.configEqual(oldConfig, newConfig) {
			slog.Info("Docker configuration changed due to container event",
				"action", event.Action,
				"container_name", event.Actor.Attributes["name"])

			select {
			case configCh <- newConfig:
				// Update lastConfig after successfully sending the new config
				p.mu.Lock()
				p.lastConfig = newConfig
				p.mu.Unlock()
			case <-ctx.Done():
				return true
			}
		}
	} else {
		// For start and other events, use debounced reload
		slog.Debug("Triggering debounced configuration reload due to container event",
			"action", event.Action,
			"container_name", event.Actor.Attributes["name"])

		p.debouncedReload(ctx, configCh)
	}

	return false
}

// isContainerEnabled checks if a container has either the enabled or enable label set to true
func (p *Provider) isContainerEnabled(labels map[string]string) bool {
	enabledLabel := fmt.Sprintf("%s.enabled", p.labelPrefix)
	enableLabel := fmt.Sprintf("%s.enable", p.labelPrefix)

	return labels[enabledLabel] == "true" || labels[enableLabel] == "true"
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "docker"
}

// debouncedReload debounces configuration reloads to prevent thundering herd issues
func (p *Provider) debouncedReload(ctx context.Context, configCh chan<- *config.Config) {
	p.debounceMu.Lock()
	defer p.debounceMu.Unlock()

	// Cancel existing timer if any
	if p.debounceTimer != nil {
		p.debounceTimer.Stop()
	}

	// Set new timer
	p.debounceTimer = time.AfterFunc(constants.DockerEventDebounceDelay, func() {
		// Check if context is still valid
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Load and send new config
		newConfig, err := p.Load(ctx)
		if err != nil {
			slog.Error("failed to reload configuration after debounce", "error", err)
			return
		}

		// Try to send config, but don't block if channel is closed or context cancelled
		select {
		case configCh <- newConfig:
			p.mu.Lock()
			p.lastConfig = newConfig
			p.mu.Unlock()
		case <-ctx.Done():
			return
		default:
			// Channel might be closed, just log and return
			slog.Debug("could not send debounced config - channel closed or full")
		}
	})
}

// Close closes the Docker client connection
func (p *Provider) Close() error {
	// Stop debounce timer
	p.debounceMu.Lock()
	if p.debounceTimer != nil {
		p.debounceTimer.Stop()
	}
	p.debounceMu.Unlock()

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
func (p *Provider) findSelfContainer(ctx context.Context) (*container.Summary, error) {
	// First try to find by hostname (which is the container ID in Docker)
	hostname, err := p.getHostname()
	if err == nil {
		slog.Debug("checking for self container by hostname", "hostname", hostname)
		container, err := p.getContainerByID(ctx, hostname)
		if err == nil {
			slog.Debug("found self container by hostname", "container", container.ID)
			return container, nil
		}

		slog.Debug("failed to find self container by hostname", "error", err)
	} else {
		slog.Debug("failed to get hostname", "error", err)
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
		return nil, errors.NewValidationError("unable to find tsbridge container")
	}

	// Return the first one (there should only be one)
	return &containers[0], nil
}

// findServiceContainers finds all containers with tsbridge.enabled=true or tsbridge.enable=true
func (p *Provider) findServiceContainers(ctx context.Context) ([]container.Summary, error) {
	// Query for containers with enabled=true
	enabledLabel := fmt.Sprintf("%s.enabled=true", p.labelPrefix)
	enabledOpts := container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("status", "running"),
			filters.Arg("label", enabledLabel),
		),
	}

	enabledContainers, err := p.client.ContainerList(ctx, enabledOpts)
	if err != nil {
		return nil, err
	}

	// Query for containers with enable=true
	enableLabel := fmt.Sprintf("%s.enable=true", p.labelPrefix)
	enableOpts := container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("status", "running"),
			filters.Arg("label", enableLabel),
		),
	}

	enableContainers, err := p.client.ContainerList(ctx, enableOpts)
	if err != nil {
		return nil, err
	}

	// Merge results and remove duplicates
	containerMap := make(map[string]container.Summary)
	for _, c := range enabledContainers {
		containerMap[c.ID] = c
	}
	for _, c := range enableContainers {
		containerMap[c.ID] = c
	}

	// Convert map back to slice
	var serviceContainers []container.Summary
	for _, c := range containerMap {
		serviceContainers = append(serviceContainers, c)
	}

	return serviceContainers, nil
}

// getContainerByID gets a container by ID
func (p *Provider) getContainerByID(ctx context.Context, id string) (*container.Summary, error) {
	// List all containers since Docker's ID filter might not work with partial IDs
	opts := container.ListOptions{
		All: true, // Include stopped containers too
	}

	containers, err := p.client.ContainerList(ctx, opts)
	if err != nil {
		return nil, err
	}

	slog.Debug("searching for container by ID",
		"target_id", id,
		"total_containers", len(containers))

	// Find container by matching ID prefix
	for _, c := range containers {
		// Log first 12 chars of each container ID for debugging
		shortID := c.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		slog.Debug("checking container",
			"container_id", shortID,
			"container_names", c.Names,
			"matches", strings.HasPrefix(c.ID, id))

		// Check if the container ID starts with our hostname/ID
		if strings.HasPrefix(c.ID, id) {
			return &c, nil
		}
	}

	for _, c := range containers {
		for _, name := range c.Names {
			// Docker container names are prefixed with '/'
			if strings.TrimPrefix(name, "/") == id {
				slog.Debug("found container by name", "container", c.ID, "name", name)
				return &c, nil
			}
		}
	}

	return nil, errors.NewValidationError("container not found")
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

// removeServiceByContainerName removes services whose backend hostname matches the given container name.
// Used to handle Docker API race conditions on stop/die events.
// Returns a new config with the service removed, or the original config if no match.
func (p *Provider) removeServiceByContainerName(cfg *config.Config, containerName string) *config.Config {
	if cfg == nil || containerName == "" {
		return cfg
	}

	// Create a new config with the same global settings
	newCfg := &config.Config{
		Tailscale: cfg.Tailscale,
		Global:    cfg.Global,
		Services:  make([]config.Service, 0, len(cfg.Services)),
	}

	// Copy all services except those matching the container name
	removed := false
	for _, svc := range cfg.Services {
		// Parse the backend address to extract the hostname part
		// Backend addresses are typically in format: "container-name:port" or "docker-container-name:port"
		hostPort := svc.BackendAddr

		// Extract just the hostname part (before the port)
		var hostname string
		if idx := strings.LastIndex(hostPort, ":"); idx > 0 {
			hostname = hostPort[:idx]
		} else {
			hostname = hostPort
		}

		// Check for exact match only to prevent false positives
		// Backend addresses should exactly match the container name (excluding port)
		if hostname == containerName {
			slog.Debug("removing service from stopped container",
				"service", svc.Name,
				"container", containerName,
				"backend", svc.BackendAddr,
				"matched_hostname", hostname)
			removed = true
			continue
		}

		newCfg.Services = append(newCfg.Services, svc)
	}

	if removed {
		slog.Info("removed service associated with stopped container",
			"container", containerName,
			"remaining_services", len(newCfg.Services))
	}

	return newCfg
}

// configEqual compares two configurations for equality
func (p *Provider) configEqual(a, b *config.Config) bool {
	if a == nil || b == nil {
		return a == b
	}

	if len(a.Services) != len(b.Services) {
		return false
	}

	// Create a map of new services for efficient lookup
	bServices := make(map[string]config.Service, len(b.Services))
	for _, svc := range b.Services {
		bServices[svc.Name] = svc
	}

	// Compare each service from the old config with the new one
	for _, aSvc := range a.Services {
		bSvc, ok := bServices[aSvc.Name]
		if !ok {
			// A service was removed
			return false
		}
		if !config.ServiceConfigEqual(aSvc, bSvc) {
			// A service's configuration has changed
			return false
		}
	}

	// Note: This assumes global/tailscale config doesn't change dynamically
	return true
}

// readFile is a helper for testing
var readFile = os.ReadFile
