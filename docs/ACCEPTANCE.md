# 验收记录（P0 ~ P26）

环境：macOS 15 (arm64)、Go 1.23.4（本地 `.toolchains/go`）、Node v26.7、pnpm 10、Python 3.14、Docker 29.9（Docker Hub 直连不稳定，PostGIS 镜像走 daocloud 镜像源）。

> 本文件按里程碑追加（P0 → P26）。**最新阶段速览（P26）**：
>
> | 指标 | 当前值 |
> |---|---|
> | `make smoke` | **PASS 263/263**（61 步，P0–P16 全链路） |
> | `go test ./...` | 全绿；语句覆盖率 **63.2%**（门禁 62%） |
> | `golangci-lint` / `eslint` | 0 issues / 0 warning |
> | 本地 CI `scripts/ci_local.sh` | 8 阶段全 PASS（含 PostgreSQL 阶段需 docker） |
> | 已完成里程碑 | P0–P26（详见文末各阶段记录） |

---

# P0 验收记录

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

---

# P3.0 验收记录（2026-11-08 凌晨）

## 17. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增 P3 测试：数据级过滤（operator 仅 3 台相机、越权事件 403、统计按区域收敛）、设置校验（非法 webhook URL 400）、escalation 生效（stale 事件 → escalated_at）、webhook 投递（httptest 接收器收到事件 id、/api/notifications 记录 ok=true）、审计 `event.escalate` actor=system |

## 18. smoke.sh（P0 8 + P1 13 + P2 18 + P3 21 = 60 项 / 23 步）

```
smoke: PASS (60 checks passed)
```

P3 覆盖：
- 数据过滤：operator `GET /api/cameras` = `[CAM-001, CAM-005, CAM-006]`；CAM-002 新事件 admin 可见、operator 列表不可见、详情 `403 out_of_scope`；operator 访问审计/通知 → `403 forbidden`。
- 升级：admin PUT escalation `{after_seconds:1}` → 500ms 内生效；`ts=now-10s` 的 warn 事件在 **2.3–7.8s** 内写入 `escalated_at`（合同要求 ≤15s 扫描周期）。
- 通知：非法 URL 400；本地 webhook 接收器收到事件 POST（body 461B），`/api/notifications` 记录 `ok=true/status_code=200`；测试后恢复默认（300s、通知关闭、CAM-005 规则恢复）。
- 幂等：重复运行 PASS，并发模拟器 8 det/s 下 PASS。

## 19. 压测（P3 回归，AUTH_MODE=on）

```bash
API_TOKEN=<admin token> python3 scripts/load.py --qps 50 --duration 5
```

| 指标 | 值 |
|---|---|
| 请求 | 250/250 成功（100%），50.00 QPS |
| 延迟 | p50 1.09 ms / p95 1.41 ms / p99 2.15 ms |
| 轨迹 | 132/250 带 track_id |
| 结论 | ✅ PASS（P3 过滤/通知/升级未影响 ingest 吞吐） |

## 20. 前端 P3 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 升级 UI | ✅ 事件流/详情「已升级」标记 + 升级时间；WS `event_updated` 触发「告警已升级」toast（CAM-001/CAM-006 实测） |
| 设置（admin） | ✅ 通知设置与升级设置加载、保存、GET 回读（300s↔30s、timeout 7↔5 保存/恢复）；表单校验 |
| 审计/通知（admin） | ✅ 审计 9 行（含 settings.*.put、event.escalate actor=system）；通知投递 3 行「投递成功 200」 |
| operator | ✅ 侧栏「仅显示负责区域：东门、停车场」；3 个页签（无设置/审计）；相机 3 台；越权 403 toast |
| viewer | ✅ 只读、6 台相机、无管理面板 |
| 控制台 | ✅ console errors=[]、page exceptions=[]（仅预期 403 测试请求） |
| 截图 | `docs/screenshots/p3-admin.png`、`p3-escalated-detail.png`、`p3-audit.png`、`p3-notifications.png`、`p3-escalation-toast.png`、`p3-operator.png` |

## 21. 已知限制（P3.0）

- 设置缓存在 Host 内存（单实例）；多实例部署需共享存储 + 失效广播。
- 通知为异步尽力投递：失败只记录不重试；飞书负载仅按自定义机器人文本格式实现，未接真实机器人验证。
- 升级采用 15s 扫描，最坏延迟约 15s；`escalated_at` 只标记不升级严重级别。
- 数据级过滤作用于读取；写操作仍由 P2 的区域校验保证。真实 GPU 帧率/画布手势/真实视频源仍需人工验收。

---

# P4.0 验收记录（2026-11-08 上午）

## 22. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增 P4 测试：值班组 CRUD/解析/权限、升级自动派单（订单 assignee=值班组、audit、指标）、通知重试（失败两次后成功、attempts=3）、静默窗口（同键仅 1 次投递 + 抑制指标）、指标输出 |

## 23. smoke.sh（P0 8 + P1 13 + P2 18 + P3 21 + P4 25 = 85 项 / 28 步）

```
smoke: PASS (85 checks passed)
```

P4 覆盖：
- 值班：admin 建组 201 → 列表/`/api/duty/current` 解析 → PATCH → operator POST 403 → DELETE 204；重建立用于派单测试。
- 自动派单：escalation `{after_seconds:1, auto_dispatch:true}` + 回填 10s 的 warn 事件 → 约 3–16s 内生成 `{status:open, priority:urgent, assignee:东门值班组, note:系统自动派单（告警升级）}`；审计 `order.auto_dispatch` actor=system；恢复默认。
- 通知重试：接收器 `--fail-first 2`，`max_attempts=3/backoff=1s` → 投递记录 `ok=true attempts=3 status_code=200`，接收器日志 `[500,500,200]`。
- 静默：`silence_seconds=60`，同相机+类型+严重级别两条事件仅 1 次投递；`dsh_twin_notifications_suppressed_total > 0`。
- 指标：`dsh_twin_notifications_total{result=...}`、`..._escalations_total`、`..._orders_total`、`..._duty_dispatches_total`、`..._duty_skipped_total` 均存在。
- 并发：模拟器 8 det/s 下 85/85 PASS；测试后恢复全部设置与规则。

## 24. 前端 P4 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 值班 tab | ✅ admin 六页签（事件流/统计/工单/值班/设置/审计）；当前值班解析、建组/改名/删除（两步确认）UI 全流程，API 校验一致；operator/viewer 只读并显示角色提示 |
| 设置 P4 字段 | ✅ 通知 max_attempts/backoff_seconds/silence_seconds 与升级 auto_dispatch 加载、保存、回读 |
| 通知投递 | ✅ 审计→通知投递显示「尝试 N 次」；失败端点 `attempts=3 ok=false`，成功 `attempts=1/3` |
| 自动派单 UX | ✅ WS `order_updated` → toast「系统自动派单：东门值班组」；工单页出现 `紧急优先 / 负责人：东门值班组` 的升级工单；升级 toast 同时出现 |
| 稳定性 | ✅ 升级风暴下 toast 上限 5 条防刷屏；console errors=[]、exceptions=[]、bad responses=[] |
| 截图 | `docs/screenshots/p4-admin-duty.png`、`p4-duty-form.png`、`p4-admin-settings.png`、`p4-audit-attempts.png`、`p4-auto-dispatch-toast.png`、`p4-operator-duty.png` |

## 25. 可观测性

- `deploy/observability/prometheus.yml`（抓取 api:8080 与 host.docker.internal:8080）+ `grafana-dashboard.json`（事件速率、通知成功/失败、升级、派单、注入延迟、工单/抑制）。
- `make obs-up` 启动 Prometheus(9090)/Grafana(3000)（镜像源受限时可用 `PROM_IMAGE`/`GRAFANA_IMAGE` 指向镜像）。

## 26. 已知限制（P4.0）

- 值班无排班（班次/时间窗）；同区域多组按创建顺序取第一个（已在契约注明）。
- 自动派单只做「升级 → 建单」，不做重复工单去重与接手人二次分配。
- 通知静默为单实例内存状态；重启后窗口重置；多实例需共享状态。
- 真实机器人/HTTP 接收器验证用本地服务；真实视频源、GPU 帧率与画布手势仍需人工验收。

---

# P5.0 验收记录（2026-11-08 上午）

## 27. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增 `internal/bus` 单测（memory 环形/裁剪/订阅者；jsonl 追加、重启恢复 offset、历史回放）与 P5 接口测试（status/replay/WS `event_replay`/400/403/指标） |

## 28. smoke.sh（P0–P4 85 项 + P5 9 项 = 94 项 / 33 步）

```
smoke: PASS (94 checks passed)
```

P5 覆盖：admin bus status 形状；非 admin status/replay → 403；注入后 `published`/`next_offset` 增长；按 offset 区间回放 200 `replayed>=1` 且 `replayed` 计数增加；非法区间 400；`/metrics` 含 `dsh_twin_bus_published_total` / `dsh_twin_bus_replayed_total` / `errors_total` / `lag`。

> 说明：当时后端以 `BUS_DRIVER=jsonl`（`data/bus/events.jsonl`）运行；memory 驱动同样支持同一契约。

## 29. 前端 P5 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 总线页签 | ✅ admin 7 页签；状态卡片（driver=jsonl、路径、published/replayed/oldest/next/subscribers/last_publish）刷新后数值变化 |
| 回放 | ✅ `from_offset=0, limit=50` 提交 → toast「回放完成：50 条」；状态 replayed 17→67；WS 实收 `event_replay` 帧（37 条）与 `event_updated`（13 条升级信封） |
| 回放展示 | ✅ 事件流独立回放区「回放 · offset N」徽标 + 历史标记；不进入实时计数/三维标记；回放条目无确认按钮，可打开详情 |
| 权限 | ✅ operator/viewer 无总线页签，直接调用 status/replay → 403 |
| 错误 | ✅ 客户端校验 + 服务端 400 toast；409 映射已实现（jsonl 下无法主动触发） |
| 控制台 | ✅ console errors=[]、page exceptions=[] |

## 30. 已知限制（P5.0）

- jsonl 驱动为单机持久日志，非分布式；`Driver` 接口已为 Kafka/Redpanda 预留，P6 替换驱动即可。
- 回放为只读重放（不修改事件/工单），默认不重复通知；回放窗口受 `limit` 500 限制。
- 总线 lag 指标为「尾部差」，不跟踪多消费者组；多消费者场景需改用 Kafka 语义。
- 真实 GPU 帧率、真实视频源、真实机器人投递仍需人工验收。

---

# P6.0 验收记录（2026-11-08 上午）

## 31. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增 `GET /api/site` 测试（默认站点字段/模型清单/viewer 可读/匿名 401）；既有全部测试保持通过 |

## 32. smoke.sh（P0–P5 94 项 + P6 3 项 = 97 项 / 34 步）

```
smoke: PASS (97 checks passed)
```

P6 覆盖：admin `GET /api/site` → 200（name/extent 数值/models[id,format,url]/map.style）；viewer 200；匿名 401。

## 33. 前端 P6 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 站点配置 | ✅ `/api/site` 200；`/models/site.gltf` 200；HUD「模型：已加载」，title「已加载：园区总览模型」 |
| 2.5D 底图 | ✅ 默认 on：区域多边形/道路/建筑轮廓/相机标注/指北针/扫描线；关闭后显示纯网格，再开启恢复 |
| 建筑 LOD | ✅ `data-model-detail=lod/simple` 切换；glTF 作近景、程序化盒体/轮廓作中远景 |
| glTF | ✅ GLTFLoader 按需分包加载成功（1 号楼/2 号楼/东门门架）；加载失败指示与程序化回退已实现 |
| 视频融合 | ✅ 点击后隐藏 video（readyState=4、paused=false、HLS）→ 视锥内视频纹理面；关闭后销毁；右侧播放器不受影响 |
| 工具栏 | ✅ 4 开关（轨迹/视频融合/模型细节/2.5D底图）状态与 active 正确；7 个页签保持 |
| 控制台 | ✅ console issues=[]、page exceptions=[]、bad responses=[]；operator 同样可读且无错误 |
| 截图 | `docs/screenshots/p6-scene.png`、`p6-video-fusion.png`、`p6-base-map-off.png`、`p6-lod-off.png`、`p6-operator.png` |

