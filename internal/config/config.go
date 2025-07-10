// Package config handles configuration parsing and validation for tsbridge.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/errors"
)

// Config represents the complete tsbridge configuration
type Config struct {
	Tailscale Tailscale `mapstructure:"tailscale"` // Tailscale authentication config
	Global    Global    `mapstructure:"global"`    // Default settings for all services
	Services  []Service `mapstructure:"services"`  // List of services to expose
}

// Tailscale contains Tailscale-specific configuration
type Tailscale struct {
	OAuthClientID         string         `mapstructure:"oauth_client_id"`          // OAuth client ID for Tailscale
	OAuthClientIDEnv      string         `mapstructure:"oauth_client_id_env"`      // Env var containing OAuth client ID
	OAuthClientIDFile     string         `mapstructure:"oauth_client_id_file"`     // File containing OAuth client ID
	OAuthClientSecret     RedactedString `mapstructure:"oauth_client_secret"`      // OAuth client secret for Tailscale
	OAuthClientSecretEnv  string         `mapstructure:"oauth_client_secret_env"`  // Env var containing OAuth client secret
	OAuthClientSecretFile string         `mapstructure:"oauth_client_secret_file"` // File containing OAuth client secret
	AuthKey               RedactedString `mapstructure:"auth_key"`                 // Tailscale auth key (alternative to OAuth)
	AuthKeyEnv            string         `mapstructure:"auth_key_env"`             // Env var containing auth key
	AuthKeyFile           string         `mapstructure:"auth_key_file"`            // File containing auth key
	StateDir              string         `mapstructure:"state_dir"`                // Directory for Tailscale state
	StateDirEnv           string         `mapstructure:"state_dir_env"`            // Env var containing state directory
	DefaultTags           []string       `mapstructure:"default_tags"`             // Default tags for services
}

// Global contains global default settings
type Global struct {
	FlushInterval         *time.Duration `mapstructure:"flush_interval"`          // Time between flushes (-1ms for immediate)
	AccessLog             *bool          `mapstructure:"access_log"`              // Enable access logging (default: true)
	TrustedProxies        []string       `mapstructure:"trusted_proxies"`         // List of trusted proxy IPs or CIDR ranges
	MetricsAddr           string         `mapstructure:"metrics_addr"`            // Address for Prometheus metrics
	ResponseHeaderTimeout *time.Duration `mapstructure:"response_header_timeout"` // Timeout for backend response headers
	ShutdownTimeout       *time.Duration `mapstructure:"shutdown_timeout"`        // Max duration for graceful shutdown
	WriteTimeout          *time.Duration `mapstructure:"write_timeout"`           // Max duration for writing response
	IdleTimeout           *time.Duration `mapstructure:"idle_timeout"`            // Max time to wait for next request
	ReadHeaderTimeout     *time.Duration `mapstructure:"read_header_timeout"`     // Time allowed to read request headers
	MaxRequestBodySize    *int64         `mapstructure:"max_request_body_size"`   // Maximum request body size in bytes
	// Transport timeouts
	DialTimeout              *time.Duration `mapstructure:"dial_timeout"`                // Max time for connection dial
	KeepAliveTimeout         *time.Duration `mapstructure:"keep_alive_timeout"`          // Keep-alive probe interval
	IdleConnTimeout          *time.Duration `mapstructure:"idle_conn_timeout"`           // Max idle connection duration
	TLSHandshakeTimeout      *time.Duration `mapstructure:"tls_handshake_timeout"`       // Max time for TLS handshake
	ExpectContinueTimeout    *time.Duration `mapstructure:"expect_continue_timeout"`     // Timeout for 100-continue response
	MetricsReadHeaderTimeout *time.Duration `mapstructure:"metrics_read_header_timeout"` // Read header timeout for metrics server
}

