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
      - TS_OAUTH_CLIENT_ID=${TS_OAUTH_CLIENT_ID}
      - TS_OAUTH_CLIENT_SECRET=${TS_OAUTH_CLIENT_SECRET}
    labels:
      - "tsbridge.tailscale.oauth_client_id_env=TS_OAUTH_CLIENT_ID"
      - "tsbridge.tailscale.oauth_client_secret_env=TS_OAUTH_CLIENT_SECRET"
      - "tsbridge.tailscale.state_dir=/var/lib/tsbridge"

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

## How It Works

1. tsbridge watches Docker events (no polling!)
2. When a container with `tsbridge.enabled=true` starts, it creates a proxy
3. When the container stops, the proxy is removed
4. Changes happen instantly

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
  - "tsbridge.global.metrics_addr=:9090"
  - "tsbridge.global.write_timeout=30s"
```

### On Service Containers

```yaml
labels:
  # Required
  - "tsbridge.enabled=true"
  
  # Service config (pick one)
  - "tsbridge.service.port=8080"                    # Recommended
  - "tsbridge.service.backend_addr=myservice:8080"  # If you need specific host
  
  # Optional
  - "tsbridge.service.name=custom-name"      # Default: container name
  - "tsbridge.service.whois_enabled=true"    # Add identity headers
  - "tsbridge.service.tags=tag:api,tag:prod" # Override default tags
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

### Streaming/SSE

```yaml
labels:
  - "tsbridge.service.write_timeout=0s"     # No timeout
  - "tsbridge.service.flush_interval=-1ms"  # No buffering
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
      - "9090:9090"  # Metrics

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

## Troubleshooting

**Service not appearing?**
- Check `tsbridge.enabled=true` is set
- Verify containers are on same network
- Look at tsbridge logs with `--verbose`

**Connection refused?**
- Don't use `localhost` - use `port` label instead
- Make sure service is listening on the port
- Check container is actually running

**Label changes ignored?**
- Labels are only read when container starts
- Restart container to apply new labels