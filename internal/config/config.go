// Package config handles configuration parsing and validation for tsbridge.
package config

import (
	"fmt"
	"net"
	"os"
	"reflect"
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
	Tailscale Tailscale `mapstructure:"tailscale"`
	Global    Global    `mapstructure:"global"`
	Services  []Service `mapstructure:"services"`
}

// Tailscale contains Tailscale-specific configuration
type Tailscale struct {
	OAuthClientID         string   `mapstructure:"oauth_client_id"`
	OAuthClientIDEnv      string   `mapstructure:"oauth_client_id_env"`
	OAuthClientIDFile     string   `mapstructure:"oauth_client_id_file"`
	OAuthClientSecret     string   `mapstructure:"oauth_client_secret"`
	OAuthClientSecretEnv  string   `mapstructure:"oauth_client_secret_env"`
	OAuthClientSecretFile string   `mapstructure:"oauth_client_secret_file"`
	AuthKey               string   `mapstructure:"auth_key"`
	AuthKeyEnv            string   `mapstructure:"auth_key_env"`
	AuthKeyFile           string   `mapstructure:"auth_key_file"`
	OAuthTags             []string `mapstructure:"oauth_tags"`
	StateDir              string   `mapstructure:"state_dir"`
}

// Global contains global default settings
type Global struct {
	ReadHeaderTimeout     Duration `mapstructure:"read_header_timeout"`
	WriteTimeout          Duration `mapstructure:"write_timeout"`
	IdleTimeout           Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout       Duration `mapstructure:"shutdown_timeout"`
	ResponseHeaderTimeout Duration `mapstructure:"response_header_timeout"`
	MetricsAddr           string   `mapstructure:"metrics_addr"`
	AccessLog             *bool    `mapstructure:"access_log"`      // Enable access logging (default: true)
	TrustedProxies        []string `mapstructure:"trusted_proxies"` // List of trusted proxy IPs or CIDR ranges
	FlushInterval         Duration `mapstructure:"flush_interval"`  // Time between flushes (-1ms for immediate)
	// Transport timeouts
	DialTimeout              Duration `mapstructure:"dial_timeout"`
	KeepAliveTimeout         Duration `mapstructure:"keep_alive_timeout"`
	IdleConnTimeout          Duration `mapstructure:"idle_conn_timeout"`
	TLSHandshakeTimeout      Duration `mapstructure:"tls_handshake_timeout"`
	ExpectContinueTimeout    Duration `mapstructure:"expect_continue_timeout"`
	MetricsReadHeaderTimeout Duration `mapstructure:"metrics_read_header_timeout"`
}

// Service represents a single service configuration
type Service struct {
	Name         string   `mapstructure:"name"`
	BackendAddr  string   `mapstructure:"backend_addr"`
	WhoisEnabled *bool    `mapstructure:"whois_enabled"`
	WhoisTimeout Duration `mapstructure:"whois_timeout"`
	TLSMode      string   `mapstructure:"tls_mode"` // "auto" (default), "off"
	// Optional overrides
	ReadHeaderTimeout     Duration `mapstructure:"read_header_timeout"`
	WriteTimeout          Duration `mapstructure:"write_timeout"`
	IdleTimeout           Duration `mapstructure:"idle_timeout"`
	ResponseHeaderTimeout Duration `mapstructure:"response_header_timeout"`
	AccessLog             *bool    `mapstructure:"access_log"`     // Override global access_log setting
	FunnelEnabled         *bool    `mapstructure:"funnel_enabled"` // Expose service via Tailscale Funnel
	Ephemeral             bool     `mapstructure:"ephemeral"`      // Create ephemeral nodes
	FlushInterval         Duration `mapstructure:"flush_interval"` // Time between flushes (-1ms for immediate)
	// Header manipulation
	UpstreamHeaders   map[string]string `mapstructure:"upstream_headers"`   // Headers to add to upstream requests
	DownstreamHeaders map[string]string `mapstructure:"downstream_headers"` // Headers to add to downstream responses
	RemoveUpstream    []string          `mapstructure:"remove_upstream"`    // Headers to remove from upstream requests
	RemoveDownstream  []string          `mapstructure:"remove_downstream"`  // Headers to remove from downstream responses
}

// Duration wraps time.Duration for TOML unmarshaling
type Duration struct {
	time.Duration
}

// UnmarshalText implements encoding.TextUnmarshaler for Duration
func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
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
			mapstructure.StringToTimeDurationHookFunc(),
			durationDecodeHook(),
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

