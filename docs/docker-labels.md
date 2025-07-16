# Docker Label Configuration

tsbridge supports dynamic configuration through Docker container labels, similar to Traefik. When running as a Docker container, tsbridge can discover and configure services automatically by reading labels from containers.

## Quick Start

```yaml
services:
  tsbridge:
    image: ghcr.io/jtdowney/tsbridge:latest
    command: ["--provider", "docker"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - tsbridge-state:/var/lib/tsbridge
    labels:
      # Global configuration
      - "tsbridge.tailscale.oauth_client_id_env=TS_OAUTH_CLIENT_ID"
      - "tsbridge.tailscale.oauth_client_secret_env=TS_OAUTH_CLIENT_SECRET"
      - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"
      - "tsbridge.tailscale.default_tags=tag:server,tag:proxy"
      - "tsbridge.global.metrics_addr=:9090"
    environment:
      - TS_OAUTH_CLIENT_ID=${TS_OAUTH_CLIENT_ID}
      - TS_OAUTH_CLIENT_SECRET=${TS_OAUTH_CLIENT_SECRET}
    ports:
      - "9090:9090" # Metrics port

  api:
    image: myapp:latest
    labels:
      # Enable tsbridge for this container
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=api"
      - "tsbridge.service.backend_addr=:8080"

volumes:
  tsbridge-state:
```

## CLI Flags

When using the Docker provider, use these CLI flags:

- `--provider docker` - Enable Docker provider (required)
- `--docker-socket <path>` - Docker socket path (default: `unix:///var/run/docker.sock`)
- `--docker-label-prefix <prefix>` - Label prefix (default: `tsbridge`)

## Label Reference

### Global Configuration (on tsbridge container)

Global labels configure tsbridge itself and provide defaults for all services.

#### Tailscale Configuration

```yaml
labels:
  # OAuth credentials
  - "tsbridge.tailscale.oauth_client_id=<value>"
  - "tsbridge.tailscale.oauth_client_id_env=<env_var>"
  - "tsbridge.tailscale.oauth_client_id_file=<file_path>"
  - "tsbridge.tailscale.oauth_client_secret=<value>"
  - "tsbridge.tailscale.oauth_client_secret_env=<env_var>"
  - "tsbridge.tailscale.oauth_client_secret_file=<file_path>"

  # Auth key (alternative to OAuth)
  - "tsbridge.tailscale.auth_key=<value>"
  - "tsbridge.tailscale.auth_key_env=<env_var>"
  - "tsbridge.tailscale.auth_key_file=<file_path>"

  # State directory
  - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"
  - "tsbridge.tailscale.state_dir_env=<env_var>"

  # Default tags for all services (comma-separated)
  - "tsbridge.tailscale.default_tags=tag:tsbridge,tag:proxy"

  # Control server URL (optional - for Headscale or custom control servers)
  - "tsbridge.tailscale.control_url=https://headscale.example.com"
```

#### Global Defaults

```yaml
labels:
  # Timeouts
  - "tsbridge.global.read_header_timeout=30s"
  - "tsbridge.global.write_timeout=30s"
  - "tsbridge.global.idle_timeout=120s"
  - "tsbridge.global.shutdown_timeout=15s"
  - "tsbridge.global.response_header_timeout=10s"

  # Metrics
  - "tsbridge.global.metrics_addr=:9090"

  # Access logging
  - "tsbridge.global.access_log=true"

  # Request body size limit
  - "tsbridge.global.max_request_body_size=10MB"

  # Trusted proxies (comma-separated)
  - "tsbridge.global.trusted_proxies=10.0.0.0/8,172.16.0.0/12"

  # Transport timeouts
  - "tsbridge.global.dial_timeout=10s"
  - "tsbridge.global.keep_alive_timeout=30s"
  - "tsbridge.global.idle_conn_timeout=90s"
  - "tsbridge.global.tls_handshake_timeout=10s"
  - "tsbridge.global.expect_continue_timeout=1s"
  - "tsbridge.global.metrics_read_header_timeout=5s"
```

### Service Configuration (on service containers)

Service labels configure individual services that tsbridge will proxy.

#### Basic Configuration

