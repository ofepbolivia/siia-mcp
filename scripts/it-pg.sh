#!/usr/bin/env bash
# Run the PostgreSQL integration suite (SDD §20.3) against an ephemeral
# container, then tear it down. Infra is Docker or Podman.
#
# Usage:
#   scripts/it-pg.sh [PG_VERSION]      # e.g. 15, 16, 17, 18 (default: 16)
#
# Environment overrides (optional):
#   SIIASQL_PG_IMAGE    container image (default postgres:<version>-alpine)
#   SIIASQL_IT_PORT     host port to bind (default 55432)
#   KEEP_CONTAINER=1    keep the container after the run (debugging)
set -euo pipefail

PG_VERSION="${1:-${SIIASQL_PG_VERSION:-16}}"
PORT="${SIIASQL_IT_PORT:-55432}"
IMAGE="${SIIASQL_PG_IMAGE:-postgres:${PG_VERSION}-alpine}"
NAME="siiasql-it-${PG_VERSION}"
CONNECTOR="$(command -v docker || command -v podman || true)"
if [ -z "${CONNECTOR}" ]; then
    echo "error: neither docker nor podman found" >&2
    exit 1
fi

DB_USER=ituser
DB_PASS=itpass
DB_NAME=itdb

# Bind to loopback with an ephemeral-ish but stable port. If the port is taken
# (e.g. a stale container), pick a free one via the kernel.
FIXTURE="$(cd "$(dirname "$0")" && pwd)/fixtures/it.sql"

cleanup() {
    if [ "${KEEP_CONTAINER:-0}" = "1" ]; then
        echo "keeping container ${NAME} on port ${PORT}"
        return
    fi
    "${CONNECTOR}" rm -f "${NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Remove any stale container with our name, then start fresh.
"${CONNECTOR}" rm -f "${NAME}" >/dev/null 2>&1 || true
echo "starting ${IMAGE} as ${NAME} on 127.0.0.1:${PORT}"
"${CONNECTOR}" run -d \
    --name "${NAME}" \
    -p "127.0.0.1:${PORT}:5432" \
    -e POSTGRES_USER="${DB_USER}" \
    -e POSTGRES_PASSWORD="${DB_PASS}" \
    -e POSTGRES_DB="${DB_NAME}" \
    "${IMAGE}" >/dev/null

# Probe TCP so initdb's temporary socket-only server is not mistaken for the final server.
echo "waiting for PostgreSQL readiness..."
for i in $(seq 1 60); do
    if "${CONNECTOR}" exec "${NAME}" pg_isready -h 127.0.0.1 -U "${DB_USER}" -d "${DB_NAME}" >/dev/null 2>&1; then
        break
    fi
    if [ "$i" = "60" ]; then
        echo "error: PostgreSQL did not become ready" >&2
        "${CONNECTOR}" logs "${NAME}" >&2 || true
        exit 1
    fi
    sleep 1
done

# Seed the fixture.
echo "seeding fixture..."
"${CONNECTOR}" exec -i "${NAME}" \
    sh -c "psql -U \"${DB_USER}\" -d \"${DB_NAME}\" -v ON_ERROR_STOP=1" < "${FIXTURE}"

# Run the integration suite against this container.
echo "running integration suite against PostgreSQL ${PG_VERSION}..."
SIIASQL_IT_HOST=127.0.0.1 \
SIIASQL_IT_PORT="${PORT}" \
SIIASQL_IT_USER="${DB_USER}" \
SIIASQL_IT_PASSWORD="${DB_PASS}" \
SIIASQL_IT_DB="${DB_NAME}" \
go test -tags=integration -count=1 ./internal/postgres/ ./internal/manager/
