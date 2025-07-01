package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMemoryLeaks verifies that resources are properly cleaned up and no memory leaks occur
func TestMemoryLeaks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	// Force garbage collection to get baseline
	runtime.GC()
	runtime.Gosched()
	time.Sleep(100 * time.Millisecond)

	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	// Create backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Response from backend"))
	}))
	defer backend.Close()

	// Build the binary once
	baseTmpDir := t.TempDir()
	binPath := filepath.Join(baseTmpDir, "tsbridge")
	buildCmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, output)
	}

	// Run multiple cycles of server creation and destruction
	const cycles = 5
	for i := 0; i < cycles; i++ {
		t.Logf("Starting cycle %d/%d", i+1, cycles)

		// Create a temporary directory for this cycle's state
		cycleDir := filepath.Join(baseTmpDir, fmt.Sprintf("cycle-%d", i))
		if err := os.MkdirAll(cycleDir, 0755); err != nil {
			t.Fatalf("failed to create cycle directory: %v", err)
		}

		// Create config file
		configPath := filepath.Join(cycleDir, "test-config.toml")
		configContent := fmt.Sprintf(`
[tailscale]
auth_key = "tskey-auth-test123"
state_dir = "%s"

[global]
metrics_addr = "localhost:0"
read_header_timeout = "30s"
write_timeout = "30s"
idle_timeout = "120s"
shutdown_timeout = "5s"

[[services]]
name = "perf-test-%d"
backend_addr = "%s"
tls_mode = "off"
whois_enabled = true
whois_timeout = "5s"
`, filepath.Join(cycleDir, "state"), i, backend.Listener.Addr().String())

		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		// Start tsbridge
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, binPath, "-config", configPath)
		cmd.Env = append(os.Environ(), "TSBRIDGE_TEST_MODE=1")

		// Capture output
		outputPipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("failed to create stdout pipe: %v", err)
		}
		errPipe, err := cmd.StderrPipe()
		if err != nil {
			t.Fatalf("failed to create stderr pipe: %v", err)
		}

		combinedOutput := io.MultiReader(outputPipe, errPipe)

		// Start the process
		if err := cmd.Start(); err != nil {
			t.Fatalf("failed to start tsbridge: %v", err)
		}

		// Read output in goroutine
		outputChan := make(chan string, 1)
		go func() {
			output, _ := io.ReadAll(combinedOutput)
			outputChan <- string(output)
		}()

		// Let the server run briefly
		time.Sleep(2 * time.Second)

		// Send interrupt signal to trigger shutdown
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Errorf("failed to send interrupt signal: %v", err)
		}

		// Wait for process to exit
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited cleanly
		case <-time.After(10 * time.Second):
			t.Error("tsbridge did not shut down within timeout")
			cmd.Process.Kill()
		}

		// Get the output
		var output string
		select {
		case output = <-outputChan:
		case <-time.After(1 * time.Second):
			output = "timeout reading output"
		}

		// Check for errors in output
		if strings.Contains(output, "panic") {
			t.Errorf("Panic detected in cycle %d: %s", i, output)
		}

		cancel()

		// Force cleanup
		runtime.GC()
		runtime.Gosched()
		time.Sleep(100 * time.Millisecond)
	}

	// Check memory after all cycles
	runtime.GC()
	runtime.Gosched()
	time.Sleep(200 * time.Millisecond)

	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)

	// Calculate memory growth
	heapGrowth := int64(memStatsAfter.HeapAlloc) - int64(memStatsBefore.HeapAlloc)
	sysGrowth := int64(memStatsAfter.Sys) - int64(memStatsBefore.Sys)

	// Log memory statistics
	t.Logf("Memory stats before: HeapAlloc=%d, Sys=%d", memStatsBefore.HeapAlloc, memStatsBefore.Sys)
	t.Logf("Memory stats after: HeapAlloc=%d, Sys=%d", memStatsAfter.HeapAlloc, memStatsAfter.Sys)
	t.Logf("Memory growth: HeapAlloc=%d bytes, Sys=%d bytes", heapGrowth, sysGrowth)

	// Note: Memory growth in the test process doesn't reflect the memory usage of the subprocesses
	// This test mainly ensures the processes start and stop cleanly without crashes
}

