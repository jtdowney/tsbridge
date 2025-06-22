package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/testutil"
	"log/slog"
	"os"
	"os/signal"
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
		testutil.AssertEqual(t, context.Canceled, ctx.Err())
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
	testutil.AssertTrue(t, exitCalled, "exitFunc should have been called")
	testutil.AssertEqual(t, 1, exitCode)
	testutil.AssertTrue(t, deferExecuted, "defer should have been executed")
	testutil.AssertContains(t, logBuf.String(), "test error")
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
		testutil.AssertEqual(t, 1, code)
		testutil.AssertContains(t, logBuf.String(), "shutdown error")
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
	testutil.AssertEqual(t, 2, len(providers))

	// Helper to check if string is in slice
	contains := func(slice []string, str string) bool {
		for _, s := range slice {
			if s == str {
				return true
			}
		}
		return false
	}

	testutil.AssertTrue(t, contains(providers, "file"), "file provider should be registered")
	testutil.AssertTrue(t, contains(providers, "docker"), "docker provider should be registered")

	// Verify file provider works
	fileProvider, err := testRegistry.Get("file", config.FileProviderOptions{Path: "/test/path"})
	testutil.AssertNil(t, err)
	testutil.AssertNotNil(t, fileProvider)
	testutil.AssertEqual(t, "file", fileProvider.Name())

	// Verify docker provider factory is registered (we can't test actual creation without Docker)
	_, err = testRegistry.Get("docker", config.DockerProviderOptions{})
	// Should get an error about Docker connection, not about provider not being registered
	testutil.AssertNotNil(t, err)
	if err != nil {
		testutil.AssertNotContains(t, err.Error(), "provider not registered")
	}
}