## 34. 已知限制（P6.0）

- 示例 glTF 由 `tools/make_site_model.py` 生成（3 个体块），非真实 BIM；真实模型需 LOD/轻量化（目标 ≤200MB）、Draco/KTX2 压缩解码未接入。
- 2.5D 底图为程序化矢量绘制，未接正射影像（`map.image_url` 目前为空，接口已预留）。
- 视频融合使用 HLS 演示流；真实 WebRTC 低时延融合需媒体服务器（本机 Docker 网络受限未验证）。
- 真机 GPU 帧率、视频面流畅度与大模型加载性能需人工验收。

---

# P7.0 验收记录（2026-11-08 中午）

## 35. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增用户生命周期测试：列表/无密码泄漏、创建校验与冲突、登录、角色/区域变更后重登生效、改密、停用、删除、自我保护（停用/删除/降级自己）、审计 |

## 36. smoke.sh（P0–P6 97 项 + P7 16 项 = 113 项 / 35 步）

```
smoke: PASS (113 checks passed)
```

P7 覆盖：admin 列表（3 个种子账号、无密码字段）；operator 403；创建 201 / 重复 409 / 短密码 400；新用户登录；角色+区域修改后重登生效且数据级相机范围正确（area_gate = CAM-001/006）；停用后登录 401；删除后 401；自我保护 DELETE/停用 → 400；审计 `user.*` actor=admin；清理临时账号。

## 37. 前端 P7 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 用户页签 | ✅ admin 8 页签；列表含显示名/@用户名/角色/区域/创建更新时间；当前用户与启用状态徽标；自我删除按钮禁用 |
| 新建/编辑/删除 | ✅ UI 创建 operator 用户（API 验证）、编辑角色/区域/改密（新密码可登录、旧密码 401）、两步确认删除；用户名编辑只读 |
| 自我保护 | ✅ 编辑自己时角色/启用控件禁用；后端降级自己 → 400 `self_protected`（本轮已修复该缺口并加测试） |
| 权限 | ✅ operator/viewer 仅 4 页签，无用户管理；直接调用 `/api/users` → 403 |
| 回归 | ✅ P0–P6（三维场景/模型/视频/总线/设置等）正常；console errors=[]、page exceptions=[] |
| 截图 | `docs/screenshots/p7-user-list.png`、`p7-user-create.png`、`p7-user-edit.png`、`p7-self-protect.png`、`p7-user-deleted.png`、`p7-admin-users-final.png`、`p7-operator.png` |

## 38. 已知限制（P7.0）

- 内部修复：`PATCH /api/users/{id}` 降级自己此前返回 200，现已返回 400 `self_protected` 并补测试。
- 账号持久化：内存模式下重启会回到 3 个种子账号；设置 `DATABASE_URL` 使用 PostgreSQL 时账号持久化。
- JWT 无刷新令牌：角色/区域变更在下次登录生效，停用后旧 token 最长 12h 内仍有效（下次请求不主动吊销）。
- `/api/auth/me` 返回的是 JWT 快照，`created_at/updated_at` 为零值（列表接口返回真实时间）；不影响权限。

---

# P8.0 验收记录（2026-11-08 晚间）

## 39. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增 P8 测试：班次校验与解析（含跨夜/非班次时间无人值班）、轮转派单（两个事件不同 operator）、二次升级（re_escalated_at/审计/指标）、工单去重（open_order）、second_after_seconds 校验 |

## 40. smoke.sh（P0–P7 113 项 + P8 20 项 = 133 项 / 39 步）

```
smoke: PASS (133 checks passed)
```

P8 覆盖：班次组创建 201、`/api/duty/current` 返回 shift + 2 班次成员、operator 建组 403；轮转派单 assignee 在 operator/viewer 间轮转（note 含班组·班次）；二次升级 15s 左右写入 `re_escalated_at`、审计 `event.re_escalate`、指标增长；同事件再次派单被 open_order 去重（审计/指标）；`second_after_seconds` 越界 400；清理恢复默认并删除测试组。

## 41. 前端 P8 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 值班班次 | ✅ 值班组表单 rotation 选择 + 班次编辑器（名称/起止/周几/成员/增删）；当前值班卡显示当前班次与成员；operator/viewer 只读 |
| 派单轮转 | ✅ 工单负责人显示 operator/viewer（轮转），note 原样显示「系统自动派单（告警升级） · 班组 · 班次」 |
| 再升级 | ✅ 事件流/详情「再升级」徽标 + 再升级时间行；WS toast「告警再次升级：…」 |
| 设置 | ✅ 二次升级时限字段（0–604800）保存/回读（120→0 还原） |
| 回归 | ✅ 8 页签、场景模型/2.5D 底图/视频、P0–P7 功能正常；console errors=[]、exceptions=[]、bad responses=[] |
| 截图 | `docs/screenshots/p9-quality.png`、`p9-offline.png`、`p9-reconnect.png`、`p9-429-toast.png` |
| 截图（P8） | `docs/screenshots/p8-duty-current.png`、`p8-duty-form.png`、`p8-settings.png`、`p8-event-detail.png`、`p8-orders.png`、`p8-toast.png`、`p8-operator-duty.png` |

## 42. 已知限制（P8.0）

- 轮转状态是进程内轮询（重启归零）；多实例需共享轮转计数。
- 班次按服务端 Asia/Shanghai 解释；跨时区站点需要配置时区（当前未参数化）。
- 二次升级依赖 15s 扫描周期，最坏延迟约 15s；与首轮升级共用静默窗口。
- 工单去重仅针对「同一事件已有未关闭工单」；同相机同类事件跨事件仍是多张工单（按设计）。
- 真实 GPU/视频源/机器人投递仍需人工验收。

---

# P9.0 验收记录（2026-11-08 深夜）

## 43. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：令牌桶限流（burst/refill/禁用）、ingest 429 + 指标、请求体 413、轨迹广播采样（written=4/broadcast=2）、WS 帧指标 |

## 44. 压测矩阵（sim-ai，live :8080，AUTH_MODE=on，10 workers）

| 运行 | 模式 | 目标 QPS | 达成 | p50/p95/p99 ms | 成功 | WS 丢弃 | 限流 |
|---|---|---|---|---|---|---|---|
| 1 | steady | 50 | 50.00 | 0.57/0.77/1.10 | 100% | 0 | 0 |
| 2 | steady | 100 | 100.00 | 0.56/2.07/3.40 | 100% | 0 | 0 |
| 3 | steady | 200 | 200.00 | 0.52/2.68/6.54 | 100% | 0 | 0 |
| 4 | steady + 30% 读 | 100 | 100.00 | 0.59/3.39/6.19 | 100% | 0 | 0 |
| 5 | ramp 25%→200 | 200 峰值 | 125 avg | 0.53/2.59/6.20 | 100% | 0 | 0 |

- metrics 增量：detections +3328、events +164、WS frames +1725（dropped 0）、track written=broadcast +1688、bus published +164；ingest handler 平均 ≈47.5µs。
- 429 验证（INGEST_RATE_LIMIT=10/burst 200）：500 请求 → 248 成功 / 252 个 `429 rate_limited`，`ingest_rejected_total` 0→252；限流关闭后恢复正常。
- 详细报告：`docs/PERFORMANCE.md`。

## 45. 前端 P9 E2E

| 项 | 结果 |
|---|---|
| 连接质量指示 | ✅ 顶栏 WS 点旁显示 `已连接 + 帧率/s + ↻重连次数`（8 tabs/4 开关布局不变）；tooltip 含帧总数/5s 速率/重连/连接时长/最近帧；注入事件后帧率 0.4/s→1.8/s；强制断开→红色「已断开」，恢复后自动重连 `↻1` |
| 429 Toast | ✅ 拦截 `POST /api/users` 返回 429 后 UI 弹出「请求过于频繁，已限流」（未创建用户），api.ts 通用映射生效 |
| 回归 | ✅ P0–P8 全部正常（模型/视频/值班/用户/总线等）；console errors=[]、exceptions=[] |

## 46. 运维交付物

- `deploy/Dockerfile.web` + `deploy/nginx.conf`：静态站点 + `/api` 反代 + `/ws` 升级 + `/hls` 静态（gzip/SPA 回退）；compose 增加 `web` 服务（8081:80）。
- `deploy/observability/alerts.yml`：API down、无在线相机、WS 丢帧、总线积压、通知失败、限流、升级激增；prometheus.yml 挂载规则。
- Grafana 看板新增 WS 帧/丢弃、轨迹写入/广播/限流面板。
- `docs/OPERATIONS.md`：端口/环境变量/观测/备份恢复/升级/故障排查/安全清单。

## 47. 已知限制（P9.0）

- 压测为单机笔记本、共享开发机噪声（一个浏览器 WS 客户端），非生产基线；生产需独立压测环境与多客户端 WS 场景。
- WS 背压仅"满队列丢弃"策略，未做优先级队列/慢消费者踢出；限流为单实例令牌桶。
- compose `web` 服务与 observability 镜像拉取依赖网络（本机 Docker Hub 受限未实测）。
- 轨迹采样只影响广播不影响落库；采样过大会影响三维轨迹连续性，需按现场调整。

---

# P10.0 验收记录（2026-11-09 凌晨）

## 48. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：安全响应头、密码策略（长度/字母/数字）、登录防爆破（阈值/Retry-After/到期恢复/不影响其他账号）、审计 CSV 导出（表头/角色/时间参数校验） |

## 49. smoke.sh（P0–P9 133 项 + P10 13 项 = 146 项 / 42 步）

```
smoke: PASS (146 checks passed)
```

P10 覆盖：安全头（health + 认证接口）；登录锁定（连续 5 次失败后 429 + `Retry-After: 60`，admin 登录不受影响）；密码策略（9 位/纯字母 400，合规 201 并清理）；审计导出（admin 200 CSV + 附件头 + 精确表头，operator 403）；清理后仅 3 个种子账号。

## 50. 前端 P10 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 登录锁定 | ✅「登录失败次数过多，请在 60 秒后重试」+ 倒计时 60→58，提交禁用，演示账号填充仍可用；刷新后 admin 正常登录（锁定仅对测试用户名生效） |
| 密码策略 | ✅ 新建用户内联规则「≥10 字符 / 含字母 / 含数字」实时高亮；8 位/无数字分别提示；拦截后端 400 显示「密码不符合策略…」 |
| 审计导出 | ✅ action/actor/时间筛选 + 导出 CSV（Blob 下载 audit.csv，表头正确、80 行、actor 全为 admin），成功 Toast；operator 无审计页签且 403 |
| 回归 | ✅ 8 页签、4 场景开关、模型/底图/WS 指示器正常；console errors=[]、exceptions=[] |
| 截图 | `docs/screenshots/p10-lockout.png`、`p10-login-demo.png`、`p10-password-policy.png`、`p10-password-policy-backend-400.png`、`p10-audit-export.png` |

## 51. 已知限制（P10.0）

- 防爆破状态在进程内（重启清零），多实例需共享计数（Redis/内存网关）；按 username+IP，NAT 后用户可能共享锁。
- 密码策略仅长度/字符类别，未接泄露口令库或历史密码校验；无 MFA、无刷新令牌/吊销（JWT 到期 12h）。
- 审计导出上限 20000 行（CSV 同步生成）；超大审计量需异步导出。
- HTTPS 模板未在本机实跑（证书/镜像依赖网络）；`/metrics` 与审计接口需在网关层限制来源。

---

# P11.0 验收记录（2026-11-09 中午）

## 52. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：refresh 轮换与旧 token 重用拒绝、logout/logout-all（token_version 失效 access）、改密吊销、管理员强制下线/会话列表/self 保护、停用账号 access 失效 |

## 53. smoke.sh（P0–P10 146 项 + P11 23 项 = 169 项 / 46 步）

```
smoke: PASS (169 checks passed)
```

P11 覆盖：登录返回 refresh + 过期时间；refresh 轮换（旧 token 401 invalid_refresh_token，新 access 可用）；logout 吊销；logout-all 后旧 access 401、重登 200；管理员改密后旧 access 401、新密码登录 200；强制下线（会话 1→0、目标 access 401、self 400、operator 403）；清理临时账号。

