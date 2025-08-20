// Package constants defines common constants used throughout the tsbridge application.
package constants

import "time"

// Timeout constants define the default timeout values used across the application.
const (
	// DefaultReadHeaderTimeout is the default maximum duration for reading the request headers.
	// A zero or negative value means no timeout.
	DefaultReadHeaderTimeout = 30 * time.Second

	// DefaultWriteTimeout is the default timeout for writing the response.
	// This includes the time from the end of the request reading to the end of the response write.
	DefaultWriteTimeout = 30 * time.Second

	// DefaultIdleTimeout is the default maximum amount of time to wait for the next request
	// when keep-alives are enabled.
	DefaultIdleTimeout = 120 * time.Second

	// DefaultShutdownTimeout is the default timeout for graceful shutdown of services.
	DefaultShutdownTimeout = 30 * time.Second

	// DefaultWhoisTimeout is the default timeout for whois lookups.
	// Whois operations can be slow, so we use a reasonable timeout.
	DefaultWhoisTimeout = 5 * time.Second

	// BackendHealthCheckTimeout is the timeout used when checking if a backend is healthy.
	// This should be relatively short to avoid blocking service startup.
	BackendHealthCheckTimeout = 5 * time.Second

	// DefaultDialTimeout is the default timeout for dialing backend connections.
	DefaultDialTimeout = 30 * time.Second

	// DefaultKeepAliveTimeout is the default keep-alive timeout for backend connections.
	DefaultKeepAliveTimeout = 30 * time.Second

	// DefaultIdleConnTimeout is the default idle connection timeout for backend connections.
	DefaultIdleConnTimeout = 90 * time.Second

	// DefaultTLSHandshakeTimeout is the default TLS handshake timeout for backend connections.
	DefaultTLSHandshakeTimeout = 10 * time.Second

	// DefaultExpectContinueTimeout is the default expect-continue timeout for backend connections.
	DefaultExpectContinueTimeout = 1 * time.Second

	// DefaultMetricsReadHeaderTimeout is the default read header timeout for the metrics server.
	DefaultMetricsReadHeaderTimeout = 5 * time.Second
)

// Auth key constants define Tailscale auth key configuration.
const (
	// AuthKeyExpirySeconds is the expiry time for Tailscale auth keys in seconds (5 minutes).
	// These auth keys are used once for service registration and then discarded,
	// so a short expiry minimizes the security exposure window.
	AuthKeyExpirySeconds = 5 * 60 // 5 minutes in seconds
)

// Default boolean values used in configuration.
const (
	// DefaultAccessLogEnabled indicates whether access logging is enabled by default.
	DefaultAccessLogEnabled = true

	// DefaultWhoisEnabled indicates whether whois lookups are enabled by default.
	DefaultWhoisEnabled = false
)

// Default string values used in configuration.
const (
	// DefaultTLSMode is the default TLS mode for services.
	DefaultTLSMode = "auto"
)

// TLS mode values for services.
const (
	// TLSModeAuto enables HTTPS with automatic certificates from Tailscale.
	TLSModeAuto = "auto"
	// TLSModeOff disables HTTPS listener and serves plain HTTP (encrypted over WireGuard).
	TLSModeOff = "off"
)

// Default size limits used in configuration.
const (
	// DefaultMaxRequestBodySize is the default maximum request body size (50 MB).
	DefaultMaxRequestBodySize = 50 * 1024 * 1024
)

// Cache configuration constants define default values for caching behavior.
const (
	// DefaultWhoisCacheSize is the default maximum number of entries in the whois cache.
	DefaultWhoisCacheSize = 1000

	// DefaultWhoisCacheTTL is the default time-to-live for whois cache entries.
	DefaultWhoisCacheTTL = 5 * time.Minute
)

// Connection pool constants define default values for HTTP transport configuration.
const (
	// DefaultMaxIdleConns is the default maximum number of idle connections across all hosts.
	DefaultMaxIdleConns = 100

	// DefaultMaxConnsPerHost is the default maximum number of connections per host.
	DefaultMaxConnsPerHost = 50

	// DefaultMaxIdleConnsPerHost is the default maximum number of idle connections per host.
	DefaultMaxIdleConnsPerHost = 10

	// DefaultMetricsCollectionInterval is the default interval for collecting metrics.
	DefaultMetricsCollectionInterval = 10 * time.Second
)

// Docker provider constants define timeouts and delays for Docker operations.
const (
	// DockerPingTimeout is the timeout for Docker daemon ping operations.
	DockerPingTimeout = 5 * time.Second

	// DockerMaxReconnectBackoff is the maximum backoff duration for Docker event stream reconnection.
	DockerMaxReconnectBackoff = 5 * time.Minute

	// DockerEventDebounceDelay is the delay for debouncing Docker events.
	DockerEventDebounceDelay = 500 * time.Millisecond
)

// Byte size constants for data size calculations.
const (
	// BytesPerKB is the number of bytes in a kilobyte.
	BytesPerKB = 1024

	// BytesPerMB is the number of bytes in a megabyte.
	BytesPerMB = 1024 * 1024

	// BytesPerGB is the number of bytes in a gigabyte.
	BytesPerGB = 1024 * 1024 * 1024

	// BytesPerTB is the number of bytes in a terabyte.
	BytesPerTB = 1024 * 1024 * 1024 * 1024
)

// Special duration values.
const (
	// ImmediateFlushInterval is a special value indicating immediate flushing without buffering.
	ImmediateFlushInterval = -1 * time.Millisecond
)

// Channel configuration constants.
const (
	// DefaultChannelBufferSize is the default buffer size for channels.
	DefaultChannelBufferSize = 1
)

// Certificate priming constants.
const (
	// CertificatePrimingTimeout is the timeout for certificate priming operations.
	CertificatePrimingTimeout = 30 * time.Second
)

// Service lifecycle constants.
const (
	// ServiceStopTimeout is the timeout for stopping a service gracefully.
	ServiceStopTimeout = 5 * time.Second

	// TsnetServerStartTimeout is the timeout for starting a tsnet server.
	TsnetServerStartTimeout = 5 * time.Second

	// TsnetServerCloseTimeout is the timeout for closing a tsnet server gracefully.
	TsnetServerCloseTimeout = 3 * time.Second
)

// Retry configuration constants.
const (
	// RetryInitialInterval is the initial interval between retry attempts.
	RetryInitialInterval = 100 * time.Millisecond

	// RetryMaxInterval is the maximum interval between retry attempts.
	RetryMaxInterval = 2 * time.Second

	// RetryMaxElapsedTime is the maximum total time for all retry attempts.
	RetryMaxElapsedTime = 10 * time.Second

	// RetryMultiplier is the multiplier for exponential backoff.
	RetryMultiplier = 2.0

	// RetryRandomizationFactor is the randomization factor for exponential backoff.
	RetryRandomizationFactor = 0.1

	// RetryMaxAttempts is the maximum number of retry attempts (2 retries = 3 total attempts).
	RetryMaxAttempts = 2

	// RetryMinTestDelay is the minimum expected delay for testing retry behavior.
	RetryMinTestDelay = 50 * time.Millisecond
)
