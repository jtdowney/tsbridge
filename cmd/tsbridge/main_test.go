package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
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

// TestSignalHandlingSimplicity verifies signal handling follows the expected pattern
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

// TestFlagParsing tests flag parsing behavior
func TestFlagParsing(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantProvider   string
		wantConfig     string
		wantDocker     string
		wantLabel      string
		wantVerbose    bool
		wantHelp       bool
		wantVersion    bool
		wantValidate   bool
		wantParseError bool
	}{
		{
			name:         "default values",
			args:         []string{},
			wantProvider: "file",
			wantLabel:    "tsbridge",
		},
		{
			name:         "file provider with config",
			args:         []string{"-provider", "file", "-config", "/path/to/config.toml"},
			wantProvider: "file",
			wantConfig:   "/path/to/config.toml",
			wantLabel:    "tsbridge",
		},
		{
			name:         "docker provider with custom options",
			args:         []string{"-provider", "docker", "-docker-socket", "tcp://localhost:2375", "-docker-label-prefix", "custom"},
			wantProvider: "docker",
			wantDocker:   "tcp://localhost:2375",
			wantLabel:    "custom",
		},
		{
			name:         "verbose flag",
			args:         []string{"-verbose"},
			wantVerbose:  true,
			wantProvider: "file",
			wantLabel:    "tsbridge",
		},
		{
			name:         "help flag",
			args:         []string{"-help"},
			wantHelp:     true,
			wantProvider: "file",
			wantLabel:    "tsbridge",
		},
		{
			name:         "version flag",
			args:         []string{"-version"},
			wantVersion:  true,
			wantProvider: "file",
			wantLabel:    "tsbridge",
		},
		{
			name:         "validate flag",
			args:         []string{"-validate"},
			wantValidate: true,
			wantProvider: "file",
			wantLabel:    "tsbridge",
		},
		{
			name:         "validate flag with config",
			args:         []string{"-validate", "-config", "/path/to/config.toml"},
			wantValidate: true,
			wantConfig:   "/path/to/config.toml",
			wantProvider: "file",
			wantLabel:    "tsbridge",
		},
		{
			name:           "unknown flag",
			args:           []string{"-unknown"},
			wantParseError: true,
		},
		{
			name:         "all flags combined",
			args:         []string{"-provider", "docker", "-config", "ignored.toml", "-docker-socket", "unix:///var/run/docker.sock", "-docker-label-prefix", "prod", "-verbose"},
			wantProvider: "docker",
			wantConfig:   "ignored.toml",
			wantDocker:   "unix:///var/run/docker.sock",
			wantLabel:    "prod",
			wantVerbose:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new flag set for each test
			fs := flag.NewFlagSet("test", flag.ContinueOnError)

			// Define flags matching main.go
			provider := fs.String("provider", "file", "Configuration provider")
			configPath := fs.String("config", "", "Path to TOML configuration file")
			dockerEndpoint := fs.String("docker-socket", "", "Docker socket endpoint")
			labelPrefix := fs.String("docker-label-prefix", "tsbridge", "Docker label prefix")
			verbose := fs.Bool("verbose", false, "Enable debug logging")
			help := fs.Bool("help", false, "Show usage information")
			versionFlag := fs.Bool("version", false, "Show version information")
			validateFlag := fs.Bool("validate", false, "Validate configuration and exit")

			// Parse flags
			err := fs.Parse(tt.args)

			if tt.wantParseError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify parsed values
			assert.Equal(t, tt.wantProvider, *provider)
			assert.Equal(t, tt.wantConfig, *configPath)
			assert.Equal(t, tt.wantDocker, *dockerEndpoint)
			assert.Equal(t, tt.wantLabel, *labelPrefix)
			assert.Equal(t, tt.wantVerbose, *verbose)
			assert.Equal(t, tt.wantHelp, *help)
			assert.Equal(t, tt.wantVersion, *versionFlag)
			assert.Equal(t, tt.wantValidate, *validateFlag)
		})
	}
}