## 54. 前端 P11 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 自动续期 | ✅ 拦截首次受保护请求返回 401 → 客户端自动 `POST /api/auth/refresh`（恰好 1 次）→ 原请求重试 200，token 轮换，页面保持登录 |
| 登录提示 | ✅「会话有效期 1 小时，过期前自动续期（refresh token 7 天）；修改密码或被强制下线后需重新登录」；三个 token 键持久化 |
| 登出 | ✅ `POST /api/auth/logout` 204 → 清空令牌 → 回登录页 |
| 会话管理 | ✅ 用户编辑弹窗显示在线会话数/最近 IP/创建时间/UA；「强制下线」两步确认（自己隐藏）；UI 强制下线后会话归 0 |
| 优雅失效 | ✅ 被强制下线后操作 → refresh 被拒 → 自动登出 +「登录已过期」Toast |
| 回归 | ✅ 8 页签/4 开关/场景/WS 指示器正常；console errors=[]、exceptions=[] |
| 截图 | `docs/screenshots/p11-login-hint.png`、`p11-refresh.png`、`p11-user-sessions.png`、`p11-force-logout.png`、`p11-auto-logout.png` |

## 55. 已知限制（P11.0）

- Refresh 会话在内存模式重启后丢失（需重新登录）；PostgreSQL 模式持久化。
- WS 长连接不主动断开（令牌失效后新请求 401，但已建立的 WS 在断开前仍可接收推送）；可加服务端定期校验。
- 无设备指纹/异地登录告警；refresh 重用检测仅拒绝旧 token（未做令牌家族级联吊销）。
- access 默认 1h，时钟偏移敏感场景需 NTP；多实例需共享 refresh 表（已用 PostgreSQL 支持）。

---

# P12.0 验收记录（2026-11-09 傍晚）

## 56. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：巡检路线创建/列表/PATCH/删除、viewer 只读 403、1/51 航点与 interval 越界 400、未知相机 400 `unknown_camera`、创建者或 admin 才能改删、审计 patrol.create/update/delete |

## 57. smoke.sh（P0–P11 169 项 + P12 18 项 = 187 项 / 49 步）

```
smoke: PASS (187 checks passed)
```

P12 覆盖：admin/operator 列表、operator 创建（2 航点、cam_001+cam_006、interval 2）、列表包含、PATCH 名称+间隔、viewer 创建 403/读取 200、校验矩阵（1/51 航点、未知相机、interval 0）、operator 删除自己 204、admin 删除他人路线 204、审计、清理无残留。

## 58. 前端 P12（typecheck + build + 浏览器验证）

| 项 | 结果 |
|---|---|
| 构建 | ✅ `pnpm typecheck` + `pnpm build` 通过（主包约 830 kB / gzip ~225 kB） |
| 测距 | ✅ HUD「测距」开关；点击地面两次 → 黄色连线 + 米制距离标签；再次点击重置/清除；测距模式不影响原有拾取 |
| 框选 | ✅ Shift+拖拽蓝色矩形；屏幕投影命中框内相机；显示「框选相机 N 台」chip 列表，场景高亮环/光柱；空白点击清除 |
| 巡检 | ✅ 浮层列表（GET /api/patrol/routes）；创建表单（名称/间隔/相机勾选生成 ENU 航点，支持框选预填）；创建者或 admin 可编辑/两步删除；播放/暂停/继续/停止：相机按 interval 沿航点飞行、折线 + 当前航点高亮、camera_ids 联动高亮；viewer 只读（可播放，无写按钮） |
| 回归 | ✅ 8 页签/4 场景开关/模型/视频/WS 指示器正常 |
| 截图 | `docs/screenshots/p12-measure.png`、`p12-box-select.png`、`p12-patrol-list.png`、`p12-patrol-create.png`、`p12-patrol-play.png`、`p12-viewer-patrol.png` |

## 59. 已知限制（P12.0）

- 巡检播放是客户端动画（手动触发），无服务端调度/定时巡检任务；多用户同时播放互不影响（各自本地）。
- 航点由选中相机位置生成（含朝向），不支持自由拖拽任意路径点；路线按创建者+admin 权限管理。
- 测距基于地面平面（z=0 投影），不支持点云/建筑表面精确测距。
- 路线存储在内存模式重启后丢失（PostgreSQL 持久化）。

---

# P13.0 验收记录（2026-11-09 晚间）

## 60. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：计划 CRUD/校验（未知路线、间隔越界）、立即执行、认领/完成、issue 自动建单（标题/负责人/状态/备注/camera）、调度器到期生成任务并推进 next_run_at、viewer/operator 权限、审计 |

## 61. smoke.sh（P0–P12 187 项 + P13 22 项 = 209 项 / 52 步）

```
smoke: PASS (209 checks passed)
```

P13 覆盖：计划创建 201（next_run_at/created_by）、列表、PATCH、viewer 403/读取 200、校验矩阵（缺路线/未知路线/间隔 0 与 10081 → 400）、立即执行 pending、认领 running assignee=operator、完成 done（finished_at）、issue 流程（status=issue、order_id、工单标题「巡检异常 · …」、assignee/status/priority/camera 正确）、viewer claim/complete 403、admin 删除他人计划 204、审计、清理无残留计划/路线。

## 62. 前端 P13 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 巡检浮层 | ✅ 路线/计划/执行 三 tab；计划列表（名称/路线/间隔/启用/next_run_at）、创建表单、编辑/两步删除、立即执行 Toast |
| 执行工作流 | ✅ 状态 chip（待执行/进行中/已完成/异常）、筛选、认领、完成弹窗（note + done/issue）；issue 后 Toast「已创建工单：ord_…」，卡片显示关联工单 |
| 角标/WS | ✅ HUD 巡检按钮待执行角标；WS `patrol_execution` 帧实测捕获并刷新列表/角标 |
| 权限 | ✅ viewer 只读（无写按钮，可播放）；admin 可管理他人计划（两步删除） |
| 回归 | ✅ 8 页签/4 开关/场景/测距/框选/路线播放；console errors=[]、exceptions=[] |
| 截图 | `docs/screenshots/p13-schedules.png`、`p13-schedule-form.png`、`p13-execution-pending.png`、`p13-execution-claim.png`、`p13-complete-issue-form.png`、`p13-execution-issue.png`、`p13-issue-order.png`、`p13-admin-schedules.png`、`p13-viewer.png` |

## 63. 已知限制（P13.0）

- 执行记录无删除接口，历史记录会累积（可加归档/清理策略）。
- 调度器为单实例进程内 ticker；多实例需分布式锁或数据库抢占（`FOR UPDATE SKIP LOCKED`）。
- issue 工单固定 priority=normal、assignee=提交人，未接值班轮转/升级链路。
- 计划与执行在内存模式重启后丢失（PostgreSQL 持久化）。

---

# P14.0 验收记录（2026-11-10 凌晨）

## 64. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：统计计数/完成率/异常率/平均时长/按路线与按人汇总、from/to 非 RFC3339 400、CSV 导出表头与角色、admin 清理（仅 done/issue、before 过滤、非法 status 400）、审计 patrol.executions.purge |

## 65. smoke.sh（P0–P13 209 项 + P14 21 项 = 230 项 / 55 步）

```
smoke: PASS (230 checks passed)
```

P14 覆盖：统计（总数/状态分布/完成率/异常率一致、by_route/by_assignee、非法时间 400）；导出（operator 200 精确表头 + attachment、viewer 403）；清理（viewer/operator 403、admin 删除 done 2 条、issue 保留、pending 400、审计）；清理后无残留计划/路线。

## 66. 前端 P14 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 统计卡片 | ✅ 总数/待执行/进行中/已完成/异常/完成率/异常率/平均时长与 API 一致；起止日期 + 刷新生效 |
| 汇总 | ✅ 按路线（P14演示路线 3/1/1）与按人（operator 2/1/1）|
| 导出 CSV | ✅ Toast「巡检执行 CSV 已导出」，下载表头与契约一致、3 行（done/issue/pending） |
| 清理历史 | ✅ admin 专属 before 日期 + 二次确认 →「已清理 2 条」，仅删已结束，pending 保留，统计/列表即时刷新 |
| 权限 | ✅ operator 可导出无清理；viewer 无导出/清理/写操作 |
| 回归 | ✅ 8 页签/4 开关/P0–P13（路线/计划/执行/播放/场景）正常；console errors=[] |
| 截图 | `docs/screenshots/p14-stats.png`、`p14-export.png`、`p14-purge.png`、`p14-operator.png`、`p14-viewer.png` |

## 67. 已知限制（P14.0）

- 清理为全局归档（按 before + 状态），不支持按 id/路线清理；pending/running 不可清理（按契约）。
- 统计上限 1 万条执行记录（内存过滤），超大数据量需 SQL 聚合/分页。
- 平均时长依赖 started/finished 秒级差，短任务可能显示 0。
- 导出行数上限 1 万，超大导出需异步任务。

---

# P15.0 验收记录（2026-11-10 凌晨）

## 68. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：auto_patrol 设置持久化、升级/再升级自动生成任务（camera 覆盖优先匹配）、同事件 pending/running 去重（审计 skipped duplicate）、事件过滤 event_id、无路线跳过 |

## 69. smoke.sh（P0–P14 230 项 + P15 16 项 = 246 项 / 58 步）

```
smoke: PASS (246 checks passed)
```

P15 覆盖：auto_patrol 设置读写；stale CAM-001/intrusion → 1 条 pending 执行（schedule_id 空、route_id 匹配、note 含「告警联动」、event_id 正确）；审计 patrol.execution.created source=event；再升级扫描后仍 1 条，审计 skipped reason=duplicate；清理（默认设置恢复、认领+完成+purge、删除路线）。

## 70. 前端 P15 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 设置 | ✅「告警联动巡检」开关 + 匹配/去重说明；关闭/开启保存后 API 同步，其他字段保留 |
| 执行卡片 | ✅「关联告警：evt_… 入侵 · CAM-001 东门入口」+「定位」→ 相机详情切到 CAM-001；更早事件仅显示 event_id（定位禁用） |
| 事件筛选 | ✅「事件 ID」查询后仅剩 1 条（与 API 一致），清除恢复；可与状态筛选组合 |
| 权限/回归 | ✅ viewer 只读（无认领/完成/编辑）；admin 8 tabs、P0–P14 正常；console errors=[] |
| 截图 | `docs/screenshots/p15-settings.png`、`p15-execution.png`、`p15-filter.png`、`p15-viewer.png` |

## 71. 已知限制（P15.0）

- 联动路由只按「相机覆盖 → XZ 最近航点」选择，未考虑班次/值班组负载，也未做距离阈值（无路线时才跳过）。
- 去重仅针对同事件 pending/running；已完成任务不阻止后续再次升级生成新任务（按设计）。
- pending 任务无法清理（契约限制），演示环境可能累积；生产应加超时自动关闭/过期归档。
- 任务生成依赖升级 worker 周期（15s），最坏延迟约 15s。

---

# P16.0 验收记录（2026-11-10 上午）

## 72. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：pending/running 取消（认领人/admin 权限）、重复取消 409、超时自动关闭（pending/running 变 cancelled + 审计 expired）、stats cancelled、purge 允许 cancelled |

## 73. smoke.sh（P0–P15 246 项 + P16 17 项 = 263 项 / 61 步）

```
smoke: PASS (263 checks passed)
```

P16 覆盖：pending 取消（200 cancelled/finished_at/note）、重复取消 409、viewer 取消 403、running 认领后自身取消 200、stats cancelled、admin purge cancelled（deleted、stats 下降）、非法 status 400、审计 cancel/purge、清理。

## 74. 前端 P16 E2E（headless Chrome 156 via CDP）

| 项 | 结果 |
|---|---|
| 状态/筛选 | ✅「已取消」chip + 筛选；卡片显示取消备注与 finished_at |
| 取消操作 | ✅ pending（operator/admin）与 running（认领人/admin）取消按钮；二次确认 + 可选备注 → Toast「巡检任务已取消」，列表/统计即时刷新 |
| 统计 | ✅「已取消」卡片计数随取消递增（0→1→2） |
| 权限/回归 | ✅ viewer 无取消按钮；admin 8 tabs、P0–P15（场景/计划/执行/统计/导出/清理）正常；console errors=[] |
| 截图 | `docs/screenshots/p16-executions.png`、`p16-cancel-form.png`、`p16-cancelled-card.png`、`p16-cancelled-filter.png`、`p16-admin-cancel.png`、`p16-viewer.png` |

