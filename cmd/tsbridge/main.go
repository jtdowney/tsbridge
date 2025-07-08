// Package main provides the tsbridge CLI application for managing Tailscale proxy services.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jtdowney/tsbridge/internal/app"
	"github.com/jtdowney/tsbridge/internal/config"
	"github.com/jtdowney/tsbridge/internal/constants"
	"github.com/jtdowney/tsbridge/internal/docker"
	"log/slog"
)

var version = "dev"

// exitFunc allows tests to override os.Exit
var exitFunc = os.Exit

// registerProviders explicitly registers all available providers
func registerProviders() {
	// Register file provider
	config.DefaultRegistry.Register("file", config.FileProviderFactory)

	// Register docker provider
	config.DefaultRegistry.Register("docker", config.DockerProviderFactory(func(opts config.DockerProviderOptions) (config.Provider, error) {
		return docker.NewProvider(docker.Options{
			DockerEndpoint: opts.DockerEndpoint,
			LabelPrefix:    opts.LabelPrefix,
		})
	}))
}

// cliArgs holds parsed command-line arguments
type cliArgs struct {
	configPath     string
	provider       string
	dockerEndpoint string
	labelPrefix    string
	verbose        bool
	help           bool
	version        bool
	validate       bool
}

// parseCLIArgs parses command-line arguments and returns the parsed values
func parseCLIArgs(args []string) (*cliArgs, error) {
	fs := flag.NewFlagSet("tsbridge", flag.ContinueOnError)

	result := &cliArgs{}
	fs.StringVar(&result.configPath, "config", "", "Path to TOML configuration file (required for file provider)")
	fs.StringVar(&result.provider, "provider", "file", "Configuration provider (file or docker)")
	fs.StringVar(&result.dockerEndpoint, "docker-socket", "", "Docker socket endpoint (default: unix:///var/run/docker.sock)")
	fs.StringVar(&result.labelPrefix, "docker-label-prefix", "tsbridge", "Docker label prefix for configuration")
	fs.BoolVar(&result.verbose, "verbose", false, "Enable debug logging")
	fs.BoolVar(&result.help, "help", false, "Show usage information")
	fs.BoolVar(&result.version, "version", false, "Show version information")
	fs.BoolVar(&result.validate, "validate", false, "Validate configuration and exit")

	// Create usage function
	usage := func() {
		fmt.Fprintf(os.Stdout, "Usage of %s:\n", fs.Name())
		fs.PrintDefaults()
	}
	fs.Usage = usage

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Set the global flag.Usage to match
	flag.Usage = usage

	return result, nil
}

// setupLogging configures the global logger based on the verbose flag
func setupLogging(verbose bool) {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if verbose {
		opts.Level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)
}

// setupCommon configures logging and validates provider-specific flags
func setupCommon(args *cliArgs) error {
	// Configure logging
	setupLogging(args.verbose)

	// Validate provider-specific flags
	if args.provider == "file" && args.configPath == "" {
		return fmt.Errorf("-config flag is required for file provider")
	}
	return nil
}

// createProvider creates a configuration provider based on the CLI arguments
func createProvider(args *cliArgs) (config.Provider, error) {
	dockerOpts := config.DockerProviderOptions{
		DockerEndpoint: args.dockerEndpoint,
		LabelPrefix:    args.labelPrefix,
	}

	provider, err := config.NewProvider(args.provider, args.configPath, dockerOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration provider: %w", err)
	}

	return provider, nil
}

// validateConfig validates the configuration and returns an error if invalid
func validateConfig(args *cliArgs) error {
	// Register all available providers
	registerProviders()

	// Perform common setup
	if err := setupCommon(args); err != nil {
		return err
	}

	slog.Debug("validating configuration", "provider", args.provider)

	// Create configuration provider
	configProvider, err := createProvider(args)
	if err != nil {
		return err
	}

	slog.Debug("loading configuration for validation", "provider", configProvider.Name())

	// Load the configuration
	cfg, err := configProvider.Load(context.Background())
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate the configuration
	if err := cfg.Validate(args.provider); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	slog.Info("configuration is valid")
	return nil
}

// Application interface for testing
type Application interface {
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// Allow replacing the app factory for tests.
var newApp = func(cfg *config.Config, opts app.Options) (Application, error) {
	return app.NewAppWithOptions(cfg, opts)
}

// run executes the main application logic
func run(args *cliArgs, sigCh <-chan os.Signal) error {
	// Register all available providers
	registerProviders()

	if args.help {
		flag.Usage()
		return nil
	}

	if args.version {
		fmt.Printf("tsbridge version: %s\n", version)
		return nil
	}

	// Check if we're in validation mode
	if args.validate {
		return validateConfig(args)
	}

	// Perform common setup
	if err := setupCommon(args); err != nil {
		return err
	}

	slog.Debug("starting tsbridge", "version", version, "provider", args.provider)

	// Create configuration provider
	configProvider, err := createProvider(args)
	if err != nil {
		return err
	}

	slog.Debug("loading configuration", "provider", configProvider.Name())

	// Create the application with the provider
	slog.Debug("creating application")
	application, err := newApp(nil, app.Options{
		Provider: configProvider,
	})
	if err != nil {
		return fmt.Errorf("failed to create application: %w", err)
	}

	// Start the application
	ctx := context.Background()
	if err := application.Start(ctx); err != nil {
		return fmt.Errorf("failed to start application: %w", err)
	}

	// Wait for signal
	sig := <-sigCh
	slog.Info("received signal, shutting down", "signal", sig)

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), constants.DefaultShutdownTimeout)
	defer cancel()

	// Call shutdown
	if err := application.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	return nil
}

func main() {
	args, err := parseCLIArgs(os.Args[1:])
	if err != nil {
		// Flag parsing errors already printed by flag package
		exitFunc(2)
	}

	// Setup signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	if err := run(args, sigCh); err != nil {
		slog.Error("error", "error", err)
		exitFunc(1)
	}

	exitFunc(0)
}
