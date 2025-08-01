# Production Deployment Guide

This guide explains how to deploy LaunchBot in a production environment with proper data management.

## Data Management

LaunchBot now supports configurable paths for the database and configuration files, making it safer for production deployments where you want to keep data separate from the application directory.

### Configuration Options

You can specify custom paths using either:
1. Command-line flags
2. Environment variables

### Command-Line Flags

```bash
./launchbot --config /path/to/config.json --data /path/to/data/directory
```

- `--config`: Path to the configuration file (defaults to `$LAUNCHBOT_CONFIG` or `./data/config.json`)
- `--data`: Path to the data directory for database and logs (defaults to `$LAUNCHBOT_DATA_DIR` or `./data`)

### Environment Variables

```bash
export LAUNCHBOT_CONFIG=/etc/launchbot/config.json
export LAUNCHBOT_DATA_DIR=/var/lib/launchbot
./launchbot
```

### Recommended Production Setup

#### Linux System Service

1. Create a dedicated user for the bot:
```bash
sudo useradd -r -s /bin/false launchbot
```

2. Create data directories:
```bash
sudo mkdir -p /var/lib/launchbot
sudo mkdir -p /etc/launchbot
sudo chown launchbot:launchbot /var/lib/launchbot
sudo chown launchbot:launchbot /etc/launchbot
```

3. Copy the bot binary:
```bash
sudo cp launchbot /usr/local/bin/
sudo chmod +x /usr/local/bin/launchbot
```

4. Create a systemd service file `/etc/systemd/system/launchbot.service`:
```ini
[Unit]
Description=LaunchBot Telegram Bot
After=network.target

[Service]
Type=simple
User=launchbot
Group=launchbot
ExecStart=/usr/local/bin/launchbot
Restart=on-failure
RestartSec=10
Environment="LAUNCHBOT_CONFIG=/etc/launchbot/config.json"
Environment="LAUNCHBOT_DATA_DIR=/var/lib/launchbot"

[Install]
WantedBy=multi-user.target
```

5. Enable and start the service:
```bash
sudo systemctl daemon-reload
sudo systemctl enable launchbot
sudo systemctl start launchbot
```

#### Docker Deployment

Create a `docker-compose.yml`:
```yaml
version: '3.8'

services:
  launchbot:
    image: launchbot:latest
    container_name: launchbot
    restart: unless-stopped
    environment:
      - LAUNCHBOT_CONFIG=/config/config.json
      - LAUNCHBOT_DATA_DIR=/data
    volumes:
      - ./config:/config
      - ./data:/data
    command: ["/app/launchbot"]
```

### Data Migration

If you have existing data in the default `./data` directory and want to move to a new location, LaunchBot will automatically detect this and offer to migrate your data.

When you run the bot with a new data path for the first time:
```bash
./launchbot --data /var/lib/launchbot
```

If existing data is found in `./data`, you'll be prompted:
```
Found existing data in ./data
Would you like to migrate it to /var/lib/launchbot? (y/n):
```

### Backup Strategy

#### Automated Backups

Create a backup script `/usr/local/bin/launchbot-backup.sh`:
```bash
#!/bin/bash
BACKUP_DIR="/backup/launchbot"
DATA_DIR="/var/lib/launchbot"
DATE=$(date +%Y%m%d_%H%M%S)

mkdir -p "$BACKUP_DIR"

# Stop the bot to ensure database consistency
systemctl stop launchbot

# Create backup
cp "$DATA_DIR/launchbot.db" "$BACKUP_DIR/launchbot_$DATE.db"
cp "/etc/launchbot/config.json" "$BACKUP_DIR/config_$DATE.json"

# Start the bot again
systemctl start launchbot

# Keep only last 7 days of backups
find "$BACKUP_DIR" -name "launchbot_*.db" -mtime +7 -delete
find "$BACKUP_DIR" -name "config_*.json" -mtime +7 -delete
```

Add to crontab for daily backups:
```bash
0 3 * * * /usr/local/bin/launchbot-backup.sh
```

### Security Considerations

1. **File Permissions**: Ensure only the launchbot user can read the config file (contains bot token):
   ```bash
   chmod 600 /etc/launchbot/config.json
   ```

2. **Directory Permissions**: Restrict access to data directory:
   ```bash
   chmod 700 /var/lib/launchbot
   ```

3. **Token Security**: Never commit the config.json file to version control.

### Monitoring

Monitor the bot's health by checking:
- Service status: `systemctl status launchbot`
- Logs: `journalctl -u launchbot -f`
- Database size: `du -h /var/lib/launchbot/launchbot.db`

### Troubleshooting

1. **Permission Errors**: Ensure the launchbot user owns all data files
2. **Path Issues**: Use absolute paths in production
3. **Migration Failed**: Check disk space and file permissions

### Development vs Production

| Setting | Development | Production |
|---------|------------|------------|
| Config Path | `./data/config.json` | `/etc/launchbot/config.json` |
| Database Path | `./data/launchbot.db` | `/var/lib/launchbot/launchbot.db` |
| Log Path | `./data/launchbot-logs.log` | `/var/lib/launchbot/launchbot-logs.log` |
| User | Current user | Dedicated `launchbot` user |
| Service Management | Manual | systemd/Docker |