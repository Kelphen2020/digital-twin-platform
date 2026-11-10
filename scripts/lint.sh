#!/usr/bin/env bash
# Lint the API (golangci-lint) and the web client (eslint + typecheck).
#   ./scripts/lint.sh            # all
#   ./scripts/lint.sh api        # Go only
#   ./scripts/lint.sh web        # web only
# Requires golangci-lint on PATH or at $GOBIN (~/go/bin).
set -uo pipefail
cd "$(dirname "$0")/.."

GO="${GO:-$(command -v go || echo "$PWD/../.toolchains/go/bin/go")}"
export PATH="$(dirname "$GO"):${HOME}/go/bin:$PATH"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-off}"

targets="${1:-all}"
status=0

lint_api() {
  if ! command -v golangci-lint >/dev/null 2>&1; then
    echo "lint: golangci-lint not found; install with:" >&2
    echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2" >&2
    return 1
  fi
  (cd services/api && golangci-lint run ./...)
}

lint_web() {
  (cd apps/web && pnpm typecheck && pnpm lint)
}

case "$targets" in
  all) lint_api || status=1; lint_web || status=1 ;;
  api) lint_api || status=1 ;;
  web) lint_web || status=1 ;;
  *) echo "usage: scripts/lint.sh [all|api|web]" >&2; exit 2 ;;
esac

if [ "$status" -eq 0 ]; then echo "lint: OK ($targets)"; fi
exit "$status"
