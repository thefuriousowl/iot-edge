# IoT Edge

IoT Edge is an industrial edge-computing platform for collecting and managing data from field devices. It is designed to run on Raspberry Pi CM4/CM5-class hardware.

> **Release v0.2.0 RC** adds Asset hierarchy and semantic measurement contracts, fixed Energy and Utilities dashboards, workload/fault/security qualification, and native Windows release-candidate artifacts to the v0.1 acquisition, logging, reporting, credential, and publishing foundation.

## Stack

- Go + Fiber + GORM
- PostgreSQL
- React + TypeScript + Vite
- pnpm + Tailwind CSS

## Development

Requirements: Go 1.26, Node.js 24, pnpm 10, Docker, and Docker Compose.

```bash
# PostgreSQL
cp infra/.env.docker.example infra/.env.docker
docker compose -f infra/docker-compose.yaml up -d

# Backend
cp backend/.env.example backend/.env
cd backend && go run ./cmd/server

# Frontend (in another terminal)
cd frontend && pnpm install && pnpm dev
```

Update the copied environment files before starting the services. Local `.env` files are ignored by Git.

## Production

Production uses ARM64-capable multi-stage images, nginx, checksum-tracked PostgreSQL migrations, health-gated Compose startup, password recovery, and backup/restore utilities. See [DEPLOYMENT.md](DEPLOYMENT.md).

The pre-installer Windows RC is built natively with `backend/scripts/build-release-candidate-windows.ps1`. It embeds production frontend assets in the Windows/amd64 server and bundles migration, password-reset, backup/restore, Windows Service control and DPAPI configuration binaries plus a manifest and SHA-256 checksums. Service installation uses the non-administrator LocalService account and must be performed from an elevated terminal; the RC is not yet an installer.

Installed Windows maintenance commands load the DPAPI-protected ProgramData configuration. `iot-edge-migrate.exe --native` and `iot-edge-reset-password.exe --native --username <name>` refuse system, superuser and non-owner database targets. Backup and restore require absolute paths beneath the IoT Edge backup directory and explicit PostgreSQL tool paths; backups use PostgreSQL custom format with SHA-256 metadata, while restore requires the exact `RESTORE <database>` confirmation and retains a pre-restore rollback backup.

## Tests

```bash
cd backend && go test ./...
cd frontend && pnpm test && pnpm lint && pnpm build
```
## License

[MIT](LICENSE)
