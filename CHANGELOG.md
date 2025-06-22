# Changelog

All notable changes to tsbridge will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **BREAKING:** Removed retry configuration (`retry_count`, `retry_delay`, `retry_strategy`, `max_retry_attempts`) 
- Simplified architecture to use lazy connections instead of startup validation
- Services now always start successfully and handle backend connection failures at request time by returning appropriate HTTP error codes (502/504)

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

[0.3.1]: https://github.com/jtdowney/tsbridge/releases/tag/v0.3.1
[0.3.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.3.0
[0.2.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.2.0
[0.1.0]: https://github.com/jtdowney/tsbridge/releases/tag/v0.1.0
