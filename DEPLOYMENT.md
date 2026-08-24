# IoT Edge v0.1 Deployment

This guide deploys IoT Edge on 64-bit Raspberry Pi OS or another ARM64 Linux host. The production stack uses host networking so Modbus TCP can reach field devices and HTTP Server Publishers can bind user-selected ports without maintaining a fixed Docker port range. PostgreSQL listens only on `127.0.0.1`.

## Requirements

- Raspberry Pi CM4/CM5 with a 64-bit operating system
- Docker Engine 27 or newer with Docker Compose v2
- At least 2 GB RAM and persistent storage with enough capacity for Logger retention
- Correct system time and timezone synchronization
- `openssl` plus `sha256sum` or `shasum` for secret generation and backup verification

Host networking is designed for Linux. Docker Desktop on macOS or Windows may show healthy containers without exposing their host-network ports to the desktop host unless host networking is enabled.

## Configure

Create the ignored production environment file and restrict its permissions:

```bash
cp infra/production.env.example infra/production.env
chmod 600 infra/production.env
```

Generate independent random values and place them in `infra/production.env`:

```bash
openssl rand -base64 48
openssl rand -base64 36
openssl rand -base64 32
```

Use the first value for `JWT_SECRET`, the second for `POSTGRES_PASSWORD`, and the third for `PUBLISHER_MASTER_KEY_BASE64`. Do not reuse or commit these values. Preserve the Publisher master key outside the device backup; encrypted Credential material cannot be recovered without it.

Set these deployment-specific values:

- `IOT_EDGE_HTTP_PORT`: browser-facing HTTP port, normally `80`
- `IOT_EDGE_BACKEND_PORT`: internal API and health port, normally `8080`
- `POSTGRES_PORT`: loopback-only PostgreSQL port, normally `5432`
- `CORS_ALLOW_ORIGINS`: exact browser origins separated by commas
- `COOKIE_SECURE`: `false` for isolated HTTP LAN operation; `true` when HTTPS terminates in front of IoT Edge
- `IOT_EDGE_BACKEND_IMAGE` and `IOT_EDGE_FRONTEND_IMAGE`: local names or registry paths
- `IOT_EDGE_VERSION`: immutable release tag, such as `0.1.0`

`PUBLISHER_MASTER_KEY_ID` and `PUBLISHER_MASTER_KEY_BASE64` must either both be present or both be blank. Leaving both blank disables Credential storage and MQTT/HTTP Publisher runtimes.

## Build ARM64 Images

Build directly on the Raspberry Pi:

```bash
docker compose \
  --env-file infra/production.env \
  -p iot-edge \
  -f infra/docker-compose.production.yaml \
  build --pull
```

To build explicitly from another buildx host:

```bash
docker buildx build --platform linux/arm64 \
  -t iot-edge-backend:0.1.0 --load backend
docker buildx build --platform linux/arm64 \
  -t iot-edge-frontend:0.1.0 --load frontend
```

For a multi-architecture registry release, replace the image names with registry paths and use `--platform linux/amd64,linux/arm64 --push`.

## Start

```bash
docker compose \
  --env-file infra/production.env \
  -p iot-edge \
  -f infra/docker-compose.production.yaml \
  up -d
```

Startup order is PostgreSQL health, checksum-tracked migrations, backend health, then nginx. The migration job uses a PostgreSQL advisory lock and is safe to rerun. Never edit an applied migration; add a new numbered migration instead.

Verify the deployment:

```bash
docker compose --env-file infra/production.env -p iot-edge \
  -f infra/docker-compose.production.yaml ps
curl --fail http://127.0.0.1/api/health
```

Open the device address in a browser and complete the one-time owner setup. If `IOT_EDGE_HTTP_PORT` is not `80`, include it in the URL.

## HTTPS

The included nginx serves HTTP and proxies same-origin `/api` requests, including SSE with buffering disabled. For any network beyond an isolated trusted LAN, terminate HTTPS in a host reverse proxy or load balancer:

1. Run IoT Edge HTTP on a loopback or firewall-restricted port.
2. Proxy HTTPS traffic to `http://127.0.0.1:<IOT_EDGE_HTTP_PORT>`.
3. Preserve `Host`, `X-Forwarded-For`, and `X-Forwarded-Proto` headers.
4. Disable response buffering and use a long read timeout for `/api/` SSE routes.
5. Set `COOKIE_SECURE=true` and set `CORS_ALLOW_ORIGINS` to the exact `https://` origin.

