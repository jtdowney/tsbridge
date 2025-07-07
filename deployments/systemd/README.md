# tsbridge Systemd Deployment

This directory contains systemd service files and related configuration for deploying tsbridge on Linux systems using systemd.

## Files

- `tsbridge.service` - The main systemd service unit file
- `tsbridge.env.example` - Example environment file for sensitive configuration
- `tsbridge.logrotate` - Logrotate configuration for log management

## Installation

### 1. Create tsbridge user and group

```bash
# Create system user and group for tsbridge
sudo useradd --system --shell /bin/false --home-dir /var/lib/tsbridge --create-home tsbridge
```

### 2. Install the binary

```bash
# Copy the tsbridge binary to system location
sudo cp tsbridge /usr/local/bin/
sudo chmod 755 /usr/local/bin/tsbridge
sudo chown root:root /usr/local/bin/tsbridge
```

### 3. Create configuration directories

```bash
# Create config directory
sudo mkdir -p /etc/tsbridge
sudo chown tsbridge:tsbridge /etc/tsbridge
sudo chmod 755 /etc/tsbridge

# Create state directory (for Tailscale state)
sudo mkdir -p /var/lib/tsbridge
sudo chown tsbridge:tsbridge /var/lib/tsbridge
sudo chmod 700 /var/lib/tsbridge

# Create log directory
sudo mkdir -p /var/log/tsbridge
sudo chown tsbridge:tsbridge /var/log/tsbridge
sudo chmod 755 /var/log/tsbridge
```

### 4. Install configuration files

```bash
# Copy your TOML configuration
sudo cp config.toml /etc/tsbridge/config.toml
sudo chown tsbridge:tsbridge /etc/tsbridge/config.toml
sudo chmod 640 /etc/tsbridge/config.toml

# Copy and edit environment file (optional)
sudo cp tsbridge.env.example /etc/tsbridge/tsbridge.env
sudo chown tsbridge:tsbridge /etc/tsbridge/tsbridge.env
sudo chmod 600 /etc/tsbridge/tsbridge.env
# Edit /etc/tsbridge/tsbridge.env with your OAuth credentials
```

### 5. Install systemd service

```bash
# Copy service file
sudo cp tsbridge.service /etc/systemd/system/
sudo chmod 644 /etc/systemd/system/tsbridge.service

# Reload systemd
sudo systemctl daemon-reload
```

### 6. Configure logrotate (optional)

```bash
# Install logrotate config
sudo cp tsbridge.logrotate /etc/logrotate.d/tsbridge
sudo chmod 644 /etc/logrotate.d/tsbridge
```

### 7. Validate configuration

Before starting the service, validate your configuration:

```bash
# Validate configuration as the tsbridge user
sudo -u tsbridge /usr/local/bin/tsbridge -config /etc/tsbridge/config.toml -validate
```

### 8. Start and enable the service

```bash
# Start the service
sudo systemctl start tsbridge

# Check status
sudo systemctl status tsbridge

# Enable auto-start on boot
sudo systemctl enable tsbridge
```

## Configuration

### Environment Variables

You can set OAuth credentials and other sensitive configuration via environment variables in `/etc/tsbridge/tsbridge.env`:

```bash
TS_OAUTH_CLIENT_ID=your-client-id
TS_OAUTH_CLIENT_SECRET=your-client-secret
```

Or use an auth key for ephemeral nodes:

```bash
TS_AUTHKEY=your-auth-key
```

### TOML Configuration

Your main configuration should be in `/etc/tsbridge/config.toml`. Make sure to update the `state_dir` to use the system directory:

```toml
[tailscale]
# Option 1: Minimal config - will use TS_OAUTH_CLIENT_ID and TS_OAUTH_CLIENT_SECRET env vars
state_dir = "/var/lib/tsbridge"

# Option 2: Explicitly specify which env vars to use
# oauth_client_id_env = "CUSTOM_OAUTH_ID"
# oauth_client_secret_env = "CUSTOM_OAUTH_SECRET"

# Option 3: Use auth key instead of OAuth
# auth_key_env = "TS_AUTHKEY"

# Default tags for all services (when using OAuth, your service must have tags)
default_tags = ["tag:server", "tag:proxy"]

[global]
metrics_addr = ":9090"

[[services]]
name = "your-service"
backend_addr = "localhost:8080"
```

## Security Considerations

The systemd service file includes several security hardening options:

- **NoNewPrivileges**: Prevents privilege escalation
- **PrivateTmp**: Isolates temporary files
- **ProtectSystem=strict**: Makes entire filesystem read-only except specified paths
- **ProtectHome**: Prevents access to user home directories
- **RestrictAddressFamilies**: Limits to IPv4, IPv6, Unix sockets, and Netlink (for Tailscale routing)
- **MemoryDenyWriteExecute**: Prevents execution of writable memory
- **ReadWritePaths**: Only allows writing to state and log directories

## Troubleshooting

### View logs

```bash
# View recent logs
sudo journalctl -u tsbridge -n 50

# Follow logs in real-time
sudo journalctl -u tsbridge -f

# View logs from the last boot
sudo journalctl -u tsbridge -b
```

### Restart the service

```bash
sudo systemctl restart tsbridge
```

### Check service status

```bash
sudo systemctl status tsbridge
```

### Common Issues

#### "netlinkrib: address family not supported by protocol"

This error occurs when Tailscale can't access network routing information. The service file already includes `AF_NETLINK` in `RestrictAddressFamilies` to allow this access. If you still see this error after updating the service file:

```bash
sudo systemctl daemon-reload
sudo systemctl restart tsbridge
```

### Debug startup issues

If the service fails to start, check:

1. Binary is executable: `ls -la /usr/local/bin/tsbridge`
2. Config file exists and is valid: `sudo -u tsbridge /usr/local/bin/tsbridge -config /etc/tsbridge/config.toml -validate`
3. Directories exist with correct permissions
4. OAuth credentials are set (either in config or environment)
5. No port conflicts on metrics endpoint or service ports
6. Tailscale network access: The service needs AF_NETLINK for routing table access

### Adjust resource limits

If you need to adjust resource limits, edit the service file:

```bash
sudo systemctl edit tsbridge
```

Add overrides like:

```ini
[Service]
LimitNOFILE=131072
LimitNPROC=1024
```

## Uninstall

To completely remove tsbridge:

```bash
# Stop and disable service
sudo systemctl stop tsbridge
sudo systemctl disable tsbridge

# Remove files
sudo rm -f /etc/systemd/system/tsbridge.service
sudo rm -f /usr/local/bin/tsbridge
sudo rm -rf /etc/tsbridge
sudo rm -rf /var/lib/tsbridge
sudo rm -rf /var/log/tsbridge
sudo rm -f /etc/logrotate.d/tsbridge

# Remove user
sudo userdel tsbridge

# Reload systemd
sudo systemctl daemon-reload
```
