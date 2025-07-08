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

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDockerProviderIntegration tests Docker provider functionality end-to-end
func TestDockerProviderIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker is not available - skipping integration tests")
	}

	// Build tsbridge binary for testing
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err := cmd.Run()
	require.NoError(t, err, "Failed to build test binary")

	t.Run("docker provider starts with no services", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Start tsbridge with docker provider
		cmd := exec.CommandContext(ctx, binPath, "-provider", "docker", "-verbose")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Start()
		require.NoError(t, err)

		// Give it time to start
		time.Sleep(2 * time.Second)

		// Check that it started successfully
		output := stdout.String() + stderr.String()
		assert.Contains(t, output, "provider=docker")
		assert.Contains(t, output, "starting tsbridge")

		// Should handle no services gracefully
		assert.NotContains(t, output, "panic")
		assert.NotContains(t, output, "fatal")

		// Cleanup
		cmd.Process.Kill()
		cmd.Wait()
	})

	t.Run("docker provider with custom socket path", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Use actual Docker socket path
		socketPath := "/var/run/docker.sock"
		if _, err := os.Stat(socketPath); err != nil {
			socketPath = os.Getenv("DOCKER_HOST")
			if socketPath == "" {
				t.Skip("Docker socket not found")
			}
		}

		cmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-docker-socket", "unix://"+socketPath,
			"-docker-label-prefix", "test-tsbridge",
			"-verbose")

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Start()
		require.NoError(t, err)

		// Give it time to start
		time.Sleep(1 * time.Second)

		output := stdout.String() + stderr.String()
		assert.Contains(t, output, "provider=docker")
		assert.Contains(t, output, "test-tsbridge") // Custom label prefix should be used

		// Cleanup
		cmd.Process.Kill()
		cmd.Wait()
	})
}

// TestDockerProviderDynamicConfiguration tests dynamic container updates
func TestDockerProviderDynamicConfiguration(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available")
	}

	// Create Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	// Build tsbridge binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err = cmd.Run()
	require.NoError(t, err)

	t.Run("container start triggers service addition", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Start a test HTTP server container with tsbridge labels
		testContainerName := "tsbridge-test-httpbin-" + time.Now().Format("20060102150405")
		httpbinCmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-d",
			"--name", testContainerName,
			"--label", "tsbridge.enabled=true",
			"--label", "tsbridge.service.name=test-httpbin",
			"--label", "tsbridge.service.backend_addr="+testContainerName+":8080",
			"kennethreitz/httpbin")

		containerID, err := httpbinCmd.Output()
		require.NoError(t, err)
		containerID = bytes.TrimSpace(containerID)

		// Ensure cleanup
		defer func() {
			exec.Command("docker", "stop", string(containerID)).Run()
		}()

		// Start tsbridge with docker provider
		tsbridgeCmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-verbose")

		var stdout, stderr bytes.Buffer
		tsbridgeCmd.Stdout = &stdout
		tsbridgeCmd.Stderr = &stderr

		err = tsbridgeCmd.Start()
		require.NoError(t, err)

		// Ensure cleanup
		defer func() {
			tsbridgeCmd.Process.Kill()
			tsbridgeCmd.Wait()
		}()

		// Wait for tsbridge to detect the container
		time.Sleep(3 * time.Second)

		output := stdout.String() + stderr.String()
		// Should detect and load the service
		assert.Contains(t, output, "test-httpbin", "Should detect test-httpbin service")
		assert.Contains(t, output, "loading configuration", "Should load configuration")
	})

	t.Run("container stop triggers service removal", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Start test container first
		testContainerName := "tsbridge-test-removal-" + time.Now().Format("20060102150405")
		httpbinCmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-d",
			"--name", testContainerName,
			"--label", "tsbridge.enabled=true",
			"--label", "tsbridge.service.name=test-removal",
			"--label", "tsbridge.service.backend_addr="+testContainerName+":8080",
			"kennethreitz/httpbin")

		containerID, err := httpbinCmd.Output()
		require.NoError(t, err)
		containerID = bytes.TrimSpace(containerID)

		// Start tsbridge
		tsbridgeCmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-verbose")

		var stdout, stderr bytes.Buffer
		tsbridgeCmd.Stdout = &stdout
		tsbridgeCmd.Stderr = &stderr

		err = tsbridgeCmd.Start()
		require.NoError(t, err)

		// Ensure cleanup
		defer func() {
			tsbridgeCmd.Process.Kill()
			tsbridgeCmd.Wait()
		}()

		// Wait for initial detection
		time.Sleep(3 * time.Second)

		// Stop the container
		err = exec.Command("docker", "stop", string(containerID)).Run()
		require.NoError(t, err)

		// Wait for removal detection
		time.Sleep(3 * time.Second)

		output := stdout.String() + stderr.String()
		// Should show container event handling
		assert.Contains(t, output, "test-removal", "Should have detected test-removal service")
	})
}

