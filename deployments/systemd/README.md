# systemd Deployment

Systemd service files for running tsbridge on Linux.

## Quick Install

```bash
# 1. Create user
sudo useradd --system --shell /bin/false --home-dir /var/lib/tsbridge --create-home tsbridge

# 2. Install binary
sudo install -o root -g root -m 755 tsbridge /usr/local/bin/

# 3. Create config directory
sudo install -d -o tsbridge -g tsbridge -m 755 /etc/tsbridge

# 4. Install your config
sudo install -o tsbridge -g tsbridge -m 640 config.toml /etc/tsbridge/
sudo install -o tsbridge -g tsbridge -m 600 tsbridge.env.example /etc/tsbridge/tsbridge.env

# 5. Install service
sudo install -m 644 tsbridge.service /etc/systemd/system/
sudo systemctl daemon-reload

# 6. Start it
sudo systemctl start tsbridge
sudo systemctl enable tsbridge
```

## Configuration

### Environment File (`/etc/tsbridge/tsbridge.env`)

```bash
TS_OAUTH_CLIENT_ID=your-client-id
TS_OAUTH_CLIENT_SECRET=your-client-secret
```

### Config File (`/etc/tsbridge/config.toml`)

```toml
[tailscale]
# OAuth credentials come from environment
state_dir = "/var/lib/tsbridge"  # Or omit - systemd sets STATE_DIRECTORY
default_tags = ["tag:server"]

[global]
metrics_addr = ":9090"

[[services]]
name = "your-service"
backend_addr = "localhost:8080"
```

## State Directory

The service uses `StateDirectory=tsbridge` which means:
- systemd creates `/var/lib/tsbridge` automatically
- Sets `STATE_DIRECTORY` environment variable
- tsbridge detects this automatically (no config needed)

## Security Features

The service file includes hardening:
- Read-only filesystem (`ProtectSystem=strict`)
- No access to /home (`ProtectHome=true`)
- Limited network access (`RestrictAddressFamilies`)
- No privilege escalation (`NoNewPrivileges`)

## Troubleshooting

```bash
# View logs
sudo journalctl -u tsbridge -f

# Validate config
sudo -u tsbridge /usr/local/bin/tsbridge -config /etc/tsbridge/config.toml -validate

# Restart
sudo systemctl restart tsbridge
```

### Common Issues

**"netlinkrib: address family not supported"**
- Already fixed in service file with `AF_NETLINK`
- If you see it: `sudo systemctl daemon-reload && sudo systemctl restart tsbridge`

**Service won't start?**
1. Check binary is executable
2. Validate config file
3. Verify OAuth credentials are set
4. Check port conflicts

## Files

- `tsbridge.service` - systemd service unit
- `tsbridge.env.example` - Example environment file