// Service represents a single service configuration
type Service struct {
	Name         string         `mapstructure:"name"`          // Service name (Tailscale hostname)
	BackendAddr  string         `mapstructure:"backend_addr"`  // Backend server address
	WhoisEnabled *bool          `mapstructure:"whois_enabled"` // Enable whois lookups (default: true)
	WhoisTimeout *time.Duration `mapstructure:"whois_timeout"` // Max time for whois lookup
	TLSMode      string         `mapstructure:"tls_mode"`      // "auto" (default), "off"
	Tags         []string       `mapstructure:"tags"`          // Service-specific tags
	// Optional overrides
	ReadHeaderTimeout     *time.Duration `mapstructure:"read_header_timeout"`     // Override global read header timeout
	WriteTimeout          *time.Duration `mapstructure:"write_timeout"`           // Override global write timeout
	IdleTimeout           *time.Duration `mapstructure:"idle_timeout"`            // Override global idle timeout
	ResponseHeaderTimeout *time.Duration `mapstructure:"response_header_timeout"` // Override global response header timeout
	AccessLog             *bool          `mapstructure:"access_log"`              // Override global access_log setting
	MaxRequestBodySize    *int64         `mapstructure:"max_request_body_size"`   // Override global max request body size
	FunnelEnabled         *bool          `mapstructure:"funnel_enabled"`          // Expose service via Tailscale Funnel
	Ephemeral             bool           `mapstructure:"ephemeral"`               // Create ephemeral nodes
	FlushInterval         *time.Duration `mapstructure:"flush_interval"`          // Time between flushes (-1ms for immediate)
	// Header manipulation
	UpstreamHeaders   map[string]string `mapstructure:"upstream_headers"`   // Headers to add to upstream requests
	DownstreamHeaders map[string]string `mapstructure:"downstream_headers"` // Headers to add to downstream responses
	RemoveUpstream    []string          `mapstructure:"remove_upstream"`    // Headers to remove from upstream requests
	RemoveDownstream  []string          `mapstructure:"remove_downstream"`  // Headers to remove from downstream responses
}

// Load reads and parses the configuration from the specified file path.
// It validates the configuration and returns an error if invalid.
// The function supports:
// - TOML file parsing
// - Environment variable overrides
// - Secret resolution from env vars and files
// LoadWithProvider reads and parses the configuration with provider context.
// It includes:
// - Loading the base config from a TOML file
// - Environment variable overrides
// - Secret resolution from env vars and files
// - Validation, defaults and normalization
func LoadWithProvider(path string, provider string) (*Config, error) {
	if path == "" {
		return nil, errors.NewProviderError(provider, errors.ErrTypeValidation, "config path cannot be empty")
	}

	k := koanf.New(".")

	// Load TOML config file
	if err := k.Load(file.Provider(path), toml.Parser()); err != nil {
		return nil, errors.WrapProviderError(err, provider, errors.ErrTypeConfig, "loading config file")
	}

	// Load environment variables with TSBRIDGE_ prefix
	// This allows overriding any config value via environment
	if err := k.Load(env.Provider("TSBRIDGE_", ".", func(s string) string {
		// Transform TSBRIDGE_TAILSCALE_OAUTH_CLIENT_ID to tailscale.oauth_client_id
		s = strings.TrimPrefix(s, "TSBRIDGE_")
		s = strings.ToLower(s)
		// Replace only the first underscore to separate section from field
		idx := strings.Index(s, "_")
		if idx > 0 {
			return s[:idx] + "." + s[idx+1:]
		}
		return s
	}), nil); err != nil {
		return nil, errors.WrapProviderError(err, provider, errors.ErrTypeConfig, "loading environment variables")
	}

	// Unmarshal into our config struct with proper decoding
	var cfg Config
	decoderConfig := &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			byteSizeDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
			redactedStringDecodeHook(),
		),
		Result:           &cfg,
		WeaklyTypedInput: true,
		TagName:          "mapstructure",
	}

	decoder, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		return nil, errors.WrapProviderError(err, provider, errors.ErrTypeConfig, "creating decoder")
	}

	// Use koanf's Raw() to get the data in the right format for mapstructure
	if err := decoder.Decode(k.Raw()); err != nil {
		return nil, errors.WrapProviderError(err, provider, errors.ErrTypeConfig, "unmarshaling config")
	}

	// Apply standard configuration processing
	if err := ProcessLoadedConfigWithProvider(&cfg, provider); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// - Validation, defaults and normalization