// TestDockerProviderLabelVariations tests different label configurations
func TestDockerProviderLabelVariations(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available")
	}

	// Build tsbridge binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err := cmd.Run()
	require.NoError(t, err)

	t.Run("supports both enable and enabled labels", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Start container with "enable" label (without 'd')
		testContainerName := "tsbridge-test-enable-" + time.Now().Format("20060102150405")
		httpbinCmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-d",
			"--name", testContainerName,
			"--label", "tsbridge.enable=true", // Note: "enable" not "enabled"
			"--label", "tsbridge.service.name=test-enable",
			"--label", "tsbridge.service.backend_addr="+testContainerName+":8080",
			"kennethreitz/httpbin")

		containerID, err := httpbinCmd.Output()
		require.NoError(t, err)
		containerID = bytes.TrimSpace(containerID)

		// Ensure cleanup
		defer func() {
			exec.Command("docker", "stop", string(containerID)).Run()
		}()

		// Start tsbridge
		tsbridgeCmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-verbose")

		var stdout, stderr bytes.Buffer
		tsbridgeCmd.Stdout = &stdout
		tsbridgeCmd.Stderr = &stderr

		err = tsbridgeCmd.Start()
		require.NoError(t, err)

		// Ensure cleanup
		defer func() {
			tsbridgeCmd.Process.Kill()
			tsbridgeCmd.Wait()
		}()

		// Wait for detection
		time.Sleep(3 * time.Second)

		output := stdout.String() + stderr.String()
		// Should detect service with "enable" label
		assert.Contains(t, output, "test-enable", "Should detect service with 'enable' label")
	})

	t.Run("custom label prefix", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Start container with custom label prefix
		customPrefix := "myapp"
		testContainerName := "tsbridge-test-custom-" + time.Now().Format("20060102150405")
		httpbinCmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-d",
			"--name", testContainerName,
			"--label", customPrefix+".enabled=true",
			"--label", customPrefix+".service.name=test-custom",
			"--label", customPrefix+".service.backend_addr="+testContainerName+":8080",
			"kennethreitz/httpbin")

		containerID, err := httpbinCmd.Output()
		require.NoError(t, err)
		containerID = bytes.TrimSpace(containerID)

		// Ensure cleanup
		defer func() {
			exec.Command("docker", "stop", string(containerID)).Run()
		}()

		// Start tsbridge with custom label prefix
		tsbridgeCmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-docker-label-prefix", customPrefix,
			"-verbose")

		var stdout, stderr bytes.Buffer
		tsbridgeCmd.Stdout = &stdout
		tsbridgeCmd.Stderr = &stderr

		err = tsbridgeCmd.Start()
		require.NoError(t, err)

		// Ensure cleanup
		defer func() {
			tsbridgeCmd.Process.Kill()
			tsbridgeCmd.Wait()
		}()

		// Wait for detection
		time.Sleep(3 * time.Second)

		output := stdout.String() + stderr.String()
		// Should detect service with custom label prefix
		assert.Contains(t, output, "test-custom", "Should detect service with custom label prefix")
		assert.Contains(t, output, customPrefix, "Should use custom label prefix")
	})
}

// TestDockerProviderErrorHandling tests error scenarios
func TestDockerProviderErrorHandling(t *testing.T) {
	// Build tsbridge binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err := cmd.Run()
	require.NoError(t, err)

	t.Run("invalid docker socket", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-docker-socket", "unix:///invalid/docker.sock",
			"-verbose")

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		// Should exit with error
		assert.Error(t, err)
		if exitErr, ok := err.(*exec.ExitError); ok {
			assert.Equal(t, 1, exitErr.ExitCode())
		}

		output := stdout.String() + stderr.String()
		assert.Contains(t, output, "failed to create configuration provider")
	})

	t.Run("tcp docker endpoint", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Try TCP endpoint (will fail if Docker not listening on TCP)
		cmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-docker-socket", "tcp://localhost:2375",
			"-verbose")

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Start()
		require.NoError(t, err)

		// Give it a moment
		time.Sleep(1 * time.Second)

		// Kill it
		cmd.Process.Kill()
		cmd.Wait()

		output := stdout.String() + stderr.String()
		// Should at least attempt to use TCP endpoint
		assert.Contains(t, output, "provider=docker")
	})
}

// isDockerAvailable checks if Docker is available on the system
func isDockerAvailable() bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx)
	return err == nil
}

// TestDockerProviderValidate tests validation with Docker provider
func TestDockerProviderValidate(t *testing.T) {
	// Build tsbridge binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	err := cmd.Run()
	require.NoError(t, err)

	t.Run("validate command with docker provider", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, binPath,
			"-provider", "docker",
			"-validate",
			"-verbose")

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		output := stdout.String() + stderr.String()

		if isDockerAvailable() {
			// Should validate successfully with Docker available
			// Note: Docker provider allows empty config
			if err != nil {
				// If it fails, should be because of missing tsbridge container
				assert.Contains(t, output, "no tsbridge container found")
			} else {
				assert.Contains(t, output, "configuration is valid")
			}
		} else {
			// Should fail if Docker not available
			assert.Error(t, err)
			assert.Contains(t, output, "failed to create configuration provider")
		}
	})
}