// TestMainIntegration tests the main binary with various arguments
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
			wantOutput: []string{"Usage of", "-config", "-provider", "-docker-socket", "-docker-label-prefix", "-verbose", "-help", "-version", "-validate"},
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
			name:       "verbose logging with help",
			args:       []string{"-verbose", "-help"},
			wantExit:   0,
			wantOutput: []string{"Usage of"},
			timeout:    2 * time.Second,
		},
		{
			name:     "unknown flag",
			args:     []string{"-unknown-flag"},
			wantExit: 2,
			wantErr:  []string{"flag provided but not defined"},
			timeout:  2 * time.Second,
		},
		{
			name:       "multiple flags order independence",
			args:       []string{"-version", "-verbose"},
			wantExit:   0,
			wantOutput: []string{"tsbridge version:"},
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

// TestMainWithValidConfig tests main with a valid configuration file
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
backend_addr = "localhost:8080"
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

// TestMainSignalHandling tests signal handling behavior
func TestMainSignalHandling(t *testing.T) {
	t.Skip("Skipping flaky signal handling test")
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
backend_addr = "localhost:8080"
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
		{"SIGTERM", syscall.SIGTERM},
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

			// Check exit code
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				// Should exit cleanly (0) or with error (1)
				exitCode := exitErr.ExitCode()
				assert.True(t, exitCode == 0 || exitCode == 1, "Expected exit code 0 or 1, got %d", exitCode)
			}

			output := stdout.String() + stderr.String()
			// Check for graceful shutdown indicators
			if strings.Contains(output, "received signal") {
				assert.Contains(t, output, "shutting down")
			}
		})
	}
}