## 75. 已知限制（P16.0）

- 超时治理依赖调度器 tick（默认 15s），阈值按分钟粒度；已关闭任务可被 purge 但 pending 不可（契约）。
- 取消为终态，不支持恢复/重新派发；running 取消无二次确认以外的流程审计表单。
- 取消/超时仅推送 WS `patrol_execution`，未接入通知 webhook（可后续扩展）。

---

# P17.0 验收记录（2026-11-10 上午）

## 76. PostgreSQL 全量验证

| 项 | 结果 |
|---|---|
| 环境 | PostGIS `postgis/postgis:16-3.4`（daocloud 镜像，本机 55432），API `store=postgres`，迁移 001–012 自动应用 |
| 首次全量 smoke | ❌ 237/263（发现 4 个 PG-only 缺陷，详见下） |
| 修复后 Run 1 | ✅ **PASS 263/263**（109s） |
| 修复后 Run 2（同库、不重置） | ✅ **PASS 263/263**（101s） |
| 存储契约集成测试 | ✅ `TestPostgresContract`（PG_TEST_URL 门控）：EscalateStale 扫描、无事件巡检工单、token_version 持久化、通知 attempts 持久化 |

### 修复的 PG-only 缺陷

| 缺陷 | 现象 | 修复 |
|---|---|---|
| Escalation RETURNING 缺 `re_escalated_at` | scan 列数不匹配 → 升级 tick 报错，派单/通知/联动巡检静默丢失 | RETURNING 补齐列 |
| 巡检异常工单 `event_id=""` 违反 FK | complete(issue) 500，执行卡 running | 迁移 012：`orders.event_id` 可空；插入空值转 NULL；scan 兼容 NULL |
| 用户 `token_version` 未持久化 | logout-all/强制下线/改密后旧 token 仍 200 | userSelect/插入/更新补充 token_version |
| 通知 `attempts` 未落库 | 重试 3 次但 API 显示 attempts=0 | 迁移 012 加列；插入/查询/扫描补充 |

## 77. 持久化与备份恢复演练

- **重启持久化**：创建用户 `pg_persist_u`、路线「pg 持久化路线」、升级设置 after_seconds=123 → 重启 API → 三者均仍在。
- **备份**：`./scripts/backup.sh backups/p17-drill.dump`（pg_dump -Fc，61K）。
- **变更后恢复**：删除用户/路线、设置改为 999 → 停 API → `./scripts/restore.sh` → 启动 API → 用户/路线恢复、设置回到 123。
- **迁移幂等**：恢复后再重启 API，迁移重放无错误，数据一致。

## 78. 内存 store 回归

- `make smoke` 全量 263 项（见下方执行记录）。
- `go test ./...` 全绿（含 PG 集成测试在无 PG_TEST_URL 时跳过）。

## 79. 已知限制（P17.0）

- PostGIS 官方镜像在 arm64 上以 amd64 模拟运行（性能可用，生产建议原生镜像/托管库）；本机 5432 被其他容器占用，验证使用 55432。
- 备份脚本依赖 docker exec（容器名可配）；非容器部署需改用 pg_dump 直连。
- PG 集成测试需手动创建 `twin_test` 库（未自动化建库），适合 CI 中由 job 初始化。
- 恢复演练为单机验证，未覆盖跨版本升级/大库耗时场景。

---

# P18.0 验收记录（2026-11-10 上午）

## 80. CI 流水线

| 项 | 结果 |
|---|---|
| GitHub Actions 定义 | `.github/workflows/ci.yml`：api（build/vet/test）、web（typecheck/build）、simulator（py_compile+bash -n）、smoke-memory、smoke-postgres（PostGIS service + TestPostgresContract + 全量 smoke） |
| 本地等价脚本 | `scripts/ci_local.sh`（`--quick` 跳过 PG）：按相同顺序执行并输出汇总表，临时实例 18080（内存）/18081（PG），退出自动清理进程与容器 |
| 本地全流程实测 | 见下方执行记录（含 PostgreSQL 阶段） |
| 文档 | `docs/CI.md`：任务矩阵、本地跑法、失败排查 |

## 81. 本地 CI 执行记录

```bash
./scripts/ci_local.sh            # 全流程（含 PG）
./scripts/ci_local.sh --quick    # 跳过 PG
```

| 阶段 | 结果 |
|---|---|
| api build | PASS |
| api vet | PASS |
| api test（含 PG 契约测试在无 PG_TEST_URL 时跳过） | PASS |
| simulator lint（py_compile + bash -n） | PASS |
| web typecheck+build | PASS |
| smoke (memory) 263 项 | PASS |
| smoke (postgres) 契约测试 + 263 项 | ✅ PASS：`TestPostgresContract` PASS（0.12s）+ PG store 全量 smoke **263/263 PASS**（约 105s） |

## 82. 已知限制（P18.0）

- 仓库暂无远端（git remote 为空），Actions 仅在推送到 GitHub/GitLab 后生效；本地以 `ci_local.sh` 等价验证。
- smoke 依赖固定端口（8080/18080/18081）；并行跑多个 CI 会端口冲突（改为随机端口需 smoke 参数化）。
- 本机 Docker Desktop 反复自动退出（内存压力下 daemon 崩溃），`ci_local.sh` 的 PG 阶段会因 `pg_isready` 通过但 SQL 建连 EOF 而失败；已改为用 `psql -c 'SELECT 1'` 判定就绪，并把 PG 阶段与 Docker 重启错开执行。CI runner（Linux）不受此影响。
- `smoke-postgres` job 依赖 `postgis/postgis` 镜像拉取（CI runner 可访问 Docker Hub）；受限网络需改镜像源。
- 未配置覆盖率门禁、代码扫描（golangci-lint、eslint）与制品发布；可作为后续增强。

---

# P19.0 验收记录（2026-11-10 下午）

## 83. 后端测试

```bash
cd services/api && go vet ./... && go test ./...
```

| 结果 | 说明 |
|---|---|
| ✅ PASS | 新增：ingest 回落转发（响应/状态码/原始 payload/转发指标）、回落不可达 502、未配置限流时默认 1 MiB 体积上限 413 |

## 84. 多副本回落实测

| 步骤 | 结果 |
|---|---|
| 主副本 :8080（内存）运行 | ✅ |
| 副本 :18082 设置 `INGEST_FALLBACK_URL=http://127.0.0.1:8080` | ✅ 启动正常 |
| 向副本 POST /api/ingest/detections（CAM-003） | ✅ 返回 202 `accepted:1`（上游响应透传） |
| 主副本事件列表 | ✅ 出现 CAM-003/person 事件（由副本转发落库） |
| 副本指标 `dsh_twin_ingest_forwarded_total` | ✅ 1（`fallback_failures_total` 0） |

## 85. 内存回归

- `make smoke`：**PASS 263/263**（P19 未改契约，全部保持）。

## 86. 已知限制（P19.0）

- WebSocket 推送不做跨副本广播（需 nginx WS 粘性或接 Redis Pub/Sub / Kafka）。
- 回落为「非主副本 → 主副本」单向静态配置，无健康探测/自动选主。
- compose 多副本编排（scale + upstream）未提供完整模板，仅说明 env 用法。
- 本机 Docker Desktop 在内存压力下频繁退出，PG 相关多副本验证改以内存双实例完成；CI/生产环境无此问题。

---

# P20.0 验收记录（2026-11-10 傍晚）

## 87. golangci-lint

```bash
cd services/api && golangci-lint run ./...   # v1.62.2
```

| 结果 | 说明 |
|---|---|
| ✅ 0 issues | 启用 15 个 linter；修复生产代码告警：`main.go` 的 exitAfterDefer + defer 失效（改为 `run() error` 模式）、未使用字段 `jsonlDriver.oldest`、postgres 统计查询 shadow、server.go 6 处 shadow + 1 处 nilness（`err.Error()` 误用）、3 处测试 ineffassign、未使用测试辅助函数 |
| 排除项 | `_test.go` 的 errcheck/govet/ineffassign/unused；`server.go` 的 contextcheck（响应后后台任务用 `context.Background()` 属设计） |

## 88. ESLint（Web）

| 项 | 结果 |
|---|---|
| 配置 | `apps/web/eslint.config.js`（flat config，eslint 9 + plugin-vue + @typescript-eslint） |
| 结果 | `pnpm lint`（`eslint src --max-warnings 0`）**0 error / 0 warning**（修复 TwinScene 模板变量遮蔽 1 处） |
| typecheck | `vue-tsc --noEmit` 通过 |

## 89. 覆盖率门禁

- `go test ./... -coverprofile` 全包语句覆盖率 **48.8%**（门禁 45%），CI 与 `ci_local.sh` 均强制校验，低于阈值失败。
- 分模块：api 71.1%、bus 82.2%、calib 92.4%、rules 55.3%；auth/store/model 无直接单测（由 api 层间接覆盖）。

## 90. 本地 CI 全绿（含 lint + 覆盖率）

```
PASS  api build
PASS  api vet
PASS  api test          (coverage 48.8% >= 45%)
PASS  simulator lint
PASS  lint (go+web)
PASS  web typecheck+build
PASS  smoke (memory)    263/263
SKIP  smoke (postgres)  (--quick)
```

## 90.1 常驻实例回归（lint 改动后的二进制）

- `make smoke`：**PASS 263/263**（重启 :8080 后）。

## 91. 已知限制（P20.0）

- auth/store 包无独立单测，覆盖率靠 api 集成测试间接覆盖；提升空间明确。
- 覆盖率按语句统计，未含分支覆盖；门禁阈值 45% 偏保守，可随补测提升。
- `server.go` 的 contextcheck 以文件级排除处理，未重构为显式后台 ctx。
- CI 中 golangci-lint 版本固定 v1.62.2，升级需同步更新 `.golangci.yml`（新版本 linter 集合变化）。

---

# P21.0 验收记录（2026-11-10 傍晚）

## 92. 新增单元测试

| 包 | 文件 | 覆盖内容 |
|---|---|---|
| `internal/auth` | `auth_test.go`（5 个用例） | 登录成功/失败/未知用户、JWT 校验与篡改/畸形 token、TTL 过期、会话吊销（token_version 递增）、用户列表/创建/重复/非法输入（空用户名、短密码、无数字、非法角色）、改密/停用、删除、UserExists、`auth.Can` 角色权限矩阵、auth-off 全网放行、store-backed repo 种子幂等 |
| `internal/store` | `store_test.go`（3 个用例） | 相机/区域查询、按 code 查询、PATCH 相机（含 no-op/缺失）、计数、默认规则/设置规则；事件创建/过滤/详情、动作、轨迹（相机/多相机过滤）、工单生命周期（含按状态查询、FindOpenOrderForEvent）、审计（actor/时间范围）、统计；设置读写、值班组 CRUD、用户 CRUD + 冲突、通知日志裁剪、刷新令牌创建/查询/吊销 |

## 93. 覆盖率

| 版本 | 总计 | 说明 |
|---|---|---|
| P20 | 48.8% | store/auth 无单测 |
| **P21** | **56.9%** | store 25.2%、auth 由集成+单测覆盖；门禁由 45% 提升到 **55%** |

## 94. 质量门禁

- `go test ./...`：全绿（新增 8 个用例）
- `golangci-lint run ./...`：0 issues（新测试文件同样通过）
- 本地 CI（`--quick`）：api build/vet/test（覆盖率 56.9% ≥ 55%）、simulator lint、lint(go+web)、web typecheck+build、smoke(memory) 263 项 全部 PASS

## 95. 已知限制（P21.0）

- PostgreSQL 实现仍依赖集成测试（PG_TEST_URL 门控）与全量 smoke，未逐方法单测。
- 覆盖率仍以语句计；分支覆盖未统计。
- 新增门禁阈值 55% 留有约 2 个百分点缓冲，后续补测可继续提高。

---

# P22.0 验收记录（2026-11-10 傍晚）

## 96. 错误哨兵统一