// durationDecodeHook creates a decode hook for the Duration type
func durationDecodeHook() mapstructure.DecodeHookFunc {
	return func(
		from reflect.Type,
		to reflect.Type,
		data interface{},
	) (interface{}, error) {
		// Check if we're converting to Duration
		if to != reflect.TypeOf(Duration{}) {
			return data, nil
		}

		// Handle string conversion
		if from.Kind() == reflect.String {
			strData := data.(string)
			if strData == "" {
				return Duration{}, nil
			}
			d, err := time.ParseDuration(strData)
			if err != nil {
				return nil, err
			}
			return Duration{Duration: d}, nil
		}

		// Handle time.Duration conversion
		if from == reflect.TypeOf(time.Duration(0)) {
			return Duration{Duration: data.(time.Duration)}, nil
		}

		// Handle int64 conversion (nanoseconds)
		if from.Kind() == reflect.Int64 {
			return Duration{Duration: time.Duration(data.(int64))}, nil
		}

		return data, nil
	}
}

// resolveSecrets resolves all secret values from their configured sources
func resolveSecrets(cfg *Config) error {
	// Define secret configurations
	type secretConfig struct {
		value       *string
		envVar      string
		fileVar     string
		fallbackEnv string
		fieldName   string
		clearEnv    *string
		clearFile   *string
	}

	secrets := []secretConfig{
		{
			value:       &cfg.Tailscale.OAuthClientID,
			envVar:      cfg.Tailscale.OAuthClientIDEnv,
			fileVar:     cfg.Tailscale.OAuthClientIDFile,
			fallbackEnv: "TS_OAUTH_CLIENT_ID",
			fieldName:   "OAuth client ID",
			clearEnv:    &cfg.Tailscale.OAuthClientIDEnv,
			clearFile:   &cfg.Tailscale.OAuthClientIDFile,
		},
		{
			value:       &cfg.Tailscale.OAuthClientSecret,
			envVar:      cfg.Tailscale.OAuthClientSecretEnv,
			fileVar:     cfg.Tailscale.OAuthClientSecretFile,
			fallbackEnv: "TS_OAUTH_CLIENT_SECRET",
			fieldName:   "OAuth client secret",
			clearEnv:    &cfg.Tailscale.OAuthClientSecretEnv,
			clearFile:   &cfg.Tailscale.OAuthClientSecretFile,
		},
		{
			value:       &cfg.Tailscale.AuthKey,
			envVar:      cfg.Tailscale.AuthKeyEnv,
			fileVar:     cfg.Tailscale.AuthKeyFile,
			fallbackEnv: "TS_AUTHKEY",
			fieldName:   "auth key",
			clearEnv:    &cfg.Tailscale.AuthKeyEnv,
			clearFile:   &cfg.Tailscale.AuthKeyFile,
		},
	}

	// Process each secret
	for _, secret := range secrets {
		if secret.envVar != "" || secret.fileVar != "" {
			// Clear the direct value to avoid conflicts
			*secret.value = ""

			resolved, err := ResolveSecretWithFallback(
				"", // No direct value
				secret.envVar,
				secret.fileVar,
				secret.fallbackEnv,
			)
			if err != nil {
				return fmt.Errorf("resolving %s: %w", secret.fieldName, err)
			}
			*secret.value = resolved

			// Clear the env/file fields after resolution
			if secret.clearEnv != nil {
				*secret.clearEnv = ""
			}
			if secret.clearFile != nil {
				*secret.clearFile = ""
			}
		} else if *secret.value == "" {
			// If no secrets are configured at all, check fallback env var
			if val := os.Getenv(secret.fallbackEnv); val != "" {
				*secret.value = val
			}
		}
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

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return errors.WrapProviderError(err, provider, errors.ErrTypeConfig, "validating config")
	}

	return nil
}

