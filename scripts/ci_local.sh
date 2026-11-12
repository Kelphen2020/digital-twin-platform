#!/usr/bin/env bash
# Local mirror of the CI pipeline (.github/workflows/ci.yml).
# Runs every stage the CI job runs, in the same order, and prints a summary.
#
#   ./scripts/ci_local.sh            # all stages
#   ./scripts/ci_local.sh --quick    # skip the PostgreSQL stage
#
# Requires: go, pnpm, python3, docker (PG stage only).
set -uo pipefail
cd "$(dirname "$0")/.."

GO="${GO:-$(command -v go || echo "$PWD/../.toolchains/go/bin/go")}"
export PATH="$(dirname "$GO"):$PATH"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-off}"

# Unit-only coverage gate (the PostgreSQL contract tests add ~11 points but need
# Docker; the PG stage enforces its own thresholds).
COVERAGE_GATE="${COVERAGE_GATE:-62}"
# The PG stage runs every test against a live database, so it holds a higher bar.
PG_COVERAGE_GATE="${PG_COVERAGE_GATE:-72}"
QUICK=0
[ "${1:-}" = "--quick" ] && QUICK=1

# Random high ports keep the CI instances away from other local services that
# may squat on a fixed port (a foreign server answering /api/health would
# otherwise make the smoke test meaningless).
MEMORY_PORT="${MEMORY_PORT:-$((20000 + RANDOM % 20000))}"
PG_PORT="${PG_PORT:-$((40000 + RANDOM % 20000))}"

STAGES=()
RESULTS=()
FAILED=0

run_stage() {
  local name="$1"; shift
  printf '\n== %s ==\n' "$name"
  if "$@"; then
    RESULTS+=("PASS  $name")
  else
    RESULTS+=("FAIL  $name")
    FAILED=1
  fi
}