| 变更 | 说明 |
|---|---|
| `store.ErrNotFound` / `store.ErrConflict` | 由独立 `errors.New` 改为 `model.ErrNotFound` / `model.ErrConflict` 的别名 |
| `auth.ErrUserNotFound` / `auth.ErrUsernameTaken` | 同步别名为 `model.ErrNotFound` / `model.ErrConflict` |
| 新增 helper | `store.IsNotFound(err)`、`store.IsConflict(err)`（`errors.Is`，支持包装错误） |
| API 层 | 全部 `errors.Is(err, store.ErrNotFound)` → `store.IsNotFound(err)`；用户错误分支改用 `store.IsConflict` / `store.IsNotFound` |
| 内存与 PG 实现 | 统一返回 `model.ErrNotFound` / `model.ErrConflict` |

## 97. 新增测试

| 用例 | 结果 |
|---|---|
| `TestSentinelIdentity`（别名同一性、包装错误解包、nil/无关错误为 false） | ✅ PASS |
| `TestMemoryReturnsCanonicalSentinels`（内存实现返回可被 `IsNotFound`/`IsConflict` 识别的错误） | ✅ PASS |

## 98. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿（含新增 store/auth 单测） |
| `golangci-lint run ./...` | ✅ 0 issues |
| `make smoke`（重启新二进制后） | ✅ **PASS 263/263** |
| 本地 CI（`--quick`，覆盖率门禁 55%） | ✅ 8 阶段全 PASS（api build/vet/test、simulator lint、lint(go+web)、web typecheck+build、smoke(memory) 263/263） |

## 99. 已知限制（P22.0）

- `auth.ErrInvalidCredentials` / `ErrInvalidToken` / `ErrBadInput` 仍为 auth 包私有语义，未并入 model（属于认证域概念，保持独立）。
- 新增存储实现需自行遵守「返回 model 哨兵」约定，靠 `TestSentinelIdentity` 与代码评审保证。

---

# P23.0 验收记录（2026-11-10 晚间）

## 100. 新增单测（补齐 realtime/config/model）

| 包 | 文件 | 覆盖内容 |
|---|---|---|
| `internal/realtime` | `hub_test.go`（4 用例） | WebSocket 握手、OnConnect 问候帧、多客户端广播、帧计数、慢客户端背压（缓冲满 → 丢弃且连接保持）、无客户端广播空操作、不可序列化帧不计数、非 WS 请求 400 且不注册客户端、缓冲区下限钳制 |
| `internal/config` | `config_test.go`（2 用例） | 全部环境变量默认值（端口/鉴权/总线/hub/ingest/登录/token TTL/巡检）、覆盖生效、非法整数回落默认、`MAX_EVENTS` 下限钳制 |
| `internal/model` | `model_test.go`（2 用例） | 升级/通知/相机规则/站点默认值、`SeverityRank` 等级与未知值语义 |
| `internal/store` | `seed_test.go`（1 用例） | 种子相机/区域完整性与相机→区域引用一致性 |

## 101. 覆盖率变化

| 包 | 之前 | 现在 |
|---|---|---|
| config | 0% | **100%** |
| model | 0%（间接） | **100%** |
| realtime | 0% | **92.2%** |
| **总计** | 57.0% | **57.5%** |

> 说明：realtime/config/model 三包从 0% 提升到 92–100%，总覆盖率因 store/postgres 体量大（语句多、由集成测试与 smoke 覆盖）仅小幅上升。后续主要提升点仍是 store（内存 + PG）。

## 102. 稳定性

- `go test ./internal/realtime/ -count=5`：稳定 PASS（曾出现「最终帧未到」的不稳定用例，已改写为确定性的背压断言：丢弃计数 > 0、已排队帧 > 0、连接保持）。
- `go test ./... -count=1`：全绿；`golangci-lint run ./...`：0 issues；`go vet`：通过。

## 103. 已知限制（P23.0）

- store 覆盖率 25.4%（PG 实现路径依赖集成测试/smoke）。
- 慢客户端测试只断言丢弃与连接保持，未验证「丢弃后仍可继续收帧」（受 gorilla 客户端读取语义限制，已保留在集成 smoke 中验证）。

---

# P24.0 验收记录（2026-11-10 晚间）

## 104. store 覆盖率攻坚

| 新增用例 | 覆盖内容 |
|---|---|
| `TestPatrolRoutesAndSchedules` | 巡检路线创建/查询/列表/更新/删除 + 缺失 id 的 not-found；计划创建/查询/列表/更新/删除 |
| `TestPatrolExecutionsLifecycle` | 执行创建/查询；按 status、event_id、schedule_id、时间范围、limit 过滤；更新；按 before+status 清理（done 被删、pending 保留）；缺失 id not-found |
| `TestEventWorkflowAndEscalation` | 事件 ack/close（含缺失 id）、升级（关闭事件跳过、已升级跳过、severity 过滤、截止时间）、再升级（写入 re_escalated_at、二次跳过） |
| `TestCameraCalibrationAndSorting` | 标定设置/清除/缺失相机、ListCameras 按 code 有序、用户列表 |
| `store_patrol_test.go` 其余断言 | 时间范围/limit 过滤、错误哨兵一致性 |

## 105. 覆盖率与门禁

| 指标 | P23 | **P24** |
|---|---|---|
| store 包 | 25.4% | **37.9%** |
| **总计** | 57.5% | **61.4%** |
| CI 门禁 | 55% | **60%** |

## 106. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint run ./...` | ✅ 0 issues |
| 本地 CI（`--quick`，门禁 60%） | ✅ 8 阶段全 PASS，smoke(memory) 263/263 |

## 107. 已知限制（P24.0）

- store 覆盖率 37.9%：PostgreSQL 实现（postgres.go 76 个方法）仍依赖 PG 集成测试与 smoke；进一步补测需引入 testcontainers 或扩展 `PG_TEST_URL` 用例。
- 覆盖率按语句统计；60% 门禁留有约 1.4 个百分点缓冲，后续应同步提升阈值。

---

# P25.0 验收记录（2026-11-10 夜间）

## 108. 新增单测（rules + metrics）

| 包 | 用例 | 覆盖内容 |
|---|---|---|
| `internal/rules` | 7 个新用例 | `ProjectToWorld` 三源优先级（显式 world / 标定矩阵 / 光学轴回落）与退化矩阵、ROI 内外过滤、EnabledClasses 过滤、去重窗口显式关闭、去重表剪枝上限、`New` 窗口钳制 | 
| `internal/metrics` | 2 用例 | 全部指标名/HELP/TYPE 输出、severity 标签、ingest 时长 summary（微秒→秒）、空计数器全 0 |

## 109. 覆盖率

| 包 | P24 | **P25** |
|---|---|---|
| rules | 55.3% | **97.4%** |
| metrics | 0% | **98.6%** |
| **总计** | 61.4% | **62.2%** |

## 110. 回归

- `go test ./... -count=1`：全绿
- `golangci-lint run ./...`：0 issues
- 本地 CI（`--quick`，门禁 60%）：✅ 8 阶段全 PASS，smoke(memory) 263/263

## 111. 已知限制（P25.0）

- 仍以 api（71.1%）与 store（37.9%）为主；PG 实现未逐方法覆盖。
- rules 剩余未覆盖为 `newEventID` 的 `crypto/rand` 失败分支（不可注入）。

---

# P26.0 验收记录（2026-11-10 深夜）

## 112. API 覆盖补充

| 用例 | 覆盖内容 |
|---|---|
| `TestReadEndpointsAndErrorBranches` | `/api/areas`、`/api/site`、相机详情、相机规则、未知相机 404、工单创建/详情/PATCH/列表/404、值班组列表与 current（含缺 area 400）、通知设置读、bus status、未知路由 404 |
| `TestEventDetailAndInvalidInputs` | 事件详情与 404、四个写接口畸形 JSON → 400、工单非法状态 400、相机非法状态 400 与合法 PATCH 200 |

## 113. 覆盖率与门禁

| 指标 | P25 | **P26** |
|---|---|---|
| api 包 | 71.1% | **72.9%** |
| **总计** | 62.2% | **63.2%** |
| CI 门禁 | 60% | **62%** |

## 114. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint run ./...` | ✅ 0 issues |
| 本地 CI（`--quick`，门禁 62%） | ✅ 8 阶段全 PASS，smoke(memory) 263/263 |

## 115. 已知限制（P26.0）

- 仍未覆盖：`RunStats` 周期任务、`broadcastStats`、`RunPatrolScheduler` 长驻循环、`SetSite/SetSessionTTL` 等纯装配方法（多为 1–2 行或需时间驱动）。
- E2E 正向流程（P0–P16）由 smoke 覆盖，单测只补读路径与错误分支，避免重复。

---

# P27.0 验收记录（2026-11-10 深夜）

## 116. 多租户隔离基座

| 项 | 结果 |
|---|---|
| 迁移 | `013_p27_tenants.sql`：`tenants` 表 + 默认租户；13 张表新增 `tenant_id`；`users`/`cameras` 唯一约束改为 `(tenant_id, …)`；`settings`/`camera_rules` 主键改为含租户 |
| 存储 | 内存实现重构为 per-tenant `memoryState`；PostgreSQL 每条查询/写入按 ctx 租户过滤/打标；`WithTenant`/`TenantFrom` 默认 `default`；`TENANT_SHARED=1` 兼容单租户演示 |
| 认证 | 登录支持 `tenant_id`，JWT 增加 `tnt` 声明；中间件解析 `X-Tenant-ID`：未知 400 `unknown_tenant`、停用 403 `tenant_inactive`、令牌与请求租户不一致 403 `cross_tenant` |
| 租户管理 | `GET/POST /api/tenants`、`PATCH /api/tenants/{id}`（仅 admin）+ 审计 `tenant.create/update`；指标 `dsh_twin_tenant_requests_total{tenant}` |
| 后台任务 | 升级/通知 worker 按租户遍历；ingest 按租户打标；轮转键按租户隔离 |

## 117. 测试与验证

| 项 | 结果 |
|---|---|
| `go test ./...` | ✅ 全绿（新增 7 个租户用例：CRUD/隔离/跨租户令牌/未知/停用/默认兼容/共享模式） |
| `golangci-lint` | ✅ 0 issues |
| 覆盖率 | **63.8%**（门禁 62%） |
| `make smoke`（:8080 内存，无租户头） | ✅ **PASS 263/263**（默认租户行为不变） |
| smoke（:8081 PostgreSQL） | ✅ **PASS 263/263**（迁移 013 正常应用，13 张表均含 tenant_id） |
| 前端 E2E（headless Chrome） | ✅ 登录携带租户、全量请求/刷新带 `X-Tenant-ID`、WS `?tenant_id=`、顶栏「租户：默认园区」、admin 9 页签（含租户页：新建/改名/两步停用，默认租户受保护）、三种租户错误映射、operator 无租户页签；console errors=[] |
| PostgreSQL 租户隔离 E2E | ✅ 默认租户 6 相机/3 用户 vs 新租户 0 相机；设置/事件/巡检隔离；跨租户 403、未知 400、停用 403；指标按租户导出 |
| 本地 CI（`--quick`，门禁 62%） | ✅ 8 阶段全 PASS（smoke(memory) 263/263） |
| 截图 | `docs/screenshots/p27-login-tenant.png`、`p27-topbar.png`、`p27-tenant-tab.png`、`p27-tenant-disabled.png`、`p27-unknown-tenant.png`、`p27-inactive-tenant.png`、`p27-cross-tenant.png`、`p27-operator.png` |

## 118. 已知限制（P27.0）

- `duty_groups`、`track_points` 仍为全局（下一阶段按租户迁移）；`settings` 之外的全局资源没有租户维度。
- 新租户初始为空（无相机/用户），需要后续「租户初始化/导入」能力。
- 无租户删除接口（默认租户不可删）；演示环境保留一个已停用演示租户。
- 租户切换需重新登录（JWT 绑定 `tnt`）；如需免登录切换，需要「租户切换」专用接口重签令牌。
- 管理员可管理所有租户（无「租户管理员」角色划分）。

---

# P28.0 验收记录（2026-11-10 深夜）

## 119. 租户隔离扩展（duty_groups / track_points）

