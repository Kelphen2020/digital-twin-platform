# P0 developer entry points. Run `make help` for the list.
# Go is optional on PATH: the P0 PoC on this machine uses ../../.toolchains/go.

GO ?= $(shell command -v go 2>/dev/null || echo $(CURDIR)/../.toolchains/go/bin/go)
export GOPROXY ?= https://goproxy.cn,direct
export GOSUMDB ?= off

API_DIR := services/api
WEB_DIR := apps/web
COMPOSE := docker compose -f deploy/docker-compose.yml
API_URL ?= http://127.0.0.1:8080
QPS ?= 100
DURATION ?= 5
ARGS ?=

.PHONY: help dev-api build-api test-api vet-api dev-web install-web build-web hls db-up db-down up down obs-up sim sim-status smoke load test fmt status stop

help:
	@echo "P0 targets:"
	@echo "  make dev-api      run the Go API on :8080 (in-memory store)"
	@echo "  make build-api    build services/api/bin/api"
	@echo "  make test-api     go test ./..."
	@echo "  make dev-web      run the Vite dev server on :5173"
	@echo "  make install-web  pnpm install for apps/web"
	@echo "  make build-web    pnpm build for apps/web"
	@echo "  make hls          regenerate the local HLS test stream (ffmpeg)"
	@echo "  make db-up        start PostgreSQL/PostGIS (docker compose)"
	@echo "  make up / down    full docker compose stack (API + PostgreSQL)"
	@echo "  make sim          run the detection simulator (ARGS=\"--qps 10 --duration 30\")"
	@echo "  make sim-status   toggle camera status every 10s"
	@echo "  make smoke        end-to-end smoke test against $(API_URL)"
	@echo "  make load         load test (QPS=$(QPS) DURATION=$(DURATION))"
	@echo "  make test         backend tests + web build"

dev-api:
	cd $(API_DIR) && PORT=8080 $(GO) run ./cmd/server

build-api:
	cd $(API_DIR) && $(GO) build -o bin/api ./cmd/server

test-api:
	cd $(API_DIR) && $(GO) test ./...

vet-api:
	cd $(API_DIR) && $(GO) vet ./...

dev-web:
	cd $(WEB_DIR) && pnpm dev

install-web:
	cd $(WEB_DIR) && pnpm install

build-web:
	cd $(WEB_DIR) && pnpm build

hls:
	bash scripts/make_hls.sh

db-up:
	$(COMPOSE) up -d postgres

db-obs-up:
	$(COMPOSE) --profile obs up -d prometheus grafana

down:
	$(COMPOSE) down

up:
	$(COMPOSE) up -d --build

obs-up:
	$(COMPOSE) --profile obs up -d prometheus grafana

down:
	$(COMPOSE) down

sim:
	python3 services/simulator/simulator.py --api-base $(API_URL) $(ARGS)

sim-status:
	python3 services/simulator/run_camera_status.py --api-base $(API_URL) $(ARGS)

smoke:
	API_BASE=$(API_URL) bash scripts/smoke.sh

load:
	python3 scripts/load.py --api-base $(API_URL) --qps $(QPS) --duration $(DURATION)

test: test-api build-web

fmt:
	cd $(API_DIR) && $(GO) fmt ./...

status:
	@curl -s -o /dev/null -w 'api  :8080 -> %{http_code}\n' $(API_URL)/api/health || true
	@curl -s -o /dev/null -w 'web  :5173 -> %{http_code}\n' http://127.0.0.1:5173/ || true

stop:
	-@pkill -f 'digital-twin-platform/services/api/bin/api' 2>/dev/null || true
	-@pkill -f 'apps/web.*vite' 2>/dev/null || true
	@echo "stopped api/web (if they were running)"