func Load(path string) (*Config, error) {
	return LoadWithProvider(path, "file")
}

// ParseByteSizeString parses a string like "10MB" into bytes
func ParseByteSizeString(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Try to parse as plain number first
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v, nil
	}

	// Try to parse as float with unit suffix
	var value float64
	var unit string

	// Find where the number ends and unit begins
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' || s[i] == ' ') {
		i++
	}

	if i == 0 || i == len(s) {
		return 0, fmt.Errorf("invalid byte size format: %q", s)
	}

	// Parse the numeric part
	numStr := strings.TrimSpace(s[:i])
	unit = strings.TrimSpace(s[i:])

	value, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size format: %q", s)
	}

	if value < 0 {
		return 0, fmt.Errorf("byte size cannot be negative: %q", s)
	}

	// Convert unit to bytes
	var multiplier int64
	switch strings.ToUpper(unit) {
	case "B", "BYTE", "BYTES":
		multiplier = 1
	case "K", "KB":
		multiplier = constants.BytesPerKB
	case "KIB":
		multiplier = constants.BytesPerKB
	case "M", "MB":
		multiplier = constants.BytesPerMB
	case "MIB":
		multiplier = constants.BytesPerMB
	case "G", "GB":
		multiplier = constants.BytesPerGB
	case "GIB":
		multiplier = constants.BytesPerGB
	case "T", "TB":
		multiplier = constants.BytesPerTB
	case "TIB":
		multiplier = constants.BytesPerTB
	default:
		return 0, fmt.Errorf("unknown unit %q in byte size: %q", unit, s)
	}

	return int64(value * float64(multiplier)), nil
}

// byteSizeDecodeHook creates a decode hook for parsing byte sizes into *int64
// It uses a more targeted approach by looking at the mapstructure tag
func byteSizeDecodeHook() mapstructure.DecodeHookFuncType {
	return func(
		f reflect.Type,
		t reflect.Type,
		data any,
	) (any, error) {
		// Only handle *int64 types
		if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Int64 {
			return data, nil
		}

		// Skip if data is nil
		if data == nil {
			return (*int64)(nil), nil
		}

		// Only process string data for byte size fields
		if f.Kind() == reflect.String {
			strData := data.(string)
			if strData == "" {
				return (*int64)(nil), nil
			}

			// Try to parse as byte size only if it contains a unit suffix
			// This helps distinguish from duration strings
			if containsByteSizeUnit(strData) {
				value, err := ParseByteSizeString(strData)
				if err != nil {
					return nil, err
				}
				return &value, nil
			}
		}

		// Handle int/int64/float64 conversion for numeric values
		switch f.Kind() {
		case reflect.Int, reflect.Int64:
			var value int64
			switch v := data.(type) {
			case int:
				value = int64(v)
			case int64:
				value = v
			default:
				return data, nil
			}
			return &value, nil
		case reflect.Float64:
			value := int64(data.(float64))
			return &value, nil
		}

		return data, nil
	}
}

// containsByteSizeUnit checks if a string contains byte size units
func containsByteSizeUnit(s string) bool {
	s = strings.ToUpper(strings.TrimSpace(s))
	// Check for byte size units (but not time units)
	units := []string{"B", "BYTE", "BYTES", "KB", "KIB", "MB", "MIB", "GB", "GIB", "TB", "TIB", "PB", "PIB"}
	for _, unit := range units {
		if strings.HasSuffix(s, unit) {
			return true
		}
	}
	// Also check for plain numbers without units (treated as bytes)
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	return false
}

// redactedStringDecodeHook provides mapstructure hook for RedactedString
func redactedStringDecodeHook() mapstructure.DecodeHookFunc {
	return func(
		from reflect.Type,
		to reflect.Type,
		data any,
	) (any, error) {
		// Check if we're converting to RedactedString
		if to != reflect.TypeOf(RedactedString("")) {
			return data, nil
		}

		// Handle string conversion
		if from.Kind() == reflect.String {
			strData := data.(string)
			return RedactedString(strData), nil
		}

		// Handle nil
		if data == nil {
			return RedactedString(""), nil
		}

		// Invalid input type
		return data, nil
	}
}

