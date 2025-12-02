# Docker Labels

Run tsbridge in Docker and let it discover services automatically via container labels.

## Quick Example

```yaml
services:
  tsbridge:
    image: ghcr.io/jtdowney/tsbridge:latest
    command: ["--provider", "docker"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - tsbridge-state:/var/lib/tsbridge
    environment:
      # Pass the actual OAuth credentials to the container
      - TS_OAUTH_CLIENT_ID=${TS_OAUTH_CLIENT_ID}
      - TS_OAUTH_CLIENT_SECRET=${TS_OAUTH_CLIENT_SECRET}
    labels:
      # Tell tsbridge which env vars contain the credentials (not redundant - both are needed)
      - "tsbridge.tailscale.oauth_client_id_env=TS_OAUTH_CLIENT_ID"
      - "tsbridge.tailscale.oauth_client_secret_env=TS_OAUTH_CLIENT_SECRET"
      - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"
      - "tsbridge.tailscale.default_tags=tag:server" # Must match or be owned by your OAuth client's tag

  myapp:
    image: myapp:latest
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=myapp"
      - "tsbridge.service.port=8080"

volumes:
  tsbridge-state:
```

Your app is now at `https://myapp.<tailnet>.ts.net`

> **Note**: The `default_tags` must match or be owned by your OAuth client's tag. Individual services can override this with their own `tags` label. See [Tag Ownership and OAuth Security](configuration-reference.md#tag-ownership-and-oauth-security) for setup details.

## How It Works

1. tsbridge watches Docker events for real-time changes
2. When a container with `tsbridge.enabled=true` starts, it creates a proxy
3. When the container stops, the proxy is removed
4. A periodic poll (default: 1 minute) acts as a safety net for any missed events

## CLI Flags

When using the Docker provider, these flags are available:

| Flag | Default | Description |
|------|---------|-------------|
| `--provider` | `file` | Set to `docker` for Docker label discovery |
| `--docker-socket` | `unix:///var/run/docker.sock` | Docker socket endpoint |
| `--docker-label-prefix` | `tsbridge` | Prefix for container labels |
| `--docker-poll-interval` | `1m` | Periodic config poll interval (0 to disable) |

**Example:**

```bash
tsbridge --provider docker --docker-poll-interval 30s
```

## Label Reference

### On tsbridge Container

```yaml
labels:
  # Required: OAuth setup
  - "tsbridge.tailscale.oauth_client_id_env=TS_OAUTH_CLIENT_ID"
  - "tsbridge.tailscale.oauth_client_secret_env=TS_OAUTH_CLIENT_SECRET"
  - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"

  # Optional: defaults
  - "tsbridge.tailscale.default_tags=tag:server,tag:proxy"
  - "tsbridge.tailscale.oauth_preauthorized=false" # Require manual device approval (default: true)
  - "tsbridge.global.metrics_addr=:9090"
  - "tsbridge.global.write_timeout=30s"
```

### On Service Containers

```yaml
labels:
  # Required
  - "tsbridge.enabled=true"

  # Service config (pick one)
  - "tsbridge.service.port=8080" # Recommended
  - "tsbridge.service.backend_addr=myservice:8080" # If you need specific host

  # Optional
  - "tsbridge.service.name=custom-name" # Default: container name
  - "tsbridge.service.whois_enabled=true" # Add identity headers
  - "tsbridge.service.tags=tag:api,tag:prod" # Override default tags
  - "tsbridge.service.oauth_preauthorized=false" # Override global preauth setting (global default: true)
  - "tsbridge.service.listen_addr=0.0.0.0:9090" # Custom address and port
  - "tsbridge.service.insecure_skip_verify=true" # Skip TLS cert verification (HTTPS backends only)
```

## Backend Address Tips

**Use port, not localhost:**

```yaml
# Good - uses container name
- "tsbridge.service.port=8080"

# Bad - localhost is the tsbridge container!
- "tsbridge.service.backend_addr=localhost:8080"
```

**Why?** In Docker, each container has its own network namespace. `localhost` inside tsbridge doesn't reach your service container.

## Advanced Features

### Custom Listen Configuration

```yaml
labels:
  # Listen on specific address and port
  - "tsbridge.service.listen_addr=127.0.0.1:9090"

  # Listen on all interfaces with custom port
  - "tsbridge.service.listen_addr=0.0.0.0:8080"

  # Listen on port only (all interfaces)
  - "tsbridge.service.listen_addr=:8443"
```

### Streaming/SSE

```yaml
labels:
  - "tsbridge.service.write_timeout=0s" # No timeout
  - "tsbridge.service.flush_interval=-1ms" # No buffering
```

### Security Headers

```yaml
labels:
  - "tsbridge.service.downstream_headers.X-Frame-Options=DENY"
  - "tsbridge.service.downstream_headers.Strict-Transport-Security=max-age=31536000"
```

### Custom Headers

```yaml
labels:
  # Add to requests
  - "tsbridge.service.upstream_headers.X-Service-Name=api"

  # Remove from responses
  - "tsbridge.service.remove_downstream=Server,X-Powered-By"
```

### HTTPS Backends

For connecting to HTTPS backend services:

```yaml
labels:
  # For services with valid certificates
  - "tsbridge.service.backend_addr=https://api.example.com:443"

  # For services with self-signed certificates (use with caution)
  - "tsbridge.service.backend_addr=https://internal.lan:8443"
  - "tsbridge.service.insecure_skip_verify=true"
```

