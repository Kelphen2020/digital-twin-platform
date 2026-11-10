#!/usr/bin/env bash
# Restore the PostgreSQL store from a custom-format dump (destructive).
#   ./scripts/restore.sh <backup.dump>
# Env: PG_CONTAINER (dtp-postgres), PG_USER (twin), PG_DB (twin)
set -euo pipefail

FILE="${1:?usage: scripts/restore.sh <backup.dump>}"
CONTAINER="${PG_CONTAINER:-dtp-postgres}"
USER="${PG_USER:-twin}"
DB="${PG_DB:-twin}"

[ -f "$FILE" ] || { echo "restore: $FILE not found" >&2; exit 1; }
if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  echo "restore: container $CONTAINER is not running" >&2
  exit 1
fi
echo "restore: dropping and restoring $DB from $FILE"
docker exec -i "$CONTAINER" pg_restore -U "$USER" -d "$DB" --clean --if-exists --no-owner <"$FILE"
echo "restore: done"
