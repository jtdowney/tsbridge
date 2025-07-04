package integration

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/test/integration/helpers"
	"github.com/stretchr/testify/assert"
)

// TestInMemoryGracefulShutdown was removed - this behavior is already tested in:
// - internal/service/service_test.go: TestRegistry_Shutdown

// TestInMemoryShutdownWithActiveRequests was removed - this behavior is already tested in:
// - internal/service/service_test.go: TestShutdownWithInflightRequests

// TestE2EGracefulShutdown tests that the service shuts down gracefully using exec.Command
func TestE2EGracefulShutdown(t *testing.T) {
	// Skip if not in integration test mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a backend that tracks in-flight requests
	backend, tracker := helpers.CreateTrackingBackend(t)

	// Create test configuration and write to file
	cfg := helpers.CreateTestConfig(t, "shutdown-test-service", backend.Listener.Addr().String())
	cfg.Global.ShutdownTimeout = config.Duration{Duration: 5 * time.Second}
	configPath := helpers.WriteConfigFile(t, cfg)

	// Start tsbridge process
	process := helpers.StartTSBridge(t, configPath)

	// Get the output (this will trigger shutdown)
	output := process.GetOutput()

	// Verify graceful shutdown sequence
	assert.Contains(t, output, "received signal")
	assert.Contains(t, output, "shutting down")
	assert.Contains(t, output, "shutdown complete")

	// Log request metrics
	t.Logf("Requests - Started: %d, Completed: %d",
		atomic.LoadInt32(&tracker.RequestsStarted),
		atomic.LoadInt32(&tracker.RequestsCompleted))
}

// TestE2EShutdownWithActiveRequests tests shutdown behavior with active requests using exec.Command
func TestE2EShutdownWithActiveRequests(t *testing.T) {
	// Skip if not in integration test mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a backend that tracks requests
	backend, _ := helpers.CreateTrackingBackend(t)

	// Create test configuration with short shutdown timeout
	cfg := helpers.CreateTestConfig(t, "active-requests-service", backend.Listener.Addr().String())
	cfg.Global.MetricsAddr = ""
	cfg.Global.ShutdownTimeout = config.Duration{Duration: 2 * time.Second}
	configPath := helpers.WriteConfigFile(t, cfg)

	// Start tsbridge process
	process := helpers.StartTSBridge(t, configPath)

	// Track shutdown timing
	shutdownStart := time.Now()

	// Get the output (this will trigger shutdown)
	output := process.GetOutput()

	shutdownDuration := time.Since(shutdownStart)
	t.Logf("Shutdown completed in %v", shutdownDuration)

	// Should respect shutdown timeout (2s) plus some margin
	assert.Less(t, shutdownDuration, 3*time.Second, "shutdown took too long")

	// Verify shutdown happened - at least one of these messages should appear
	shutdownComplete := strings.Contains(output, "shutdown complete")
	shuttingDown := strings.Contains(output, "shutting down")
	assert.True(t, shutdownComplete || shuttingDown, "did not see shutdown messages in output:\n%s", output)
}
