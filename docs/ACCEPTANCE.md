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