```yaml
labels:
  # Enable tsbridge for this container (required)
  - "tsbridge.enabled=true"

  # Service name (defaults to container name)
  - "tsbridge.service.name=api"

  # Backend address (see "Backend Address Resolution" below)
  - "tsbridge.service.backend_addr=localhost:8080"
  # OR just the port (container name will be used as host)
  - "tsbridge.service.port=8080"
```

#### Advanced Configuration

```yaml
labels:
  # Service-specific tags (comma-separated) - overrides tailscale.default_tags
  - "tsbridge.service.tags=tag:api,tag:prod"

  # Whois configuration
  - "tsbridge.service.whois_enabled=true"
  - "tsbridge.service.whois_timeout=2s"

  # TLS mode
  - "tsbridge.service.tls_mode=auto" # or "off"
  
  # Listen port (optional, defaults: 443 for TLS, 80 for non-TLS)
  - "tsbridge.service.listen_port=8443"

  # Service-specific timeouts (override global)
  - "tsbridge.service.read_header_timeout=60s"
  - "tsbridge.service.write_timeout=60s"
  - "tsbridge.service.idle_timeout=300s"
  - "tsbridge.service.response_header_timeout=30s"

  # Access logging (override global)
  - "tsbridge.service.access_log=false"

  # Request body size limit (override global)
  - "tsbridge.service.max_request_body_size=50MB"

  # Tailscale Funnel
  - "tsbridge.service.funnel_enabled=true"

  # Ephemeral nodes
  - "tsbridge.service.ephemeral=true"
```

#### Header Manipulation

```yaml
labels:
  # Add headers to upstream requests
  - "tsbridge.service.upstream_headers.X-Custom-Header=value"
  - "tsbridge.service.upstream_headers.X-Request-ID=generated"

  # Add headers to downstream responses
  - "tsbridge.service.downstream_headers.X-Frame-Options=DENY"
  - "tsbridge.service.downstream_headers.X-Content-Type-Options=nosniff"

  # Remove headers from upstream requests (comma-separated)
  - "tsbridge.service.remove_upstream=X-Forwarded-For,X-Real-IP"

  # Remove headers from downstream responses (comma-separated)
  - "tsbridge.service.remove_downstream=Server,X-Powered-By"
```

## Backend Address Resolution

tsbridge resolves backend addresses in the following order:

1. **Explicit address**: If `tsbridge.service.backend_addr` is set, it's used as-is
2. **Port-based**: If `tsbridge.service.port` is set, the address is `<container_name>:<port>`
3. **Auto-detection**: First exposed port from the container is used with the container name

### Port vs Backend Address in Docker

When running in Docker, use `tsbridge.service.port` instead of `tsbridge.service.backend_addr`:

- **Docker Networking**: Each container has its own IP address. Container names resolve to their IPs automatically.
- **Why port works**: `tsbridge.service.port=8080` becomes `<container_name>:8080`, which Docker's DNS resolves correctly.
- **Why localhost fails**: `localhost` or `127.0.0.1` refers to the tsbridge container itself, not your service container.

### Examples

```yaml
# RECOMMENDED: Port only (container name will be used)
- "tsbridge.service.port=3000"

# Also works: Explicit container name with port
- "tsbridge.service.backend_addr=myservice:3000"

# AVOID: localhost won't reach other containers
- "tsbridge.service.backend_addr=localhost:8080"

# Unix socket (requires volume mount)
- "tsbridge.service.backend_addr=unix:///var/run/app.sock"
# Auto-detection (uses first exposed port)
# No backend labels needed if container exposes a port
```

## Dynamic Updates

The Docker provider enables true dynamic service management by monitoring Docker container lifecycle events in real-time. This allows tsbridge to automatically adjust its configuration as containers start, stop, or change, without requiring manual intervention or restarts.

### How It Works

tsbridge subscribes to Docker's event stream and responds to specific container events:

- **Container Start**: When a container with `tsbridge.enabled=true` starts, tsbridge automatically:
  - Detects the new container
  - Parses its labels to extract service configuration
  - Creates and starts a new proxy service
  - Makes it available at `https://<service-name>.<tailnet>.ts.net`
