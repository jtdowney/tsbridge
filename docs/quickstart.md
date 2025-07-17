# Quick Start Guide

Get tsbridge running in 5 minutes.

## 1. Get OAuth Credentials

1. Go to <https://login.tailscale.com/admin/settings/oauth>
2. Click **Generate OAuth client...**
3. Name it something like `tsbridge`
4. Under **Scopes** → **Auth Keys**, check both **Read** and **Write**
5. **Tags**: Select a tag (e.g., `tag:server`) - this tag will be used by your services
6. Click **Generate client**
7. Save the Client ID and Secret

## 2. Create Config File

Create `tsbridge.toml`:

```toml
[tailscale]
oauth_client_id_env = "TS_OAUTH_CLIENT_ID"
oauth_client_secret_env = "TS_OAUTH_CLIENT_SECRET"

[global]
default_tags = ["tag:server"]  # Must match or be owned by your OAuth client's tag

[[services]]
name = "app"
backend_addr = "localhost:8080"
```

## 3. Run It

```bash
export TS_OAUTH_CLIENT_ID=your-client-id
export TS_OAUTH_CLIENT_SECRET=your-client-secret
tsbridge -config tsbridge.toml
```

Your service is now available at `https://app.<tailnet>.ts.net`

## Common Patterns

### Multiple Services

```toml
[[services]]
name = "api"
backend_addr = "localhost:8080"

[[services]]
name = "web"
backend_addr = "localhost:3000"
```

### Unix Sockets

```toml
[[services]]
name = "app"
backend_addr = "unix:///var/run/app.sock"
```

### Add Identity Headers

```toml
[[services]]
name = "internal-api"
backend_addr = "localhost:8080"
whois_enabled = true  # Adds X-Tailscale-User headers
```

### Streaming/SSE Support

```toml
[[services]]
name = "events"
backend_addr = "localhost:3000"
write_timeout = "0s"     # No timeout
flush_interval = "-1ms"  # No buffering
```

### Production Setup

```toml
[tailscale]
oauth_client_id_file = "/etc/tsbridge/oauth-id"
oauth_client_secret_file = "/etc/tsbridge/oauth-secret"
state_dir = "/var/lib/tsbridge"

[global]
default_tags = ["tag:server", "tag:prod"]
metrics_addr = ":9090"  # Prometheus metrics

[[services]]
name = "api"
backend_addr = "api.internal:8080"
whois_enabled = true
downstream_headers = {
  "Strict-Transport-Security" = "max-age=63072000"
}
```

## Docker

### With Docker Compose

```yaml
services:
  tsbridge:
    image: ghcr.io/jtdowney/tsbridge:latest
    command: ["--provider", "docker"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
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

## Troubleshooting

### Validate Your Config

```bash
tsbridge -config tsbridge.toml -validate
```

### Common Issues

- **"services must have at least one tag"**: Add `default_tags` to `[global]` section
- **"OAuth client ID not set"**: Check your environment variables
- **Connection timeouts**: For streaming, set `write_timeout = "0s"`

## Next Steps

- See [Configuration Reference](configuration-reference.md) for all options
- Check [Docker Labels](docker-labels.md) for dynamic container management
- Review [examples/](../example/) for complete setups