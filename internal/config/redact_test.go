package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRedactedString(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{
			name:     "empty string",
			value:    "",
			expected: "[REDACTED]",
		},
		{
			name:     "non-empty string",
			value:    "super-secret-value",
			expected: "[REDACTED]",
		},
		{
			name:     "string with special characters",
			value:    "secret!@#$%^&*()",
			expected: "[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := RedactedString(tt.value)
			if r.String() != tt.expected {
				t.Errorf("RedactedString.String() = %v, want %v", r.String(), tt.expected)
			}
		})
	}
}

func TestRedactedString_MarshalJSON(t *testing.T) {
	r := RedactedString("secret-value")

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	if string(data) != `"[REDACTED]"` {
		t.Errorf("MarshalJSON() = %s, want %s", string(data), `"[REDACTED]"`)
	}
}

func TestRedactedString_Format(t *testing.T) {
	r := RedactedString("secret-value")

	// Test various format verbs
	formats := []string{"%s", "%v", "%q", "%#v"}
	for _, format := range formats {
		result := fmt.Sprintf(format, r)
		if strings.Contains(result, "secret-value") {
			t.Errorf("Format %s leaked secret value: %s", format, result)
		}
	}
}

func TestRedactedConfig(t *testing.T) {
	// Test that RedactedConfig properly redacts sensitive fields
	cfg := &Config{
		Tailscale: Tailscale{
			OAuthClientID:     "client-id",
			OAuthClientSecret: "super-secret",
			AuthKey:           "tskey-auth-xxx",
			StateDir:          "/var/lib/tsbridge",
		},
		Services: []Service{
			{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
		},
	}

	redacted := cfg.Redacted()

	// Check that sensitive fields are redacted
	if redacted.Tailscale.OAuthClientSecret != "[REDACTED]" {
		t.Errorf("OAuthClientSecret not redacted: %s", redacted.Tailscale.OAuthClientSecret)
	}
	if redacted.Tailscale.AuthKey != "[REDACTED]" {
		t.Errorf("AuthKey not redacted: %s", redacted.Tailscale.AuthKey)
	}

	// Check that non-sensitive fields are preserved
	if redacted.Tailscale.OAuthClientID != cfg.Tailscale.OAuthClientID {
		t.Errorf("OAuthClientID should not be redacted: got %s, want %s",
			redacted.Tailscale.OAuthClientID, cfg.Tailscale.OAuthClientID)
	}
	if redacted.Tailscale.StateDir != cfg.Tailscale.StateDir {
		t.Errorf("StateDir should not be redacted: got %s, want %s",
			redacted.Tailscale.StateDir, cfg.Tailscale.StateDir)
	}
	if redacted.Services[0].Name != cfg.Services[0].Name {
		t.Errorf("Service name should not be redacted: got %s, want %s",
			redacted.Services[0].Name, cfg.Services[0].Name)
	}
}

func TestRedactedConfig_JSON(t *testing.T) {
	cfg := &Config{
		Tailscale: Tailscale{
			OAuthClientID:     "client-id",
			OAuthClientSecret: "super-secret",
			AuthKey:           "tskey-auth-xxx",
		},
	}

	redacted := cfg.Redacted()
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, "super-secret") {
		t.Errorf("JSON output contains secret: %s", jsonStr)
	}
	if strings.Contains(jsonStr, "tskey-auth-xxx") {
		t.Errorf("JSON output contains auth key: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "[REDACTED]") {
		t.Errorf("JSON output does not contain redacted marker: %s", jsonStr)
	}
}

func TestRedactedConfig_String(t *testing.T) {
	cfg := &Config{
		Tailscale: Tailscale{
			OAuthClientID:     "client-id",
			OAuthClientSecret: "super-secret",
			AuthKey:           "tskey-auth-xxx",
		},
		Services: []Service{
			{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
		},
	}

	redacted := cfg.Redacted()
	str := fmt.Sprintf("%+v", redacted)

	// Check that secrets are not in the string representation
	if strings.Contains(str, "super-secret") {
		t.Errorf("String representation contains secret: %s", str)
	}
	if strings.Contains(str, "tskey-auth-xxx") {
		t.Errorf("String representation contains auth key: %s", str)
	}

	// Check that non-sensitive data is present
	if !strings.Contains(str, "client-id") {
		t.Errorf("String representation should contain client ID: %s", str)
	}
	if !strings.Contains(str, "test-service") {
		t.Errorf("String representation should contain service name: %s", str)
	}
}
