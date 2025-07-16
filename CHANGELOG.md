# Changelog

All notable changes to tsbridge will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.9.2] - 2025-07-12

### Added

- Trace-level debug logging for tsnet authentication and startup (#53)
  - Provides detailed debugging information for troubleshooting connection issues
  - Helps diagnose tsnet authentication and initialization problems
  - Only active when verbose logging is enabled

### Fixed

- Skip state check for ephemeral services (#55)
  - Ephemeral services no longer fail startup when state directory is unavailable
  - Improves compatibility with containerized and stateless deployments
  - Ephemeral services now work correctly without persistent state requirements
  - Thanks to @svenvg93 for reporting the bug

### Development

- CI: Capture GoReleaser artifacts on PR builds (#54)
  - Artifacts from PR builds are now available for testing
  - Improves pre-release testing workflow

### Documentation

- Fixed docker compose example for v0.7.0 breaking change
- Removed redundant ReadWritePaths from systemd service documentation

## [0.9.1] - 2025-07-10

### Fixed

- Added TSBRIDGE_DEBUG environment variable support (#50)
  - The systemd documentation referenced TSBRIDGE_DEBUG but it was not actually implemented
  - Now supports enabling verbose logging via TSBRIDGE_DEBUG environment variable
  - The -verbose CLI flag takes precedence if both are set

## [0.9.0] - 2025-07-10

### Added

- STATE_DIRECTORY environment variable support (#49)
  - Allows overriding the default state directory location
  - Useful for containerized deployments and systems with specific directory requirements
  - Thanks to @namelessjon for the suggestion
- Retry logic with exponential backoff (#47)
  - Improved resilience for backend connections
  - Configurable retry attempts and delays
  - Exponential backoff prevents overwhelming failing backends

### Fixed

- Improved signal handling and CLI behavior
  - Better graceful shutdown handling
  - More responsive to interrupt signals
- Resolved duplicate provider registration issue
  - Fixed panic when multiple configuration providers were registered
  - Improved provider initialization logic

### Development

- Increased test coverage for OAuth error handling
- Simplified service configuration comparison using go-cmp library
- Consolidated test helper functions for better maintainability
- Simplified Duration and ByteSize configuration types
- Added RedactedString type for sensitive configuration fields
- Refactored main.go with more focused, testable functions

## [0.8.0] - 2025-07-07

### Added

- FreeBSD deployment support (#41)
  - Added FreeBSD to GoReleaser configuration
  - Expanded platform support for broader deployment options
- Config validation command (#42)
  - Added `validate` subcommand to verify configuration files without starting services
  - Useful for CI/CD pipelines and pre-deployment checks
  - Validates TOML syntax and configuration semantics

## [0.7.1] - 2025-07-07

### Fixed

- Fixed handling of zero value for max_request_body_size configuration (#35)
  - Explicitly set zero values are now properly respected
  - Zero value disables request body size limits as intended

### Dependencies

- Bumped github.com/docker/docker to latest version (#39)

### Development

- Added auto-cleanup for architecture-specific Docker tags (#38)
  - Reduces registry clutter during releases
  - Improves build pipeline efficiency
- Added minor version tags to Docker releases (#40)
  - Docker images now tagged with both full version (e.g., v0.7.1) and minor version (e.g., v0.7)
  - Allows users to track minor version updates automatically

## [0.7.0] - 2025-07-05

### Added

- Dynamic service management support for Docker provider (#27)
  - Services can now be dynamically added/removed based on Docker container lifecycle
  - Improved resource management and service lifecycle handling
- Configurable request body size limits (#33)
  - Added `max_request_body_size` configuration option
  - Helps prevent resource exhaustion from large request bodies
  - Configurable per-service or globally
- Per-service tag support for OAuth authentication (#25)
  - Services can now have individual tags for better Tailscale ACL control
  - Tags can be specified per-service or inherited from global defaults
- Support for both 'enable' and 'enabled' Docker labels (#31)
  - Improved compatibility with different labeling conventions
  - More flexible Docker label parsing

### Fixed

- OAuth auth key security posture improvements (#34)
  - Enhanced validation and handling of OAuth credentials
  - Better error messages for authentication issues
- HTTP header injection prevention in whois middleware (#32)
  - Added proper sanitization to prevent header injection attacks
  - Improved security of whois information handling
- Fixed WebSocket support in request body limit middleware
  - Added http.Hijacker interface support to maxBytesResponseWriter
  - Ensures WebSocket connections work properly with max_request_body_size configured

### Changed

- **BREAKING:** OAuth tag configuration changes (#25)
  - Renamed `oauth_tags` in `[tailscale]` section to `default_tags`
  - OAuth-authenticated services now MUST have at least one tag (either inherited from `default_tags` or explicitly set via `tags` field)
  - Migration: Update `oauth_tags` to `default_tags` in your configuration files
- Consolidated metrics server constructor functions (#24)
  - Cleaner API for metrics server initialization
  - Reduced code duplication

### Development

- Improved test reliability and fixed goroutine leaks (#26)
- Added GoReleaser support for PR Docker builds (#28)
- Enhanced Docker documentation with clearer port vs backend_addr usage
- Added comprehensive inline documentation to config structs

## [0.6.1] - 2025-07-02

### Fixed

- Handle zero-duration timeouts correctly (#21)
  - Fixed panic when timeout durations were set to zero
  - Zero durations now properly disable the respective timeout
- Add missing flush_interval parsing to Docker labels
  - Docker label provider now correctly parses flush_interval configuration
  - Ensures feature parity between file and Docker label configuration

### Changed

- Clean up codebase and remove dead code
  - Removed unused functions and variables
  - Improved code organization and maintainability

## [0.6.0] - 2025-07-01

### Added

- Flush interval configuration for proxy handler (#18)
  - Added `flush_interval` configuration option (e.g., "10ms", "100ms")
  - Allows control over response buffering behavior
  - Useful for streaming responses and Server-Sent Events (SSE)

### Changed

- Replaced ReadTimeout with ReadHeaderTimeout for long-lived requests (#17)
  - Supports WebSocket connections and streaming responses
  - Prevents timeout issues with long-running connections
  - ReadHeaderTimeout defaults to 30 seconds

### Fixed

- WebSocket support in metrics and access log middleware (#19)
  - Fixed panic when WebSocket connections were used
  - Properly handles WebSocket upgrade requests
  - Access logs now correctly log WebSocket connections
- Disabled compression and HTTP/2 in proxy transport (#20)
  - Prevents issues with certain backend servers
  - Improves compatibility with various HTTP implementations
  - Transport now uses HTTP/1.1 only

## [0.5.0] - 2025-06-30

### Added

- Connection pool metrics collection for monitoring active requests (#16)
  - Tracks active requests per service via Prometheus metrics
  - Background goroutine collects metrics every 10 seconds
  - Added Close() method to Handler interface for proper cleanup
- Whois result caching to reduce resource usage (#15)
  - LRU cache with 5-minute TTL and 1000 entry limit
  - Prevents creating new goroutines for every request
  - Cache is shared across all services for efficiency

### Fixed

- Request body drainage in proxy error handler to prevent resource leaks
- Connection pool limits to prevent memory leaks
  - Added MaxConnsPerHost (50) and MaxIdleConnsPerHost (10) limits
  - Prevents unbounded connection growth

### Dependencies

- Bumped tailscale.com from 1.84.2 to 1.84.3 (#13)
- Bumped github.com/docker/docker from 28.2.2 to 28.3.0 (#12)

## [0.4.1] - 2025-06-26

### Added

- ARM64 Docker image support in GoReleaser configuration (#11)

### Fixed

- Docker Compose example now includes proper environment section for OAuth credentials

### Documentation

- Added comprehensive threat model and security considerations documentation
- Fixed Docker Compose example configuration

## [0.4.0] - 2025-06-23

### Changed

- **BREAKING:** Removed retry configuration (`retry_count`, `retry_delay`, `retry_strategy`, `max_retry_attempts`)
- Simplified architecture to use lazy connections instead of startup validation
- Services now always start successfully and handle backend connection failures at request time by returning appropriate HTTP error codes (502/504)
- Replaced Docker polling with event-based monitoring for better performance and resource usage

## [0.3.1] - 2025-06-22

### Fixed

- Allow `:port` format in Docker backend_addr validation to support port-only backend addresses
- Improve TLS certificate priming to work correctly in all environments, including Docker containers

### Documentation

- Updated Docker Compose examples and added clarification on Docker label configuration requirements

## [0.3.0] - 2025-06-22

### Added

- Docker label-based configuration provider for dynamic service discovery
  - Automatically discover and configure services from Docker container labels
  - Hot-reload configuration when container labels change
  - Similar to Traefik's label-based configuration approach
  - Support for all existing tsbridge configuration options via labels
  - Security validation for headers and backend addresses
  - Comprehensive documentation and examples in `docs/docker-labels.md`
  - Example Docker Compose configuration in `example/docker-compose-labels.yml`

### Fixed

- Fixed test coverage for configuration provider registration
- Improved error handling in test utilities

### Changed

- Removed old example configuration file to avoid confusion

## [0.2.0] - 2025-06-21

### Added

- TLS certificate priming on startup to avoid ~1 minute delay on first HTTPS request
  - Automatically primes certificates in the background after service starts
  - Only applies to services using TLS auto mode
  - Non-blocking operation that doesn't delay service startup

### Documentation

- Added HSTS header examples to documentation
- Updated README with OAuth credential provisioning instructions
- Moved AI agent rules to generic RULES.md file

## [0.1.0] - 2025-06-21

### Added

- Initial release of tsbridge - a lightweight proxy manager built on Tailscale's tsnet library

[0.9.2]: https://github.com/jtdowney/tsbridge/releases/tag/v0.9.2
[0.9.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.9.1
[0.9.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.9.0
[0.8.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.8.0
[0.7.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.7.1
[0.7.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.7.0
[0.6.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.6.1
[0.6.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.6.0
[0.5.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.5.0
[0.4.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.4.1
[0.4.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.4.0
[0.3.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.3.1
[0.3.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.3.0
[0.2.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.2.0
[0.1.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.1.0
