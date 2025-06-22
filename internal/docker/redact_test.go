package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/jtdowney/tsbridge/internal/config"
)

func TestDockerProvider_LogsRedactedConfig(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	slog.SetDefault(logger)

	// Create a config with sensitive data
	cfg := &config.Config{
		Tailscale: config.Tailscale{
			OAuthClientID:     "test-client-id",
			OAuthClientSecret: "super-secret-oauth",
			AuthKey:           "tskey-auth-secret",
		},
		Services: []config.Service{
			{
				Name:        "test-service",
				BackendAddr: "localhost:8080",
			},
		},
	}

	// Log the config using structured logging
	slog.Info("configuration loaded",
		"config", cfg.Redacted(),
		"service_count", len(cfg.Services))

	// Check that the log output doesn't contain secrets
	logOutput := buf.String()
	if strings.Contains(logOutput, "super-secret-oauth") {
		t.Errorf("Log output contains OAuth secret: %s", logOutput)
	}
	if strings.Contains(logOutput, "tskey-auth-secret") {
		t.Errorf("Log output contains auth key: %s", logOutput)
	}

	// Verify the log contains redacted markers
	if !strings.Contains(logOutput, "[REDACTED]") {
		t.Errorf("Log output doesn't contain redacted markers: %s", logOutput)
	}

	// Verify non-sensitive data is still present
	if !strings.Contains(logOutput, "test-client-id") {
		t.Errorf("Log output should contain client ID: %s", logOutput)
	}
	if !strings.Contains(logOutput, "test-service") {
		t.Errorf("Log output should contain service name: %s", logOutput)
	}
}

func TestDockerProvider_ConfigDebugLogging(t *testing.T) {
	// Test that when Docker provider logs config for debugging, it's redacted
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	cfg := &config.Config{
		Tailscale: config.Tailscale{
			OAuthClientSecret: "secret-value",
			AuthKey:           "tskey-xxx",
		},
	}

	// Simulate Docker provider debug logging
	slog.Debug("Docker provider loaded configuration",
		"config", cfg.Redacted(),
		"provider", "docker")

	logOutput := buf.String()
	if strings.Contains(logOutput, "secret-value") || strings.Contains(logOutput, "tskey-xxx") {
		t.Errorf("Debug log contains secrets: %s", logOutput)
	}
}

func TestDockerProvider_ConfigChangeLogging(t *testing.T) {
	// Test configuration change logging
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	slog.SetDefault(logger)

	oldCfg := &config.Config{
		Tailscale: config.Tailscale{
			OAuthClientSecret: "old-secret",
		},
	}

	newCfg := &config.Config{
		Tailscale: config.Tailscale{
			OAuthClientSecret: "new-secret",
		},
		Services: []config.Service{
			{Name: "new-service"},
		},
	}

	// Log configuration change
	slog.Info("configuration changed",
		"old_config", oldCfg.Redacted(),
		"new_config", newCfg.Redacted(),
		"service_count_change", len(newCfg.Services)-len(oldCfg.Services))

	logOutput := buf.String()
	if strings.Contains(logOutput, "old-secret") || strings.Contains(logOutput, "new-secret") {
		t.Errorf("Configuration change log contains secrets: %s", logOutput)
	}
}

func TestDockerProvider_ErrorLoggingWithConfig(t *testing.T) {
	// Test that errors that might include config data are safe
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	slog.SetDefault(logger)

	cfg := &config.Config{
		Tailscale: config.Tailscale{
			OAuthClientSecret: "secret-oauth",
			AuthKey:           "tskey-secret",
		},
	}

	// Simulate an error that might include config
	// Note: Using string key for test purposes only
	ctx := context.Background()

	slog.ErrorContext(ctx, "failed to validate configuration",
		"config", cfg.Redacted(),
		"error", "invalid backend address")

	logOutput := buf.String()
	if strings.Contains(logOutput, "secret-oauth") || strings.Contains(logOutput, "tskey-secret") {
		t.Errorf("Error log contains secrets: %s", logOutput)
	}
}

func TestRedactedConfig_StructuredLogging(t *testing.T) {
	// Test that RedactedConfig works well with slog's structured logging
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	cfg := &config.Config{
		Tailscale: config.Tailscale{
			OAuthClientID:     "client-123",
			OAuthClientSecret: "very-secret",
			AuthKey:           "tskey-private",
			StateDir:          "/var/lib/tsbridge",
		},
		Services: []config.Service{
			{
				Name:        "api",
				BackendAddr: "localhost:8080",
			},
		},
	}

	redacted := cfg.Redacted()

	// Log using structured logging
	logger.Info("test log entry",
		slog.Any("config", redacted),
		slog.String("action", "startup"))

	// Parse the JSON log
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}

	// Navigate to the config in the log entry
	configData, ok := logEntry["config"].(map[string]interface{})
	if !ok {
		t.Fatal("config not found in log entry")
	}

	tailscaleData, ok := configData["tailscale"].(map[string]interface{})
	if !ok {
		t.Fatal("tailscale config not found in log entry")
	}

	// Check that secrets are redacted
	if secret, ok := tailscaleData["oauth_client_secret"].(string); ok && secret != "[REDACTED]" {
		t.Errorf("OAuth secret not redacted in JSON log: %s", secret)
	}
	if authKey, ok := tailscaleData["auth_key"].(string); ok && authKey != "[REDACTED]" {
		t.Errorf("Auth key not redacted in JSON log: %s", authKey)
	}

	// Check that non-sensitive fields are preserved
	if clientID, ok := tailscaleData["oauth_client_id"].(string); !ok || clientID != "client-123" {
		t.Errorf("OAuth client ID should be preserved: %v", clientID)
	}
}