// resolveSecrets resolves all secret values from their configured sources
func resolveSecrets(cfg *Config) error {
	// Resolve OAuth Client ID (regular string)
	if cfg.Tailscale.OAuthClientIDEnv != "" || cfg.Tailscale.OAuthClientIDFile != "" {
		cfg.Tailscale.OAuthClientID = ""
		resolved, err := ResolveSecretWithFallback(
			"",
			cfg.Tailscale.OAuthClientIDEnv,
			cfg.Tailscale.OAuthClientIDFile,
			"TS_OAUTH_CLIENT_ID",
		)
		if err != nil {
			return fmt.Errorf("resolving OAuth client ID: %w", err)
		}
		cfg.Tailscale.OAuthClientID = resolved
		cfg.Tailscale.OAuthClientIDEnv = ""
		cfg.Tailscale.OAuthClientIDFile = ""
	} else if cfg.Tailscale.OAuthClientID == "" {
		if val := os.Getenv("TS_OAUTH_CLIENT_ID"); val != "" {
			cfg.Tailscale.OAuthClientID = val
		}
	}

	// Resolve OAuth Client Secret (RedactedString)
	if cfg.Tailscale.OAuthClientSecretEnv != "" || cfg.Tailscale.OAuthClientSecretFile != "" {
		cfg.Tailscale.OAuthClientSecret = ""
		resolved, err := ResolveSecretWithFallback(
			"",
			cfg.Tailscale.OAuthClientSecretEnv,
			cfg.Tailscale.OAuthClientSecretFile,
			"TS_OAUTH_CLIENT_SECRET",
		)
		if err != nil {
			return fmt.Errorf("resolving OAuth client secret: %w", err)
		}
		cfg.Tailscale.OAuthClientSecret = RedactedString(resolved)
		cfg.Tailscale.OAuthClientSecretEnv = ""
		cfg.Tailscale.OAuthClientSecretFile = ""
	} else if cfg.Tailscale.OAuthClientSecret.Value() == "" {
		if val := os.Getenv("TS_OAUTH_CLIENT_SECRET"); val != "" {
			cfg.Tailscale.OAuthClientSecret = RedactedString(val)
		}
	}

	// Resolve Auth Key (RedactedString)
	if cfg.Tailscale.AuthKeyEnv != "" || cfg.Tailscale.AuthKeyFile != "" {
		cfg.Tailscale.AuthKey = ""
		resolved, err := ResolveSecretWithFallback(
			"",
			cfg.Tailscale.AuthKeyEnv,
			cfg.Tailscale.AuthKeyFile,
			"TS_AUTHKEY",
		)
		if err != nil {
			return fmt.Errorf("resolving auth key: %w", err)
		}
		cfg.Tailscale.AuthKey = RedactedString(resolved)
		cfg.Tailscale.AuthKeyEnv = ""
		cfg.Tailscale.AuthKeyFile = ""
	} else if cfg.Tailscale.AuthKey.Value() == "" {
		if val := os.Getenv("TS_AUTHKEY"); val != "" {
			cfg.Tailscale.AuthKey = RedactedString(val)
		}
	}

	// Resolve State Directory (only if state_dir is not already set)
	if cfg.Tailscale.StateDir == "" && cfg.Tailscale.StateDirEnv != "" {
		resolved := os.Getenv(cfg.Tailscale.StateDirEnv)
		if resolved == "" {
			return fmt.Errorf("resolving state directory: environment variable %q is not set", cfg.Tailscale.StateDirEnv)
		}
		cfg.Tailscale.StateDir = resolved
		cfg.Tailscale.StateDirEnv = ""
	}

	return nil
}

