# Metrics

tsbridge exposes Prometheus metrics for monitoring and alerting.

## Enabling Metrics

Set the metrics endpoint in your config:

```toml
[global]
metrics_addr = ":9090"  # Listen on all interfaces, port 9090
```

Access metrics at `http://localhost:9090/metrics`

## Available Metrics

### Request Metrics

#### tsbridge_requests_total

- **Type**: Counter
- **Labels**: `service`, `status` (HTTP status code)
- **Description**: Total number of HTTP requests processed
- **Use case**: Request rate, success/error ratios

```promql
# Request rate per service
rate(tsbridge_requests_total[5m])

# Error rate (non-2xx responses)
rate(tsbridge_requests_total{status!~"2.."}[5m])
```

#### tsbridge_request_duration_seconds

- **Type**: Histogram
- **Labels**: `service`
- **Description**: Request processing time in seconds
- **Use case**: Latency monitoring, SLO tracking

```promql
# 95th percentile latency per service
histogram_quantile(0.95, sum by (service, le) (
  rate(tsbridge_request_duration_seconds_bucket[5m])
))

# Average request duration
rate(tsbridge_request_duration_seconds_sum[5m]) / rate(tsbridge_request_duration_seconds_count[5m])
```

### Error Tracking

#### tsbridge_errors_total

- **Type**: Counter
- **Labels**: `service`, `type` (error type)
- **Description**: Total number of errors by type
- **Error types**:
  - `backend_connection` - Failed to connect to backend
  - `backend_error` - Backend returned an error
  - `whois_timeout` - Whois lookup timed out
  - `panic` - Request handler panic

```promql
# Error rate by type
rate(tsbridge_errors_total[5m])
```

### Connection Metrics

#### tsbridge_connections_active

- **Type**: Gauge
- **Labels**: `service`
- **Description**: Current number of active connections
- **Use case**: Load monitoring, capacity planning

#### tsbridge_connection_pool_active

- **Type**: Gauge
- **Labels**: `service`
- **Description**: Active requests to backend (in-flight requests)
- **Use case**: Backend load monitoring

### Whois Metrics

#### tsbridge_whois_duration_seconds

- **Type**: Histogram
- **Labels**: `service`
- **Description**: Time taken for Tailscale whois lookups
- **Use case**: Monitor whois performance

```promql
# Whois lookup latency
histogram_quantile(0.99, rate(tsbridge_whois_duration_seconds_bucket[5m]))
```

### Backend Health

#### tsbridge_backend_health

- **Type**: Gauge
- **Labels**: `service`
- **Description**: Backend health status (1 = healthy, 0 = unhealthy)
- **Note**: Currently not actively updated (reserved for health checks)

### Service Lifecycle

#### tsbridge_services_active

- **Type**: Gauge
- **Description**: Number of currently active services
- **Use case**: Track service count

#### tsbridge_service_operations_total

- **Type**: Counter
- **Labels**: `operation`, `status`
- **Operations**: `add`, `remove`, `update`
- **Status**: `success`, `failure`
- **Description**: Service lifecycle operations

```promql
# Service operation failure rate
rate(tsbridge_service_operations_total{status="failure"}[5m])
```

#### tsbridge_service_operation_duration_seconds

- **Type**: Histogram
- **Labels**: `operation`
- **Description**: Time taken for service operations
- **Use case**: Monitor startup/shutdown performance

### Configuration

#### tsbridge_config_reloads_total

- **Type**: Counter
- **Labels**: `status` (success/failure)
- **Description**: Configuration reload attempts
- **Note**: For future dynamic reload support

#### tsbridge_config_reload_duration_seconds

- **Type**: Histogram
- **Description**: Time taken to reload configuration

### OAuth Metrics

#### tsbridge_oauth_refresh_total

- **Type**: Counter
- **Labels**: `status` (success/failure)
- **Description**: OAuth token refresh operations
- **Note**: Reserved for future OAuth token refresh tracking

## Example Queries

### Service Overview

```promql
# Request rate
sum(rate(tsbridge_requests_total[5m])) by (service)

# Error rate
sum(rate(tsbridge_requests_total{status!~"2.."}[5m])) by (service)

# P95 latency
histogram_quantile(0.95, sum(rate(tsbridge_request_duration_seconds_bucket[5m])) by (service, le))

# Active services
tsbridge_services_active
```
