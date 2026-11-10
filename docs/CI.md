# 持续集成（P18.0）

仓库内置 GitHub Actions 流水线（`.github/workflows/ci.yml`）与本地等价脚本（`scripts/ci_local.sh`），保证每次改动都能在 PR 前跑完全部回归。

## 1. 流水线任务

| Job | 内容 | 触发 |
|---|---|---|
| `api` | `go build ./...`、`go vet ./...`、`go test ./... -count=1 -coverprofile`，**覆盖率门禁 45%** | push / PR |
| `lint` | `golangci-lint`（errcheck/govet/staticcheck/unused/gocritic/bodyclose 等 15 个 linter）+ `eslint src --max-warnings 0` | push / PR |
| `web` | `pnpm install --frozen-lockfile`、`pnpm typecheck`、`pnpm build` | push / PR |
| `simulator` | `python -m py_compile services/simulator/*.py scripts/*.py`、`bash -n` 全部脚本 | push / PR |
| `smoke-memory` | 内存 store 起 API → 全量 `scripts/smoke.sh`（263 项） | push / PR |
| `smoke-postgres` | PostGIS service → `TestPostgresContract` → PG store 起 API → 全量 smoke | push / PR |

- 并发组 `ci-${{ github.ref }}`，新提交自动取消旧运行。
- 失败时上传 API 日志尾部（`tail -100 /tmp/api.log`）。
- Go 版本取自 `services/api/go.mod`；Node 20 + pnpm 10。

## 2. 本地跑全流程

```bash
# 全流程（含 PostgreSQL 阶段，需要 docker；会临时创建 dtp-ci-postgres 容器与 55433 端口）
./scripts/ci_local.sh

# 跳过 PostgreSQL 阶段（约 3–4 分钟）
./scripts/ci_local.sh --quick

# 只跑某段（等价于单独命令）
make test-api && make vet-api
(cd apps/web && pnpm typecheck && pnpm build)
API_BASE=http://127.0.0.1:8080 bash scripts/smoke.sh
```

本地脚本行为：
- 使用 `services/api/bin/api` 二进制起临时实例（端口 18080 内存 / 18081 PostgreSQL），结束后自动清理进程与容器。
- 输出统一汇总：

```
================ CI SUMMARY ================
PASS  api build
PASS  api vet
PASS  api test
PASS  simulator lint
PASS  web typecheck+build
PASS  smoke (memory)
PASS  smoke (postgres)
============================================
```

## 3. 常见失败排查

| 失败阶段 | 排查方向 |
|---|---|
| `api test` | `cd services/api && go test ./... -v` 看具体用例；新增 PG 逻辑用 `PG_TEST_URL` 跑契约测试 |
| `web` | `pnpm typecheck` 报错通常是类型/未使用变量；`pnpm-lock.yaml` 改动需提交 |
| `smoke (memory)` | 先用 `make smoke` 复现；检查 8080/18080 端口占用 |
| `smoke (postgres)` | docker 不可用会跳过；容器 55433 冲突时 `docker rm -f dtp-ci-postgres` |
| 超时 | GitHub 免费 runner 单 job 上限 6h；smoke 约 2–4 分钟，PG 阶段约 3–5 分钟 |

## 4. 与本地开发的关系

- 开发时 `make smoke` 针对 8080 上的常驻实例；CI 用临时实例 + 随机/固定测试端口，互不影响。
- CI 中 `BUS_DRIVER=memory`（smoke-memory）与默认 jsonl（smoke-postgres 未显式设置时用默认内存 bus）均可运行；本地默认 jsonl 便于回放调试。
- 新增 smoke 检查后无需改 CI：脚本自检，检查数随 `scripts/smoke.sh` 自动变化。

## 5. 代码质量门禁（P20.0）

### Go：golangci-lint

配置 `services/api/.golangci.yml`，启用 15 个 linter（errcheck、govet、ineffassign、staticcheck、unused、misspell、revive、gocritic、bodyclose、contextcheck、nilerr、rowserrcheck、sqlclosecheck、unconvert、wastedassign）。

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2
cd services/api && golangci-lint run ./...
```

排除规则（有意为之，非降级）：
- `_test.go`：允许忽略错误、shadow、未使用变量（测试辅助代码）。
- `internal/api/server.go` 的 `contextcheck`：请求返回后派生的通知/WS 后台任务使用 `context.Background()` 属预期设计；服务级 worker 由 `main` 注入长期 ctx。

### Web：ESLint flat config

`apps/web/eslint.config.js`（eslint 9 + eslint-plugin-vue + @typescript-eslint），`pnpm lint` = `eslint src --max-warnings 0`。

### 覆盖率门禁

`go test ./... -coverprofile` 全包语句覆盖率 ≥ **45%**（当前约 49%），低于阈值 CI 失败；报告作为 artifact 上传。提升建议：为 `internal/store` 增加内存实现单测、为 `auth` 增加 PBKDF2/JWT 边界测试。

### 一键跑

```bash
./scripts/lint.sh          # 全部（Go + Web）
./scripts/lint.sh api
./scripts/lint.sh web
./scripts/ci_local.sh --quick   # 含 lint + 覆盖率门禁的完整本地流水线
```
