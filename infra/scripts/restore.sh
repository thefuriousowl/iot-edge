#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
infra_dir=$(dirname "$script_dir")
env_file=${1:-"$infra_dir/production.env"}
backup_file=${2:-}

if [ ! -f "$env_file" ]; then
    printf 'Environment file not found: %s\n' "$env_file" >&2
    exit 1
fi
if [ -z "$backup_file" ] || [ ! -f "$backup_file" ]; then
    printf 'Usage: IOT_EDGE_RESTORE_CONFIRM=restore %s ENV_FILE BACKUP.dump\n' "$0" >&2
    exit 1
fi
if [ "${IOT_EDGE_RESTORE_CONFIRM:-}" != "restore" ]; then
    printf 'Restore refused: set IOT_EDGE_RESTORE_CONFIRM=restore\n' >&2
    exit 1
fi
if [ -f "$backup_file.sha256" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$(dirname "$backup_file")" && sha256sum -c "$(basename "$backup_file").sha256")
    else
        (cd "$(dirname "$backup_file")" && shasum -a 256 -c "$(basename "$backup_file").sha256")
    fi
fi

set -a
. "$env_file"
set +a
: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
POSTGRES_PORT=${POSTGRES_PORT:-5432}

compose() {
    docker compose --env-file "$env_file" -p iot-edge -f "$infra_dir/docker-compose.production.yaml" "$@"
}

compose stop frontend backend
compose exec -T postgres psql \
    --host 127.0.0.1 \
    --port "$POSTGRES_PORT" \
    --username "$POSTGRES_USER" \
    --dbname "$POSTGRES_DB" \
    --set ON_ERROR_STOP=1 \
    --command 'DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public AUTHORIZATION CURRENT_USER; GRANT ALL ON SCHEMA public TO PUBLIC;'
compose exec -T postgres pg_restore \
    --host 127.0.0.1 \
    --port "$POSTGRES_PORT" \
    --username "$POSTGRES_USER" \
    --dbname "$POSTGRES_DB" \
    --no-owner \
    --no-privileges \
    --exit-on-error < "$backup_file"
compose run --rm migrate
compose up -d backend frontend
printf 'Restore completed from %s\n' "$backup_file"
