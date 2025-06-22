# tsbridge Example

This example demonstrates running tsbridge with multiple backend services using Docker Compose.

## Prerequisites

- Docker and Docker Compose installed
- Tailscale OAuth credentials (get them from [Tailscale Admin Console](https://login.tailscale.com/admin/settings/oauth))

## Quick Start

1. Set your OAuth credentials:

```bash
export TS_OAUTH_CLIENT_ID="your-client-id"
export TS_OAUTH_CLIENT_SECRET="your-client-secret"
```

2. Start the services:

```bash
docker-compose up --build
```

3. Your services will be available at:
   - `https://demo-api.<your-tailnet>.ts.net`
   - `https://demo-web.<your-tailnet>.ts.net`
   - `https://demo-slow.<your-tailnet>.ts.net`

4. Check the metrics endpoint:

```bash
curl http://localhost:9090/metrics
```

## What's Included

- **Two backend services**: Simple HTTP servers that echo requests and show Tailscale headers
- **tsbridge proxy**: Configured with three services demonstrating different configurations
- **Metrics**: Prometheus metrics exposed on port 9090
- **Access logging**: Enabled to show request logs

## Testing the Services

Once running, you can test the services from another machine on your Tailnet:

```bash
# Test the API service
curl https://demo-api.<your-tailnet>.ts.net

# Test the Web service
curl https://demo-web.<your-tailnet>.ts.net

# Test with headers
curl -v https://demo-api.<your-tailnet>.ts.net
```

The response will include any Tailscale identity headers that were injected:

```json
{
  "service": "api",
  "timestamp": "2024-01-15T10:30:45Z",
  "message": "Hello from api backend!",
  "headers": {
    "X-Tailscale-Login": ["user@example.com"],
    "X-Tailscale-Name": ["User Name"],
    "X-Tailscale-User": ["user@example.com"]
  }
}
```

## Configuration Details

The `tsbridge.toml` file demonstrates:

- OAuth credential configuration via environment variables
- Global timeout settings
- Multiple service configurations
- Per-service timeout overrides
- Whois header injection (enabled/disabled per service)
- Metrics and access logging

## Viewing Logs

To see tsbridge logs with debug information:

```bash
docker-compose logs -f tsbridge
```

To see backend service logs:

```bash
docker-compose logs -f api-backend web-backend
```

## Cleanup

To stop and remove all containers:

```bash
docker-compose down -v
```

## Customization

You can modify the example to:

- Add more backend services
- Test Unix socket connections
- Experiment with different timeout configurations
- Add authentication or other middleware

Simply edit `tsbridge.toml` and restart the services.