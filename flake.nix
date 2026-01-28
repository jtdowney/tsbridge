{
  description = "Development environment for tsbridge - a Go-based proxy manager built on Tailscale's tsnet library";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = nixpkgs.legacyPackages.${system};

        go = pkgs.go_1_26;
      in {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            # Core development tools
            go
            gotools
            gopls # Go language server for IDE support

            # Testing and quality tools
            golangci-lint # Main linter as specified in AGENTS.md
            go-tools # Multiple tools like staticcheck
            govulncheck # Go vulnerability checker

            # Build and automation tools
            gnumake # For Makefile targets
            goreleaser # For releases (mentioned in Makefile)

            # Development workflow tools
            pre-commit # Pre-commit hooks
            git # Version control and for version info in builds

            # Docker tools (for container builds mentioned in Makefile)
            docker
            docker-buildx
          ];

          # Environment variables
          shellHook = ''
            echo "🚀 tsbridge development environment loaded!"
            echo ""
            echo "Available commands:"
            echo "  make build       - Build the tsbridge binary"
            echo "  make test        - Run all tests"
            echo "  make lint        - Run golangci-lint"
            echo "  make fmt         - Format Go code"
            echo "  make vet         - Run go vet"
            echo "  make tidy        - Run go mod tidy"
            echo "  make integration - Run integration tests"
            echo "  make run ARGS=... - Build and run with arguments"
            echo ""
            echo "Development workflow:"
            echo "  pre-commit install        - Install pre-commit hooks"
            echo "  go test ./...            - Run unit tests"
            echo "  go test -cover ./...     - Run tests with coverage"
            echo "  go test -race ./...      - Run tests with race detection"
            echo "  golangci-lint run        - Run comprehensive linting"
            echo "  staticcheck ./...        - Run static analysis"
            echo "  govulncheck ./...        - Check for vulnerabilities"
            echo ""
            echo "Go version: $(go version)"
            echo "Project: tsbridge - Tailscale tsnet proxy manager"
            echo ""

            # Set up Go environment
            export GOPATH="${pkgs.buildGoModule}/share/go"
            export GOCACHE="$PWD/.cache/go-build"
            export GOMODCACHE="$PWD/.cache/go-mod"

            # Create cache directories if they don't exist
            mkdir -p .cache/go-build .cache/go-mod

            # Check if pre-commit is installed and suggest setup
            if [ ! -f .git/hooks/pre-commit ]; then
              echo "💡 Tip: Run 'pre-commit install' to set up Git hooks for automatic formatting and linting"
            fi

            # Verify Go module is ready
            if [ ! -f go.sum ]; then
              echo "📦 Running go mod download to fetch dependencies..."
              go mod download
            fi
          '';

          # Additional environment variables for Go development
          CGO_ENABLED = "1";

          # Set Go build flags for development
          GOFLAGS = "-buildvcs=true";
        };

        # Optional: Add packages that can be built from this flake
        packages.default = pkgs.buildGoModule {
          pname = "tsbridge";
          version = "dev";

          src = ./.;

          vendorHash = null; # Will need to be updated when dependencies change

          ldflags = [
            "-X main.version=dev"
          ];

          meta = with pkgs.lib; {
            description = "A Go-based proxy manager built on Tailscale's tsnet library";
            homepage = "https://github.com/jtdowney/tsbridge";
            license = licenses.mit; # Adjust based on actual license
            maintainers = [];
          };
        };
      }
    );
}
