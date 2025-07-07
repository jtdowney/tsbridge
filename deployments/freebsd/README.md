# FreeBSD Deployment Guide for tsbridge

This guide covers deploying tsbridge on FreeBSD systems using the rc.d init system.

## Prerequisites

- FreeBSD
- Go 1.24+ (for building from source)
- Tailscale OAuth client credentials

## Installation

### Option 1: Download Pre-built Binary

Download the latest FreeBSD binary from the [releases page](https://github.com/jtdowney/tsbridge/releases):

```bash
# For amd64
fetch https://github.com/jtdowney/tsbridge/releases/latest/download/tsbridge_VERSION_freebsd_amd64.tar.gz
tar -xzf tsbridge_VERSION_freebsd_amd64.tar.gz
install -m 755 tsbridge /usr/local/bin/

# For arm64
fetch https://github.com/jtdowney/tsbridge/releases/latest/download/tsbridge_VERSION_freebsd_arm64.tar.gz
tar -xzf tsbridge_VERSION_freebsd_arm64.tar.gz
install -m 755 tsbridge /usr/local/bin/
```

### Option 2: Build from Source

```bash
git clone https://github.com/jtdowney/tsbridge.git
cd tsbridge
make build
install -m 755 tsbridge /usr/local/bin/
```

## System Setup

### 1. Create User and Group

```bash
# Create tsbridge user and group
pw groupadd tsbridge
pw useradd tsbridge -g tsbridge -d /nonexistent -s /usr/sbin/nologin -c "tsbridge daemon"
```

### 2. Create Required Directories

```bash
# Configuration directory
install -d -o root -g wheel -m 755 /usr/local/etc/tsbridge

# Runtime directories (will be created by rc script, but can be pre-created)
install -d -o tsbridge -g tsbridge -m 750 /var/run/tsbridge
install -d -o tsbridge -g tsbridge -m 750 /var/log/tsbridge
install -d -o tsbridge -g tsbridge -m 750 /var/db/tsbridge
```

### 3. Install Configuration

Copy the example configuration and customize it:

```bash
cp /path/to/tsbridge/deployments/freebsd/config.example.toml /usr/local/etc/tsbridge/config.toml
```

Basic configuration example at `/usr/local/etc/tsbridge/config.toml`:

```toml
# Tailscale authentication (required)
[tailscale]
oauth_client_id_file = "/usr/local/etc/tsbridge/oauth_client_id"
oauth_client_secret_file = "/usr/local/etc/tsbridge/oauth_client_secret"
state_dir = "/var/db/tsbridge"
default_tags = ["tag:server", "tag:tsbridge"]

# Global defaults (optional)
[global]
access_log = true
metrics_addr = ":9100"
whois_timeout = "1s"

# Services (at least one required)
[[services]]
name = "wiki"
backend_addr = "127.0.0.1:8080"
whois_enabled = true
```

See `config.example.toml` for a complete configuration reference with all available options.

### 4. Configure OAuth Credentials

Store your Tailscale OAuth credentials securely:

```bash
# Save OAuth client ID
echo "YOUR_CLIENT_ID" > /usr/local/etc/tsbridge/oauth_client_id
chmod 600 /usr/local/etc/tsbridge/oauth_client_id
chown tsbridge:tsbridge /usr/local/etc/tsbridge/oauth_client_id

# Save OAuth client secret
echo "YOUR_CLIENT_SECRET" > /usr/local/etc/tsbridge/oauth_client_secret
chmod 600 /usr/local/etc/tsbridge/oauth_client_secret
chown tsbridge:tsbridge /usr/local/etc/tsbridge/oauth_client_secret
```

### 5. Install rc.d Script

```bash
# Copy the rc.d script
install -m 755 tsbridge /usr/local/etc/rc.d/tsbridge
```

## Service Management

### Enable the Service

Add to `/etc/rc.conf`:

```bash
# Basic configuration
tsbridge_enable="YES"

# Optional: Custom configuration file location
tsbridge_config="/usr/local/etc/tsbridge/config.toml"

# Optional: Run as different user/group
tsbridge_user="tsbridge"
tsbridge_group="tsbridge"

# Optional: Additional command line flags
tsbridge_flags=""

# Optional: Environment variables
tsbridge_env="GOMAXPROCS=4"

# Optional: Process limits (see limits(1))
tsbridge_limits="-n 65535"  # Max file descriptors
```

### Service Commands

```bash
# Start the service
service tsbridge start

# Stop the service
service tsbridge stop

# Check service status
service tsbridge status

# Reload configuration (if supported by tsbridge)
service tsbridge reload

# Enable service to start at boot
sysrc tsbridge_enable="YES"

# Disable service from starting at boot
sysrc tsbridge_enable="NO"
```

## Monitoring

### View Logs

```bash
# Service logs
tail -f /var/log/tsbridge/tsbridge.log

# Or use newsyslog for log rotation
```

### Configure Log Rotation

Install the provided newsyslog configuration:

```bash
# Copy the newsyslog configuration
install -m 644 tsbridge-newsyslog.conf /usr/local/etc/newsyslog.conf.d/tsbridge.conf

# Or if using /etc/newsyslog.conf directly, append the configuration:
cat tsbridge-newsyslog.conf >> /etc/newsyslog.conf
```

This will rotate logs daily at midnight, keeping 7 days of compressed logs.

### Check Service Status

```bash
# Check if service is running
service tsbridge status

# Check process
ps aux | grep tsbridge

# Check network listeners
sockstat -4 -6 | grep tsbridge
```

## Security Considerations

### File Permissions

Ensure proper permissions on sensitive files:

```bash
# Configuration files
chmod 644 /usr/local/etc/tsbridge/config.toml
chmod 600 /usr/local/etc/tsbridge/oauth_client_*

# Directories
chmod 750 /var/run/tsbridge
chmod 750 /var/log/tsbridge
chmod 750 /var/db/tsbridge
```

### Firewall Configuration

If using `pf`, add rules to allow tsbridge traffic:

```pf
# /etc/pf.conf
# Allow tsbridge metrics (if enabled)
pass in quick on $int_if proto tcp to port 9100

# Tailscale traffic is handled by tailscale itself
```

### Resource Limits

The rc.d script sets appropriate file descriptor limits by default. Additional limits can be configured in `/etc/rc.conf`:

```bash
# Example: Set custom limits
tsbridge_limits="-n 100000 -u 512"  # Max files: 100k, Max processes: 512
```

## Troubleshooting

### Service Won't Start

1. Check configuration syntax:

   ```bash
   /usr/local/bin/tsbridge -config /usr/local/etc/tsbridge/config.toml -validate
   ```

2. Check logs:

   ```bash
   tail -n 50 /var/log/tsbridge/tsbridge.log
   ```

3. Verify permissions:

   ```bash
   ls -la /usr/local/etc/tsbridge/
   ls -la /var/run/tsbridge/
   ls -la /var/log/tsbridge/
   ```

4. Run manually for debugging:
   ```bash
   su -m tsbridge -c '/usr/local/bin/tsbridge -config /usr/local/etc/tsbridge/config.toml -verbose'
   ```

### Common Issues

1. **OAuth Authentication Fails**
   - Verify OAuth credentials are correct
   - Check file permissions on credential files
   - Ensure tsbridge user can read the files

2. **Port Already in Use**
   - Check for conflicting services: `sockstat -4 -6 | grep :PORT`
   - Adjust service configuration to use different ports

3. **File Descriptor Limits**
   - Increase limits in rc.conf: `tsbridge_limits="-n 100000"`
   - Check system-wide limits: `sysctl kern.maxfiles`

## Jail Deployment

To run tsbridge in a FreeBSD jail:

1. Create jail with network access
2. Install tsbridge inside the jail
3. Configure as normal, but ensure:
   - Tailscale connectivity works from within the jail
   - Backend services are accessible from the jail network

Example jail configuration in `/etc/jail.conf`:

```
tsbridge {
    host.hostname = tsbridge.local;
    ip4.addr = "lo0|10.0.0.10/32";
    ip6 = "new";
    exec.start = "/bin/sh /etc/rc";
    exec.stop = "/bin/sh /etc/rc.shutdown";
    mount.devfs;
    persist;
}
```

## Maintenance

### Updating tsbridge

1. Download new binary
2. Stop service: `service tsbridge stop`
3. Replace binary: `install -m 755 tsbridge /usr/local/bin/`
4. Start service: `service tsbridge start`

### Backup

Important files to backup:

- `/usr/local/etc/tsbridge/` (configuration and credentials)
- `/var/db/tsbridge/` (if persistent state is stored)