- **Container Stop/Die**: When a labeled container stops or crashes, tsbridge automatically:
  - Detects the container state change
  - Gracefully stops the associated proxy service
  - Removes it from the active service registry
  - Cleans up all associated resources

- **Container Pause/Unpause**: Services are temporarily unavailable during pause and restored on unpause

### Event-Based Architecture

Unlike polling-based systems, tsbridge uses Docker's event stream for immediate responsiveness:

```yaml
# Example: Rolling deployment with zero downtime
# 1. Start new version of your service
docker-compose up -d api-v2  # tsbridge detects and adds the service

# 2. Verify new version is working
curl https://api-v2.example.ts.net/health

# 3. Stop old version
docker-compose stop api-v1   # tsbridge detects and removes the service
```

### Important Notes

- There's no polling interval - updates happen immediately when Docker events occur
- Services must have unique names to avoid conflicts
- Stopping tsbridge itself will stop all proxy services, but won't affect your containers
- Container restarts (with same name) are handled gracefully with minimal downtime

## Network Considerations

- Containers must be on the same Docker network as tsbridge for name-based resolution
- Use explicit IP addresses or hostnames if containers are on different networks
- Unix sockets require volume mounts to be accessible

## Security

- tsbridge requires read-only access to the Docker socket
- Never expose the Docker socket without proper security measures
- Use secret management (env vars or files) for sensitive data
- Labels are visible in container inspect - avoid putting secrets directly in labels

## Complete Example

```yaml
services:
  tsbridge:
    image: ghcr.io/jtdowney/tsbridge:latest
    command:
      - "--provider"
      - "docker"
      - "--verbose"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - tsbridge-state:/var/lib/tsbridge
    networks:
      - tsbridge-network
    labels:
      # Tailscale configuration
      - "tsbridge.tailscale.oauth_client_id_env=TS_OAUTH_CLIENT_ID"
      - "tsbridge.tailscale.oauth_client_secret_env=TS_OAUTH_CLIENT_SECRET"
      - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"
      - "tsbridge.tailscale.default_tags=tag:server,tag:proxy"

      # Global defaults
      - "tsbridge.global.metrics_addr=:9090"
      - "tsbridge.global.read_header_timeout=30s"
      - "tsbridge.global.access_log=true"
    environment:
      - TS_OAUTH_CLIENT_ID=${TS_OAUTH_CLIENT_ID}
      - TS_OAUTH_CLIENT_SECRET=${TS_OAUTH_CLIENT_SECRET}
    ports:
      - "9090:9090" # Metrics

  # API service
  api:
    image: myapp/api:latest
    networks:
      - tsbridge-network
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=api"
      - "tsbridge.service.port=8080"
      - "tsbridge.service.whois_enabled=true"
      - "tsbridge.service.upstream_headers.X-Service=api"
    expose:
      - "8080"

  # Web service with custom configuration
  web:
    image: myapp/web:latest
    networks:
      - tsbridge-network
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=web"
      - "tsbridge.service.backend_addr=web:3000"
      - "tsbridge.service.read_header_timeout=60s"
      - "tsbridge.service.access_log=false"
      - "tsbridge.service.downstream_headers.Cache-Control=no-cache"
      - "tsbridge.service.remove_downstream=Server"
    expose:
      - "3000"

  # Admin service with Funnel enabled
  admin:
    image: myapp/admin:latest
    networks:
      - tsbridge-network
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=admin"
      - "tsbridge.service.port=9000"
      - "tsbridge.service.funnel_enabled=true"
      - "tsbridge.service.whois_enabled=true"
      - "tsbridge.service.whois_timeout=2s"

volumes:
  tsbridge-state:

networks:
  tsbridge-network:
    driver: bridge
```

## Troubleshooting

### Container not discovered

- Ensure `tsbridge.enabled=true` is set
- Check that the container is running
- Verify tsbridge has access to Docker socket
- Check logs with `--verbose` flag

### Backend connection failures

- Verify containers are on the same network
- Check the backend address format
- Ensure the backend service is listening on the specified port
- Check container logs for startup errors

### Label changes not detected

- The watch interval is 5 seconds - wait for the next check
- Restart tsbridge if changes aren't picked up
- Check tsbridge logs for configuration errors