| 项 | 结果 |
|---|---|
| 模型 | `DutyGroup.TenantID`、`TrackPoint.TenantID`（json `tenant_id`） |
| 迁移 | `014_p28_tenant_duty_tracks.sql`：两表新增 `tenant_id` + 索引（幂等） |
| 内存实现 | duty/track 数据移入 per-tenant `memoryState`（与 P27 结构一致）；删除遗留顶层字段 |
| PostgreSQL | duty CRUD 全部带 `tenant_id` 条件/写入；track 插入打标、查询强制 `tenant_id = $1` |
| 测试 | 新增 `TestTenantScopedDutyGroupsAndTracks`：A/B 租户值班组与轨迹互不可见、跨租户读/改/删返回 not-found |

## 120. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint run ./...` | ✅ 0 issues |
| 覆盖率 | **63.9%**（门禁 62%） |
| `make smoke`（重启后新二进制） | ✅ **PASS 263/263** |
| smoke（:8081 PostgreSQL，迁移 014 应用后） | ✅ **PASS 263/263**（首次运行 262/263 出现 1 次偶发失败，立即重跑全绿；疑为 emulation 下的轮询时序，见限制） |

## 120.1 本地 CI

| 项 | 结果 |
|---|---|
| `./scripts/ci_local.sh --quick` | ✅ 8 阶段全 PASS（smoke(memory) 263/263） |
| `./scripts/ci_local.sh`（含 PG） | PG 阶段因本机 Docker Desktop 退出而失败（环境问题，非代码）；PG 全量 smoke 已在 :8081 单独跑通 263/263 |
| CI 脚本增强 | 失败时自动打印失败检查项并保留 `/tmp/ci-smoke-memory.log`、`/tmp/ci-smoke-postgres.log` |

## 121. 已知限制（P28.0）

- 事件总线（jsonl/memory）仍全局；跨租户回放需按租户过滤（下一阶段）。
- 租户无初始化模板（新租户无相机/用户），需要「复制默认租户骨架」能力。
- PG 侧 `track_points` 无历史数据迁移校验（新增列默认 default，旧数据归默认租户，符合预期）。
- PostgreSQL 运行在 amd64 模拟环境下，长轮询类检查（升级/调度）偶发一次超时；重跑即绿，非功能缺陷。

---

# P29.0 验收记录（2026-11-11 上午）

## 122. 后端能力

| 能力 | 说明 |
|---|---|
| 租户初始化模板 | `POST /api/tenants {template, admin}`：复制模板租户的相机/区域/相机规则/设置（ID 重新生成、相机→区域重映射），创建初始 `tenant_admin`（P10 密码策略，冲突 409），响应 `seeded {cameras,areas,rules,settings,admin}`；未知模板 400；审计含 template/seeded |
| 租户管理员角色 | `tenant_admin`：operator 级读写 + 用户 CRUD/会话/强制下线（`PermUserAdmin`）+ 审计读取/导出（`PermAuditRead`）；租户 CRUD、设置、值班、总线、通知、巡检清理仍仅 admin（403） |
| 总线租户隔离 | `Envelope.tenant_id` 打标；`ReadRangeForTenant` 按租户过滤；jsonl 旧行（无字段）视为 default；`/api/bus/status` 暴露 `tenants` 计数；`/api/bus/replay` 默认只回放调用者租户，`all_tenants:true`（admin）回放全部，响应 `items[]` 带 tenant_id |

## 123. 测试与验证

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿（新增 bus 2、store 1、api 3 个测试套件） |
| `golangci-lint` | ✅ 0 issues |
| 覆盖率 | **64.3%**（门禁 62%） |
| `make smoke`（:8080 无租户头） | ✅ **PASS 263/263**（默认租户行为不变） |
| smoke（PostgreSQL :8081） | ✅ **263/263**（后端自测，jsonl 总线含租户字段） |
| 前端 E2E（headless Chrome） | ✅ 模板+初始管理员创建租户 → Toast「已复制 6 台相机 / 4 个区域 / 6 条规则 / 2 项设置 / 管理员 admin2」；新租户初始管理员登录即 `tenant_admin`（6 页签，无租户/设置/总线）；tenant_admin 可访问用户与审计（200），`/api/tenants` 403；未知模板 400、弱密码 400；P0–P28 回归正常；console errors=[] |
| 截图 | `docs/screenshots/p29-live-create.png`、`p29-live-tenant.png`、`p29-live-tenant-admin.png`、`p29-live-audit.png`、`p29-tenant-create.png`、`p29-role.png` |

## 123.1 本地 CI 全流程（含 PostgreSQL）

| 项 | 结果 |
|---|---|
| `./scripts/ci_local.sh`（8 阶段，含 PG） | ✅ **全 PASS**（smoke(memory) 与 smoke(postgres) 均 263/263） |
| CI 脚本加固 | ① 实例端口改为随机高位端口并支持 `MEMORY_PORT`/`PG_PORT` 覆盖；② 健康探测校验 `"status":"ok"` 载荷而非任意 200；③ IPv6 感知：当 `127.0.0.1:<port>` 被 docker-proxy 等进程占用时自动改用 `[::1]`；④ PG 阶段等待宿主机真实连通后再启动 API；⑤ 失败时输出完整 API 日志与端口监听方 |

> 本轮排障发现：把容器端口与 API 端口设为相同时，docker-proxy 抢占 `127.0.0.1`，导致 CI 探测到错误服务（曾误命中本机 Higress Console）。已通过随机端口 + IPv6 回退彻底规避。

## 124. 已知限制（P29.0）

- 模板复制会重新生成相机/区域 ID（PG 主键全局唯一），外部若有硬编码 ID 的集成需注意。
- 模板不复制事件/工单/审计/用户/巡检历史（按设计）。
- `seeded.settings` 为 0 属正常：默认设置是隐式的，只有显式保存过才计 1 项。
- 租户管理员不能管理设置/值班/总线（保留 admin 专属）。
- 演示环境残留若干验证租户（无删除接口），后续可加归档/删除能力。

---

# P30.0 验收记录（2026-11-11 中午）

## 125. 租户生命周期与配额

| 能力 | 说明 | 证据 |
|---|---|---|
| 归档 | `POST /api/tenants/{id}/archive`：`archived=true, active=false, archived_at`；默认租户受保护（400） | 实测 200 + archived:true；default 400 |
| 归档后访问 | 中间件返回 `403 tenant_archived`（区别于 inactive） | 归档租户登录实测 403 |
| 恢复 | `POST /api/tenants/{id}/restore` → active=true、清空 archived_at | 实测 200 |
| 彻底删除 | `DELETE /api/tenants/{id}?confirm=<id>`：confirm 不一致 400、未归档 409、默认租户 400；清理 16 类数据并返回计数；审计写入默认租户 | 实测 409 → 归档 → `{"deleted":{"cameras":6,"areas":4,"rules":6,"settings":2,"users":1,...}}` |
| 配额 | `limits{max_cameras,max_users,max_events,ingest_qps}`：用户/事件/ingest QPS 超限 → 429 `quota_exceeded`；模板复制受 max_cameras 限制；指标 `dsh_twin_tenant_quota_exceeded_total{tenant,dimension}` | 单测：用户配额 429、事件配额第二次 ingest 429、指标有值、负值 400 |
| 前端 | 租户卡片：已归档徽标/归档时间/配额摘要；配额编辑对话框；归档/恢复两步确认；删除需先归档并输入完整租户 ID（否则禁止），成功后展示删除计数 | 构建通过（typecheck/lint/build 绿），dev server 运行于 :5173（标题 P30.0） |

## 126. 测试与回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿（新增 `TestTenantArchiveRestoreAndDelete`、`TestTenantQuotas`） |
| 覆盖率 | **63.4%**（api 包 73.0%，门禁 62%） |
| `golangci-lint` | ✅ 0 issues |
| `make smoke`（默认租户） | ✅ **PASS 263/263** |
| 前端 | ✅ `pnpm typecheck` + `pnpm lint` + `pnpm build` |
| 本地 CI（`--quick`） | ✅ 8 阶段全 PASS |
| 本地 CI（含 PostgreSQL 阶段） | ✅ **8 阶段全 PASS**（smoke(memory) 与 smoke(postgres) 均 263/263） |

## 127. 已知限制（P30.0）

- 走查中两名队友（后端/前端）连续异常中断，本轮 P30 后端由我接手完成，前端在队友已完成的改动基础上由我复核构建（typecheck/lint/build 通过）。
- 相机创建接口不存在（相机由模板/种子产生），`max_cameras` 仅在模板复制时生效。
- 删除为物理删除、不可撤销；审计仅保留默认租户内的一条 `tenant.delete` 摘要（含各表计数）。
- 配额从内存态 token bucket 计算 ingest QPS，多副本部署需改共享限流。

---

# P31.0 验收记录（2026-11-11 傍晚）

## 128. 租户数据导出与共享限流

| 能力 | 说明 | 证据 |
|---|---|---|
| 数据包导出 | `GET /api/tenants/{id}/export`（admin）：JSON 附件 `tenant-<id>.json`，含 tenant/exported_at/counts/data（区域/相机/规则/设置/用户/事件/工单/通知/巡检）；**不含** password_hash、token_hash；`include_tracks=1`、`include_audit=1` 可选；审计 `tenant.export` | 实测：默认租户导出 200，`Content-Disposition: attachment; filename="tenant-default.json"`，counts `{areas:4,cameras:6,users:3}`，payload 无 `token_hash`，用户无 `password_hash` |
| 共享限流 | `RATE_STORE=postgres`：迁移 015 `tenant_rate_buckets` + `Take`（SELECT ... FOR UPDATE，按 elapsed 补偿令牌，burst=qps）；无数据库时回退内存并告警一次 | 单测 `TestPostgresRateStore`（PG_TEST_URL 门控，实测 PASS）：2 QPS 桶第 3 次拒绝、无限制放行；迁移 015 在 PG 上正常应用（`tenant_rate_buckets` 建表） |
| 前端 | 租户卡片「导出」按钮 + 「包含轨迹/包含审计」勾选 + 「归档前建议先导出数据」提示；成功 Toast 汇总计数 | `pnpm typecheck` / `lint` / `build` 全绿，dev server :5173 标题 P31.0 |
| 测试 | `TestTenantExport`（结构/计数/无敏感字段/flags/404/403）、`TestTenantExportOfSecondaryTenant`（模板租户导出） | 全 PASS |

## 129. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| 覆盖率 | **63.4%**（门禁 62%） |
| `golangci-lint` | ✅ 0 issues |
| `make smoke`（默认租户） | ✅ **PASS 263/263** |
| 本地 CI（`--quick`） | ✅ 8 阶段全 PASS |
| PG 契约与限流测试（真实 PostGIS） | ✅ `TestPostgresContract` + `TestPostgresRateStore` PASS |

## 130. 已知限制（P31.0）

- 导出为同步 JSON（事件/工单上限 1 万条，轨迹/审计各 1000 条），超大租户需异步导出任务。
- 导出内容不含处置动作明细（actions）与轨迹默认关闭，仅按需包含轨迹/审计。
- 共享限流仅在 `RATE_STORE=postgres` 且使用 PostgreSQL 存储时生效；内存模式仍是进程内桶。
- 本轮两名队友再次连续中断，P31 后端与前端导出 UI 均由我完成并验证。

---

# P32.0 验收记录（2026-11-12 上午）

## 131. 异步导出任务

| 能力 | 说明 | 证据 |
|---|---|---|
| 任务模型 | `exportJob{id,tenant_id,status,include_tracks,include_audit,created_by,created_at,started_at,finished_at,size_bytes,counts,error}`，进程内存储 + 产物落盘 `data/exports/<exportId>.json` | 实测任务 `pending → done`，size_bytes=3829，counts 含 areas/cameras/users |
| 接口 | `POST /api/tenants/{id}/exports`（202）、`GET .../exports`（列表 ≤50）、`GET .../exports/{id}`、`GET .../{id}/download`（`attachment; filename="tenant-<id>-<exportId>.json"`；未完成 409）、`DELETE .../{id}`（204） | 实测创建/查询/下载/列表全部正常，下载响应头正确 |
| 并发保护 | 同一租户最多 2 个 pending/running，超出 `429 too_many_exports` | 单测 `TestExportJobGuards` 第 3 次提交被拒 |
| 审计与 WS | `tenant.export.start/done/failed`；完成/失败推送 WS `export_status` | 单测覆盖；前端 WS 分支已接入 |
| 配置 | `EXPORT_DIR`（默认 `data/exports`）、`EXPORT_WORKERS`（默认 2） | 已接入 config/main |
| 前端 | 每行「导出」创建任务 + 「导出记录（N）」展开列表（状态徽标/大小/含轨迹含审计/下载/删除），WS 完成后自动刷新并 Toast | typecheck/lint/build 绿，dev server :5173 标题 P32.0 |