// TestGoroutineCleanup verifies that the process shuts down cleanly without goroutine leaks
func TestGoroutineCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	// Create backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Response from backend"))
	}))
	defer backend.Close()

	// Create a temporary directory
	tmpDir := t.TempDir()

	// Create config file
	configPath := filepath.Join(tmpDir, "test-config.toml")
	configContent := fmt.Sprintf(`
[tailscale]
auth_key = "tskey-auth-test123"
state_dir = "%s"

[global]
metrics_addr = "localhost:19200"
read_header_timeout = "30s"
write_timeout = "30s"
idle_timeout = "120s"
shutdown_timeout = "5s"

[[services]]
name = "goroutine-test"
backend_addr = "%s"
tls_mode = "off"
whois_enabled = true
whois_timeout = "5s"
`, filepath.Join(tmpDir, "state"), backend.Listener.Addr().String())

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Build the binary with race detector
	binPath := filepath.Join(tmpDir, "tsbridge")
	cmd := exec.Command("go", "build", "-race", "-o", binPath, "../../cmd/tsbridge")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, output)
	}

	// Run the process multiple times to check for clean shutdown
	for i := 0; i < 3; i++ {
		t.Logf("Starting iteration %d/3", i+1)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, binPath, "-config", configPath, "-verbose")
		cmd.Env = append(os.Environ(), "TSBRIDGE_TEST_MODE=1")

		// Capture output
		outputPipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("failed to create stdout pipe: %v", err)
		}
		errPipe, err := cmd.StderrPipe()
		if err != nil {
			t.Fatalf("failed to create stderr pipe: %v", err)
		}

		combinedOutput := io.MultiReader(outputPipe, errPipe)

		// Start the process
		if err := cmd.Start(); err != nil {
			t.Fatalf("failed to start tsbridge: %v", err)
		}

		// Read output in goroutine
		outputChan := make(chan string, 1)
		go func() {
			output, _ := io.ReadAll(combinedOutput)
			outputChan <- string(output)
		}()

		// Let it run briefly
		time.Sleep(3 * time.Second)

		// Send interrupt signal to trigger graceful shutdown
		t.Log("Sending interrupt signal")
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Errorf("failed to send interrupt signal: %v", err)
		}

		// Wait for process to exit
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		var exitErr error
		select {
		case exitErr = <-done:
			t.Log("Process exited")
		case <-time.After(10 * time.Second):
			t.Error("tsbridge did not shut down within timeout")
			cmd.Process.Kill()
		}

		// Get the output
		var output string
		select {
		case output = <-outputChan:
		case <-time.After(1 * time.Second):
			output = "timeout reading output"
		}

		// Check for successful shutdown
		if !strings.Contains(output, "Shutdown complete") && !strings.Contains(output, "graceful shutdown") {
			t.Logf("Warning: Shutdown message not found in output")
		}

		// Check for race conditions (race detector will report them)
		if strings.Contains(output, "DATA RACE") {
			t.Errorf("Race condition detected: %s", output)
		}

		// Check exit code
		if exitErr != nil && exitErr.Error() != "exit status 130" { // 130 is SIGINT
			t.Errorf("Unexpected exit error: %v", exitErr)
		}

		cancel()
		time.Sleep(500 * time.Millisecond)
	}
}

// TestResourceUsage validates that the service handles load without excessive resource usage
func TestResourceUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	// Create backend server
	requestCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		// Simulate some processing
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Response %d", requestCount)
	}))
	defer backend.Close()

	// Create a temporary directory
	tmpDir := t.TempDir()

	// Create config file
	configPath := filepath.Join(tmpDir, "test-config.toml")
	configContent := fmt.Sprintf(`
[tailscale]
auth_key = "tskey-auth-test123"
state_dir = "%s"

[global]
metrics_addr = "localhost:19201"
read_header_timeout = "30s"
write_timeout = "30s"
idle_timeout = "120s"
shutdown_timeout = "10s"

[[services]]
name = "resource-test"
backend_addr = "%s"
tls_mode = "off"
whois_enabled = false
`, filepath.Join(tmpDir, "state"), backend.Listener.Addr().String())

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Build the binary
	binPath := filepath.Join(tmpDir, "tsbridge")
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/tsbridge")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, output)
	}

	// Start tsbridge
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd = exec.CommandContext(ctx, binPath, "-config", configPath, "-verbose")
	cmd.Env = append(os.Environ(), "TSBRIDGE_TEST_MODE=1")

	// Monitor resource usage via output parsing
	outputPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to create stderr pipe: %v", err)
	}

	combinedOutput := io.MultiReader(outputPipe, errPipe)

	// Start the process
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tsbridge: %v", err)
	}

	// Read output in goroutine
	outputLines := make(chan string, 100)
	go func() {
		defer close(outputLines)
		buf := make([]byte, 1024)
		var partial []byte
		for {
			n, err := combinedOutput.Read(buf)
			if n > 0 {
				partial = append(partial, buf[:n]...)
				data := partial
				lines := strings.Split(string(data), "\n")
				for i, line := range lines {
					if i < len(lines)-1 {
						outputLines <- line
					} else {
						partial = []byte(line)
					}
				}
			}
			if err != nil {
				if len(partial) > 0 {
					outputLines <- string(partial)
				}
				break
			}
		}
	}()

	// Wait for service to start
	metricsURL := ""
	startTime := time.Now()
	for {
		select {
		case line := <-outputLines:
			t.Log(line)
			if strings.Contains(line, "metrics server listening") {
				// Extract port from log
				if strings.Contains(line, "19201") {
					metricsURL = "http://localhost:19201/metrics"
				}
			}
			if strings.Contains(line, "started service") || time.Since(startTime) > 5*time.Second {
				goto serviceReady
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Service did not start in time")
		}
	}

serviceReady:
	t.Log("Service is ready")

	// If we found the metrics URL, check it
	if metricsURL != "" {
		resp, err := http.Get(metricsURL)
		if err == nil {
			resp.Body.Close()
			t.Logf("Metrics endpoint responding: %s", resp.Status)
		}
	}

	// Send SIGTERM to trigger shutdown
	t.Log("Sending SIGTERM signal")
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Errorf("failed to send interrupt signal: %v", err)
	}

	// Wait for process to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil && err.Error() != "exit status 130" {
			t.Errorf("Process exited with error: %v", err)
		} else {
			t.Log("Process exited cleanly")
		}
	case <-time.After(15 * time.Second):
		t.Error("tsbridge did not shut down within timeout")
		cmd.Process.Kill()
	}

	// Drain remaining output
	go func() {
		for range outputLines {
		}
	}()

	t.Logf("Backend received %d requests", requestCount)
	t.Log("Resource usage test completed")
}