// ProcessLoadedConfig applies the standard configuration processing pipeline:
// resolves secrets, sets defaults, normalizes, and validates the configuration.
// This function encapsulates the common pattern used by different configuration providers.
func ProcessLoadedConfig(cfg *Config) error {
	return ProcessLoadedConfigWithProvider(cfg, "unknown")
}

// ProcessLoadedConfigWithProvider applies the standard configuration processing pipeline
// with provider context for better error messages.
func ProcessLoadedConfigWithProvider(cfg *Config, provider string) error {
	// Resolve secrets
	if err := resolveSecrets(cfg); err != nil {
		return errors.WrapProviderError(err, provider, errors.ErrTypeConfig, "resolving secrets")
	}

	// Set defaults
	cfg.SetDefaults()

	// Normalize configuration (copy global values to services)
	cfg.Normalize()

	// Validate the configuration with provider context
	if err := cfg.Validate(provider); err != nil {
		return errors.WrapProviderError(err, provider, errors.ErrTypeConfig, "validating config")
	}

	return nil
}

// SetDefaults sets default values for any unspecified configuration
func (c *Config) SetDefaults() {
	// Set global defaults if not specified
	if c.Global.ReadHeaderTimeout == nil {
		defaultTimeout := constants.DefaultReadHeaderTimeout
		c.Global.ReadHeaderTimeout = &defaultTimeout
	}
	if c.Global.WriteTimeout == nil {
		defaultTimeout := constants.DefaultWriteTimeout
		c.Global.WriteTimeout = &defaultTimeout
	}
	if c.Global.IdleTimeout == nil {
		defaultTimeout := constants.DefaultIdleTimeout
		c.Global.IdleTimeout = &defaultTimeout
	}
	if c.Global.ShutdownTimeout == nil {
		defaultTimeout := constants.DefaultShutdownTimeout
		c.Global.ShutdownTimeout = &defaultTimeout
	}

	// Default access_log to true if not specified
	if c.Global.AccessLog == nil {
		enabled := constants.DefaultAccessLogEnabled
		c.Global.AccessLog = &enabled
	}

	// Default max request body size if not specified
	if c.Global.MaxRequestBodySize == nil {
		defaultSize := int64(constants.DefaultMaxRequestBodySize)
		c.Global.MaxRequestBodySize = &defaultSize
	}

	// Set transport timeout defaults if not specified
	if c.Global.DialTimeout == nil {
		defaultTimeout := constants.DefaultDialTimeout
		c.Global.DialTimeout = &defaultTimeout
	}
	if c.Global.KeepAliveTimeout == nil {
		defaultTimeout := constants.DefaultKeepAliveTimeout
		c.Global.KeepAliveTimeout = &defaultTimeout
	}
	if c.Global.IdleConnTimeout == nil {
		defaultTimeout := constants.DefaultIdleConnTimeout
		c.Global.IdleConnTimeout = &defaultTimeout
	}
	if c.Global.TLSHandshakeTimeout == nil {
		defaultTimeout := constants.DefaultTLSHandshakeTimeout
		c.Global.TLSHandshakeTimeout = &defaultTimeout
	}
	if c.Global.ExpectContinueTimeout == nil {
		defaultTimeout := constants.DefaultExpectContinueTimeout
		c.Global.ExpectContinueTimeout = &defaultTimeout
	}
	if c.Global.MetricsReadHeaderTimeout == nil {
		defaultTimeout := constants.DefaultMetricsReadHeaderTimeout
		c.Global.MetricsReadHeaderTimeout = &defaultTimeout
	}

	// Set service defaults
	for i := range c.Services {
		svc := &c.Services[i]

		// Default whois_enabled to false if not specified
		if svc.WhoisEnabled == nil {
			enabled := constants.DefaultWhoisEnabled
			svc.WhoisEnabled = &enabled
		}

		// Default whois_timeout to 5 seconds if not specified
		if svc.WhoisTimeout == nil {
			defaultTimeout := constants.DefaultWhoisTimeout
			svc.WhoisTimeout = &defaultTimeout
		}

		// Default tls_mode to "auto" if not specified
		if svc.TLSMode == "" {
			svc.TLSMode = constants.DefaultTLSMode
		}
	}
}