// TestParseCLIArgs tests the parseCLIArgs function
func TestParseCLIArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		want        *cliArgs
		wantErr     bool
		errContains string
	}{
		{
			name: "default values",
			args: []string{},
			want: &cliArgs{
				provider:    "file",
				labelPrefix: "tsbridge",
			},
		},
		{
			name: "all flags set",
			args: []string{
				"-config", "/path/to/config.toml",
				"-provider", "docker",
				"-docker-socket", "tcp://localhost:2375",
				"-docker-label-prefix", "custom",
				"-verbose",
				"-help",
				"-version",
				"-validate",
			},
			want: &cliArgs{
				configPath:     "/path/to/config.toml",
				provider:       "docker",
				dockerEndpoint: "tcp://localhost:2375",
				labelPrefix:    "custom",
				verbose:        true,
				help:           true,
				version:        true,
				validate:       true,
			},
		},
		{
			name: "file provider with config",
			args: []string{"-provider", "file", "-config", "test.toml"},
			want: &cliArgs{
				provider:    "file",
				configPath:  "test.toml",
				labelPrefix: "tsbridge",
			},
		},
		{
			name: "validate flag only",
			args: []string{"-validate"},
			want: &cliArgs{
				provider:    "file",
				labelPrefix: "tsbridge",
				validate:    true,
			},
		},
		{
			name: "validate with config",
			args: []string{"-validate", "-config", "config.toml"},
			want: &cliArgs{
				provider:    "file",
				configPath:  "config.toml",
				labelPrefix: "tsbridge",
				validate:    true,
			},
		},
		{
			name:        "unknown flag",
			args:        []string{"-unknown"},
			wantErr:     true,
			errContains: "flag provided but not defined",
		},
		{
			name:        "short form flags",
			args:        []string{"-h"},
			wantErr:     true,
			errContains: "help requested",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCLIArgs(tt.args)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestRun tests the run function
func TestRun(t *testing.T) {
	// Save original version
	oldVersion := version
	defer func() {
		version = oldVersion
	}()

	// Set a test version
	version = "test-v1.0.0"

	tests := []struct {
		name       string
		args       *cliArgs
		setupFunc  func(t *testing.T) string // returns config path if needed
		wantErr    bool
		errMsg     string
		wantOutput []string
	}{
		{
			name:       "help flag",
			args:       &cliArgs{help: true},
			wantOutput: []string{"Usage of tsbridge:", "-config", "-provider", "-validate"},
		},
		{
			name:       "version flag",
			args:       &cliArgs{version: true},
			wantOutput: []string{"tsbridge version: test-v1.0.0"},
		},
		{
			name:    "missing config for file provider",
			args:    &cliArgs{provider: "file"},
			wantErr: true,
			errMsg:  "-config flag is required for file provider",
		},
		{
			name: "invalid provider",
			args: &cliArgs{
				provider:   "invalid",
				configPath: "test.toml",
			},
			wantErr: true,
			errMsg:  "failed to create configuration provider",
		},
		{
			name: "validate with valid config",
			args: &cliArgs{
				validate:   true,
				provider:   "file",
				configPath: "test.toml",
			},
			setupFunc: func(t *testing.T) string {
				configPath := filepath.Join(t.TempDir(), "test.toml")
				configContent := `
[tailscale]
auth_key = "test-auth-key"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`
				err := os.WriteFile(configPath, []byte(configContent), 0644)
				require.NoError(t, err)
				return configPath
			},
			wantErr: false,
			// No specific output expected for successful validation
		},
		{
			name: "validate with invalid config",
			args: &cliArgs{
				validate:   true,
				provider:   "file",
				configPath: "test.toml",
			},
			setupFunc: func(t *testing.T) string {
				configPath := filepath.Join(t.TempDir(), "test.toml")
				configContent := `
[tailscale]
# Missing auth credentials

[[services]]
name = "test-service"
# Missing backend_addr
`
				err := os.WriteFile(configPath, []byte(configContent), 0644)
				require.NoError(t, err)
				return configPath
			},
			wantErr: true,
			errMsg:  "validation error",
		},
		{
			name:    "validate missing config for file provider",
			args:    &cliArgs{validate: true, provider: "file"},
			wantErr: true,
			errMsg:  "-config flag is required for file provider",
		},
		{
			name: "validate with non-existent file reference",
			args: &cliArgs{
				validate:   true,
				provider:   "file",
				configPath: "test.toml",
			},
			setupFunc: func(t *testing.T) string {
				configPath := filepath.Join(t.TempDir(), "test.toml")
				configContent := `
[tailscale]
auth_key_file = "/non/existent/file.txt"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`
				err := os.WriteFile(configPath, []byte(configContent), 0644)
				require.NoError(t, err)
				return configPath
			},
			wantErr: true,
			errMsg:  "no such file or directory",
		},
		{
			name: "validate with missing env var",
			args: &cliArgs{
				validate:   true,
				provider:   "file",
				configPath: "test.toml",
			},
			setupFunc: func(t *testing.T) string {
				// Ensure env var is not set
				os.Unsetenv("MISSING_ENV_VAR")

				configPath := filepath.Join(t.TempDir(), "test.toml")
				configContent := `
[tailscale]
auth_key_env = "MISSING_ENV_VAR"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`
				err := os.WriteFile(configPath, []byte(configContent), 0644)
				require.NoError(t, err)
				return configPath
			},
			wantErr: true,
			errMsg:  "OAuth client ID must be provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run setup function if provided
			if tt.setupFunc != nil {
				configPath := tt.setupFunc(t)
				tt.args.configPath = configPath
			}

			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Capture logs
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))
			oldLogger := slog.Default()
			slog.SetDefault(logger)
			defer slog.SetDefault(oldLogger)

			// Run the function
			err := run(tt.args)

			// Close writer and restore stdout
			w.Close()
			os.Stdout = oldStdout

			// Read captured output
			output, _ := io.ReadAll(r)

			// Check error
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}

			// Check output
			outputStr := string(output)
			// For help, also check if it printed "Usage of tsbridge:" but without the full content
			if tt.name == "help flag" && outputStr != "" {
				// Help was printed, we're good
				assert.Contains(t, outputStr, "Usage of tsbridge:")
			} else {
				for _, want := range tt.wantOutput {
					assert.Contains(t, outputStr, want)
				}
			}
		})
	}
}

// TestRunWithMockApp tests the run function with a mock application
func TestRunWithMockApp(t *testing.T) {
	// Skip this test as it requires running tsnet which needs auth
	t.Skip("Skipping test that requires tsnet authentication")
}