// SetDefaults sets default values for any unspecified configuration
func (c *Config) SetDefaults() {
	// Set global defaults if not specified
	if c.Global.ReadHeaderTimeout.Duration == 0 {
		c.Global.ReadHeaderTimeout.Duration = constants.DefaultReadHeaderTimeout
	}
	if c.Global.WriteTimeout.Duration == 0 {
		c.Global.WriteTimeout.Duration = constants.DefaultWriteTimeout
	}
	if c.Global.IdleTimeout.Duration == 0 {
		c.Global.IdleTimeout.Duration = constants.DefaultIdleTimeout
	}
	if c.Global.ShutdownTimeout.Duration == 0 {
		c.Global.ShutdownTimeout.Duration = constants.DefaultShutdownTimeout
	}

	// Default access_log to true if not specified
	if c.Global.AccessLog == nil {
		enabled := constants.DefaultAccessLogEnabled
		c.Global.AccessLog = &enabled
	}

	// Set transport timeout defaults if not specified
	if c.Global.DialTimeout.Duration == 0 {
		c.Global.DialTimeout.Duration = constants.DefaultDialTimeout
	}
	if c.Global.KeepAliveTimeout.Duration == 0 {
		c.Global.KeepAliveTimeout.Duration = constants.DefaultKeepAliveTimeout
	}
	if c.Global.IdleConnTimeout.Duration == 0 {
		c.Global.IdleConnTimeout.Duration = constants.DefaultIdleConnTimeout
	}
	if c.Global.TLSHandshakeTimeout.Duration == 0 {
		c.Global.TLSHandshakeTimeout.Duration = constants.DefaultTLSHandshakeTimeout
	}
	if c.Global.ExpectContinueTimeout.Duration == 0 {
		c.Global.ExpectContinueTimeout.Duration = constants.DefaultExpectContinueTimeout
	}
	if c.Global.MetricsReadHeaderTimeout.Duration == 0 {
		c.Global.MetricsReadHeaderTimeout.Duration = constants.DefaultMetricsReadHeaderTimeout
	}

	// Set service defaults
	for i := range c.Services {
		svc := &c.Services[i]

		// Default whois_enabled to true if not specified
		if svc.WhoisEnabled == nil {
			enabled := constants.DefaultWhoisEnabled
			svc.WhoisEnabled = &enabled
		}

		// Default whois_timeout to 5 seconds if not specified
		if svc.WhoisTimeout.Duration == 0 {
			svc.WhoisTimeout.Duration = constants.DefaultWhoisTimeout
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

		// Only copy if the service value is zero (not set)
		if svc.ReadHeaderTimeout.Duration == 0 {
			svc.ReadHeaderTimeout = c.Global.ReadHeaderTimeout
		}
		if svc.WriteTimeout.Duration == 0 {
			svc.WriteTimeout = c.Global.WriteTimeout
		}
		if svc.IdleTimeout.Duration == 0 {
			svc.IdleTimeout = c.Global.IdleTimeout
		}
		if svc.ResponseHeaderTimeout.Duration == 0 {
			svc.ResponseHeaderTimeout = c.Global.ResponseHeaderTimeout
		}

		// Copy access log setting if not set
		if svc.AccessLog == nil {
			svc.AccessLog = c.Global.AccessLog
		}

		// Copy flush interval if not set
		if svc.FlushInterval.Duration == 0 {
			svc.FlushInterval = c.Global.FlushInterval
		}
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate OAuth credentials
	if err := c.validateOAuth(); err != nil {
		return err
	}

	// Validate global settings
	if err := c.validateGlobal(); err != nil {
		return err
	}

	// Validate services
	if len(c.Services) == 0 {
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

// validateAuthKeySources validates AuthKey authentication configuration
func validateAuthKeySources(ts Tailscale) error {
	if ts.AuthKey != "" && len(ts.OAuthTags) > 0 {
		return errors.NewValidationError("oauth_tags can only be used with OAuth authentication")
	}

	return nil
}

// validateOAuthSources validates OAuth authentication configuration
func validateOAuthSources(ts Tailscale) error {
	if ts.OAuthClientID == "" {
		return errors.NewValidationError("OAuth client ID must be provided")
	}
	if ts.OAuthClientSecret == "" {
		return errors.NewValidationError("OAuth client secret must be provided")
	}
	return nil
}

// validateAuthMethodSelection ensures only one auth method is configured
func validateAuthMethodSelection(ts Tailscale) error {
	hasAuthKey := ts.AuthKey != ""
	hasOAuthID := ts.OAuthClientID != ""
	hasOAuthSecret := ts.OAuthClientSecret != ""

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

	// Determine which auth method is being used
	hasAuthKey := c.Tailscale.AuthKey != ""
	hasOAuth := c.Tailscale.OAuthClientID != "" || c.Tailscale.OAuthClientSecret != ""

	// Validate based on the auth method
	if hasAuthKey {
		return validateAuthKeySources(c.Tailscale)
	}

	if hasOAuth || (!hasAuthKey && !hasOAuth) {
		// If OAuth is being used, or if no auth is configured yet,
		// validate OAuth (which will require both ID and secret)
		return validateOAuthSources(c.Tailscale)
	}

	return nil
}

func (c *Config) validateGlobal() error {
	if c.Global.ReadHeaderTimeout.Duration <= 0 {
		return errors.NewValidationError("read_header_timeout must be positive")
	}
	if c.Global.WriteTimeout.Duration <= 0 {
		return errors.NewValidationError("write_timeout must be positive")
	}
	if c.Global.IdleTimeout.Duration <= 0 {
		return errors.NewValidationError("idle_timeout must be positive")
	}
	if c.Global.ShutdownTimeout.Duration <= 0 {
		return errors.NewValidationError("shutdown_timeout must be positive")
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
		if svc.WhoisTimeout.Duration < 0 {
			return errors.NewValidationError("whois_timeout must be non-negative")
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

	// Validate service-level overrides if provided
	if svc.ReadHeaderTimeout.Duration < 0 {
		return errors.NewValidationError("read_header_timeout must be non-negative")
	}
	if svc.WriteTimeout.Duration < 0 {
		return errors.NewValidationError("write_timeout must be non-negative")
	}
	if svc.IdleTimeout.Duration < 0 {
		return errors.NewValidationError("idle_timeout must be non-negative")
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
	if t.OAuthClientSecret != "" {
		b.WriteString("  OAuthClientSecret: [REDACTED]\n")
	} else {
		b.WriteString("  OAuthClientSecret: \n")
	}
	b.WriteString(fmt.Sprintf("  OAuthClientSecretEnv: %s\n", t.OAuthClientSecretEnv))
	b.WriteString(fmt.Sprintf("  OAuthClientSecretFile: %s\n", t.OAuthClientSecretFile))

	// Auth Key (only the actual value is sensitive)
	if t.AuthKey != "" {
		b.WriteString("  AuthKey: [REDACTED]\n")
	} else {
		b.WriteString("  AuthKey: \n")
	}
	b.WriteString(fmt.Sprintf("  AuthKeyEnv: %s\n", t.AuthKeyEnv))
	b.WriteString(fmt.Sprintf("  AuthKeyFile: %s\n", t.AuthKeyFile))

	// OAuth Tags (not sensitive)
	b.WriteString(fmt.Sprintf("  OAuthTags: %v\n", t.OAuthTags))

	// State Directory (not sensitive)
	b.WriteString(fmt.Sprintf("  StateDir: %s\n", t.StateDir))

	return b.String()
}

// String returns a string representation of the Config with secrets redacted
func (c *Config) String() string {
	var b strings.Builder

	// Tailscale section
	b.WriteString(c.Tailscale.String())

	// Global section
	b.WriteString("\nGlobal:\n")
	b.WriteString(fmt.Sprintf("  ReadHeaderTimeout: %s\n", c.Global.ReadHeaderTimeout.Duration))
	b.WriteString(fmt.Sprintf("  WriteTimeout: %s\n", c.Global.WriteTimeout.Duration))
	b.WriteString(fmt.Sprintf("  IdleTimeout: %s\n", c.Global.IdleTimeout.Duration))
	b.WriteString(fmt.Sprintf("  ResponseHeaderTimeout: %s\n", c.Global.ResponseHeaderTimeout.Duration))
	b.WriteString(fmt.Sprintf("  ShutdownTimeout: %s\n", c.Global.ShutdownTimeout.Duration))
	b.WriteString(fmt.Sprintf("  MetricsAddr: %s\n", c.Global.MetricsAddr))
	if c.Global.AccessLog != nil {
		b.WriteString(fmt.Sprintf("  AccessLog: %t\n", *c.Global.AccessLog))
	}
	if len(c.Global.TrustedProxies) > 0 {
		b.WriteString(fmt.Sprintf("  TrustedProxies: %v\n", c.Global.TrustedProxies))
	}

	// Services section
	b.WriteString("\nServices:\n")
	for _, svc := range c.Services {
		b.WriteString(fmt.Sprintf("  - Name: %s\n", svc.Name))
		b.WriteString(fmt.Sprintf("    BackendAddr: %s\n", svc.BackendAddr))
		if svc.WhoisEnabled != nil {
			b.WriteString(fmt.Sprintf("    WhoisEnabled: %t\n", *svc.WhoisEnabled))
		}
		b.WriteString(fmt.Sprintf("    WhoisTimeout: %s\n", svc.WhoisTimeout.Duration))
		if svc.TLSMode != "" {
			b.WriteString(fmt.Sprintf("    TLSMode: %s\n", svc.TLSMode))
		}
		// Add service-level overrides if set
		if svc.ReadHeaderTimeout.Duration > 0 {
			b.WriteString(fmt.Sprintf("    ReadHeaderTimeout: %s\n", svc.ReadHeaderTimeout.Duration))
		}
		if svc.WriteTimeout.Duration > 0 {
			b.WriteString(fmt.Sprintf("    WriteTimeout: %s\n", svc.WriteTimeout.Duration))
		}
		if svc.IdleTimeout.Duration > 0 {
			b.WriteString(fmt.Sprintf("    IdleTimeout: %s\n", svc.IdleTimeout.Duration))
		}
		if svc.ResponseHeaderTimeout.Duration > 0 {
			b.WriteString(fmt.Sprintf("    ResponseHeaderTimeout: %s\n", svc.ResponseHeaderTimeout.Duration))
		}
		if svc.AccessLog != nil {
			b.WriteString(fmt.Sprintf("    AccessLog: %t\n", *svc.AccessLog))
		}
	}

	return b.String()
}