// Normalize resolves all configuration values by copying global defaults to services
// that haven't specified their own values. This ensures all timeout values are fully
// resolved before the config is used, eliminating the need for getter methods.
func (c *Config) Normalize() {
	// Copy global timeouts to services that don't have them set
	for i := range c.Services {
		svc := &c.Services[i]

		// Only copy if the service value is nil
		if svc.ReadHeaderTimeout == nil && c.Global.ReadHeaderTimeout != nil {
			// Copy the value, not the pointer
			timeout := *c.Global.ReadHeaderTimeout
			svc.ReadHeaderTimeout = &timeout
		}
		if svc.WriteTimeout == nil && c.Global.WriteTimeout != nil {
			timeout := *c.Global.WriteTimeout
			svc.WriteTimeout = &timeout
		}
		if svc.IdleTimeout == nil && c.Global.IdleTimeout != nil {
			timeout := *c.Global.IdleTimeout
			svc.IdleTimeout = &timeout
		}
		if svc.ResponseHeaderTimeout == nil && c.Global.ResponseHeaderTimeout != nil {
			timeout := *c.Global.ResponseHeaderTimeout
			svc.ResponseHeaderTimeout = &timeout
		}

		// Copy access log setting if not set
		if svc.AccessLog == nil {
			svc.AccessLog = c.Global.AccessLog
		}

		// Copy flush interval if not set
		if svc.FlushInterval == nil && c.Global.FlushInterval != nil {
			interval := *c.Global.FlushInterval
			svc.FlushInterval = &interval
		}

		// Copy max request body size if not set
		if svc.MaxRequestBodySize == nil && c.Global.MaxRequestBodySize != nil {
			size := *c.Global.MaxRequestBodySize
			svc.MaxRequestBodySize = &size
		}

		// Copy tags if not set
		if svc.Tags == nil && c.Tailscale.DefaultTags != nil {
			// Make a copy to prevent services from modifying the global default slice
			svc.Tags = make([]string, len(c.Tailscale.DefaultTags))
			copy(svc.Tags, c.Tailscale.DefaultTags)
		}
	}
}

// Validate validates the configuration with provider context
func (c *Config) Validate(provider string) error {
	// Validate OAuth credentials
	if err := c.validateOAuth(); err != nil {
		return err
	}

	// Validate global settings
	if err := c.validateGlobal(); err != nil {
		return err
	}

	// Validate services - Docker provider allows zero services at startup
	if len(c.Services) == 0 && provider != "docker" {
		return errors.NewValidationError("at least one service must be defined in the [[services]] array")
	}

	// Check for duplicate service names
	seen := make(map[string]bool)
	for i, svc := range c.Services {
		if svc.Name == "" {
			return errors.NewValidationError(fmt.Sprintf("service[%d]: service name is required", i))
		}
		if seen[svc.Name] {
			return errors.NewValidationError(fmt.Sprintf("duplicate service name: %q", svc.Name))
		}
		seen[svc.Name] = true

		if err := c.validateService(&c.Services[i]); err != nil {
			return errors.WrapValidation(err, fmt.Sprintf("service %q", svc.Name))
		}
	}

	return nil
}

// validateOAuthSources validates OAuth authentication configuration
func validateOAuthSources(ts Tailscale) error {
	if ts.OAuthClientID == "" {
		return errors.NewValidationError("OAuth client ID must be provided")
	}
	if ts.OAuthClientSecret.Value() == "" {
		return errors.NewValidationError("OAuth client secret must be provided")
	}
	return nil
}

// validateAuthMethodSelection ensures only one auth method is configured
func validateAuthMethodSelection(ts Tailscale) error {
	hasAuthKey := ts.AuthKey.Value() != ""
	hasOAuthID := ts.OAuthClientID != ""
	hasOAuthSecret := ts.OAuthClientSecret.Value() != ""

	if hasAuthKey && (hasOAuthID || hasOAuthSecret) {
		return errors.NewValidationError("cannot specify both OAuth and AuthKey credentials")
	}
	return nil
}

