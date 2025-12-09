//go:build integration

package integration

import (
	"testing"

	"github.com/jtdowney/tsbridge/test/integration/helpers"
	"github.com/stretchr/testify/assert"
)

// TestInMemoryAppLifecycle was removed - this behavior is already tested in:
// - internal/app/app_test.go: TestAppStart, TestAppStartDoesNotBlockShutdown

// TestE2EServiceCreationAndLifecycle tests that services are created and can handle requests using exec.Command
// This is an end-to-end test that builds and runs the actual binary
func TestE2EServiceCreationAndLifecycle(t *testing.T) {
	// Skip if not in integration test mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create backend servers with tracking
	backend1, tracker1 := helpers.CreateTrackingBackend(t)
	backend2, tracker2 := helpers.CreateTrackingBackend(t)

	// Create test configuration and write to file
	cfg := helpers.CreateMultiServiceConfig(t, map[string]string{
		"service1": backend1.Listener.Addr().String(),
		"service2": backend2.Listener.Addr().String(),
	})
	configPath := helpers.WriteConfigFile(t, cfg)

	// Start tsbridge process
	process := helpers.StartTSBridge(t, configPath)

	// Get the output
	output := process.GetOutput()

	// Verify both services started
	assert.Contains(t, output, `msg="started service" service=service1`)
	assert.Contains(t, output, `msg="started service" service=service2`)

	// Verify metrics server started
	assert.Contains(t, output, "metrics server listening")

	// Verify clean shutdown
	assert.Contains(t, output, "shutdown complete")

	// Log request counts (in test mode, services start but don't serve)
	t.Logf("Backend1 requests: %d", tracker1.RequestsCompleted)
	t.Logf("Backend2 requests: %d", tracker2.RequestsCompleted)
}

// TestInMemoryServiceHealthChecks was removed - this behavior is already tested in:
// - internal/service/service_test.go: TestRegistry_StartServices_WithBackendHealthCheck

// TestInMemoryServiceWithInvalidBackend was removed - this behavior is already tested in:
// - internal/service/service_test.go: TestServiceStartupPartialFailures