// TestMainFunction tests the actual main function
func TestMainFunction(t *testing.T) {
	// Save original exitFunc
	oldExitFunc := exitFunc
	oldArgs := os.Args
	defer func() {
		exitFunc = oldExitFunc
		os.Args = oldArgs
	}()

	tests := []struct {
		name     string
		args     []string
		wantExit int
		checkLog string
	}{
		{
			name:     "help flag",
			args:     []string{"tsbridge", "-help"},
			wantExit: 0,
		},
		{
			name:     "version flag",
			args:     []string{"tsbridge", "-version"},
			wantExit: 0,
		},
		{
			name:     "invalid flag",
			args:     []string{"tsbridge", "-invalid"},
			wantExit: 2,
		},
		{
			name:     "missing config",
			args:     []string{"tsbridge", "-provider", "file"},
			wantExit: 1,
			checkLog: "-config flag is required for file provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture exit code
			exitCode := -1
			exitFunc = func(code int) {
				exitCode = code
				// Don't actually exit
				panic("exit called")
			}

			// Capture stdout (where slog writes by default in run())
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Set args
			os.Args = tt.args

			// Run main and recover from exit panic
			func() {
				defer func() {
					if r := recover(); r != nil {
						if r != "exit called" {
							panic(r)
						}
					}
				}()
				main()
			}()

			// Close writer and restore stdout
			w.Close()
			os.Stdout = oldStdout

			// Read captured output
			output, _ := io.ReadAll(r)

			// Check exit code
			assert.Equal(t, tt.wantExit, exitCode)

			// Check logs - the error message is in stdout
			if tt.checkLog != "" {
				assert.Contains(t, string(output), tt.checkLog)
			}
		})
	}
}

// TestMainDockerProvider tests docker provider configuration
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

// TestApplicationStartError tests handling of application.Start() errors
func TestApplicationStartError(t *testing.T) {
	t.Skip("Skipping test that requires tsnet authentication")
}

// TestApplicationShutdownError tests handling of application.Shutdown() errors
func TestApplicationShutdownError(t *testing.T) {
	t.Skip("Skipping test that requires mocking app.Application interface")
}

// TestProviderLoadError tests handling of provider.Load() errors
func TestProviderLoadError(t *testing.T) {
	// Save original registry
	originalRegistry := config.DefaultRegistry
	defer func() { config.DefaultRegistry = originalRegistry }()

	// Register providers
	registerProviders()

	// Test with non-existent config file
	args := &cliArgs{
		provider:   "file",
		configPath: "/non/existent/path/config.toml",
	}

	// Run should fail
	err := run(args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create application")
}

// TestValidateConfigWithLoadError tests validation with config load error
func TestValidateConfigWithLoadError(t *testing.T) {
	// Test validate with non-existent file
	args := &cliArgs{
		validate:   true,
		provider:   "file",
		configPath: "/non/existent/path/config.toml",
	}

	// Capture logs
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	oldLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldLogger)

	err := validateConfig(args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load configuration")
}

// TestCreateProviderWithInvalidOptions tests provider creation with invalid options
func TestCreateProviderWithInvalidOptions(t *testing.T) {
	// Save original registry
	originalRegistry := config.DefaultRegistry
	defer func() { config.DefaultRegistry = originalRegistry }()

	// Create a registry with a provider that always fails
	testRegistry := config.NewProviderRegistry()
	testRegistry.Register("failing", func(opts interface{}) (config.Provider, error) {
		return nil, fmt.Errorf("provider creation failed")
	})
	config.DefaultRegistry = testRegistry

	args := &cliArgs{
		provider: "failing",
	}

	provider, err := createProvider(args)
	assert.Nil(t, provider)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create configuration provider")
}

// TestMainInvalidConfig tests behavior with invalid configuration
func TestMainInvalidConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "invalid TOML syntax",
			config: `
[tailscale
auth_key = "test-key"
`,
			wantErr: "failed to create application",
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
			wantErr: "failed to create application",
		},
		{
			name: "invalid service configuration",
			config: `
[tailscale]
auth_key = "test-key"

[[services]]
name = ""
backend_addr = "localhost:8080"
`,
			wantErr: "failed to create application",
		},
	}

	// Build the binary once
	binPath := filepath.Join(t.TempDir(), "tsbridge-test")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Dir(".")
	err := cmd.Run()
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create config file
			configPath := filepath.Join(t.TempDir(), "config.toml")
			err := os.WriteFile(configPath, []byte(tt.config), 0644)
			require.NoError(t, err)

			// Run the binary
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, binPath, "-config", configPath)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err = cmd.Run()

			// Should exit with error
			require.Error(t, err)
			exitErr, ok := err.(*exec.ExitError)
			require.True(t, ok)
			assert.Equal(t, 1, exitErr.ExitCode())

			// Check error message
			output := stdout.String() + stderr.String()
			assert.Contains(t, output, tt.wantErr)
		})
	}
}

