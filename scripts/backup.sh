#!/usr/bin/env bash
# Backup the PostgreSQL store to a custom-format dump.
#   ./scripts/backup.sh [output.dump]
# Env: PG_CONTAINER (default dtp-postgres), PG_USER (twin), PG_DB (twin)
set -euo pipefail

CONTAINER="${PG_CONTAINER:-dtp-postgres}"
USER="${PG_USER:-twin}"
DB="${PG_DB:-twin}"
OUT="${1:-backups/twin-$(date +%Y%m%d-%H%M%S).dump}"

mkdir -p "$(dirname "$OUT")"
if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  echo "backup: container $CONTAINER is not running" >&2
  exit 1
fi
docker exec "$CONTAINER" pg_dump -U "$USER" -d "$DB" -Fc >"$OUT"
SIZE="$(du -h "$OUT" | cut -f1)"
echo "backup: wrote $OUT ($SIZE)"