stage_api_build()     { (cd services/api && "$GO" build ./...); }
stage_api_vet()       { (cd services/api && "$GO" vet ./...); }
stage_api_test() {
  local profile
  profile="$(mktemp)"
  (cd services/api && "$GO" test ./... -count=1 -coverprofile="$profile") || { rm -f "$profile"; return 1; }
  local total
  total="$(cd services/api && "$GO" tool cover -func="$profile" | tail -1 | awk '{print $3}' | tr -d '%')"
  rm -f "$profile"
  echo "coverage: ${total:-unknown}% (gate ${COVERAGE_GATE}%)"
  [ -n "$total" ] || return 1
  awk -v t="$total" -v g="$COVERAGE_GATE" 'BEGIN { if (t+0 < g) { printf "coverage %s%% < %s%% gate\n", t, g; exit 1 } }'
}
stage_lint() {
  export PATH="$(dirname "$GO"):$HOME/go/bin:$PATH"
  (cd services/api && golangci-lint run ./...) || return 1
  (cd apps/web && pnpm lint)
}
stage_simulator_lint() {
  python3 -m py_compile services/simulator/*.py scripts/*.py
  bash -n scripts/smoke.sh scripts/backup.sh scripts/restore.sh scripts/ci_local.sh
}
stage_web() {
  (cd apps/web && pnpm install --frozen-lockfile --cache ../../.npm-cache >/dev/null 2>&1 || pnpm install --frozen-lockfile >/dev/null
   pnpm typecheck && pnpm build)
}

# api_base returns the loopback address that actually serves our API, since
# another process (e.g. docker-proxy) may already own 127.0.0.1:<port>.
api_base() {
  local port="$1"
  if curl -sf "http://127.0.0.1:$port/api/health" 2>/dev/null | grep -q '"status":"ok"'; then
    echo "http://127.0.0.1:$port"
  else
    echo "http://[::1]:$port"
  fi
}

start_api() {
  # start_api <port> <logfile> [extra env pairs...]
  local port="$1" log="$2"; shift 2
  (cd services/api && env PORT="$port" AUTH_MODE=on JWT_SECRET=ci-secret "$@" ./bin/api >"$log" 2>&1) &
  API_PIDS+=($!)
  for attempt in $(seq 1 60); do
    payload="$(curl -sf "http://127.0.0.1:$port/api/health" 2>/dev/null || curl -sf "http://[::1]:$port/api/health" 2>/dev/null || true)"
    if printf '%s' "$payload" | grep -q '"status":"ok"'; then
      return 0
    fi
    # Surface the first few failures so a broken health probe is visible.
    if [ "$attempt" -le 3 ]; then
      probe="$(curl -s -o /dev/null -w '%{http_code}' "http://[::1]:$port/api/health" 2>/dev/null)" || probe="000"
      echo "health[$port] attempt $attempt: code=${probe} body=$(printf '%s' "$payload" | head -c 120)" >&2
    fi
    sleep 1
  done
  echo "api on :$port did not become healthy; log:" >&2
  cat "$log" 2>/dev/null >&2 || echo "(no log)" >&2
  echo "listeners on :$port:" >&2
  lsof -nP -iTCP:"$port" -sTCP:LISTEN >&2 2>/dev/null || echo "(none)" >&2
  return 1
}

stop_api() {
  local port="$1"
  lsof -ti "tcp:$port" 2>/dev/null | xargs -r kill 2>/dev/null || true
  lsof -ti "tcp:$port" 2>/dev/null | xargs -r kill -9 2>/dev/null || true
}

API_PIDS=()
cleanup_apis() {
  for pid in "${API_PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  stop_api "$MEMORY_PORT"
  stop_api "$PG_PORT"
}
trap cleanup_apis EXIT

stage_memory_smoke() {
  local port="$MEMORY_PORT" log
  log="$(mktemp)"
  (cd services/api && "$GO" build -o bin/api ./cmd/server) || return 1
  start_api "$port" "$log" BUS_DRIVER=memory || return 1
  local smoke_log
  smoke_log="$(mktemp)"
  API_BASE="$(api_base "$port")" bash scripts/smoke.sh >"$smoke_log" 2>&1
  local code=$?
  tail -5 "$smoke_log"
  if [ "$code" -ne 0 ]; then
    echo "--- failing checks (memory smoke) ---" >&2
    grep -E '^FAIL' "$smoke_log" | head -20 >&2
  fi
  cp "$smoke_log" /tmp/ci-smoke-memory.log 2>/dev/null || true
  rm -f "$smoke_log"
  stop_api "$port"
  [ "$code" -eq 0 ] || tail -30 "$log" >&2
  return $code
}

stage_pg_smoke() {
  command -v docker >/dev/null 2>&1 || { echo "docker not available; skipping PG stage" >&2; return 0; }
  local name="dtp-ci-postgres" port="$PG_PORT" image="${POSTGIS_IMAGE:-docker.m.daocloud.io/postgis/postgis:16-3.4}"
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker run -d --name "$name" -e POSTGRES_USER=twin -e POSTGRES_PASSWORD=twin -e POSTGRES_DB=twin \
    -p "127.0.0.1:$port:5432" "$image" >/dev/null || return 1
  local ready=0
  for _ in $(seq 1 90); do
    # pg_isready can pass while the server is still finishing startup recovery;
    # require an actual SQL round-trip before handing the DB to the tests.
    if docker exec "$name" psql -U twin -d twin -tAc 'SELECT 1' >/dev/null 2>&1; then
      ready=1; break
    fi
    sleep 2
  done
  if [ "$ready" -ne 1 ]; then echo "postgres did not become ready" >&2; docker rm -f "$name" >/dev/null 2>&1; return 1; fi
  # pg is accepting connections, but the socket can still reset while the server
  # finishes recovery; wait for a real connection from the host too.
  local host_ready=0
  for _ in $(seq 1 30); do
    if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then host_ready=1; exec 3>&- 3<&-; break; fi
    sleep 1
  done
  if [ "$host_ready" -ne 1 ]; then echo "postgres port not reachable from host" >&2; return 1; fi
  sleep 3
  local dburl="postgres://twin:twin@127.0.0.1:$port/twin?sslmode=disable" code=0 log
  log="$(mktemp)"
  echo "--- store contract tests (PG_TEST_URL) ---"
  (cd services/api && PG_TEST_URL="$dburl" "$GO" test ./internal/store/ -run TestPostgres -v) || { echo "store contract test FAILED" >&2; code=1; }
  echo "--- PostgreSQL-inclusive coverage ---"
  local pg_profile pg_total
  pg_profile="$(mktemp)"
  (cd services/api && PG_TEST_URL="$dburl" "$GO" test ./... -count=1 -coverprofile="$pg_profile") || code=1
  pg_total="$(cd services/api && "$GO" tool cover -func="$pg_profile" | tail -1 | awk '{print $3}' | tr -d '%')"
  rm -f "$pg_profile"
  echo "coverage with PostgreSQL: ${pg_total:-unknown}% (gate ${PG_COVERAGE_GATE}%)"
  if [ -n "$pg_total" ]; then
    awk -v t="$pg_total" -v g="$PG_COVERAGE_GATE" 'BEGIN { if (t+0 < g) { printf "PG coverage %s%% < %s%% gate\n", t, g; exit 1 } }' || code=1
  else
    code=1
  fi
  echo "--- pg api health probe ---"
  for _ in $(seq 1 10); do
    printf '%s ' "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/health" 2>/dev/null || echo 000)"
    sleep 1
  done
  echo
  if start_api "$PG_PORT" "$log" DATABASE_URL="$dburl"; then
    local pg_smoke
    pg_smoke="$(mktemp)"
    API_BASE="$(api_base "$PG_PORT")" bash scripts/smoke.sh >"$pg_smoke" 2>&1 || code=1
    tail -5 "$pg_smoke"
    [ "$code" -eq 0 ] || { echo "--- failing checks (postgres smoke) ---" >&2; grep -E '^FAIL' "$pg_smoke" | head -20 >&2; }
    cp "$pg_smoke" /tmp/ci-smoke-postgres.log 2>/dev/null || true
    rm -f "$pg_smoke"
    stop_api "$PG_PORT"
    [ "$code" -eq 0 ] || tail -30 "$log" >&2
  else
    echo "postgres API did not become healthy" >&2
    tail -20 "$log" >&2 || true
    code=1
  fi
  docker rm -f "$name" >/dev/null 2>&1 || true
  return $code
}

run_stage "api build"        stage_api_build
run_stage "api vet"          stage_api_vet
run_stage "api test"         stage_api_test
run_stage "simulator lint"   stage_simulator_lint
run_stage "lint (go+web)"    stage_lint
run_stage "web typecheck+build" stage_web
run_stage "smoke (memory)"   stage_memory_smoke
if [ "$QUICK" -eq 0 ]; then
  run_stage "smoke (postgres)" stage_pg_smoke
else
  RESULTS+=("SKIP  smoke (postgres)")
fi

printf '\n================ CI SUMMARY ================\n'
for row in "${RESULTS[@]}"; do printf '%s\n' "$row"; done
printf '============================================\n'
exit "$FAILED"
