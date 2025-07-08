package docker

import (
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/errors"
)

var (
	// headerNameRegex validates HTTP header names according to RFC 7230
	// Header names must consist of visible ASCII characters excluding separators
	headerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`)

	// controlCharRegex matches control characters that are not allowed in header values
	controlCharRegex = regexp.MustCompile(`[\x00-\x1F\x7F]`)
)

// isValidHeaderName validates that a header name conforms to RFC 7230
func isValidHeaderName(name string) bool {
	if name == "" {
		return false
	}
	return headerNameRegex.MatchString(name)
}

// isValidHeaderValue validates that a header value doesn't contain control characters
// that could be used for header injection attacks
func isValidHeaderValue(value string) bool {
	// Check for control characters including CR, LF, NULL, etc.
	return !controlCharRegex.MatchString(value)
}

// validateBackendAddress validates a backend address for security and format
func validateBackendAddress(addr string) error {
	if addr == "" {
		return errors.NewValidationError("backend address cannot be empty")
	}

	// Check for unix socket addresses
	if strings.HasPrefix(addr, "unix:") {
		// Must start with unix://
		if !strings.HasPrefix(addr, "unix://") {
			return errors.NewValidationError("unix socket path must start with unix://")
		}

		// Extract path after unix://
		socketPath := strings.TrimPrefix(addr, "unix://")

		// Unix socket should not have port
		if strings.Contains(socketPath, ":") {
			return errors.NewValidationError("unix socket cannot have port")
		}

		// Check for path traversal
		if strings.Contains(socketPath, "..") {
			return errors.NewValidationError("invalid unix socket path")
		}

		// Must be absolute path
		if !strings.HasPrefix(socketPath, "/") {
			return errors.NewValidationError("unix socket path must be absolute")
		}

		return nil
	}

	// For network addresses, validate host:port format
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.NewValidationError("invalid backend address format")
	}

	// Validate port
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return errors.NewValidationError("invalid port")
	}

	if port < 1 || port > 65535 {
		return errors.NewValidationError("port must be between 1 and 65535")
	}

	return nil
}

// labelParser helps parse Docker labels with a given prefix
type labelParser struct {
	labels map[string]string
	prefix string
}

// newLabelParser creates a new label parser
func newLabelParser(labels map[string]string, prefix string) *labelParser {
	return &labelParser{
		labels: labels,
		prefix: prefix,
	}
}

// getString gets a string value from labels
func (p *labelParser) getString(key string) string {
	fullKey := fmt.Sprintf("%s.%s", p.prefix, key)
	return p.labels[fullKey]
}

// getBool gets a boolean pointer from labels
func (p *labelParser) getBool(key string) *bool {
	value := p.getString(key)
	result, _ := parseBool(value)
	return result
}

// getInt gets an integer pointer from labels
func (p *labelParser) getInt(key string) *int {
	value := p.getString(key)
	result, _ := parseInt(value)
	return result
}

// getDuration gets a duration from labels
func (p *labelParser) getDuration(key string) config.Duration {
	value := p.getString(key)
	result, _ := parseDuration(value)
	return result
}

// getByteSize gets a ByteSize pointer from labels
func (p *labelParser) getByteSize(key string) *config.ByteSize {
	value := p.getString(key)
	result, err := parseByteSize(value)
	if err != nil {
		slog.Warn("failed to parse ByteSize from Docker label",
			"key", key,
			"value", value,
			"error", err)
		return nil
	}
	return result
}

// getStringSlice gets a string slice from labels
func (p *labelParser) getStringSlice(key, separator string) []string {
	value := p.getString(key)
	return parseStringSlice(value, separator)
}

// getHeaders parses header configuration from labels with security validation
func (p *labelParser) getHeaders(key string) map[string]string {
	headers := make(map[string]string)
	fullPrefix := fmt.Sprintf("%s.%s.", p.prefix, key)

	for label, value := range p.labels {
		if strings.HasPrefix(label, fullPrefix) {
			headerName := strings.TrimPrefix(label, fullPrefix)

			// Validate header name to prevent injection attacks
			if !isValidHeaderName(headerName) {
				slog.Warn("rejecting invalid header name from Docker label",
					"header", headerName,
					"label", label,
					"reason", "invalid characters in header name")
				continue
			}

			// Validate header value to prevent CRLF injection
			if !isValidHeaderValue(value) {
				slog.Warn("rejecting header with invalid value from Docker label",
					"header", headerName,
					"label", label,
					"reason", "control characters in header value")
				continue
			}

			headers[headerName] = value
		}
	}

	if len(headers) == 0 {
		return nil
	}
	return headers
}

// parseGlobalConfig parses global configuration from container labels
func (p *Provider) parseGlobalConfig(container *container.Summary, cfg *config.Config) error {
	parser := newLabelParser(container.Labels, p.labelPrefix)

	// Parse Tailscale configuration
	cfg.Tailscale = config.Tailscale{
		OAuthClientID:         parser.getString("tailscale.oauth_client_id"),
		OAuthClientIDEnv:      parser.getString("tailscale.oauth_client_id_env"),
		OAuthClientIDFile:     parser.getString("tailscale.oauth_client_id_file"),
		OAuthClientSecret:     config.RedactedString(parser.getString("tailscale.oauth_client_secret")),
		OAuthClientSecretEnv:  parser.getString("tailscale.oauth_client_secret_env"),
		OAuthClientSecretFile: parser.getString("tailscale.oauth_client_secret_file"),
		AuthKey:               config.RedactedString(parser.getString("tailscale.auth_key")),
		AuthKeyEnv:            parser.getString("tailscale.auth_key_env"),
		AuthKeyFile:           parser.getString("tailscale.auth_key_file"),
		StateDir:              parser.getString("tailscale.state_dir"),
		DefaultTags:           parser.getStringSlice("tailscale.default_tags", ","),
	}

	// Parse global configuration
	cfg.Global = config.Global{
		MetricsAddr:              parser.getString("global.metrics_addr"),
		ReadHeaderTimeout:        parser.getDuration("global.read_header_timeout"),
		WriteTimeout:             parser.getDuration("global.write_timeout"),
		IdleTimeout:              parser.getDuration("global.idle_timeout"),
		ShutdownTimeout:          parser.getDuration("global.shutdown_timeout"),
		ResponseHeaderTimeout:    parser.getDuration("global.response_header_timeout"),
		AccessLog:                parser.getBool("global.access_log"),
		TrustedProxies:           parser.getStringSlice("global.trusted_proxies", ","),
		DialTimeout:              parser.getDuration("global.dial_timeout"),
		KeepAliveTimeout:         parser.getDuration("global.keep_alive_timeout"),
		IdleConnTimeout:          parser.getDuration("global.idle_conn_timeout"),
		TLSHandshakeTimeout:      parser.getDuration("global.tls_handshake_timeout"),
		ExpectContinueTimeout:    parser.getDuration("global.expect_continue_timeout"),
		MetricsReadHeaderTimeout: parser.getDuration("global.metrics_read_header_timeout"),
		FlushInterval:            parser.getDuration("global.flush_interval"),
	}

	// Handle MaxRequestBodySize separately since it's a ByteSize type
	if bs := parser.getByteSize("global.max_request_body_size"); bs != nil {
		cfg.Global.MaxRequestBodySize = *bs
	}

	return nil
}

// parseServiceConfig parses service configuration from container labels
func (p *Provider) parseServiceConfig(container container.Summary) (*config.Service, error) {
	parser := newLabelParser(container.Labels, p.labelPrefix)
	svc := &config.Service{}

	// Service name (required)
	svc.Name = parser.getString("service.name")
	if svc.Name == "" {
		// Use container name as fallback
		if len(container.Names) > 0 {
			svc.Name = strings.TrimPrefix(container.Names[0], "/")
		} else {
			return nil, errors.NewProviderError("docker", errors.ErrTypeValidation, "service name is required")
		}
	}

	// Backend address
	backendAddr := parser.getString("service.backend_addr")
	if backendAddr == "" {
		// Default to port from label or first exposed port
		port := parser.getString("service.port")
		if port == "" {
			// Try to get the first exposed port
			for p := range container.Ports {
				if container.Ports[p].PrivatePort != 0 {
					port = fmt.Sprintf("%d", container.Ports[p].PrivatePort)
					break
				}
			}
		}

		if port != "" {
			// Resolve container address
			containerAddr := p.getContainerAddress(container)
			backendAddr = fmt.Sprintf("%s:%s", containerAddr, port)
		}
	}

	if backendAddr == "" {
		return nil, errors.NewProviderError("docker", errors.ErrTypeValidation, "backend address could not be determined")
	}

	// Validate backend address
	if err := validateBackendAddress(backendAddr); err != nil {
		return nil, errors.WrapProviderError(err, "docker", errors.ErrTypeValidation, "invalid backend address")
	}

	svc.BackendAddr = backendAddr

	// Parse configuration
	svc.Tags = parser.getStringSlice("service.tags", ",")
	svc.WhoisEnabled = parser.getBool("service.whois_enabled")
	svc.AccessLog = parser.getBool("service.access_log")
	svc.FunnelEnabled = parser.getBool("service.funnel_enabled")
	svc.TLSMode = parser.getString("service.tls_mode")
	svc.WhoisTimeout = parser.getDuration("service.whois_timeout")
	svc.ReadHeaderTimeout = parser.getDuration("service.read_header_timeout")
	svc.WriteTimeout = parser.getDuration("service.write_timeout")
	svc.IdleTimeout = parser.getDuration("service.idle_timeout")
	svc.ResponseHeaderTimeout = parser.getDuration("service.response_header_timeout")
	svc.FlushInterval = parser.getDuration("service.flush_interval")
	svc.UpstreamHeaders = parser.getHeaders("service.upstream_headers")
	svc.DownstreamHeaders = parser.getHeaders("service.downstream_headers")
	svc.RemoveUpstream = parser.getStringSlice("service.remove_upstream", ",")
	svc.RemoveDownstream = parser.getStringSlice("service.remove_downstream", ",")
	svc.MaxRequestBodySize = parser.getByteSize("service.max_request_body_size")

	// Handle ephemeral (non-pointer bool)
	if ephemeral := parser.getBool("service.ephemeral"); ephemeral != nil {
		svc.Ephemeral = *ephemeral
	}

	return svc, nil
}

// getContainerAddress returns the address to reach the container
func (p *Provider) getContainerAddress(container container.Summary) string {
	// If container is on the same network, use container name
	// This works in Docker networks where containers can reach each other by name
	if len(container.Names) > 0 {
		return strings.TrimPrefix(container.Names[0], "/")
	}

	// Fallback to container ID
	return container.ID[:12]
}

// parseDuration parses a duration string and returns a config.Duration
func parseDuration(value string) (config.Duration, error) {
	if value == "" {
		return config.Duration{}, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return config.Duration{}, err
	}
	return config.Duration{Duration: d, IsSet: true}, nil
}

// parseBool parses a boolean string and returns a pointer to bool
func parseBool(value string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// parseInt parses an integer string and returns a pointer to int
func parseInt(value string) (*int, error) {
	if value == "" {
		return nil, nil
	}
	i, err := strconv.Atoi(value)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// parseStringSlice parses a delimited string and returns a slice of strings
func parseStringSlice(value, separator string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, separator)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// parseByteSize parses a byte size string and returns a pointer to ByteSize
func parseByteSize(value string) (*config.ByteSize, error) {
	if value == "" {
		return nil, nil
	}
	var b config.ByteSize
	if err := b.UnmarshalText([]byte(value)); err != nil {
		return nil, err
	}
	return &b, nil
}
