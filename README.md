# IoT Edge

IoT Edge is an industrial edge-computing platform for collecting and managing data from field devices. It is designed to run on Raspberry Pi CM4/CM5-class hardware.

> **Release v0.1 is under active development.** The backend authentication flow and responsive Login page are complete. Gateway, device, tag, acquisition, and plugin features are being built next.

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

## Tests

```bash
cd backend && go test ./...
cd frontend && pnpm test && pnpm lint && pnpm build
```
## License

[MIT](LICENSE)