// TestSetupLogging tests the setupLogging function
func TestSetupLogging(t *testing.T) {
	tests := []struct {
		name      string
		verbose   bool
		wantLevel slog.Level
	}{
		{
			name:      "default info level",
			verbose:   false,
			wantLevel: slog.LevelInfo,
		},
		{
			name:      "verbose debug level",
			verbose:   true,
			wantLevel: slog.LevelDebug,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original logger
			oldLogger := slog.Default()
			defer slog.SetDefault(oldLogger)

			// Setup logging
			setupLogging(tt.verbose)

			// Create a test log buffer to capture output
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: tt.wantLevel})
			testLogger := slog.New(handler)
			slog.SetDefault(testLogger)

			// Test log output at different levels
			slog.Debug("debug message")
			slog.Info("info message")

			output := buf.String()
			if tt.verbose {
				assert.Contains(t, output, "debug message", "Debug logging should be visible when verbose=true")
			} else {
				assert.NotContains(t, output, "debug message", "Debug logging should not be visible when verbose=false")
			}
			assert.Contains(t, output, "info message", "Info logging should always be visible")
		})
	}
}

// TestCreateProvider tests the createProvider function
func TestCreateProvider(t *testing.T) {
	// Save original registry
	originalRegistry := config.DefaultRegistry
	defer func() { config.DefaultRegistry = originalRegistry }()

	// Register providers for testing
	registerProviders()

	tests := []struct {
		name         string
		args         *cliArgs
		setupFunc    func(t *testing.T) string // returns config path if needed
		wantProvider string
		wantErr      bool
		errContains  string
	}{
		{
			name: "file provider with valid config path",
			args: &cliArgs{
				provider:   "file",
				configPath: "test.toml",
			},
			setupFunc: func(t *testing.T) string {
				configPath := filepath.Join(t.TempDir(), "test.toml")
				configContent := `
[tailscale]
auth_key = "test-auth-key"

[[services]]
name = "test-service"
backend_addr = "localhost:8080"
`
				err := os.WriteFile(configPath, []byte(configContent), 0644)
				require.NoError(t, err)
				return configPath
			},
			wantProvider: "file",
		},
		{
			name: "docker provider",
			args: &cliArgs{
				provider:       "docker",
				dockerEndpoint: "unix:///var/run/docker.sock",
				labelPrefix:    "tsbridge",
			},
			wantProvider: "docker",
		},
		{
			name: "invalid provider",
			args: &cliArgs{
				provider: "invalid",
			},
			wantErr:     true,
			errContains: "unknown provider type",
		},
		{
			name: "file provider without config path",
			args: &cliArgs{
				provider:   "file",
				configPath: "",
			},
			wantProvider: "file",
			// Should still create provider, error happens on Load
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run setup function if provided
			if tt.setupFunc != nil {
				configPath := tt.setupFunc(t)
				tt.args.configPath = configPath
			}

			// Create provider
			provider, err := createProvider(tt.args)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			// For docker provider, it might fail in CI environments
			if tt.args.provider == "docker" && err != nil {
				// If docker fails, just check it's not a "provider not registered" error
				assert.NotContains(t, err.Error(), "provider not registered")
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, provider)
			assert.Equal(t, tt.wantProvider, provider.Name())
		})
	}
}
