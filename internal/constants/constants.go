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

// OAuth constants define OAuth-related configuration.
const (
	// OAuthTokenExpirySeconds is the expiry time for OAuth tokens in seconds (90 days).
	// This is a reasonable default that balances security with user convenience.
	OAuthTokenExpirySeconds = 90 * 24 * 60 * 60 // 90 days in seconds
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

// Default size limits used in configuration.
const (
	// DefaultMaxRequestBodySize is the default maximum request body size (50 MB).
	DefaultMaxRequestBodySize = 50 * 1024 * 1024
)
