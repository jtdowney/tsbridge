# tsbridge Configuration Reference

This document provides a comprehensive guide to configuring tsbridge using TOML format.

## Table of Contents

- [Configuration File Structure](#configuration-file-structure)
- [Tailscale Section](#tailscale-section)
- [Global Section](#global-section)
- [Services Section](#services-section)
- [Streaming Services Configuration](#streaming-services-configuration)
- [Environment Variables](#environment-variables)
- [Configuration Precedence](#configuration-precedence)
- [Examples](#examples)

## Configuration File Structure

tsbridge uses a TOML configuration file with three main sections:

```toml
[tailscale]
# Tailscale authentication and state configuration

[global]
# Default settings for all services

[[services]]
# Service-specific configuration (can have multiple)
```

## Tailscale Section

The `[tailscale]` section configures authentication and state management for the Tailscale connection.

### Secret Resolution Timing

**Important**: While tsbridge validates that authentication is configured during startup (in `NewServer`), the actual authentication with Tailscale happens later when services start listening. The secret resolution process works as follows:

1. **Configuration Validation (at startup)**: tsbridge checks that valid auth configuration exists - either OAuth credentials or an auth key must be resolvable from the configured sources.
2. **Secret Resolution (at startup)**: Environment variables and files are read to ensure credentials are available.
3. **Authentication (per service)**: When each service calls `Listen()`, tsbridge uses `generateOrResolveAuthKey()` to either:
   - Generate a new auth key using OAuth credentials (if configured)
   - Use the provided auth key

This means that while secrets must be available at startup, the actual Tailscale authentication is deferred until each service starts.

### OAuth Authentication

OAuth is the recommended authentication method for production use.

**Important**: When using OAuth authentication, you MUST also specify `oauth_tags` to define which ACL tags the node should have.

#### Provisioning OAuth Credentials

To use OAuth authentication, you need to generate an OAuth client ID and secret from the Tailscale admin console. These credentials allow `tsbridge` to create auth keys on your behalf.

1.  In the Tailscale admin console, navigate to **Settings** -> **OAuth clients**.
2.  Click **Generate OAuth client...**.
3.  Give your client a descriptive name, for example `tsbridge-production`.
4.  In the **Scopes** section, find the **Auth Keys** category and check both the **Read** and **Write** checkboxes. This provides the necessary permissions for `tsbridge` to manage auth keys. These are the only permissions required.
5.  Click **Generate client**.
6.  Copy the **Client ID** and **Client secret**. Store these securely, as the secret will not be shown again.

Use these credentials to configure `tsbridge`, as shown in the configuration example below.

```toml
[tailscale]
# OAuth Client ID - choose one method:
oauth_client_id = "direct-value"              # Direct value (not recommended)
oauth_client_id_env = "TS_OAUTH_CLIENT_ID"    # From environment variable
oauth_client_id_file = "/path/to/id.txt"      # From file

# OAuth Client Secret - choose one method:
oauth_client_secret = "direct-value"              # Direct value (not recommended)
oauth_client_secret_env = "TS_OAUTH_CLIENT_SECRET" # From environment variable
oauth_client_secret_file = "/path/to/secret.txt"  # From file

# OAuth tags - REQUIRED when using OAuth
oauth_tags = ["tag:server", "tag:proxy"]      # ACL tags for the node
```

### Auth Key Authentication

For ephemeral nodes or testing, you can use auth keys instead:

```toml
[tailscale]
# Auth key - choose one method:
auth_key = "tskey-auth-..."          # Direct value
auth_key_env = "TS_AUTHKEY"          # From environment variable
auth_key_file = "/path/to/key.txt"   # From file
```

**Note**: You cannot use both OAuth and auth key authentication. Choose one method.

### State Directory

Each service gets its own subdirectory under the configured state directory:

```toml
[tailscale]
# State directory - choose one method:
state_dir = "/var/lib/tsbridge"      # Direct path
state_dir_env = "TSBRIDGE_STATE_DIR" # From environment variable

# If not specified, defaults to:
# - Linux: $XDG_DATA_HOME/tsbridge or ~/.local/share/tsbridge
# - macOS: ~/Library/Application Support/tsbridge
# - Windows: %APPDATA%/tsbridge
```

### OAuth Tags

**Required** when using OAuth authentication. These tags are applied to the Tailscale node created by tsbridge:

```toml
[tailscale]
oauth_tags = ["tag:server", "tag:proxy", "tag:production"]
```

**Important**: OAuth tags are only used with OAuth authentication and cannot be specified when using auth keys.

## Global Section

The `[global]` section defines default values that apply to all services unless overridden.

### Timeouts

All timeouts use Go duration format (e.g., "30s", "1m", "1h30m"). Setting a timeout to "0s" disables that specific timeout:

```toml
[global]
read_header_timeout = "30s"  # Maximum time to read request headers (0s = no timeout)
write_timeout = "30s"        # Maximum time to write response (0s = no timeout)
idle_timeout = "120s"        # Maximum time to wait for next request on keep-alive connection (0s = no timeout)
shutdown_timeout = "15s"     # Maximum time to wait for graceful shutdown (cannot be disabled)
```

**Important distinctions**:
- **Omitting a timeout**: Uses the default value (e.g., 30s for write_timeout)
- **Setting to "0s"**: Explicitly disables the timeout, allowing unlimited duration

**Note**: Use caution when disabling timeouts. While useful for streaming services, disabled timeouts can lead to resource exhaustion if clients don't properly close connections.

### Backend Connection

Configuration for connecting to backend services:

```toml
[global]
# HTTP Transport timeouts (all optional with defaults)
dial_timeout = "30s"              # Maximum time to establish connection (default: 30s)
keep_alive_timeout = "30s"        # Keep-alive probe interval (default: 30s)
idle_conn_timeout = "90s"         # Maximum time idle connections are kept open (default: 90s)
tls_handshake_timeout = "10s"     # Maximum time for TLS handshake (default: 10s)
expect_continue_timeout = "1s"    # Maximum time to wait for server's first response headers after "Expect: 100-continue" (default: 1s)
response_header_timeout = "0s"    # Maximum time to wait for response headers after request is sent (default: 0s = no timeout)

# Response streaming
flush_interval = "0s"             # Time between response flushes (default: 0s = default buffering; -1ms = immediate flush)
```

### Whois Configuration

Settings for Tailscale identity header injection:

```toml
[global]
whois_timeout = "1s"     # Maximum time to wait for whois lookup
```

### Security

Control trusted proxy settings for proper X-Forwarded-For header handling:

```toml
[global]
# List of trusted proxy IPs or CIDR ranges
trusted_proxies = ["10.0.0.0/8", "172.16.0.0/12", "192.168.1.1"]
```

When a request comes from a trusted proxy, tsbridge will:

- Preserve and append to existing X-Forwarded-For headers
- Extract the real client IP from the beginning of the X-Forwarded-For chain

When a request comes from an untrusted source, tsbridge will:

- Remove any existing X-Forwarded-For headers to prevent spoofing
- Set X-Forwarded-For to only the immediate client IP

### Observability

```toml
[global]
metrics_addr = ":9090"   # Prometheus metrics endpoint (empty to disable)
access_log = true        # Enable/disable access logging
metrics_read_header_timeout = "5s"  # Maximum time to read request headers for metrics endpoint (default: 5s)
```

## Services Section

Each `[[services]]` section defines a unique service that tsbridge will proxy.

### Basic Configuration

```toml
[[services]]
name = "api"                      # Unique service name (becomes hostname)
backend_addr = "127.0.0.1:8080"  # Backend address to proxy to
```

The `name` field becomes part of your Tailscale hostname: `https://api.<tailnet-name>.ts.net`

### Backend Address Formats

tsbridge supports multiple backend address formats:

```toml
# TCP port
backend_addr = "127.0.0.1:8080"
backend_addr = "localhost:3000"
backend_addr = "backend.internal:80"

# Unix socket
backend_addr = "unix:///var/run/app.sock"
backend_addr = "unix:///tmp/backend.sock"
```

### Whois Headers

Control Tailscale identity header injection:

```toml
[[services]]
whois_enabled = true      # Enable whois header injection (default: false)
whois_timeout = "500ms"   # Override global whois timeout for this service
```

When enabled, the following headers are added to backend requests:

- `X-Tailscale-User`: User email
- `X-Tailscale-Login`: Login name
- `X-Tailscale-Name`: Display name
- `X-Tailscale-Profile-Picture`: Profile picture URL

### Header Manipulation

Control headers added to or removed from requests and responses:

```toml
[[services]]
name = "secure-api"
backend_addr = "localhost:8080"

# Add headers to upstream requests (to backend)
upstream_headers = {
  "X-Service-Name" = "tsbridge"
}

# Add headers to downstream responses (to client)
downstream_headers = {
  "Strict-Transport-Security" = "max-age=31536000; includeSubDomains",
  "X-Frame-Options" = "DENY",
  "X-Content-Type-Options" = "nosniff"
}

# Remove headers from upstream requests
remove_upstream = ["Cookie", "Authorization"]

# Remove headers from downstream responses
remove_downstream = ["Server", "X-Powered-By"]
```

This is particularly useful for:

- Adding security headers like HSTS to all responses
- Removing sensitive headers before forwarding
- Adding service identification headers
- Implementing security best practices

### Per-Service Overrides

Any global setting can be overridden per service:

```toml
[[services]]
name = "slow-api"
backend_addr = "localhost:8080"

# Override timeouts for this service
read_header_timeout = "60s"
write_timeout = "60s"
idle_timeout = "300s"

# Override logging
access_log = false
```

## Environment Variables

tsbridge supports environment variable substitution for sensitive values:

### Fallback Environment Variables

If no OAuth configuration is specified, tsbridge checks these environment variables:

- `TS_OAUTH_CLIENT_ID`
- `TS_OAUTH_CLIENT_SECRET`
- `TS_AUTHKEY`

### Custom Environment Variables

You can specify custom environment variables:

```toml
[tailscale]
oauth_client_id_env = "CUSTOM_OAUTH_ID"
oauth_client_secret_env = "CUSTOM_OAUTH_SECRET"
state_dir_env = "CUSTOM_STATE_DIR"
```

## Configuration Precedence

For each configuration value, the precedence order is:

1. Direct value in config file
2. Environment variable (if `_env` variant is specified)
3. File contents (if `_file` variant is specified)
4. Default fallback environment variables (for OAuth/auth key)
5. Built-in defaults

## Examples

### Minimal Configuration

```toml
# Minimal config - uses environment variables for auth
[tailscale]
# Will automatically use TS_OAUTH_CLIENT_ID and TS_OAUTH_CLIENT_SECRET
# OAuth tags are REQUIRED when using OAuth authentication
oauth_tags = ["tag:server", "tag:proxy"]

[[services]]
name = "app"
backend_addr = "localhost:8080"
```

### Production Configuration

```toml
[tailscale]
oauth_client_id_file = "/etc/tsbridge/oauth-id"
oauth_client_secret_file = "/etc/tsbridge/oauth-secret"
state_dir = "/var/lib/tsbridge"
oauth_tags = ["tag:server", "tag:proxy", "tag:prod"]

[global]
# Conservative timeouts
read_header_timeout = "30s"
write_timeout = "30s"
idle_timeout = "120s"
shutdown_timeout = "30s"


# Security - trust load balancer and internal network
trusted_proxies = ["10.0.0.0/8", "172.16.0.0/12"]

# Observability
metrics_addr = ":9090"
access_log = true
whois_timeout = "2s"

[[services]]
name = "api"
backend_addr = "api-backend.internal:8080"
whois_enabled = true
# Add security headers for production
downstream_headers = {
  "Strict-Transport-Security" = "max-age=63072000; includeSubDomains; preload",
  "X-Content-Type-Options" = "nosniff",
  "X-Frame-Options" = "DENY"
}

[[services]]
name = "web"
backend_addr = "unix:///var/run/web/web.sock"
whois_enabled = true
# Add security headers for production
downstream_headers = {
  "Strict-Transport-Security" = "max-age=63072000; includeSubDomains; preload",
  "Content-Security-Policy" = "default-src 'self'; script-src 'self' 'unsafe-inline'"
}

[[services]]
name = "admin"
backend_addr = "127.0.0.1:9000"
whois_enabled = true
# Longer timeouts for admin operations
read_header_timeout = "5m"
write_timeout = "5m"
```

### Development Configuration

```toml
[tailscale]
oauth_client_id_env = "TS_OAUTH_CLIENT_ID"
oauth_client_secret_env = "TS_OAUTH_CLIENT_SECRET"
# OAuth tags are REQUIRED when using OAuth authentication
oauth_tags = ["tag:dev", "tag:proxy"]

[global]
# Shorter timeouts for development
read_header_timeout = "5s"
write_timeout = "5s"
metrics_addr = ":9090"

[[services]]
name = "dev-app"
backend_addr = "localhost:3000"
whois_enabled = false  # Disable for local development
```

### Multi-Service with Mixed Backends

```toml
[tailscale]
oauth_client_id_env = "TS_OAUTH_CLIENT_ID"
oauth_client_secret_env = "TS_OAUTH_CLIENT_SECRET"
# OAuth tags are REQUIRED when using OAuth authentication
oauth_tags = ["tag:server", "tag:proxy"]
state_dir = "/opt/tsbridge/state"

[global]
read_header_timeout = "30s"
write_timeout = "30s"
metrics_addr = ":9090"

# HTTP API service
[[services]]
name = "api"
backend_addr = "api.internal:8080"
whois_enabled = true

# Unix socket service
[[services]]
name = "app"
backend_addr = "unix:///var/run/app/app.sock"
whois_enabled = true

# External service with custom timeouts
[[services]]
name = "external"
backend_addr = "external-api.example.com:443"
whois_enabled = false
read_header_timeout = "60s"
write_timeout = "60s"

# Internal admin panel
[[services]]
name = "admin"
backend_addr = "localhost:8888"
whois_enabled = true
access_log = false  # Disable access logs for admin
```

## Streaming Services Configuration

tsbridge supports long-lived streaming connections such as media streaming, Server-Sent Events (SSE), and real-time data feeds. Proper configuration is critical for these services to work correctly.

### Understanding Flush Interval

The `flush_interval` setting controls how often buffered response data is flushed to the client:

- **Default behavior**: Without setting `flush_interval`, responses are buffered for performance
- **`-1ms`**: Disables buffering entirely - data is sent immediately as it arrives from the backend
- **Positive duration** (e.g., `100ms`): Flushes buffered data at the specified interval
- **`0s` or unset**: Uses default buffering behavior

### Common Streaming Scenarios

#### Media Streaming (e.g., Jellyfin, Plex)

For media streaming services that send large video/audio files:

```toml
[[services]]
name = "jellyfin"
backend_addr = "localhost:8096"
write_timeout = "0s"              # Disable write timeout for long streams
flush_interval = "-1ms"           # Immediate flushing for smooth playback
response_header_timeout = "0s"    # No timeout waiting for backend headers
idle_timeout = "300s"             # Keep connections alive for 5 minutes
```

#### Server-Sent Events (SSE)

For real-time event streams that stay open indefinitely:

```toml
[[services]]
name = "sse-endpoint"
backend_addr = "localhost:3000"
write_timeout = "0s"              # SSE connections stay open indefinitely
flush_interval = "-1ms"           # Immediate flushing for real-time events
read_header_timeout = "10s"       # Still enforce header read timeout
```

#### Regular API (Default Buffering)

For standard REST APIs where buffering improves performance:

```toml
[[services]]
name = "api"
backend_addr = "localhost:8080"
# flush_interval not set - uses default buffering for performance
# write_timeout uses global default (30s)
```

#### Metrics Endpoint (Periodic Flushing)

For endpoints that benefit from controlled flushing:

```toml
[[services]]
name = "prometheus"
backend_addr = "localhost:9090"
flush_interval = "100ms"          # Flush every 100ms for timely metrics
write_timeout = "30s"             # Standard timeout for metrics scraping
```

### Important Considerations

#### Write Timeout Impact

The `write_timeout` setting has a critical impact on streaming services:

- **`write_timeout = "30s"`** (default): Connections will be terminated after 30 seconds, breaking streams
- **`write_timeout = "0s"`**: Disables write timeout entirely, allowing indefinite streaming
- **Choose carefully**: Only disable write timeout for services that truly need long-lived connections

⚠️ **Warning**: Setting `write_timeout = "0s"` can lead to resource exhaustion if clients don't properly close connections. Only use this for trusted streaming services.

#### Global vs Service-Level Configuration

You can set `flush_interval` globally or per-service:

```toml
[global]
# Default for all services
flush_interval = "1s"

[[services]]
name = "streaming"
backend_addr = "localhost:8080"
# Override for this specific service
flush_interval = "-1ms"
```

### Debugging Streaming Issues

Common symptoms and solutions:

1. **Stream appears frozen or data arrives in bursts**
   - **Cause**: Default response buffering
   - **Solution**: Set `flush_interval = "-1ms"`

2. **Stream disconnects after 30 seconds**
   - **Cause**: Default write timeout
   - **Solution**: Set `write_timeout = "0s"`

3. **High latency in real-time applications**
   - **Cause**: Buffering delays
   - **Solution**: Set `flush_interval = "-1ms"` for immediate flushing

4. **Poor performance with many small requests**
   - **Cause**: Immediate flushing overhead
   - **Solution**: Use default buffering or set a small positive `flush_interval`

### Best Practices

1. **Be selective**: Only configure streaming settings for services that need them
2. **Monitor resources**: Streaming connections consume more resources than regular requests
3. **Set appropriate timeouts**: Don't disable all timeouts - keep `read_header_timeout` for security
4. **Test thoroughly**: Verify streaming behavior under load before production deployment
5. **Document your choices**: Comment why specific timeout/flush settings were chosen

## Error Messages

When secret resolution fails, tsbridge provides clear error messages indicating:

- Which secret could not be resolved (OAuth client ID, OAuth client secret, or auth key)
- The source that was checked (environment variable name or file path)
- Whether the failure was due to missing configuration or file read errors

Example error messages:

- `configuration error: resolving OAuth client ID: environment variable "TS_OAUTH_CLIENT_ID" is not set`
- `configuration error: resolving auth key: reading secret file: open /etc/tsbridge/authkey: no such file or directory`
- `configuration error: either auth key or OAuth credentials must be provided`

## Validation Rules

tsbridge validates configuration at startup and will exit with an error if:

1. **Missing required fields**: `name` and `backend_addr` are required for each service
2. **Duplicate service names**: Each service must have a unique name
3. **Invalid durations**: Timeout values must be valid Go duration strings
4. **Conflicting auth**: Cannot specify both OAuth and auth key credentials
5. **Invalid backend address**: Must be valid TCP address or Unix socket path
6. **Multiple credential sources**: Cannot specify multiple sources for the same credential (e.g., both `oauth_client_id` and `oauth_client_id_env`)
7. **OAuth tags with auth keys**: `oauth_tags` can only be used with OAuth authentication, not with auth keys
8. **Invalid trusted proxies**: Each trusted proxy must be a valid IP address or CIDR range

## Best Practices

1. **Use environment variables or files for secrets**: Never commit OAuth credentials or auth keys to version control
2. **Set appropriate timeouts**: Consider your application's needs and adjust timeouts accordingly
3. **Enable metrics**: Use the metrics endpoint for monitoring and alerting
4. **Use whois headers**: Enable for internal services that need user identity
5. **Organize services logically**: Group related configuration together
6. **Document your configuration**: Add comments to explain non-obvious settings
7. **Test configuration changes**: Use `-verbose` flag to debug configuration issues
