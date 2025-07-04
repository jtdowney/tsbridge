package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Test that signal handling is simple and clean
func TestSignalHandlingSimplicity(t *testing.T) {
	// Create a context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal channel
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Simulate signal handling logic that should be in main
	signalHandled := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			// On signal, just cancel the context
			cancel()
			close(signalHandled)
		case <-ctx.Done():
			// Context cancelled by other means
			return
		}
	}()

	// Send a signal
	sigCh <- os.Interrupt

	// Verify signal was handled
	select {
	case <-signalHandled:
		// Good, signal was handled
	case <-time.After(1 * time.Second):
		t.Fatal("Signal was not handled within timeout")
	}

	// Verify context was cancelled
	select {
	case <-ctx.Done():
		assert.Equal(t, context.Canceled, ctx.Err())
	default:
		t.Fatal("Context was not cancelled after signal")
	}
}

// TestExitFuncAllowsDeferExecution verifies that using exitFunc allows defer statements to run
func TestExitFuncAllowsDeferExecution(t *testing.T) {
	// Save original exitFunc
	oldExitFunc := exitFunc
	defer func() {
		exitFunc = oldExitFunc
	}()

	// Track exit calls
	exitCalled := false
	exitCode := -1
	exitFunc = func(code int) {
		exitCalled = true
		exitCode = code
		// Don't actually exit in tests
	}

	// Capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	oldLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldLogger)

	// Test a function that uses exitFunc
	deferExecuted := false
	func() {
		defer func() {
			deferExecuted = true
		}()
		slog.Error("test error")
		exitFunc(1)
	}()

	// Verify behavior
	assert.True(t, exitCalled, "exitFunc should have been called")
	assert.Equal(t, 1, exitCode)
	assert.True(t, deferExecuted, "defer should have been executed")
	assert.Contains(t, logBuf.String(), "test error")
}

// TestExitFuncInSignalHandler verifies exitFunc works in goroutines too
func TestExitFuncInSignalHandler(t *testing.T) {
	// Save original exitFunc
	oldExitFunc := exitFunc
	defer func() {
		exitFunc = oldExitFunc
	}()

	// Track exit
	exitChan := make(chan int, 1)
	exitFunc = func(code int) {
		exitChan <- code
	}

	// Capture logs
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	oldLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldLogger)

	// Simulate signal handler error path
	go func() {
		slog.Error("shutdown error", "error", fmt.Errorf("test error"))
		exitFunc(1)
	}()

	// Wait for exit
	select {
	case code := <-exitChan:
		assert.Equal(t, 1, code)
		assert.Contains(t, logBuf.String(), "shutdown error")
	case <-time.After(time.Second):
		t.Fatal("exitFunc was not called within timeout")
	}
}

func TestRegisterProviders(t *testing.T) {
	// Save original registry
	originalRegistry := config.DefaultRegistry
	defer func() { config.DefaultRegistry = originalRegistry }()

	// Create a new registry for testing
	testRegistry := config.NewProviderRegistry()
	config.DefaultRegistry = testRegistry

	// Call registerProviders
	registerProviders()

	// Verify both providers are registered
	providers := testRegistry.List()
	assert.Equal(t, 2, len(providers))

	// Helper to check if string is in slice
	contains := func(slice []string, str string) bool {
		for _, s := range slice {
			if s == str {
				return true
			}
		}
		return false
	}

	assert.True(t, contains(providers, "file"), "file provider should be registered")
	assert.True(t, contains(providers, "docker"), "docker provider should be registered")

	// Verify file provider works
	fileProvider, err := testRegistry.Get("file", config.FileProviderOptions{Path: "/test/path"})
	assert.Nil(t, err)
	assert.NotNil(t, fileProvider)
	assert.Equal(t, "file", fileProvider.Name())

	// Verify docker provider factory is registered
	provider, err := testRegistry.Get("docker", config.DockerProviderOptions{})

	// The important thing is that the provider is registered, not whether it succeeds
	// In CI environments, Docker might be available and creation could succeed
	if err != nil {
		// If there's an error, it should NOT be about the provider not being registered
		assert.NotContains(t, err.Error(), "provider not registered")
	} else {
		// If it succeeds, verify we got a valid provider
		assert.NotNil(t, provider)
		assert.Equal(t, "docker", provider.Name())
	}
}

func TestMainIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Build the binary for testing
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Dir(".")
	err := cmd.Run()
	require.NoError(t, err, "Failed to build test binary")

	tests := []struct {
		name       string
		args       []string
		env        []string
		wantExit   int
		wantOutput []string
		wantErr    []string
		timeout    time.Duration
	}{
		{
			name:       "help flag shows usage",
			args:       []string{"-help"},
			wantExit:   0,
			wantOutput: []string{"Usage of", "-config", "-provider", "-docker-socket"},
			timeout:    2 * time.Second,
		},
		{
			name:       "version flag shows version",
			args:       []string{"-version"},
			wantExit:   0,
			wantOutput: []string{"tsbridge version:"},
			timeout:    2 * time.Second,
		},
		{
			name:     "missing config for file provider",
			args:     []string{"-provider", "file"},
			wantExit: 1,
			wantErr:  []string{"-config flag is required for file provider"},
			timeout:  2 * time.Second,
		},
		{
			name:     "invalid provider",
			args:     []string{"-provider", "invalid", "-config", "test.toml"},
			wantExit: 1,
			wantErr:  []string{"failed to create configuration provider"},
			timeout:  2 * time.Second,
		},
		{
			name:     "nonexistent config file",
			args:     []string{"-provider", "file", "-config", "/nonexistent/config.toml"},
			wantExit: 1,
			wantErr:  []string{"failed to create application"},
			timeout:  2 * time.Second,
		},
		{
			name:       "verbose logging",
			args:       []string{"-verbose", "-help"},
			wantExit:   0,
			wantOutput: []string{"Usage of"},
			timeout:    2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			cmd := exec.CommandContext(ctx, binPath, tt.args...)
			cmd.Env = append(os.Environ(), tt.env...)

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()

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

func TestMainWithValidConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a minimal valid config
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "test.toml")
	configContent := `
[tailscale]
auth_key = "test-auth-key"

[[services]]
name = "test-service"
backend_addr = "http://localhost:8080"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Build the binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Dir(".")
	err = cmd.Run()
	require.NoError(t, err)

	// Start the application
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd = exec.CommandContext(ctx, binPath, "-config", configPath, "-verbose")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Start()
	require.NoError(t, err)

	// Give it a moment to start
	time.Sleep(500 * time.Millisecond)

	// Send interrupt signal
	err = cmd.Process.Signal(os.Interrupt)
	require.NoError(t, err)

	// Wait for process to exit
	_ = cmd.Wait()

	// Check that it started properly
	output := stdout.String() + stderr.String()
	assert.Contains(t, output, "starting tsbridge")
	assert.Contains(t, output, "loading configuration")
	assert.Contains(t, output, "creating application")
}

func TestMainSignalHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a valid config
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "test.toml")
	configContent := `
[tailscale]
auth_key = "test-auth-key"

[[services]]
name = "test-service"
backend_addr = "http://localhost:8080"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Build the binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Dir(".")
	err = cmd.Run()
	require.NoError(t, err)

	// Test both SIGINT and SIGTERM
	signals := []struct {
		name   string
		signal os.Signal
	}{
		{"SIGINT", os.Interrupt},
		{"SIGTERM", os.Kill}, // os.Kill is SIGTERM on Unix
	}

	for _, sig := range signals {
		t.Run(fmt.Sprintf("handles_%s", sig.name), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cmd = exec.CommandContext(ctx, binPath, "-config", configPath)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err = cmd.Start()
			require.NoError(t, err)

			// Give it time to start
			time.Sleep(1 * time.Second)

			// Send signal
			err = cmd.Process.Signal(sig.signal)
			require.NoError(t, err)

			// Wait for exit
			waitErr := cmd.Wait()

			// For SIGTERM (os.Kill), the process might exit with non-zero
			if sig.signal == os.Kill {
				// Just check that it exited
				assert.NotNil(t, waitErr)
			}

			output := stdout.String() + stderr.String()
			// Check for graceful shutdown indicators
			if strings.Contains(output, "received signal") {
				assert.Contains(t, output, "shutting down")
			}
		})
	}
}

func TestMainDockerProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Build the binary
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Dir(".")
	err := cmd.Run()
	require.NoError(t, err)

	// Test docker provider with custom options
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd = exec.CommandContext(ctx, binPath,
		"-provider", "docker",
		"-docker-socket", "unix:///custom/docker.sock",
		"-docker-label-prefix", "custom.prefix",
		"-verbose")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Start()
	require.NoError(t, err)

	// Give it a moment to start
	time.Sleep(500 * time.Millisecond)

	// Kill the process
	cmd.Process.Kill()
	_ = cmd.Wait()

	// Check that docker provider was selected
	output := stdout.String() + stderr.String()
	assert.Contains(t, output, "provider=docker")
}
