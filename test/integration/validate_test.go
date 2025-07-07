//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateFlagIntegration tests the -validate flag end-to-end
func TestValidateFlagIntegration(t *testing.T) {
	// Build tsbridge binary for testing
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err := cmd.Run()
	require.NoError(t, err, "Failed to build test binary")

	tests := []struct {
		name       string
		config     string
		env        map[string]string
		wantExit   int
		wantOutput []string
		wantErr    []string
	}{
		{
			name: "valid minimal config",
			config: `
[tailscale]
auth_key = "test-auth-key"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`,
			wantExit:   0,
			wantOutput: []string{"configuration is valid"},
		},
		{
			name: "valid complex config with all features",
			config: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"
state_dir = "/tmp/tsbridge"
default_tags = ["tag:prod"]

[global]
flush_interval = "100ms"
access_log = true
trusted_proxies = ["192.168.1.0/24", "10.0.0.1"]
metrics_addr = ":9090"
response_header_timeout = "10s"
keep_alive_timeout = "30s"
dial_timeout = "5s"
read_header_timeout = "5s"
write_timeout = "10s"
idle_timeout = "60s"
shutdown_timeout = "15s"
buffer_size = "64KB"

[[services]]
name = "api"
backend_addr = "localhost:8080"
tags = ["tag:api", "tag:prod"]
whois_enabled = true
whois_timeout = "2s"
access_log = false
tls_mode = "auto"
read_header_timeout = "3s"
write_timeout = "15s"
idle_timeout = "90s"

[[services]]
name = "web"
backend_addr = "unix:///var/run/web.sock"
tags = ["tag:web", "tag:prod"]
whois_enabled = false
tls_mode = "off"
`,
			wantExit:   0,
			wantOutput: []string{"configuration is valid"},
		},
		{
			name: "missing required fields",
			config: `
[tailscale]
# Missing auth credentials

[[services]]
name = "test"
# Missing backend_addr
`,
			wantExit: 1,
			wantErr:  []string{"validation error"},
		},
		{
			name: "invalid field values",
			config: `
[tailscale]
auth_key = "test-key"

[global]
shutdown_timeout = "-5s"

[[services]]
name = "test"
backend_addr = "not-a-valid-addr"
`,
			wantExit: 1,
			wantErr:  []string{"validation error"},
		},
		{
			name: "duplicate service names",
			config: `
[tailscale]
auth_key = "test-key"

[[services]]
name = "api"
backend_addr = "localhost:8080"

[[services]]
name = "api"
backend_addr = "localhost:8081"
`,
			wantExit: 1,
			wantErr:  []string{"duplicate service name"},
		},
		{
			name: "syntax error in TOML",
			config: `
[tailscale
auth_key = "test-key"
`,
			wantExit: 1,
			wantErr:  []string{"failed to load configuration"},
		},
		{
			name: "file permission error",
			config: `
[tailscale]
auth_key_file = "/root/secret.txt"

[[services]]
name = "test"
backend_addr = "localhost:8080"
`,
			wantExit: 1,
			wantErr:  []string{"no such file or directory"},
		},
		{
			name: "environment variable validation",
			config: `
[tailscale]
auth_key_env = "TEST_AUTH_KEY"

[[services]]
name = "test"
backend_addr = "localhost:8080"
`,
			env:        map[string]string{"TEST_AUTH_KEY": "valid-key"},
			wantExit:   0,
			wantOutput: []string{"configuration is valid"},
		},
		{
			name: "missing environment variable",
			config: `
[tailscale]
auth_key_env = "MISSING_AUTH_KEY"

[[services]]
name = "test"
backend_addr = "localhost:8080"
`,
			wantExit: 1,
			wantErr:  []string{"OAuth client ID must be provided"},
		},
		{
			name: "conflicting auth methods",
			config: `
[tailscale]
auth_key = "test-key"
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test"
backend_addr = "localhost:8080"
`,
			wantExit: 1,
			wantErr:  []string{"cannot specify both OAuth and AuthKey"},
		},
		{
			name: "missing tags with OAuth",
			config: `
[tailscale]
oauth_client_id = "test-id"
oauth_client_secret = "test-secret"

[[services]]
name = "test"
backend_addr = "localhost:8080"
# Missing tags when using OAuth
`,
			wantExit: 1,
			wantErr:  []string{"must have at least one tag when using OAuth"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create config file
			configPath := filepath.Join(t.TempDir(), "config.toml")
			err := os.WriteFile(configPath, []byte(tt.config), 0644)
			require.NoError(t, err)

			// Prepare command
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, binPath, "-config", configPath, "-validate")

			// Set environment variables
			cmd.Env = os.Environ()
			for k, v := range tt.env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			// Run command
			err = cmd.Run()

			// Check exit code
			exitCode := 0
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			assert.Equal(t, tt.wantExit, exitCode, "Exit code mismatch. Stdout: %s, Stderr: %s", stdout.String(), stderr.String())

			// Check expected output
			output := stdout.String() + stderr.String()
			for _, want := range tt.wantOutput {
				assert.Contains(t, output, want, "Expected output not found")
			}

			// Check expected errors
			for _, want := range tt.wantErr {
				assert.Contains(t, output, want, "Expected error not found")
			}
		})
	}
}

// TestValidateWithDockerProvider tests validation with docker provider
func TestValidateWithDockerProvider(t *testing.T) {
	// Build tsbridge binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err := cmd.Run()
	require.NoError(t, err, "Failed to build test binary")

	// Test docker provider validation (no services required at startup)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd = exec.CommandContext(ctx, binPath, "-provider", "docker", "-validate")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := stdout.String() + stderr.String()

	// Docker provider should fail if docker is not available
	// but should validate the provider configuration itself
	if err != nil {
		assert.Contains(t, output, "failed to create configuration provider", "Should fail to create docker provider")
	} else {
		// If docker is available, validation should succeed
		assert.Contains(t, output, "configuration is valid")
	}
}

// TestValidateVerboseOutput tests validation with verbose flag
func TestValidateVerboseOutput(t *testing.T) {
	// Build tsbridge binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err := cmd.Run()
	require.NoError(t, err, "Failed to build test binary")

	// Create valid config
	configPath := filepath.Join(t.TempDir(), "config.toml")
	configContent := `
[tailscale]
auth_key = "test-auth-key"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`
	err = os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Run with verbose flag
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd = exec.CommandContext(ctx, binPath, "-config", configPath, "-validate", "-verbose")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	require.NoError(t, err)

	output := stdout.String() + stderr.String()
	// Should see debug logging
	assert.Contains(t, output, "validating configuration")
	assert.Contains(t, output, "loading configuration for validation")
	assert.Contains(t, output, "configuration is valid")
}