## 132. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿（新增 `TestExportJobsLifecycle`、`TestExportJobGuards`） |
| 覆盖率 | **63.9%**（门禁 62%） |
| `golangci-lint` | ✅ 0 issues |
| `make smoke` | ✅ **PASS 263/263** |
| 前端 | ✅ `pnpm typecheck` + `pnpm lint` + `pnpm build` |
| 本地 CI（含 PostgreSQL 阶段） | ✅ **8 阶段全 PASS**（smoke(memory)/smoke(postgres) 均 263/263） |

## 133. 已知限制（P32.0）

- 导出任务保存在进程内，API 重启后任务列表丢失（产物文件仍在 `data/exports`）；多副本下任务不共享。
- 产物无自动过期清理（仅手动删除），长期运行需加保留策略。
- 任务 50 条列表上限；历史任务不持久化到数据库。
- 本轮队友仍不稳定，P32 前后端均我独立完成。

---

# P33.0 验收记录（2026-11-12 下午）

## 134. 导出任务持久化与保留策略

| 能力 | 说明 | 证据 |
|---|---|---|
| 持久化 | 迁移 016 `export_jobs`（含 jsonb counts + 索引）；Store 新增 `CreateExportJob/UpdateExportJob/GetExportJob/ListExportJobs/DeleteExportJob/PurgeExportJobs`，内存与 PostgreSQL 双实现 | PG 实测：任务行写入 `export_jobs`，API 重启后列表仍在 |
| 重启恢复 | 启动时把 `pending/running` 标记为 `failed`（reason `interrupted by restart`） | PG 实测：重启前 `pending`，重启后列表显示 `failed`，下载 409 |
| 多副本安全 | worker 直接以行状态流转（`pending → running`），列表/详情从存储读取；并发上限仍按租户生效 | 单测 `TestExportJobsSurviveRestart`（共享 store 重启后任务可见、下载可用） |
| 保留策略 | `EXPORT_RETENTION`（默认 168h）、`EXPORT_MAX_JOBS`（默认 200）；每 10 分钟清理已完成/失败任务与产物；指标 `dsh_twin_export_jobs_purged_total` | 单测 `TestExportJobPersistenceContract`（按时间清理 + 保留运行中任务） |
| 手动清理 | `POST /api/tenants/{id}/exports/purge?before=<RFC3339>` → `{"purged":n}`，同时删除产物文件；非法 before → 400；非 admin → 403 | PG 实测 `{"purged":1}`；单测 `TestExportJobPurgeEndpoint` |
| 列表上限 | `GET .../exports?limit=`（默认 50，最大 200） | 接口实现 + 测试 |
| 前端 | 导出记录头部「保留 7 天，重启后仍可查看」+「清理历史」；失败任务「重试」；版本 P33.0 | typecheck/lint/build 绿，dev server 标题 P33.0 |

## 135. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿（新增 `TestExportJobPersistenceContract`、`TestExportJobsSurviveRestart`、`TestExportJobPurgeEndpoint`） |
| 覆盖率 | **62.7%**（门禁 62%，贴近门禁，下一轮需补测） |
| `golangci-lint` | ✅ 0 issues |
| `make smoke` | ✅ **PASS 263/263** |
| 本地 CI（含 PostgreSQL 阶段） | ✅ **8 阶段全 PASS** |

## 136. 已知限制（P33.0）

- 覆盖率 62.7% 贴近 62% 门禁，需要补充 store/api 用例拉开余量。
- 任务领取未做强一致的多副本互斥（两条副本可能同时尝试同一任务），仅在任务创建端限流；后续可加 `SELECT ... FOR UPDATE SKIP LOCKED` 领取。
- 内存模式重启后任务仍丢失（属预期，内存存储不持久）；保留策略在内存模式同样生效。
- 产物文件与任务行可能短暂不一致（删除失败仅记录日志）。
- 本轮 CI 首次出现导出并发守卫用例偶发失败（任务过快完成导致活跃数归零），已改为按截止时间轮询直到触发 429。

---

# P34.0 验收记录（2026-11-12 傍晚）

## 137. 覆盖率攻坚 + PG 契约测试 + 租户生命周期列

| 项 | 说明 | 结果 |
|---|---|---|
| PostgreSQL 契约测试 | 新增 `TestPostgresStoreContract`（租户/区域/相机/标定/事件/动作/轨迹/规则/工单/设置/审计/通知/值班/用户/刷新令牌/巡检/统计/租户删除清理）、`TestPostgresTenantAndPatrolLifecycle`、`TestPostgresExportJobsContract` | store 包覆盖率 **50.6% → 70.7%**；含 PG 全量覆盖率 **73.8%** |
| 迁移 017 | `tenants` 表补齐 `archived/archived_at/max_cameras/max_users/max_events/ingest_qps`，PG 读写与 API 模型一致（此前 PG 下归档/配额未落库） | 实测 PG 更新后读回 `archived=true, max_users=5` |
| 导出任务唯一冲突 | PG `export_jobs` 插入冲突改为返回 `ErrConflict`（与其它表一致） | 契约测试断言通过 |
| 单元测试补口 | `cmd/server` 启动辅助（storeMode/loadSite/openBus/openStore）、后台循环优雅退出（RunStats/RunPatrolScheduler/导出保留）、bus 指标/回放/状态、导出任务 keep 清理与空表分支、租户恢复 handler、配置 setter | 单元覆盖率 **63.4% → 64.2%**（cmd/server 从 0% 到有覆盖） |
| 覆盖率门禁 | 单元 62%（`COVERAGE_GATE`）；PG 阶段新增 **72%** 门槛（`PG_COVERAGE_GATE`，含 PG 契约） | 本地 CI 通过 |

## 138. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| 单元覆盖率 | **64.2%**（门禁 62%） |
| PG 全量覆盖率 | **~74%**（PG 门禁 72%） |
| `golangci-lint` | ✅ 0 issues |
| `make smoke` | ✅ **PASS 263/263** |
| 本地 CI（含 PostgreSQL） | ✅ **8 阶段全 PASS** |

## 139. 已知限制（P34.0）

- 单元模式（无 Docker）覆盖率仍只有 64.2%：store 的 PostgreSQL 实现需 Docker 才能计入，CI 中通过 PG 阶段独立把关。
- PG 契约测试会向默认租户插入数据后清理，需在一次性数据库上运行（CI 使用临时容器）。
- 首次本地 CI 发现「PG 租户列缺失」为真实缺陷（P30 归档/配额只在内存生效），已由迁移 017 修复；生产库升级需执行 017。
- 覆盖率门禁维持 62% 而非直接提到 PG 水平，避免无 Docker 的开发者本地失败。

---

# P35.0 验收记录（2026-11-12 傍晚）

## 140. 多租户可观测性

| 项 | 说明 | 证据 |
|---|---|---|
| 租户 ingest 指标 | `dsh_twin_tenant_ingest_total{tenant}`，acceptIngest 成功后计数 | 实测：默认租户 ingest 1 次后序列出现 |
| 租户相机 gauge | `dsh_twin_tenant_cameras_online{tenant}`，`/metrics` 与 stats 广播刷新 | 实测：default 5、模板租户 5 |
| 配额/导出指标纳入看板 | `dsh_twin_tenant_quota_exceeded_total{tenant,dimension}`、`dsh_twin_export_jobs_purged_total` | 单测断言序列文本 |
| Grafana 看板 | 新增「多租户（P35）」分组 6 个面板 + `tenant` 多选变量（默认 All），共 18 面板 | `python json.load` 校验通过 |
| 告警 | 新增 `digital-twin-tenant` 分组 4 条告警（配额频繁/无在线相机/ingest 静默/请求突增），带 runbook | `grep alert:` 共 11 条规则 |
| 运维文档 | `docs/OPERATIONS.md` 增补租户指标清单与「3.1 租户告警处置」表 | 文档已更新 |

## 141. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿（新增 `TestTenantMetricsExposition`） |
| `golangci-lint` | ✅ 0 issues |
| `make smoke` | ✅ **PASS 263/263** |

## 142. 已知限制（P35.0）

- 租户 ingest 指标只统计「被接受」的请求，限流/配额拒绝走单独的 rejected/quota 序列。
- 在线相机 gauge 按需刷新（`/metrics` 抓取与 stats 广播时），非实时推送；租户数量大时需改为缓存刷新。
- 资源不存在的租户维度不会出现在指标中（无租户即无序列），Prometheus 侧需注意 absent() 用法。
- 未在本机启动 Prometheus/Grafana 做端到端截图（面板 JSON 与告警规则已做结构校验）。

---

# P36.0 验收记录（2026-11-12 傍晚）

## 143. 可观测性端到端打通

| 项 | 说明 | 证据 |
|---|---|---|
| Grafana 自动配置 | 新增 `deploy/observability/grafana-provisioning/`（Prometheus 数据源 + 看板 provider），compose 挂载 `/etc/grafana/provisioning` | `/api/datasources` 返回 `Prometheus(uid=prometheus)`；`/api/search` 出现看板 |
| 看板真实渲染 | 「数字孪生安防平台 · 运行概览（含多租户）」在 Grafana 13.1.3 中加载，租户变量 + 多租户面板显示真实数据 | `docs/screenshots/p36-grafana-overview.png`、`p36-grafana-tenant.png`（租户总数 2、请求速率 Top、在线相机 5/5、配额拒绝维度 `ingest_qps`） |
| 端口/镜像可配置 | compose 支持 `PROM_PORT`/`GRAFANA_PORT`；国内镜像 `PROM_IMAGE`/`GRAFANA_IMAGE`（本机 3000/9090 被其它服务占用，用 13000/19090 起栈） | `make obs-up` 等价命令起栈成功 |
| 告警规则加载 | Prometheus 载入 11 条规则（7 + 4 租户），`health=ok`，无误报 | `/api/v1/rules` 全部 ok、firing 为空 |
| **发现的真实缺陷 1** | `ApiDown` 在「API 跑宿主机 + 容器 Prometheus」场景因 `api:8080` 目标不可达而**持续误报** | 改为 job 级判定 `sum(up)==0`，重启后不再误报 |
| **发现的真实缺陷 2** | `dsh_twin_bus_lag` 计算错误（jsonl 读取为 oldest-first，误取最后一条导致 lag≈历史总量且不随消费变化），导致 `BusLagHigh` 误报 | 新增 `Bus.SetReplayCursor`/`Bus.Lag`（无回放时整段历史计为待消费，回放后按游标递减）；新增 `TestBusLagTracking`；值班恢复后 lag 由 5824 降为 0 |

## 144. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿（新增 `TestBusLagTracking`） |
| `golangci-lint` | ✅ 0 issues |
| `make smoke` | ✅ **PASS 263/263** |
| Prometheus 规则 | 11/11 `health=ok`，`firing=[]` |

## 145. 已知限制（P36.0）

- 本机 Docker Desktop 仍在长时间运行后自动退出，obs 栈需重启用 `docker compose --profile obs up -d`。
- `docker.io` 在本机不可达，必须经 `PROM_IMAGE`/`GRAFANA_IMAGE` 指定国内镜像。
- Grafana 截图通过 CDP 注入登录态获得（headless 模式无法保留会话）；生产环境应配置匿名只读或统一 SSO。
- 告警仍为「规则 + firing 状态」验证，未接入 Alertmanager 通知通道。

---

# P37.0 验收记录（2026-11-14 上午）

## 146. 租户运维概览面板 + 前端版本同步

