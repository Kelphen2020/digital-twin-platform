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