> **⚠️ Security Warning**: `insecure_skip_verify=true` disables TLS certificate validation. Only use this for trusted internal services with self-signed certificates, as it makes connections vulnerable to attacks.

## Complete Example

```yaml
services:
  tsbridge:
    image: ghcr.io/jtdowney/tsbridge:latest
    command: ["--provider", "docker", "--verbose"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - tsbridge-state:/var/lib/tsbridge
    networks:
      - app-network
    environment:
      - TS_OAUTH_CLIENT_ID=${TS_OAUTH_CLIENT_ID}
      - TS_OAUTH_CLIENT_SECRET=${TS_OAUTH_CLIENT_SECRET}
    labels:
      - "tsbridge.tailscale.oauth_client_id_env=TS_OAUTH_CLIENT_ID"
      - "tsbridge.tailscale.oauth_client_secret_env=TS_OAUTH_CLIENT_SECRET"
      - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"
      - "tsbridge.global.metrics_addr=:9090"
    ports:
      - "9090:9090" # Metrics

  api:
    image: myapp/api:latest
    networks:
      - app-network
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=api"
      - "tsbridge.service.port=8080"
      - "tsbridge.service.whois_enabled=true"

  web:
    image: myapp/web:latest
    networks:
      - app-network
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=web"
      - "tsbridge.service.port=3000"
      - "tsbridge.service.access_log=false"

volumes:
  tsbridge-state:

networks:
  app-network:
```

## Docker Networking

### Network Requirements

tsbridge and service containers must be on the same Docker network for communication. They don't need to be in the same compose file, but network connectivity is required.

```yaml
# tsbridge can forward traffic to service containers only if they share a network
networks:
  app-network:  # Same network name in both files

# In tsbridge compose file
services:
  tsbridge:
    networks:
      - app-network

# In service compose file
services:
  myservice:
    networks:
      - app-network
```

### Single Compose File (Simplest)

When everything is in one compose file, Docker automatically creates a shared network:

```yaml
services:
  tsbridge:
    image: ghcr.io/jtdowney/tsbridge:latest
    command: ["--provider", "docker"]
    # ... other config

  myapp:
    image: myapp:latest
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.port=8080"
# Both containers automatically share the default network
```

### Multiple Compose Files

For services in separate compose files, use external networks:

**1. Create the network first:**

```bash
docker network create tsbridge-network
```

**2. tsbridge-compose.yml:**

```yaml
services:
  tsbridge:
    image: ghcr.io/jtdowney/tsbridge:latest
    command: ["--provider", "docker"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      - TS_OAUTH_CLIENT_ID=${TS_OAUTH_CLIENT_ID}
      - TS_OAUTH_CLIENT_SECRET=${TS_OAUTH_CLIENT_SECRET}
    labels:
      - "tsbridge.tailscale.oauth_client_id_env=TS_OAUTH_CLIENT_ID"
      - "tsbridge.tailscale.oauth_client_secret_env=TS_OAUTH_CLIENT_SECRET"
    networks:
      - tsbridge-network

networks:
  tsbridge-network:
    external: true
```

**3. services-compose.yml:**

```yaml
services:
  api:
    image: myapi:latest
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=api"
      - "tsbridge.service.port=8080"
    networks:
      - tsbridge-network

  web:
    image: myweb:latest
    labels:
      - "tsbridge.enabled=true"
      - "tsbridge.service.name=web"
      - "tsbridge.service.port=3000"
    networks:
      - tsbridge-network

networks:
  tsbridge-network:
    external: true
```

**4. Start them:**

```bash
# Start tsbridge
docker compose -f tsbridge-compose.yml up -d

# Start services (in any order)
docker compose -f services-compose.yml up -d
```

### Alternative: Define Network in One File

You can also define the network in one compose file and reference it as external in others:

**tsbridge-compose.yml (defines network):**

```yaml
services:
  tsbridge:
    # ... config
    networks:
      - shared-network

networks:
  shared-network:
    name: tsbridge-shared
```

**services-compose.yml (uses external network):**

```yaml
services:
  myapp:
    # ... config
    networks:
      - shared-network

networks:
  shared-network:
    external: true
    name: tsbridge-shared
```

### Network Troubleshooting

**Why does networking matter?**

- tsbridge acts as a reverse proxy
- It needs to reach your service containers over the network
- `localhost` inside tsbridge container ≠ service containers
- Docker networks enable container-to-container communication

**Common networking issues:**

**Service not appearing?**

- Check `tsbridge.enabled=true` is set
- Verify containers are on same network - use `docker network ls` and `docker inspect <container>`
- Look at tsbridge logs with `--verbose`

**Connection refused?**

- Don't use `localhost` - use `port` label instead
- Make sure service is listening on the port
- Check container is actually running
- Verify network connectivity: `docker exec tsbridge-container ping service-container`

**Cross-compose networking not working?**

- Ensure both compose files reference the same network name
- Check network exists: `docker network ls`
- Verify containers joined the network: `docker network inspect <network-name>`
- Make sure network is external in dependent compose files

## Troubleshooting

**Label changes ignored?**

- Labels are only read when container starts
- Restart container to apply new labels

**Cannot connect between compose files?**

- Ensure both files use the same network name
- Network must be marked as `external: true` in dependent compose files
- Create network manually if needed: `docker network create <network-name>`
