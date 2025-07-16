# tsbridge Examples

This directory contains various example configurations for tsbridge, organized by complexity and use case.

## Example Configurations

### 1. Simple Example (`simple/`)
Basic tsbridge setup with OAuth authentication and simple backend services.

- Uses traditional TOML configuration file
- Demonstrates basic proxy setup with two backend services
- OAuth authentication with Tailscale

**Quick Start:**
```bash
cd simple
export TS_OAUTH_CLIENT_ID="your-client-id"
export TS_OAUTH_CLIENT_SECRET="your-client-secret"
docker-compose up --build
```

### 2. Docker Labels Example (`docker-labels/`)
Dynamic service discovery using Docker container labels.

- No configuration file needed - everything configured via labels
- Automatic service discovery when containers start/stop
- Perfect for dynamic environments

**Quick Start:**
```bash
cd docker-labels
export TS_OAUTH_CLIENT_ID="your-client-id"
export TS_OAUTH_CLIENT_SECRET="your-client-secret"
docker-compose up --build
```

### 3. Headscale Example (`headscale/`)
Self-hosted Tailscale control server setup using Headscale with built-in testing client.

- Complete Headscale + tsbridge setup
- Uses auth keys instead of OAuth  
- Includes Docker label configuration
- **Built-in Linux client with Tailscale for testing**
- Perfect for on-premise deployments

**Quick Start:**
```bash
cd headscale
docker-compose up -d

# Create user and auth key
docker exec example-headscale-1 headscale users create testuser
docker exec example-headscale-1 headscale --user 1 preauthkeys create --reusable --expiration 90d

# Set auth key and restart services
export TS_AUTHKEY="<auth-key-from-above>"
docker-compose up -d tsbridge tailscale-client

# Test the setup
./test-client.sh
```

**Testing the services:**
```bash
# Test from the built-in Tailscale client
docker exec headscale-tailscale-client-1 curl http://demo-api/
docker exec headscale-tailscale-client-1 curl http://demo-web/
```

## Shared Components

### Backend Service (`backend/`)
A simple Go HTTP server used by all examples that:
- Echoes request information
- Shows Tailscale identity headers
- Demonstrates whois functionality
- Provides health check endpoints

## Prerequisites

All examples require:
- Docker and Docker Compose
- Either Tailscale OAuth credentials OR Headscale setup

## Getting OAuth Credentials

For the simple and docker-labels examples, get OAuth credentials from:
https://login.tailscale.com/admin/settings/oauth

## Common Commands

```bash
# View logs
docker-compose logs -f tsbridge

# Check metrics
curl http://localhost:9090/metrics  # or :9091 for headscale example

# Clean up
docker-compose down -v
```

## Which Example Should I Use?

- **Simple**: Best for getting started, static configurations
- **Docker Labels**: Best for dynamic environments, microservices
- **Headscale**: Best for self-hosted/on-premise deployments