Do not expose PostgreSQL or the backend API port directly to an untrusted network. Allow only the UI/TLS port and intentional HTTP Publisher ports through the firewall.

## Operations

View status and logs:

```bash
docker compose --env-file infra/production.env -p iot-edge \
  -f infra/docker-compose.production.yaml ps
docker compose --env-file infra/production.env -p iot-edge \
  -f infra/docker-compose.production.yaml logs -f --tail=200
```

Restart application services without restarting PostgreSQL:

```bash
docker compose --env-file infra/production.env -p iot-edge \
  -f infra/docker-compose.production.yaml restart backend frontend
```

Reset an owner password without placing it in shell history:

```bash
read -r -s new_password
printf '\n'
printf '%s\n' "$new_password" | docker compose \
  --env-file infra/production.env -p iot-edge \
  -f infra/docker-compose.production.yaml \
  run --rm -T password-reset --username iot-admin
unset new_password
```

The reset enforces the normal password policy and history, clears lockout state, and invalidates existing sessions.

## Backup and Restore

Create a checksum-protected PostgreSQL custom-format backup:

```bash
infra/scripts/backup.sh infra/production.env /secure/backup/location
```

Copy both `.dump` and `.dump.sha256` files off the device. Back up `infra/production.env` separately in a protected secret store.

Restore stops the application, verifies the checksum when present, replaces the application `public` schema, reapplies newer migrations, and restarts the application:

```bash
IOT_EDGE_RESTORE_CONFIRM=restore \
  infra/scripts/restore.sh infra/production.env /secure/backup/location/iot-edge-TIMESTAMP.dump
```

Run restore only during a maintenance window. If restore fails, inspect PostgreSQL logs and keep backend/frontend stopped until the database is verified.

## Upgrade and Rollback

Before every upgrade:

1. Run the backup script and copy the backup plus production secrets off-device.
2. Pull or build the new immutable image tag.
3. Change only `IOT_EDGE_VERSION` in `infra/production.env`.
4. Run `docker compose ... up -d` and verify migration, backend, and frontend health.

Application rollback is safe only when the database schema is compatible. If an upgrade applied a new migration, restore the pre-upgrade database backup before starting the previous image tag. Do not run `.down.sql` files manually on production data.

## Environment Reference

| Variable | Required | Default | Purpose |
|---|---:|---|---|
| `IOT_EDGE_VERSION` | No | `0.1.0` | Backend/frontend image tag |
| `IOT_EDGE_BACKEND_IMAGE` | No | `iot-edge-backend` | Backend image or registry path |
| `IOT_EDGE_FRONTEND_IMAGE` | No | `iot-edge-frontend` | Frontend image or registry path |
| `IOT_EDGE_HTTP_PORT` | No | `80` | nginx UI/API listener |
| `IOT_EDGE_BACKEND_PORT` | No | `8080` | Go API listener |
| `POSTGRES_USER` | Yes | — | PostgreSQL owner |
| `POSTGRES_PASSWORD` | Yes | — | PostgreSQL password |
| `POSTGRES_DB` | Yes | — | PostgreSQL database |
| `POSTGRES_PORT` | No | `5432` | Loopback PostgreSQL listener |
| `JWT_SECRET` | Yes | — | Access/refresh token signing secret, minimum 32 bytes |
| `JWT_ACCESS_EXPIRY` | No | `15m` | Access-token lifetime |
| `JWT_REFRESH_EXPIRY` | No | `168h` | Refresh-session lifetime |
| `LOG_LEVEL` | No | `info` | Backend log level |
| `CORS_ALLOW_ORIGINS` | No | `http://localhost` | Exact comma-separated browser origins |
| `COOKIE_SECURE` | No | `false` | Require HTTPS for refresh cookies |
| `INTERNET_CHECK_ADDRESS` | No | `1.1.1.1:443` | Connectivity probe target |
| `INTERNET_CHECK_TIMEOUT` | No | `2s` | Connectivity probe timeout |
| `PUBLISHER_MASTER_KEY_ID` | No | blank | Publisher encryption key identifier |
| `PUBLISHER_MASTER_KEY_BASE64` | No | blank | Base64 encoding of exactly 32 random bytes |