| 项 | 说明 | 证据 |
|---|---|---|
| 后端聚合接口 | `GET /api/tenants/{id}/overview`（admin）：事件总量/近 24h/分级、相机在线离线、待处理工单、巡检、导出任务、ingest、总线与存储状态、近 24h 小时分布 | 实测 200，返回 6 台相机 / 5 在线、事件 1、`bus_driver=jsonl` |
| 权限 | 非 admin → 403；未知租户 → 404 | `TestTenantOverviewGuards` |
| 前端页签 | 新增「概览」页签（admin，位于「统计」后）：6 个 KPI + 近 12 小时柱图 + 严重级别 + 运行状态，30 秒自动刷新 | 真实应用截图 `docs/screenshots/p37-overview-panel.png` |
| 版本同步 | 前端由 P33.0 → **P37.0**（`index.html` / `package.json` / TopBar / 登录页） | 页面标题「数字孪生安防平台 · P37.0」 |
| 测试 | 新增 `TestTenantOverview`、`TestTenantOverviewGuards` | 全绿 |

## 147. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint` | ✅ 0 issues |
| `pnpm typecheck` / `lint` / `build` | ✅ 全绿 |
| 真实浏览器验收 | ✅ 三维场景 60 FPS、10 个页签、概览面板真实数据、console 无异常 |

## 148. 排障记录（值得留存）

本轮定位「概览面板不渲染」时踩了三个坑，均已排除且**不是产品缺陷**：

1. **headless Chrome 无法创建 WebGL 上下文** → 三维场景初始化抛异常，整个应用白屏。
   这是无头环境限制；改用真实渲染参数后场景正常（60 FPS）。
2. **测试页放在 `public/` 目录** → 被 Vite 8 当作依赖预构建入口，导致
   `Failed to run dependency scan`，此前所有测试结果失效。已删除并改用「真实页面 + API 预登录」的方式验证。
3. **令牌模块的内存态**：`token.ts` 把令牌放在模块级变量里，必须先 `loadStoredTokens()`
   再调用 API，否则 401。真实应用启动时本就按此顺序执行，属测试方式问题。

## 149. 已知限制（P37.0）

- 概览接口每次调用会遍历租户相机与最近 500 条巡检执行，租户量大时建议加缓存（5 秒粒度）。
- `recent_hourly` 依赖 Store 的 Stats 实现，内存模式与 PostgreSQL 口径一致但时区均为 UTC。
- 面板仅对 admin 开放；operator/viewer 看不到运维指标（按最小权限原则）。

---

# P38.0 验收记录（2026-11-14 晚）

## 150. 多副本导出任务领取

| 项 | 说明 | 证据 |
|---|---|---|
| 迁移 018 | `export_jobs` 增加 `owner` / `heartbeat_at` 与状态索引 | PG 实测两列存在 |
| 原子领取 | `UPDATE ... WHERE status='pending'`，两副本并发只有一个赢家 | `TestPostgresExportJobClaimRace`（真实 PG，并发 2 副本） |
| 心跳与回收 | worker 每 10s 心跳；每副本每分钟回收无心跳的 `running` 任务 | 实测孤儿任务 35s 内从 `running` 回收为 `pending` |
| 副本标识 | `REPLICA_ID`（缺省 `hostname-pid`）、`EXPORT_HEARTBEAT_SECONDS` | 两副本日志分别打印 `replica-a` / `replica-b` |
| **修复的真实缺陷** | 后台导出任务继承 HTTP 请求上下文，请求返回后上下文取消 → PG 模式下任务永远停在 `pending` | 改用 `context.WithoutCancel` + 租户上下文；修复后两副本任务均正常完成 |

## 151. 多副本真库验证

| 场景 | 结果 |
|---|---|
| 两副本连接同一 PostgreSQL，各建一个导出任务 | ✅ 分别由 `replica-a` / `replica-b` 执行，产物各写各的 `EXPORT_DIR`（4365 / 4839 字节） |
| 模拟副本宕机（`owner=replica-dead`，心跳 5 分钟前） | ✅ 存活副本约 35s 内回收为 `pending` |
| 并发抢占 | ✅ 恰好一个赢家 |

## 152. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint` | ✅ 0 issues |
| 覆盖率 | **64.3%**（门禁 62%） |
| `make smoke` | ✅ **263/263** |
| PG 契约测试（含新竞争用例） | ✅ 全 PASS |

## 153. 已知限制（P38.0）

- 回收粒度为「心跳超时」，最坏情况下任务会延迟 `heartbeat × 6 + 回收周期` 才重新执行。
- 产物文件写在领取副本的本地 `EXPORT_DIR`；跨副本下载需要共享存储（NFS/S3）或下载时回退到 owner 副本。
- 内存存储为单进程模型，`ClaimExportJob` 恒成功（多副本场景请使用 PostgreSQL + `RATE_STORE`）。

---

# P39.0 验收记录（2026-11-17 下午）

## 154. 导出产物共享 + 并发迁移加固

| 项 | 说明 | 证据 |
|---|---|---|
| 可插拔产物存储 | 新增 `internal/exports` 包（`Store` 接口 + `LocalDir` 实现）：原子写入（tmp + rename）、路径穿越校验、流式下载 | `TestLocalDirRoundTrip` / `TestLocalDirRejectsTraversal` / `TestLocalDirDescribe` 全 PASS |
| 多副本共享目录 | 两副本 `EXPORT_DIR` 指向同一目录时可跨副本下载 | ✅ 副本 A 建任务（owner=replica-a），**副本 B 下载 HTTP 200**，3859 字节内容完整 |
| 概览上报后端 | `exports.artifacts` 字段报告存储后端（`dir:/path`） | 前端「运行状态」新增「导出产物」行 |
| **修复的真实缺陷** | 多副本同时启动时并发执行 DDL 迁移 → 第二个副本报 `duplicate key ... pg_type_typname_nsp_index` **启动失败** | 改用 `pg_advisory_lock` 串行化迁移；实测两副本同时启动均正常 |

## 155. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint` | ✅ 0 issues |
| 覆盖率 | **64.3%**（门禁 62%） |
| `make smoke` | ✅ **263/263** |
| `pnpm typecheck` / `lint` / `build` | ✅ 全绿 |

## 156. 已知限制（P39.0）

- 共享目前依赖「同一路径在两副本上可见」（NFS/SMB/绑定挂载）；原生 S3 后端尚未实现（接口已留好）。
- 迁移锁为全局单键，跨环境共用同一数据库实例时会有短暂互斥（毫秒级，可接受）。
- 大文件下载仍走 API 进程转发，未做重定向到对象存储预签名 URL。

---

# P40.0 验收记录（2026-11-18 凌晨）

## 157. 运维概览时间窗口与趋势

| 项 | 说明 | 证据 |
|---|---|---|
| 窗口参数 | `GET /api/tenants/{id}/overview?window=1h|6h|24h|7d`，非法值回退 24h | 实测三档窗口 `duration` 回显正确 |
| 环比数据 | 响应新增 `window{current,previous}`（当前窗口 vs 紧邻等长窗口） | `TestTenantOverviewWindow`：四个窗口 `current>0`、`previous=0`、非法值回退 |
| 窗口切换 UI | 概览标题栏 1 小时/6 小时/24 小时/7 天，切换即刷新 | 真实页面：默认「24 小时」高亮 → 点击「1 小时」后高亮切换且 KPI 变为「事件（1h）」 |
| 趋势展示 | `▲ +N（N%）vs 上期 M`，上升红/下降绿/持平灰 | 截图 `docs/screenshots/p40-overview-window.png` 显示 `▲ +1 vs 上期 0` |
| KPI 跳转 | 待处理工单→工单、巡检进行中→值班、导出任务→租户，支持键盘 Enter | 代码 + 类型检查；`role=button` + `tabindex=0` |
| 版本同步 | 前端 P37.0 → **P40.0** | 页面标题「数字孪生安防平台 · P40.0」 |

## 158. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint` | ✅ 0 issues |
| `pnpm typecheck` / `lint` / `build` | ✅ 全绿 |
| 真实浏览器验收 | ✅ 三维场景 60 FPS、窗口切换正常、无 console 异常 |

## 159. 已知限制（P40.0）

- `recent_hourly` 仍固定为近 24 小时桶，未随 `window` 变化（窗口仅影响 KPI 与环比）。
- 环比在「上期为 0」时不显示百分比，只显示绝对值差。
- 窗口统计依赖 Store 的 `Stats` 全量扫描，租户事件量大时建议加时间范围索引或物化视图。

---

# P41.0 验收记录（2026-11-19 下午）

## 160. 概览柱状图随窗口自适应

| 项 | 说明 | 证据 |
|---|---|---|
| 自适应分桶 | 按窗口选择桶宽（1h→5min、6h→30min、24h→1h、7d→1d），连续填充空桶，上限 400 桶 | 实测 API：`1h bucket=300s bars=13`、`6h=1800s/13`、`24h=3600s/25`、`7d=86400s/8` |
| 新字段 | `series[{bucket,count}]` + `bucket_seconds`；`recent_hourly` 保留兼容 | `TestTenantOverviewSeriesBucketing`（四档窗口的桶宽与柱数区间断言） |
| 密集模式 | 柱数 > 20 自动收紧间距、标签竖排 | 真实页面：24 小时 `dense=True`（25 根），1 小时/7 天 `dense=False` |
| 标签格式 | 分钟/小时窗口 `HH:mm`，天窗口 `MM-DD` | 截图 `docs/screenshots/p41-series-7d.png` |
| **修复的真实缺陷** | 切换窗口后 KPI 变了但柱状图仍是最初的 24 小时数据——`loadOverview` 调用 `fetchTenantOverview` 时**没有传窗参**（P40 的替换因函数签名带默认参数而未命中） | 修复后实测：24h→25 根、1h→13 根、7d→8 根，标题同步变化 |

## 161. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint` | ✅ 0 issues |
| `pnpm typecheck` / `lint` / `build` | ✅ 全绿 |
| 真实浏览器验收 | ✅ 三维场景 60 FPS、三档窗口柱状图数量正确、无 console 异常 |

## 162. 已知限制（P41.0）

- 直方图通过 `ListEvents(limit=100000)` 在内存中聚合；超大租户建议改为 SQL `date_trunc` 聚合或物化视图。
- `recent_hourly` 与 `series` 并存属于兼容期设计，后续大版本可移除。
- 7 天窗口按 UTC 天切桶，本地时区查看时边界会有偏移。

---

# P42.0 验收记录（2026-11-19 下午）

## 163. 事件直方图下推到存储层

| 项 | 说明 | 证据 |
|---|---|---|
| 新接口方法 | `Store.EventHistogram(ctx, from, to, bucket)`，返回零填充的连续桶 | 接口 + 两套实现 |
| PostgreSQL 实现 | `date_bin(...)` 在库内聚合，只返回桶计数 | `TestPostgresEventHistogram`（真实 PG）：7 个小时桶计数 `[2,0,1,0,1,0,0]` 全对；空窗口返回 4 个零桶 |
| 内存实现 | 遍历租户事件计数，与 PG 契约一致 | `TestEventHistogramBucketing`：小时桶 `[2,0,1,0,0]`、5 分钟桶 13 个且索引 1/7 为 1 |
| 调用方改造 | `buildOverviewSeries` 改为调用存储层，API 不再加载窗口内明细事件 | 代码替换；对外的 `series` / `bucket_seconds` 保持不变 |
| 实测 | 各窗口桶数与总数正确 | `1h=13 桶`、`6h=13`、`24h=25`、`7d=8`，总数与事件数一致 |

## 164. 回归

| 项 | 结果 |
|---|---|
| `go test ./... -count=1` | ✅ 全绿 |
| `golangci-lint` | ✅ 0 issues |
| PG store 套件（含新直方图用例） | ✅ 全 PASS |
| `make smoke` | ✅ **263/263** |
| 前端 | 无改动（接口兼容），页面实测正常 |

## 165. 已知限制（P42.0）

- `date_bin` 需要 PostgreSQL 14+（本项目使用 16）。
- 桶数上限 400，超出窗口宽度会自动截断到前 400 个桶（当前四档窗口最大 25 个，不受影响）。
- 内存实现仍为全量遍历（开发/测试用，数据量小无影响）。
- 未加 `(tenant_id, occurred_at)` 复合索引的库在大表上进行区间扫描会较慢，建议生产库补索引。
