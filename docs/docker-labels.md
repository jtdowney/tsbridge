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
      - "tsbridge.tailscale.oauth_tags=tag:server"  # Required when using OAuth
      - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"
      - "tsbridge.global.metrics_addr=:9090"
    environment:
      - TS_OAUTH_CLIENT_ID=${TS_OAUTH_CLIENT_ID}
      - TS_OAUTH_CLIENT_SECRET=${TS_OAUTH_CLIENT_SECRET}
    ports:
      - "9090:9090"  # Metrics port

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

  # OAuth tags (comma-separated) - REQUIRED when using OAuth
  - "tsbridge.tailscale.oauth_tags=tag:server,tag:proxy"

  # State directory
  - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"
```

#### Global Defaults

```yaml
labels:
  # Timeouts
  - "tsbridge.global.read_timeout=30s"
  - "tsbridge.global.write_timeout=30s"
  - "tsbridge.global.idle_timeout=120s"
  - "tsbridge.global.shutdown_timeout=15s"
  - "tsbridge.global.response_header_timeout=10s"


  # Metrics
  - "tsbridge.global.metrics_addr=:9090"

  # Access logging
  - "tsbridge.global.access_log=true"

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
  # Whois configuration
  - "tsbridge.service.whois_enabled=true"
  - "tsbridge.service.whois_timeout=2s"

  # TLS mode
  - "tsbridge.service.tls_mode=auto" # or "off"

  # Service-specific timeouts (override global)
  - "tsbridge.service.read_timeout=60s"
  - "tsbridge.service.write_timeout=60s"
  - "tsbridge.service.idle_timeout=300s"
  - "tsbridge.service.response_header_timeout=30s"


  # Access logging (override global)
  - "tsbridge.service.access_log=false"

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

### Examples

```yaml
# Explicit TCP address
- "tsbridge.service.backend_addr=localhost:8080"

# Unix socket
- "tsbridge.service.backend_addr=unix:///var/run/app.sock"

# Port only (container name will be used)
- "tsbridge.service.port=3000"
# Auto-detection (uses first exposed port)
# No backend labels needed if container exposes a port
```

## Dynamic Updates

The Docker provider watches for container events and updates the configuration automatically:

- New containers with `tsbridge.enabled=true` are added as services
- Stopped containers are removed from the configuration
- Label changes trigger service updates

Updates are checked every 5 seconds by default.

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
      - "tsbridge.tailscale.oauth_tags=tag:server,tag:proxy"
      - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"

      # Global defaults
      - "tsbridge.global.metrics_addr=:9090"
      - "tsbridge.global.read_timeout=30s"
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
      - "tsbridge.service.read_timeout=60s"
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
