//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/test/integration/helpers"
	"github.com/moby/moby/client"
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
	binPath := helpers.BuildTestBinary(t)

	t.Run("docker provider starts with no services", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Start tsbridge with docker provider
		_, output := startTSBridgeDocker(t, ctx, binPath, "-verbose")

		// Give it time to start
		time.Sleep(2 * time.Second)

		// Check that it started successfully
		assert.Contains(t, output.String(), "provider=docker")
		assert.Contains(t, output.String(), "starting tsbridge")

		// Should handle no services gracefully
		assert.NotContains(t, output.String(), "panic")
		assert.NotContains(t, output.String(), "fatal")
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

		_, output := startTSBridgeDocker(t, ctx, binPath,
			"-docker-socket", "unix://"+socketPath,
			"-docker-label-prefix", "test-tsbridge",
			"-verbose")

		// Give it time to start
		time.Sleep(1 * time.Second)

		assert.Contains(t, output.String(), "provider=docker")
		assert.Contains(t, output.String(), "test-tsbridge") // Custom label prefix should be used
	})
}

// TestDockerProviderDynamicConfiguration tests dynamic container updates
func TestDockerProviderDynamicConfiguration(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available")
	}

	// Create Docker client
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()

	// Build tsbridge binary
	binPath := helpers.BuildTestBinary(t)

	t.Run("container start triggers service addition", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Start a test HTTP server container with tsbridge labels
		testContainerName := "tsbridge-test-httpbin-" + time.Now().Format("20060102150405")
		runHTTPBin(t, ctx, testContainerName,
			"tsbridge.enabled=true",
			"tsbridge.service.name=test-httpbin",
			"tsbridge.service.backend_addr="+testContainerName+":8080")

		// Start tsbridge with docker provider
		_, output := startTSBridgeDocker(t, ctx, binPath, "-verbose")

		// Wait for tsbridge to detect the container
		time.Sleep(3 * time.Second)

		// Should detect and load the service
		assert.Contains(t, output.String(), "test-httpbin", "Should detect test-httpbin service")
		assert.Contains(t, output.String(), "loading configuration", "Should load configuration")
	})

	t.Run("container stop triggers service removal", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Start test container first
		testContainerName := "tsbridge-test-removal-" + time.Now().Format("20060102150405")
		containerID := runHTTPBin(t, ctx, testContainerName,
			"tsbridge.enabled=true",
			"tsbridge.service.name=test-removal",
			"tsbridge.service.backend_addr="+testContainerName+":8080")

		// Start tsbridge
		_, output := startTSBridgeDocker(t, ctx, binPath, "-verbose")

		// Wait for initial detection
		time.Sleep(3 * time.Second)

		// Stop the container
		err := exec.Command("docker", "stop", containerID).Run()
		require.NoError(t, err)

		// Wait for removal detection
		time.Sleep(3 * time.Second)

		// Should show container event handling
		assert.Contains(t, output.String(), "test-removal", "Should have detected test-removal service")
	})
}

// TestDockerProviderLabelVariations tests different label configurations
func TestDockerProviderLabelVariations(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available")
	}

	// Build tsbridge binary
	binPath := helpers.BuildTestBinary(t)

	t.Run("supports both enable and enabled labels", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Start container with "enable" label (without 'd')
		testContainerName := "tsbridge-test-enable-" + time.Now().Format("20060102150405")
		runHTTPBin(t, ctx, testContainerName,
			"tsbridge.enable=true", // Note: "enable" not "enabled"
			"tsbridge.service.name=test-enable",
			"tsbridge.service.backend_addr="+testContainerName+":8080")

		// Start tsbridge
		_, output := startTSBridgeDocker(t, ctx, binPath, "-verbose")

		// Wait for detection
		time.Sleep(3 * time.Second)

		// Should detect service with "enable" label
		assert.Contains(t, output.String(), "test-enable", "Should detect service with 'enable' label")
	})

	t.Run("custom label prefix", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Start container with custom label prefix
		customPrefix := "myapp"
		testContainerName := "tsbridge-test-custom-" + time.Now().Format("20060102150405")
		runHTTPBin(t, ctx, testContainerName,
			customPrefix+".enabled=true",
			customPrefix+".service.name=test-custom",
			customPrefix+".service.backend_addr="+testContainerName+":8080")

		// Start tsbridge with custom label prefix
		_, output := startTSBridgeDocker(t, ctx, binPath,
			"-docker-label-prefix", customPrefix,
			"-verbose")

		// Wait for detection
		time.Sleep(3 * time.Second)

		// Should detect service with custom label prefix
		assert.Contains(t, output.String(), "test-custom", "Should detect service with custom label prefix")
		assert.Contains(t, output.String(), customPrefix, "Should use custom label prefix")
	})
}

// TestDockerProviderErrorHandling tests error scenarios
func TestDockerProviderErrorHandling(t *testing.T) {
	// Build tsbridge binary
	binPath := helpers.BuildTestBinary(t)

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
		_, output := startTSBridgeDocker(t, ctx, binPath,
			"-docker-socket", "tcp://localhost:2375",
			"-verbose")

		// Give it a moment
		time.Sleep(1 * time.Second)

		// Should at least attempt to use TCP endpoint
		assert.Contains(t, output.String(), "provider=docker")
	})
}

// startTSBridgeDocker starts a tsbridge process with the docker provider,
// capturing combined stdout/stderr into the returned buffer. The process is
// killed and reaped via t.Cleanup. Using a single buffer for both streams is
// safe because os/exec serializes writes when Stdout and Stderr are equal.
func startTSBridgeDocker(t *testing.T, ctx context.Context, binPath string, extraArgs ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()

	args := append([]string{"-provider", "docker"}, extraArgs...)
	cmd := exec.CommandContext(ctx, binPath, args...)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	return cmd, &output
}

// runHTTPBin starts a kennethreitz/httpbin container with the given labels and
// registers cleanup to stop it. It returns the trimmed container ID.
func runHTTPBin(t *testing.T, ctx context.Context, name string, labels ...string) string {
	t.Helper()

	args := []string{"run", "--rm", "-d", "--name", name}
	for _, label := range labels {
		args = append(args, "--label", label)
	}
	args = append(args, "kennethreitz/httpbin")

	containerID, err := exec.CommandContext(ctx, "docker", args...).Output()
	require.NoError(t, err)
	containerID = bytes.TrimSpace(containerID)

	t.Cleanup(func() {
		_ = exec.Command("docker", "stop", string(containerID)).Run()
	})

	return string(containerID)
}

// isDockerAvailable checks if Docker is available on the system
func isDockerAvailable() bool {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return false
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	return err == nil
}

// TestDockerProviderValidate tests validation with Docker provider
func TestDockerProviderValidate(t *testing.T) {
	// Build tsbridge binary
	binPath := helpers.BuildTestBinary(t)

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
				assert.Contains(t, output, "unable to find tsbridge container")
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
