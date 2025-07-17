# Examples

Working examples of tsbridge configurations.

## simple/

Basic setup with TOML config:

```bash
cd simple
export TS_OAUTH_CLIENT_ID="your-client-id"
export TS_OAUTH_CLIENT_SECRET="your-client-secret"
docker compose up
```

## docker-labels/

Dynamic service discovery via Docker labels:

```bash
cd docker-labels
export TS_OAUTH_CLIENT_ID="your-client-id"
export TS_OAUTH_CLIENT_SECRET="your-client-secret"
docker compose up
```

## headscale/

Self-hosted control server with Headscale:

```bash
cd headscale
docker compose up -d

# Create user and auth key
docker compose exec headscale headscale users create testuser
docker compose exec headscale headscale --user 1 preauthkeys create --reusable --expiration 90d

# Set auth key and restart
export TS_AUTHKEY="<auth-key-from-above>"
docker compose up -d tsbridge tailscale-client
```

## backend/

Shared test backend that echoes request info and shows Tailscale headers.

## Common Tasks

```bash
# Logs
docker compose logs -f tsbridge

# Metrics
curl http://localhost:9090

# Cleanup
docker compose down -v
```
