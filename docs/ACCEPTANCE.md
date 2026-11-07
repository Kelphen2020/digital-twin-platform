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

---

# P1.0 验收记录（2026-11-07 晚）

## 7. P1 后端接口测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | `internal/calib`（仿射/单位矩阵还原、退化点拒绝）、`internal/api`（标定 solve/put/get/delete、轨迹、确认/派单/关闭、审计、统计、CSV、可选 Bearer Token、错误码）全部通过 |

## 8. smoke.sh（P0 8 项 + P1 13 项 = 21 项）

```
smoke: PASS (21 checks passed)
```

P1 覆盖：标定 solve（rmse=7.6e-14）→ PUT → GET 相机标定；带 `track_id` 注入 → `/api/tracks` 首轮命中；事件 → 详情（actions 空）→ 派单（target=安保A组）→ 关闭（closed_at 设置）→ 详情 actions=[dispatch,close]；`/api/audit` 含标定/处置动作；`/api/stats/summary` totals>0；`/api/stats/events.csv` 表头匹配。

> 顺带修复 P0 smoke 的一个潜在抖动：在模拟器高并发时，5s 去重窗口可能吞掉 smoke 的注入，现已加入去重窗口重试（3 次并发 smoke 全绿）。

## 9. 压测（P1 轨迹路径）

```bash
python3 scripts/load.py --qps 50 --duration 5    # 默认 --track-ratio 0.5
```

| 指标 | 值 |
|---|---|
| 请求 | 250/250 成功（100%），50.00 QPS |
| 延迟 | p50 0.50 ms / p95 1.51 ms / p99 2.47 ms |
| 事件/轨迹 | 25 事件，108/250 条检测带 track_id 落库 |
| 结论 | ✅ PASS |

## 10. 前端 P1 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 视频 | ✅ HLS 真实播放：`video.readyState=4`，`currentSrc=/hls/test.m3u8`，`paused=false`，LIVE 标识，切换相机后重新附着 |
| 轨迹 | ✅ 相机选择加载 + WS 实时增量；「显示轨迹」计数 29；3D 轨迹线渲染；开关往返正常 |
| 处置闭环 | ✅ 通过真实按钮：确认 → 派单（安保B组+备注）→ 关闭（备注）；actions 3 条，按钮置灰；收到 3 次 `event_updated` |
| 统计 | ✅ 24h/1h 切换、等级/类型分布、TOP 相机、小时柱状；CSV 下载 200 |
| 标定 UI | ✅ 显示 CAM-001「已标定 RMSE 0.00 m / 5 点」；对话框 4–20 点求解返回矩阵与残差 |
| 控制台 | ✅ console errors=[]，page exceptions=[]，failed HTTP=[] |
| 截图 | `docs/screenshots/p1-web.png`、`p1-actions.png`、`p1-stats.png`、`p1-calibration.png` |

## 11. 已知限制（P1.0）

- 视频源是本机 ffmpeg 测试流（`scripts/make_hls.sh`），HLS 有 2–6s 级延迟；WebRTC 低时延需接 ZLMediaKit，网络/Docker 环境具备后进行。
- 标定求解是代数最小二乘（非 SVD/RANSAC），合成数据验证通过；真实场景需加鲁棒估计与多相机融合。
- 真机 GPU 帧率、画布交互（拖拽/悬停/3D 点选）未自动化，headless 软渲染 46 FPS 不代表真机（目标 ≥30fps@1080p）。
- 可选鉴权（`AUTH_TOKEN`）代码与前端支持已实现，但本次未以开启状态做端到端验证。
- PostgreSQL 容器化仍未在本机验证（Docker Hub 网络受限，镜像经镜像源拉取过慢）。

---

# P2.0 验收记录（2026-11-07 深夜）

## 12. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增 `internal/auth`（PBKDF2 口令、JWT 签发/校验、角色/区域策略）与 `server_p2_test.go`：登录、401/403、`out_of_scope`、规则阈值与类别过滤、工单状态机与 `closed_at`、审计 actor=用户名、ingest token 守卫、WS 鉴权 |

## 13. smoke.sh（P0 8 + P1 13 + P2 18 = 39 项）

```
smoke: PASS (39 checks passed)
```

P2 覆盖：无 token 401；admin 登录 + `/auth/me`；viewer 读 200 / 写 403；operator 区域内 ack 200、区域外 `403 out_of_scope`；operator 建单 201 → processing（指派）→ done（closed_at）→ GET；admin 规则 threshold=0.99 → 0.95 检测产生 0 事件（规则生效）→ 恢复默认；审计含 `order.create/order.update/camera.rules.put` 且 actor=operator。

## 14. 压测（AUTH_MODE=on）

```bash
API_TOKEN=<admin token> python3 scripts/load.py --qps 50 --duration 5
```

| 指标 | 值 |
|---|---|
| 请求 | 250/250 成功（100%），50.00 QPS |
| 延迟 | p50 1.13 ms / p95 1.42 ms / p99 1.60 ms |
| 轨迹 | 130/250 条检测带 track_id |
| 结论 | ✅ PASS（鉴权开启后延迟仍在 1–2ms 量级） |

## 15. 前端 P2 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 登录页 | ✅ 三种演示账号（点击填充）；表单登录成功；标题 P2.0 |
| admin | ✅ 用户标识「管理员 · 全部区域」；标定与规则配置可见可编辑（规则保存后 `updated_at` 更新）；工单可建可改；事件处置可用；登出回到登录页 |
| operator | ✅「值班员 · 东门、停车场」；标定/规则只读；事件处置与建单可用；修改区域外相机（CAM-004）→ toast「403 out_of_scope」 |
| viewer | ✅ 只读：处置按钮禁用 + 只读提示、无建单、无相机编辑、规则/工单只读 |
| 401 自动登出 | ✅ 无效 token 重载 → 登录页 +「登录已过期」提示 |
| 控制台 | ✅ console errors=[]、page exceptions=[]（仅预期的 403/401 测试请求） |
| 截图 | `docs/screenshots/p2-login.png`、`p2-admin.png`、`p2-rules.png`、`p2-operator.png`、`p2-viewer.png` |

## 16. 已知限制（P2.0）

- 演示账号与固定口令仅用于本机；生产需接企业 IdP/SSO、HTTPS、密钥轮换，并替换 `JWT_SECRET`。
- JWT 存 localStorage，无刷新令牌/多标签同步；`AUTH_MODE=off` 仅供本地联调。
- 操作员区域权限作用于相机/事件/工单；跨区域**只读**仍可见（P2.0 简化，P3 可做数据级过滤）。
- ROI 编辑器为归一化坐标点列表（无拖拽手柄）；规则未配置时 `updated_at` 省略。
- 真实 GPU 帧率、画布手势、真实视频源仍需人工验收；PostgreSQL 容器化仍受本机 Docker 网络限制未验证。