func (c *Config) validateOAuth() error {
	// First check for conflicting auth methods
	if err := validateAuthMethodSelection(c.Tailscale); err != nil {
		return err
	}

	// If an auth key is provided, auth validation is complete
	if c.Tailscale.AuthKey.Value() != "" {
		return nil
	}

	// Otherwise, we must have valid OAuth credentials
	return validateOAuthSources(c.Tailscale)
}

// validateTimeout validates a timeout duration field.
// Most timeouts cannot be negative, but FlushInterval allows -1ms for immediate flushing.
func validateTimeout(name string, d *time.Duration, allowNegativeOne bool) error {
	if d == nil {
		return nil // nil is valid - will use default
	}

	if allowNegativeOne && *d == constants.ImmediateFlushInterval {
		return nil // -1ms is valid for immediate flushing
	}

	if *d < 0 {
		if allowNegativeOne {
			return errors.NewValidationError(fmt.Sprintf("%s can only be %v for immediate flushing", name, constants.ImmediateFlushInterval))
		}
		return errors.NewValidationError(fmt.Sprintf("%s cannot be negative", name))
	}

	return nil
}

// validateTimeoutPositive validates that a timeout is positive (> 0).
// Used for timeouts that must be positive, like ShutdownTimeout.
func validateTimeoutPositive(name string, d *time.Duration) error {
	if d == nil {
		return nil // nil is valid - will use default
	}

	if *d <= 0 {
		return errors.NewValidationError(fmt.Sprintf("%s must be positive", name))
	}

	return nil
}

