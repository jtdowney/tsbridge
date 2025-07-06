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

// run executes the main application logic
func run(args *cliArgs) error {
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

	// Configure logging
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if args.verbose {
		opts.Level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Validate provider-specific flags
	if args.provider == "file" && args.configPath == "" {
		return fmt.Errorf("-config flag is required for file provider")
	}

	slog.Debug("starting tsbridge", "version", version, "provider", args.provider)

	// Create configuration provider
	dockerOpts := config.DockerProviderOptions{
		DockerEndpoint: args.dockerEndpoint,
		LabelPrefix:    args.labelPrefix,
	}

	configProvider, err := config.NewProvider(args.provider, args.configPath, dockerOpts)
	if err != nil {
		return fmt.Errorf("failed to create configuration provider: %w", err)
	}

	slog.Debug("loading configuration", "provider", configProvider.Name())

	// Create the application with the provider
	slog.Debug("creating application")
	application, err := app.NewAppWithOptions(nil, app.Options{
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

	// Setup signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

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

	if err := run(args); err != nil {
		slog.Error("error", "error", err)
		exitFunc(1)
	}

	exitFunc(0)
}
