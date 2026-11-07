# P0 验收记录

环境：macOS 15 (arm64)、Go 1.23.4（本地 `.toolchains/go`）、Node v26.7、pnpm 10、Python 3.14、Docker 29.9（本次 Docker Hub 直连超时，PostGIS 镜像走 daocloud 镜像源）。

## 1. 后端单元/接口测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | `internal/api` 与 `internal/rules` 全部通过；覆盖规则映射/去重/坐标、REST 健康/台账/注入/查询/确认、metrics、WebSocket hello+event |

## 2. 端到端冒烟（`scripts/smoke.sh`）

```
smoke: PASS (8 checks passed)
```

| 检查 | 结果 |
|---|---|
| `/api/health` | status=ok |
| 相机数量 | 6（CAM-001..CAM-006 全部存在） |
| 注入检测 | HTTP 202，accepted=1 dropped=0 |
| 事件可见 | evt_e05213a6b53a60b9 出现在 `/api/events`（第 1 次轮询命中） |
| 事件确认 | status=acked |
| `/metrics` | 含 `dsh_twin_events_total` 与 `dsh_twin_ws_clients` |

## 3. 压测（`scripts/load.py --qps 100 --duration 5`）

| 指标 | 值 |
|---|---|
| 目标 | 100 QPS × 5s = 500 请求，10 workers |
| 实际 | 500/500 成功（100.00%），100.00 req/s |
| HTTP 延迟 | p50 **0.53 ms** / p95 **0.82 ms** / p99 **2.03 ms** |
| 事件产出 | 27 条（其余 473 条被 5s 去重窗口按规则丢弃，符合预期） |
| 结论 | ✅ PASS（阈值：达成 ≥80% 目标且无失败） |

> 说明：accepted/dropped 的比值由去重规则决定，不是吞吐瓶颈；压测关注 HTTP 成功率与延迟。

## 4. 前端

```bash
cd apps/web && pnpm install && pnpm typecheck && pnpm build && pnpm dev   # :5173
```

| 指标 | 目标 | 实测 |
|---|---|---|
| `pnpm typecheck` / `pnpm build` | 通过 | ✅ 通过（dist JS 666.6 kB / gzip 178.1 kB；仅 three.js 分包体积告警） |
| 页面与代理 | 正常 | ✅ `/` 返回应用壳（标题「数字孪生安防平台 · P0」），`/api/health` 经 Vite 代理 200 |
| WS 直连与代理 | 正常 | ✅ 经 `ws://localhost:5173/ws` 收到 `hello` + `stats`；注入检测后收到 `event`；PATCH 后收到 `camera_status` |
| 事件上图 | 端到端 ≤1s | ✅ 注入 10 条事件后，右侧事件流与三维事件点同屏出现（见 `docs/screenshots/p0-web.png`） |
| 首屏 / 帧率 | ≤3s / ≥30fps@1080p | ⚠️ 截图来自 headless Chrome **软件渲染**（页面显示 4 FPS，不具代表性）；需真机 Chrome 人工确认 |

截图：`docs/screenshots/p0-web.png`（1600×900，含顶栏统计、6 个相机视锥、事件流、相机详情）。

## 5. 数据库模式（PostgreSQL + PostGIS）

```bash
POSTGIS_IMAGE=docker.m.daocloud.io/postgis/postgis:16-3.4 make db-up
DATABASE_URL='postgres://twin:twin@127.0.0.1:5432/twin?sslmode=disable' make dev-api
make smoke
```

- 状态：compose 文件与 PostgreSQL store（迁移/种子/CURD）已实现并在容器镜像配置中使用；
  本次会话环境访问 Docker Hub 超时，PostGIS 镜像经 daocloud 镜像源拉取（大镜像、网络受限），
  完整容器化验证建议在有稳定镜像源的机器上执行 `make up`。
- 代码路径已通过单元测试覆盖（`go test ./...`）；PostgreSQL 的 SQL 迁移与查询在启用 `DATABASE_URL` 时自动执行。

## 6. 结论

- 软件闭环（注入 → 规则 → 事件 → WS → 列表/确认/metrics）已在本机跑通，延迟与吞吐远高于 P0 目标。
- 三维前端与数据库持久化用于演示"可交付形态"，真实视频接入、标定与 AI 指标属于 P1/P2。