func (c *Config) validateGlobal() error {
	// Validate server timeouts
	if err := validateTimeout("read_header_timeout", c.Global.ReadHeaderTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("write_timeout", c.Global.WriteTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("idle_timeout", c.Global.IdleTimeout, false); err != nil {
		return err
	}
	if err := validateTimeoutPositive("shutdown_timeout", c.Global.ShutdownTimeout); err != nil {
		return err
	}

	// Validate response timeout
	if err := validateTimeout("response_header_timeout", c.Global.ResponseHeaderTimeout, false); err != nil {
		return err
	}

	// Validate transport timeouts
	if err := validateTimeout("dial_timeout", c.Global.DialTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("keep_alive_timeout", c.Global.KeepAliveTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("idle_conn_timeout", c.Global.IdleConnTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("tls_handshake_timeout", c.Global.TLSHandshakeTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("expect_continue_timeout", c.Global.ExpectContinueTimeout, false); err != nil {
		return err
	}

	// Validate metrics timeout
	if err := validateTimeout("metrics_read_header_timeout", c.Global.MetricsReadHeaderTimeout, false); err != nil {
		return err
	}

	// Validate flush interval (allows -1ms)
	if err := validateTimeout("flush_interval", c.Global.FlushInterval, true); err != nil {
		return err
	}

	// Validate metrics address if provided
	if c.Global.MetricsAddr != "" {
		if _, err := net.ResolveTCPAddr("tcp", c.Global.MetricsAddr); err != nil {
			return errors.WrapValidation(err, fmt.Sprintf("invalid metrics address %q", c.Global.MetricsAddr))
		}
	}

	// Validate trusted proxies
	for _, proxy := range c.Global.TrustedProxies {
		if strings.Contains(proxy, "/") {
			// CIDR range
			_, _, err := net.ParseCIDR(proxy)
			if err != nil {
				return errors.WrapValidation(err, fmt.Sprintf("invalid trusted proxy CIDR %q", proxy))
			}
		} else {
			// Single IP address
			ip := net.ParseIP(proxy)
			if ip == nil {
				return errors.NewValidationError(fmt.Sprintf("invalid trusted proxy IP %q", proxy))
			}
		}
	}

	return nil
}

func (c *Config) validateService(svc *Service) error {
	if svc.BackendAddr == "" {
		return errors.NewValidationError("backend address is required")
	}

	// Validate backend address format
	if strings.HasPrefix(svc.BackendAddr, "unix://") {
		// Unix socket - just check it has a path
		if len(svc.BackendAddr) <= 7 { // len("unix://") == 7
			return errors.NewValidationError("invalid unix socket address: missing path")
		}
	} else {
		// TCP address
		if _, err := net.ResolveTCPAddr("tcp", svc.BackendAddr); err != nil {
			return errors.WrapValidation(err, fmt.Sprintf("invalid backend address %q", svc.BackendAddr))
		}
	}

	// Validate whois timeout if whois is enabled
	if svc.WhoisEnabled == nil || *svc.WhoisEnabled {
		if err := validateTimeout("whois_timeout", svc.WhoisTimeout, false); err != nil {
			return err
		}
	}

	// Validate TLS mode (only if set)
	if svc.TLSMode != "" {
		switch svc.TLSMode {
		case "auto", "off":
			// Valid values
		default:
			return errors.NewValidationError(fmt.Sprintf("invalid tls_mode %q: must be 'auto' or 'off'", svc.TLSMode))
		}
	}

	// Validate service-level timeout overrides
	if err := validateTimeout("read_header_timeout", svc.ReadHeaderTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("write_timeout", svc.WriteTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("idle_timeout", svc.IdleTimeout, false); err != nil {
		return err
	}
	if err := validateTimeout("response_header_timeout", svc.ResponseHeaderTimeout, false); err != nil {
		return err
	}

	// Validate flush interval (allows -1ms)
	if err := validateTimeout("flush_interval", svc.FlushInterval, true); err != nil {
		return err
	}

	// Validate tags when using OAuth
	if c.Tailscale.OAuthClientID != "" || c.Tailscale.OAuthClientSecret.Value() != "" {
		if len(svc.Tags) == 0 {
			return errors.NewValidationError("service must have at least one tag when using OAuth authentication")
		}
	}

	return nil
}

// String returns a string representation of the Tailscale config with secrets redacted
func (t Tailscale) String() string {
	var b strings.Builder
	b.WriteString("Tailscale:\n")

	// OAuth Client ID (not sensitive)
	b.WriteString(fmt.Sprintf("  OAuthClientID: %s\n", t.OAuthClientID))
	b.WriteString(fmt.Sprintf("  OAuthClientIDEnv: %s\n", t.OAuthClientIDEnv))
	b.WriteString(fmt.Sprintf("  OAuthClientIDFile: %s\n", t.OAuthClientIDFile))

	// OAuth Client Secret (only the actual value is sensitive)
	if t.OAuthClientSecret.Value() != "" {
		b.WriteString("  OAuthClientSecret: [REDACTED]\n")
	} else {
		b.WriteString("  OAuthClientSecret: \n")
	}
	b.WriteString(fmt.Sprintf("  OAuthClientSecretEnv: %s\n", t.OAuthClientSecretEnv))
	b.WriteString(fmt.Sprintf("  OAuthClientSecretFile: %s\n", t.OAuthClientSecretFile))

	// Auth Key (only the actual value is sensitive)
	if t.AuthKey.Value() != "" {
		b.WriteString("  AuthKey: [REDACTED]\n")
	} else {
		b.WriteString("  AuthKey: \n")
	}
	b.WriteString(fmt.Sprintf("  AuthKeyEnv: %s\n", t.AuthKeyEnv))
	b.WriteString(fmt.Sprintf("  AuthKeyFile: %s\n", t.AuthKeyFile))

	// State Directory (not sensitive)
	b.WriteString(fmt.Sprintf("  StateDir: %s\n", t.StateDir))
	b.WriteString(fmt.Sprintf("  StateDirEnv: %s\n", t.StateDirEnv))

	// Default Tags (not sensitive)
	b.WriteString(fmt.Sprintf("  DefaultTags: %v\n", t.DefaultTags))

	return b.String()
}

// String returns a string representation of the Config with secrets redacted
func (c *Config) String() string {
	redacted := c.Redacted()
	data, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return fmt.Sprintf("Config{error: %v}", err)
	}
	return string(data)
}
