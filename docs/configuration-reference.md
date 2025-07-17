# Configuration Reference

Complete reference for all tsbridge configuration options.

## Configuration Structure

```toml
[tailscale]
# Tailscale authentication and settings

[global]
# Default settings for all services

[[services]]
# Service definitions (multiple allowed)
```

## [tailscale] Section

### Authentication

You must provide either OAuth credentials OR an auth key.

#### OAuth Credentials

```toml
# Client ID - choose one method:
oauth_client_id = "k12...89"                      # Direct value (not recommended)
oauth_client_id_env = "TS_OAUTH_CLIENT_ID"        # From environment variable (default)
oauth_client_id_file = "/etc/tsbridge/oauth-id"   # From file

# Client Secret - choose one method:
oauth_client_secret = "tskey-client-..."                   # Direct value (not recommended)
oauth_client_secret_env = "TS_OAUTH_CLIENT_SECRET"         # From environment variable (default)
oauth_client_secret_file = "/etc/tsbridge/oauth-secret"    # From file
```

#### Auth Key (Alternative)

```toml
# Auth key - choose one method:
auth_key = "tskey-auth-..."              # Direct value
auth_key_env = "TS_AUTHKEY"              # From environment variable (default)
auth_key_file = "/etc/tsbridge/authkey"  # From file
```

### Other Tailscale Options

```toml
# State directory for tsnet data
state_dir = "/var/lib/tsbridge"          # Direct path
state_dir_env = "CUSTOM_STATE_DIR"       # From environment variable

# Default tags for all services (required when using OAuth)
default_tags = ["tag:server", "tag:proxy"]

# Control server URL (for Headscale or custom servers)
control_url = "https://headscale.example.com"
```

### Tag Ownership and OAuth Security

tsbridge supports Tailscale's tag ownership model for enhanced security. This allows an OAuth client with a parent tag to manage multiple service tags through a permission hierarchy.

#### Setting Up Tag Ownership

Configure tag ownership in your Tailscale ACL policy:

```json
{
  "tagOwners": {
    "tag:tsbridge": [],               // Parent tag for the OAuth client
    "tag:server": ["tag:tsbridge"],   // Service tag owned by tag:tsbridge
    "tag:proxy": ["tag:tsbridge"],    // Additional service tags as needed
    "tag:prod": ["tag:tsbridge"],
    "tag:dev": ["tag:tsbridge"]
  }
}
```

#### Creating the OAuth Client

1. Go to **Settings** → **OAuth clients** in Tailscale admin console
2. Click **Generate OAuth client...**
3. Configure:
   - **Scopes**: Check both **Read** and **Write** under **Auth Keys**
   - **Tags**: Select `tag:tsbridge` (the parent tag, NOT the service tags)
4. Save the credentials

**Important**: Select only the parent tag for the OAuth client. This grants permission to create auth keys for all tags it owns.

#### Using Tag Hierarchies in tsbridge

```toml
[tailscale]
oauth_client_id_env = "TS_OAUTH_CLIENT_ID"
oauth_client_secret_env = "TS_OAUTH_CLIENT_SECRET"

[global]
# These tags must be owned by tag:tsbridge in your ACL
default_tags = ["tag:server", "tag:proxy"]

[[services]]
name = "api"
backend_addr = "localhost:8080"
tags = ["tag:server", "tag:prod"]  # Both owned by tag:tsbridge
```

This approach provides centralized permission management while maintaining clear security boundaries between services.

## [global] Section

All settings here provide defaults that can be overridden per-service.

### Timeouts

All timeouts use Go duration format (`"30s"`, `"5m"`, `"1h30m"`). Set to `"0s"` to disable.

```toml
# Server timeouts
read_header_timeout = "30s"      # Time to read request headers (default: 30s)
write_timeout = "30s"            # Time to write response (default: 30s)
idle_timeout = "120s"            # Keep-alive timeout (default: 120s)
shutdown_timeout = "15s"         # Graceful shutdown timeout (default: 15s)

# Backend connection timeouts
dial_timeout = "30s"                   # Time to establish connection (default: 30s)
response_header_timeout = "0s"         # Time to wait for backend headers (default: 0s = no timeout)
keep_alive_timeout = "30s"             # Keep-alive probe interval (default: 30s)
idle_conn_timeout = "90s"              # Idle connection timeout (default: 90s)
tls_handshake_timeout = "10s"          # TLS handshake timeout (default: 10s)
expect_continue_timeout = "1s"         # 100-continue timeout (default: 1s)

# Metrics endpoint timeout
metrics_read_header_timeout = "5s"     # Header read timeout for metrics (default: 5s)
```

### Response Handling

```toml
# Flush interval for response buffering
flush_interval = "0s"      # Default buffering (default)
flush_interval = "-1ms"    # Immediate flushing (for streaming)
flush_interval = "100ms"   # Flush every 100ms

# Request body size limit
max_request_body_size = "52428800"  # In bytes (default: 50MB)
max_request_body_size = "10MB"      # Human readable
max_request_body_size = "-1"        # No limit
```

