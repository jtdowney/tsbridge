package config

import (
	"encoding/json"
	"fmt"
)

// RedactedString is a string type that masks its value in logs and string representations
type RedactedString string

// String implements the Stringer interface, always returning [REDACTED]
func (r RedactedString) String() string {
	return "[REDACTED]"
}

// MarshalJSON implements json.Marshaler, always returning "[REDACTED]"
func (r RedactedString) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED]")
}

// Format implements fmt.Formatter to handle all format verbs safely
func (r RedactedString) Format(f fmt.State, verb rune) {
	fmt.Fprint(f, "[REDACTED]")
}

// Value returns the actual string value for internal use
func (r RedactedString) Value() string {
	return string(r)
}

// RedactedTailscale is a version of Tailscale config with sensitive fields redacted
type RedactedTailscale struct {
	OAuthClientID         string   `json:"oauth_client_id,omitempty"`
	OAuthClientIDEnv      string   `json:"oauth_client_id_env,omitempty"`
	OAuthClientIDFile     string   `json:"oauth_client_id_file,omitempty"`
	OAuthClientSecret     string   `json:"oauth_client_secret,omitempty"`
	OAuthClientSecretEnv  string   `json:"oauth_client_secret_env,omitempty"`
	OAuthClientSecretFile string   `json:"oauth_client_secret_file,omitempty"`
	AuthKey               string   `json:"auth_key,omitempty"`
	AuthKeyEnv            string   `json:"auth_key_env,omitempty"`
	AuthKeyFile           string   `json:"auth_key_file,omitempty"`
	StateDir              string   `json:"state_dir,omitempty"`
	DefaultTags           []string `json:"default_tags,omitempty"`
}

// RedactedConfig is a version of Config with sensitive fields redacted for safe logging
type RedactedConfig struct {
	Tailscale RedactedTailscale `json:"tailscale"`
	Global    Global            `json:"global"`
	Services  []Service         `json:"services"`
}

// Redacted returns a copy of the Config with sensitive fields masked
func (c *Config) Redacted() *RedactedConfig {
	if c == nil {
		return nil
	}

	redacted := &RedactedConfig{
		Tailscale: RedactedTailscale{
			OAuthClientID:         c.Tailscale.OAuthClientID,
			OAuthClientIDEnv:      c.Tailscale.OAuthClientIDEnv,
			OAuthClientIDFile:     c.Tailscale.OAuthClientIDFile,
			OAuthClientSecretEnv:  c.Tailscale.OAuthClientSecretEnv,
			OAuthClientSecretFile: c.Tailscale.OAuthClientSecretFile,
			AuthKeyEnv:            c.Tailscale.AuthKeyEnv,
			AuthKeyFile:           c.Tailscale.AuthKeyFile,
			StateDir:              c.Tailscale.StateDir,
			DefaultTags:           c.Tailscale.DefaultTags,
		},
		Global:   c.Global,
		Services: make([]Service, len(c.Services)),
	}

	// Redact sensitive values
	if c.Tailscale.OAuthClientSecret.Value() != "" {
		redacted.Tailscale.OAuthClientSecret = "[REDACTED]"
	}
	if c.Tailscale.AuthKey.Value() != "" {
		redacted.Tailscale.AuthKey = "[REDACTED]"
	}

	// Copy services (they don't contain sensitive data currently)
	copy(redacted.Services, c.Services)

	return redacted
}

// String implements Stringer for RedactedConfig to ensure safe string representation
func (r *RedactedConfig) String() string {
	// Use JSON representation for consistent formatting
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf("RedactedConfig{error: %v}", err)
	}
	return string(data)
}
