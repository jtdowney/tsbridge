# Changelog

All notable changes to tsbridge will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.13.0] - 2025-12-04

### Added

- Support DOCKER_HOST environment variable for Docker socket configuration (#147)
  - Allows overriding the default Docker socket path
  - Useful for connecting to remote Docker daemons or custom socket locations
- Periodic poll interval for Docker provider (#145)
  - Docker provider can now periodically poll for container changes
  - Complements event-based monitoring for improved reliability

### Fixed

- Clean up tsnet server when handler creation fails
  - Prevents resource leaks when service startup fails during handler setup
- Clean up tsnet server on listener creation failure
  - Ensures proper cleanup when listener creation fails

### Development

- Updated pre-commit hook versions
- CI: Build and push dev Docker images on push to main

## [0.12.3] - 2025-11-29

### Fixed

- Remove deprecated `// +build` directives from integration tests

### Dependencies

- Bump tailscale.com from 1.90.6 to 1.90.9 (#142)
- Bump golang.org/x/crypto from 0.41.0 to 0.45.0 (#141)

## [0.12.2] - 2025-11-13

### Dependencies

- Bump github.com/docker/docker from 28.4.0 to 28.5.2 (#126, #128, #139)
- Bump golang.org/x/oauth2 from 0.31.0 to 0.33.0 (#127, #138)
- Bump tailscale.com from 1.88.3 to 1.90.6 (#129, #132, #137)

## [0.12.1] - 2025-09-29

### Fixed

- Allow underscores in backend hostnames (#124)
  - Backend addresses can now include underscores in hostname segments
  - Improves compatibility with internal DNS naming conventions
  - Thanks to @keonramses for reporting the issue

### Dependencies

- Bump tailscale.com from 1.86.4 to 1.88.3 (#116, #118, #123)
- Bump github.com/knadh/koanf/v2 from 2.2.2 to 2.3.0 (#117)
- Bump github.com/docker/docker from 28.3.3 to 28.4.0 (#114)
- Bump golang.org/x/oauth2 from 0.30.0 to 0.31.0 (#113)
- Bump github.com/prometheus/client_golang from 1.23.0 to 1.23.2 (#112)
- Bump github.com/stretchr/testify from 1.10.0 to 1.11.1 (#103, #106)

### Development

- Updated Go version to 1.25
- Updated golangci-lint to v2.4 for Go 1.25.1 compatibility

### Documentation

- Added Docker networking guide and examples

## [0.12.0] - 2025-08-20

### Breaking Changes

- OAuth-generated auth keys are now preauthorized by default (#98)
  - Previous behavior required manual device approval
  - To restore previous behavior, set `oauth_preauthorized=false`

### Added

- Support for TLS/HTTPS upstream backends (#83)
  - Backends can now be specified as `https://` URLs
  - Added `insecure_skip_verify` option for self-signed certificates
  - Automatically configures TLS transport for HTTPS backends

### Fixed

- Prevent shutdown hanging during tsnet authentication (#101)
  - Add 3-second timeout for tsnet server close operations
  - Prevent indefinite hanging when tsnet.Close() blocks
- Unified TLS mode accepted values to only allow "auto" and "off" (#93)
  - Removed invalid "on" value that was incorrectly documented
- Respect request context when dialing unix sockets (#91)
- Restored container name matching in Docker self-discovery (#80)

### Changed

- Improved secret validation to use RedactedString.Value() method (#92)

### Dependencies

- Bump tailscale.com from 1.84.3 to 1.86.4 (#77, #94)
- Bump github.com/prometheus/client_golang (#87)
- Bump github.com/docker/docker (#79)
- Bump github.com/go-viper/mapstructure/v2 (#72)

### Documentation

- Added default_tags to OAuth docker-compose examples (#89)
  - Thanks to @jbp35 for reporting the missing tags issue (#88)
- Fixed default_tags placement in quickstart guide
  - Thanks to @beautiful-orca for reporting the documentation mistake (#96)
- Fixed traefik/whoami port in Docker example
  - Thanks to @jbp35 for reporting the incorrect port in the documentation (#88)
- Added logo and README reference

## [0.11.1] - 2025-07-20

### Fixed

- Improved Docker container self-discovery by listing all containers instead of relying on Docker's ID filter (#70)
  - Fixes issues where tsbridge couldn't find its own container in certain Docker environments
  - Improves compatibility with different container runtime configurations
  - Thanks to @korpa and @rbollampally for their help identifying the issue and testing the fix

### Internal

- Added debug logging to Docker provider startup for better troubleshooting
- Enhanced container discovery logging to aid in debugging

## [0.11.0] - 2025-07-17

### Added

- Simplified listener configuration to use listen_addr (#68)

  - Replaces separate listen_port field with a single listen_addr field
  - Provides more flexibility for binding to specific interfaces
  - Supports formats like ":8080", "127.0.0.1:8080", "[::1]:8080"
  - Default to ":443" for TLS mode, ":80" for non-TLS mode

- tsnet logging adapters to reduce log noise (#64)
  - Improved log output readability by filtering tsnet internal logs
  - Better separation of tsbridge logs from underlying tsnet logs

### Changed

- **BREAKING:** listen_port configuration field has been replaced by listen_addr (#68)
  - Migration: Change `listen_port = 8080` to `listen_addr = ":8080"`
  - For specific interfaces, use `listen_addr = "127.0.0.1:8080"`
- **BREAKING:** Configuration resolution order is now strict: direct > file > env > default (#67)
  - Empty environment variables configured with \_env suffix now return an error
  - Previously would silently fall back to defaults

### Fixed

- Configuration resolution order now properly prioritizes configured sources (#67)
  - Files (\_file suffix) and env vars (\_env suffix) no longer silently fall back
  - Direct values are properly preserved through resolution
  - Clear error messages when configured sources are missing or empty

### Removed

- Unimplemented connection pool metrics code (#66)
  - Removed dead code that was never fully implemented
  - Simplifies codebase maintenance

### Documentation

- Restructured documentation for better organization (#65)
- Simplified README for clarity and developer focus
- Updated configuration reference with clearer resolution order explanation
- Updated Docker labels documentation with new listen_addr field

## [0.10.1] - 2025-07-16

### Fixed

- Allow startup without OAuth/authkey for pre-registered services (#62)
  - Services that are already registered with Tailscale no longer require OAuth credentials
  - Credentials are now only required for registering new services

### Testing

- Resolved race conditions in test mocks (#61)
  - Fixed concurrent map access issues in mock implementations
  - Improved test reliability and stability

## [0.10.0] - 2025-07-16

### Added

- Headscale support (#60)
  - Added support for Headscale as an alternative to Tailscale's coordination server
  - Enables use of self-hosted Tailscale coordination servers
  - Configurable via existing Tailscale configuration options
  - Thanks to @korpa for the suggestion

### Fixed

- Add missing state_dir_env to Docker provider (#59)
  - Docker provider now properly handles state directory environment variables
  - Improves compatibility with containerized deployments

### Documentation

- Add security policy
  - Added SECURITY.md with vulnerability reporting guidelines

### Dependencies

- Bumped github.com/docker/docker (#57)
  - Updated Docker client library to latest version
  - Improved compatibility and security

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

[0.13.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.13.0
[0.12.3]: https://github.com/jtdowney/tsbridge/releases/tag/v0.12.3
[0.12.2]: https://github.com/jtdowney/tsbridge/releases/tag/v0.12.2
[0.12.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.12.1
[0.12.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.12.0
[0.11.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.11.1
[0.11.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.11.0
[0.10.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.10.1
[0.10.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.10.0
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