### Observability

```toml
# Prometheus metrics endpoint
metrics_addr = ":9090"     # Listen address (empty to disable)

# Access logging
access_log = true          # Enable/disable (default: true)
```

### Security

```toml
# Trusted proxy IPs for X-Forwarded-For handling
trusted_proxies = ["10.0.0.0/8", "172.16.0.0/12", "192.168.1.1"]
```

## [[services]] Section

Each service requires `name` and `backend_addr`. All global settings can be overridden.

### Basic Configuration

```toml
[[services]]
name = "api"                           # Required: becomes api.<tailnet>.ts.net
backend_addr = "localhost:8080"        # Required: where to proxy to
tags = ["tag:api", "tag:prod"]         # Service tags (overrides default_tags)
```

### Backend Address Formats

```toml
# TCP addresses
backend_addr = "localhost:8080"
backend_addr = "10.0.0.5:3000"
backend_addr = "backend.internal:80"

# Unix sockets
backend_addr = "unix:///var/run/app.sock"
```

### Network Options

```toml
# TLS mode
tls_mode = "auto"          # Use Tailscale HTTPS (default)
tls_mode = "off"           # HTTP only (for Headscale)

# Custom listen port
listen_port = "443"        # Default for TLS
listen_port = "80"         # Default for non-TLS
listen_port = "8443"       # Custom port
```

### Tailscale Features

```toml
# Whois identity headers
whois_enabled = true       # Add X-Tailscale-User headers (default: false)
whois_timeout = "1s"       # Whois lookup timeout (default: from global or 1s)

# Funnel (public access)
funnel_enabled = true      # Expose to internet via Funnel (default: false)

# Ephemeral nodes
ephemeral = true           # Don't persist node state (default: false)
```

### Header Manipulation

```toml
# Add headers to requests going to backend
upstream_headers = {
  "X-Service-Name" = "tsbridge",
  "X-Request-ID" = "generated"
}

# Add headers to responses going to client
downstream_headers = {
  "Strict-Transport-Security" = "max-age=31536000",
  "X-Frame-Options" = "DENY"
}

# Remove headers from requests (comma-separated in Docker labels)
remove_upstream = ["Cookie", "Authorization"]

# Remove headers from responses
remove_downstream = ["Server", "X-Powered-By"]
```

### Service-Specific Overrides

Any global setting can be overridden:

```toml
[[services]]
name = "streaming"
backend_addr = "localhost:8080"

# Override timeouts
write_timeout = "0s"              # No timeout for streaming
response_header_timeout = "30s"   # Different from global
flush_interval = "-1ms"           # Immediate flushing

# Override other settings
access_log = false                # Disable for this service
max_request_body_size = "100MB"   # Larger uploads allowed
```

## Environment Variables

Default environment variables checked if no config specified:
- `TS_OAUTH_CLIENT_ID` - OAuth client ID
- `TS_OAUTH_CLIENT_SECRET` - OAuth client secret
- `TS_AUTHKEY` - Auth key
- `STATE_DIRECTORY` - State directory (systemd)
- `TSBRIDGE_STATE_DIR` - State directory

## Secret Resolution

For any `_env` or `_file` option:
1. Direct value takes precedence
2. Then environment variable
3. Then file contents
4. Then default environment variables

## Configuration Validation

Run with `-validate` flag to check configuration:

```bash
tsbridge -config tsbridge.toml -validate
```

Validates:
- Required fields present
- No duplicate service names
- Valid duration formats
- Valid addresses
- File permissions for secrets
- Authentication configured
- Services have tags (when using OAuth)

## Complete Example

```toml
[tailscale]
oauth_client_id_env = "TS_OAUTH_CLIENT_ID"
oauth_client_secret_file = "/etc/tsbridge/oauth-secret"
state_dir = "/var/lib/tsbridge"
default_tags = ["tag:server", "tag:proxy"]

[global]
# Timeouts
read_header_timeout = "30s"
write_timeout = "30s"
idle_timeout = "120s"
shutdown_timeout = "30s"

# Backend connection
dial_timeout = "10s"
response_header_timeout = "0s"

# Observability
metrics_addr = ":9090"
access_log = true

# Security
trusted_proxies = ["10.0.0.0/8"]

# Limits
max_request_body_size = "50MB"

[[services]]
name = "api"
backend_addr = "api.internal:8080"
tags = ["tag:api", "tag:prod"]
whois_enabled = true
downstream_headers = {
  "Strict-Transport-Security" = "max-age=63072000"
}

[[services]]
name = "streaming"
backend_addr = "localhost:4000"
write_timeout = "0s"
flush_interval = "-1ms"
max_request_body_size = "-1"

[[services]]
name = "admin"
backend_addr = "unix:///var/run/admin.sock"
whois_enabled = true
funnel_enabled = false
access_log = true
```