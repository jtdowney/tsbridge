# tsbridge Project Instructions

tsbridge is a Go-based proxy manager built on Tailscale's tsnet library. It allows multiple named services on a Tailnet to be configured via a single TOML file.

## Commands

- `make build` - Build binary with git SHA version
- `make test` - Run all tests (`go test ./...`)
- `make lint` - Run golangci-lint (`golangci-lint run ./...`)
- `make fmt` - Format code
- `make vet` - Run go vet
- `make tidy` - Run go mod tidy
- `make clean` - Remove built binaries
- `make run ARGS="-config example.toml"` - Build and run
- `make integration` - Run integration tests (build tag: `integration`)
- `make release` - Build release binaries via goreleaser
- `make release-snapshot` - Build release snapshot without tagging
- `make docker-push-sha` - Build and push multi-arch Docker images with SHA tag
- Run specific test: `go test -run TestName ./path/to/package`
- Race detection: `go test -race ./...`
- Coverage: `go test -cover ./...`

## Project Structure

```
tsbridge/
├── cmd/tsbridge/        # Main application entry point
├── internal/
│   ├── app/            # Application lifecycle management
│   ├── config/         # Configuration parsing and validation
│   ├── constants/      # Shared constants
│   ├── dialer/         # Network dialer implementations
│   ├── docker/         # Docker label-based configuration
│   ├── errors/         # Custom error types
│   ├── metrics/        # Prometheus metrics
│   ├── middleware/     # HTTP middleware (access logs, request ID, whois)
│   ├── proxy/          # Reverse proxy implementation
│   ├── service/        # Service registry and management
│   ├── tailscale/      # Tailscale integration utilities
│   ├── testhelpers/    # Simple test utilities shared across packages
│   ├── testutil/       # Common test utilities (logging, etc.)
│   └── tsnet/          # Tailscale tsnet integration
├── test/integration/   # Integration test suite
│   └── helpers/        # Integration test fixtures and helpers
├── deployments/        # Deployment configs (FreeBSD, systemd)
├── docs/               # Documentation (config reference, metrics, quickstart)
├── example/            # Example backend server
└── assets/             # Project assets (logo)
```

## Key Dependencies

- Go 1.26+ required
- `tailscale.com` - Core Tailscale networking
- `github.com/docker/docker` - Docker API for label-based config
- `github.com/prometheus/client_golang` - Metrics
- `github.com/knadh/koanf` - Configuration management
- `github.com/stretchr/testify` - Test assertions (assert + require)
- `github.com/cenkalti/backoff/v4` - Retry logic
- `github.com/hashicorp/golang-lru/v2` - LRU caching

## Development Workflow

1. Follow TDD: write failing tests first, then implement
2. Use conventional commit format: `feat:`, `fix:`, `docs:`, `chore:`, `test:`, `refactor:`, `perf:`, `ci:`
3. Include issue/PR number when applicable: `fix: correct validation (#42)`
4. Update CHANGELOG.md for notable changes before release
5. Run `make lint` and `make test` before marking any task complete
6. Pre-commit hooks run automatically: go-mod-tidy, go-fmt, golangci-lint (v2)

## Testing Guidelines

- Unit tests go next to the code (`config.go` → `config_test.go`)
- Integration tests go in `test/integration/` with build tag `integration`
- Use table-driven tests for multiple scenarios
- Mock external dependencies (tsnet, filesystem) for unit tests
- Use Testify: `assert` for non-critical checks, `require` for critical ones
- Use `t.Helper()`, `t.Cleanup()`, `t.TempDir()` as appropriate

## Architecture Conventions

- **Error handling**: Wrap errors with context via `fmt.Errorf("context: %w", err)`. Log at the handling point, not creation.
- **Logging**: Structured logging with consistent field names. Levels: Debug (-verbose), Info (default), Warn, Error. Include service name in service-specific logs. Never log OAuth tokens or secrets.
- **Config**: Loading order is CLI flags → TOML file → Environment variables. Validate at startup, fail fast. Global defaults can be overridden per-service.
- **Metrics**: Prefix all with `tsbridge_`, include `service` label, follow Prometheus naming conventions.
- **Access logs**: Enabled by default. Fields: method, path, status, size, duration_ms, request_id, user_agent, remote_addr.
- **Package comments**: Every package needs `// Package <name> <verb> <description>.` immediately before the `package` declaration.

## Linting

- golangci-lint v2 config (`.golangci.yml`) with `default: standard` plus: gocognit, gocritic, gocyclo, gosec
- gosec exclusion: G706
- Linting rules relaxed for test files (errcheck, gocognit, gocyclo, gosec excluded)

## Common Gotchas

1. tsnet requires OAuth client credentials — always validate these exist at startup
2. Unix socket paths must be absolute and start with `unix://`
3. Service names must be unique across the config
4. Graceful shutdown must wait for in-flight requests
5. Whois lookups can timeout — always enforce whois_timeout

## Versioning and Release

- Semantic Versioning with `vX.Y.Z` tags
- CHANGELOG.md in [Keep a Changelog](https://keepachangelog.com/) format
- Releases automated via GitHub Actions + GoReleaser
- Artifacts: multi-platform binaries, Docker images, checksums

## CI/CD

- GitHub Actions: `ci.yml` (tests, linting, builds), `lint.yml` (dedicated linting), `release.yml` (version tags)
- Race detection enabled in CI
- All checks must pass before merging
