#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
infra_dir=$(dirname "$script_dir")
env_file=${1:-"$infra_dir/production.env"}
backup_dir=${2:-"$infra_dir/backups"}

if [ ! -f "$env_file" ]; then
    printf 'Environment file not found: %s\n' "$env_file" >&2
    exit 1
fi

set -a
. "$env_file"
set +a
: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
POSTGRES_PORT=${POSTGRES_PORT:-5432}

mkdir -p "$backup_dir"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_file="$backup_dir/iot-edge-${timestamp}.dump"
partial_file="$backup_file.partial"

if ! docker compose --env-file "$env_file" -p iot-edge -f "$infra_dir/docker-compose.production.yaml" \
    exec -T postgres pg_dump \
        --host 127.0.0.1 \
        --port "$POSTGRES_PORT" \
        --username "$POSTGRES_USER" \
        --dbname "$POSTGRES_DB" \
        --format custom \
        --no-owner \
        --no-privileges > "$partial_file"; then
    rm -f "$partial_file"
    exit 1
fi
mv "$partial_file" "$backup_file"

if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$backup_file" > "$backup_file.sha256"
else
    shasum -a 256 "$backup_file" > "$backup_file.sha256"
fi
printf 'Backup written to %s\n' "$backup_file"
