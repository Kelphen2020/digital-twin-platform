# 基于视频监控系统的数字孪生平台 — P0 软件 MVP

本仓库是《详细实现方案》的 **P0 落地**：先用最小可运行系统把方案里最容易翻车的三件事提前验证——**视频/事件链路能通、时空对齐有落点、三维首屏能达标**。

- 范围与验收：`docs/P0_SCOPE.md`
- 接口契约：`docs/API.md`
- 验收记录：`docs/ACCEPTANCE.md`
- 架构与演进：`docs/ARCHITECTURE.md`

## 架构（P0）

```
┌────────────────────┐   HTTP POST /api/ingest/detections   ┌─────────────────────────┐
│ services/simulator │ ───────────────────────────────────► │ services/api (Go)       │
│ 扮演 AI 推理服务    │                                      │  规则引擎 → 事件         │
└────────────────────┘                                      │  REST + WS + /metrics   │
                                                            └──────────┬──────────────┘
                                                                       │ WebSocket (event / camera_status / stats)
                                                                       ▼
                                                            ┌─────────────────────────┐
                                                            │ apps/web (Vue3+Three.js)│
                                                            │ 三维态势一张图           │
                                                            └─────────────────────────┘
                                                  PostgreSQL 16 + PostGIS（P0 台账与事件）
```

P0 的"进程内事件总线"在 P1 换成 Kafka，对外契约不变；`/api/ingest/detections` 就是未来 Triton/YOLO 推理服务的入口。

![P0 三维态势界面](docs/screenshots/p0-web.png)

> 上面是 P0 实际运行截图（headless Chrome 软件渲染，4 FPS 为软渲染限制，真机 Chrome 请以页面内 FPS 计数器为准）。

## 快速开始（本机，3 条命令）

```bash
# 1) 后端（默认内存存储，0 依赖，先跑通链路）
make dev-api                       # :8080

# 2) 前端
make install-web && make dev-web   # :5173

# 3) 模拟器（扮演 AI 推理，持续产生事件）
make sim                           # 默认 5 QPS

# 打开 http://127.0.0.1:5173
```

用 PostgreSQL（数据落库）时：

```bash
# Docker Hub 直连不通的网络可换镜像源：
POSTGIS_IMAGE=docker.m.daocloud.io/postgis/postgis:16-3.4 make db-up
DATABASE_URL='postgres://twin:twin@127.0.0.1:5432/twin?sslmode=disable' make dev-api
```

全栈容器化：`make up`（API + PostgreSQL，API 镜像内构建）。

## 验收

```bash
make smoke                 # 端到端冒烟：健康检查 → 注入检测 → 事件出现 → 确认 → metrics
make load QPS=100 DURATION=5   # 压测并输出 p50/p95/p99
```

预期结果记录在 `docs/ACCEPTANCE.md`。

![P1.0 界面（视频 + 轨迹 + 处置 + 统计）](docs/screenshots/p1-web.png)

## P1.0 能力（在 P0 之上）

- **相机视频**：`stream_url` + hls.js 播放器（本机测试流 `scripts/make_hls.sh` 生成），LIVE 标识与错误态。
- **单目标定**：地面控制点（≥4）→ DLT 求单应矩阵 → `POST /api/calibration/solve` → 保存到相机；规则引擎优先用标定投影事件世界坐标（`world > calibration > fallback`）。
- **轨迹**：检测带 `track_id` 时落库轨迹点并通过 WS 推送，前端按 track 绘制轨迹线。
- **告警处置闭环**：确认 / 派单 / 关闭 + 处置动作记录 + 审计日志（`/api/audit`）。
- **统计与导出**：`/api/stats/summary`（按等级/类型/相机/小时）、`/api/stats/events.csv`。
- **可选鉴权**：设置 `AUTH_TOKEN` 后启用 Bearer Token（前端 `VITE_API_TOKEN`）。

契约见 [`docs/API_P1.md`](docs/API_P1.md)。

![P2.0 登录与 RBAC](docs/screenshots/p2-login.png)

## P2.0 能力（运营底座）

- **登录与 RBAC**：`POST /api/auth/login` 返回 JWT；角色 `admin` / `operator` / `viewer`；操作员按区域（areas）限制相机与告警处置；前端登录页 + 角色化按钮。
- **相机规则配置**：`GET/PUT /api/cameras/{id}/rules` —— 置信度阈值、去重窗口、启用类别、ROI 多边形（归一化图像坐标），仅 admin 可改。
- **工单闭环**：`POST/GET/PATCH /api/orders` —— 从事件一键转工单，指派/处理/完成，WS `order_updated` 实时刷新；审计记录 `order.create/order.update`。
- **审计主体**：写操作的 `actor` 为登录用户名（不再是 `web`）。
- **鉴权默认开启**（`AUTH_MODE=on`），`AUTH_MODE=off` 可关闭用于本机联调；机器入口可用 `INGEST_TOKEN` 保护。

内置演示账号：`admin/Admin@123456`、`operator/Operator@123`（区域：东门+停车场）、`viewer/Viewer@123`（只读）。**生产必须替换**。

契约见 [`docs/API_P2.md`](docs/API_P2.md)。

![P3.0 管理设置与审计](docs/screenshots/p3-admin.png)

## P3.0 能力（数据权限 / 升级 / 通知）

- **数据级权限**：非 admin 且配置了 `areas` 的账号，读取相机/事件/轨迹/工单/统计时自动按区域过滤；单资源越权返回 `403 out_of_scope`；审计与通知日志仅 admin 可见。
- **告警升级**：后台每 15 秒扫描，超过阈值（默认 300s）仍未关闭的 warn/critical 事件标记 `escalated_at`，WS 推送 `event_updated` 并触发升级通知；前端显示「已升级」。
- **Webhook 通知**：管理员可配置 `generic`（JSON）或 `feishu`（飞书机器人格式）webhook、最小严重级别、超时；新事件与升级都会投递，最近 1000 条投递记录可在 `GET /api/notifications` 查询。
- **运行时设置**：`GET/PUT /api/settings/notifications` 与 `/api/settings/escalation`（仅 admin，实时生效并审计）。

契约见 [`docs/API_P3.md`](docs/API_P3.md)。

![P4.0 值班与自动派单](docs/screenshots/p4-auto-dispatch-toast.png)

## P4.0 能力（值班 / 自动派单 / 通知可靠性 / 可观测性）

- **值班组**：admin 维护值班组（名称、负责区域、成员、启停）；按相机区域解析当前值班组（区域优先，`areas` 为空的全局组兜底）。
- **升级自动派单**：告警升级时按区域找值班组，自动创建 `urgent` 工单（assignee=值班组），WS 推送、审计 `order.auto_dispatch`；无值班组则记 `order.auto_dispatch_skipped`。
- **通知可靠性**：失败按指数退避重试（`max_attempts`/`backoff_seconds`），同相机+类型+严重级别在 `silence_seconds` 内只投递一次（抑制计数进指标）；投递记录含实际尝试次数。
- **可观测性**：`/metrics` 新增通知成功/失败/抑制、升级、工单、自动派单/跳过等指标；`deploy/observability/` 提供 Prometheus 抓取配置与 Grafana 看板 JSON（`make obs-up` 启动）。

契约见 [`docs/API_P4.md`](docs/API_P4.md)。

![P5.0 总线状态与回放](docs/screenshots/p5-bus-panel.png)

## P5.0 能力（可回放事件总线）

- **事件总线**：事件产生后写入总线（`memory` 环形缓冲 或 `jsonl` 持久日志），再由订阅者统一驱动 WebSocket 推送与 Webhook 通知；写库仍是同步事实源。
- **持久与回放**：`BUS_DRIVER=jsonl` 时事件追加到 `data/bus/events.jsonl`，重启后继续 offset；admin 可按 offset 区间回放，WS 以 `event_replay` 帧重推（可选是否重复通知）。
- **管理接口**：`GET /api/bus/status`、`POST /api/bus/replay`（仅 admin，写审计 `bus.replay`）。
- **指标**：`dsh_twin_bus_published_total`、`..._replayed_total`、`..._errors_total`、`..._lag`；Grafana 看板增加总线面板。

契约见 [`docs/API_P5.md`](docs/API_P5.md)。

![P6.0 三维增强：2.5D 底图 + glTF + LOD + 视频融合](docs/screenshots/p6-scene.png)

## P6.0 能力（三维增强）

- **站点配置**：`GET /api/site` 下发站点名称、ENU 范围、模型清单与底图样式；`SITE_CONFIG_FILE` 可覆盖。
- **2.5D 矢量底图**：前端按站点范围程序化绘制建筑轮廓/道路/区域/相机标注/指北针/比例尺（CanvasTexture），可开关。
- **建筑 LOD**：近景细节体块、中景简化盒子、远景轮廓，按距离切换；可开关「模型细节」。
- **glTF 模型**：GLTFLoader 加载 `site.models`（示例模型由 `tools/make_site_model.py` 生成），失败自动回退到程序化建筑。
- **视频纹理融合**：选中相机的 HLS 流作为 THREE.VideoTexture 贴在视锥内视频面，可开关。

契约见 [`docs/API_P6.md`](docs/API_P6.md)。

![P7.0 用户管理](docs/screenshots/p7-user-list.png)

## P7.0 能力（用户与账号管理）

- **账号持久化**：登录账号从存储读取（内存模式启动时写入种子账号；`DATABASE_URL` 下持久化到 `users` 表）。
- **管理员 CRUD**：`GET/POST/PATCH/DELETE /api/users` —— 新建（用户名唯一、密码 ≥8 位、角色/区域校验）、改角色/区域/显示名/密码/启停、删除；不能停用或删除自己（`self_protected`）。
- **登录语义**：停用账号登录返回 401；改密后旧密码立即失效；角色/区域变更在下次登录时生效（JWT 12h 有效期内旧 token 仍可用）。
- **审计**：`user.create` / `user.update` / `user.delete`（actor=管理员用户名）。
- **前端**：admin 新增「用户」页签（列表/新建/编辑/启停/删除/改密），operator/viewer 不可见。

契约见 [`docs/API_P7.md`](docs/API_P7.md)。

![P8.0 值班班次与轮转派单](docs/screenshots/p8-duty-form.png)

## P8.0 能力（值班排班 / 派单去重与轮转 / 超时再升级）

- **值班班次**：值班组可配置多个班次（名称、开始/结束、ISO 周几、成员）；`end <= start` 支持跨夜；`/api/duty/current?area_id=&at=` 返回当前班次与成员（非班次时间无人值班）。
- **派单轮转**：`rotation=operator` 时按当前班次成员轮转 assignee；否则仍派给班组名（兼容 P4）。
- **工单去重**：同一事件已有未关闭工单时不再自动建单，审计 `order.auto_dispatch_skipped`（reason=open_order），指标 `dsh_twin_orders_deduped_total`。
- **超时再升级**：`second_after_seconds` 控制升级后仍未关闭的再次升级（`re_escalated_at`、审计 `event.re_escalate`、指标 `dsh_twin_re_escalations_total`）。
- 前端：值班表单支持班次编辑与轮转选择；事件显示「再升级」；设置页支持二次升级时长。

契约见 [`docs/API_P8.md`](docs/API_P8.md)。

![P9.0 WebSocket 连接质量指示](docs/screenshots/p9-quality.png)

## P9.0 能力（性能、背压与运维交付）

- **背压与采样**：每客户端 WS 队列可配（`HUB_CLIENT_BUFFER`），满队列丢弃并计数；轨迹点广播可采样（`WS_TRACK_SAMPLE`，落库不受影响）。
- **接入限流与体积上限**：`INGEST_RATE_LIMIT`/`INGEST_BURST` 令牌桶（超限 429），`MAX_BODY_BYTES` 请求体上限（超限 413）。
- **可观测性**：新增 WS 帧/丢弃、轨迹写入/广播、限流拒绝指标；Prometheus 告警规则（`alerts.yml`）与 Grafana 面板。
- **部署交付**：`deploy/Dockerfile.web` + `nginx.conf`（静态站点 + `/api` 反代 + `/ws` 升级 + `/hls`），compose 增加 `web` 服务；`docs/OPERATIONS.md` 运维手册；`docs/PERFORMANCE.md` 压测报告。

契约见 [`docs/API_P9.md`](docs/API_P9.md)。

![P10.0 登录锁定与密码策略](docs/screenshots/p10-lockout.png)

## P10.0 能力（安全加固）

- **登录防爆破**：同一 username+IP 连续失败达阈值（默认 5 次）锁定（默认 60s），锁定期间返回 429 + `Retry-After`；成功登录清零。
- **密码策略**：≥10 位且至少包含字母与数字（创建/修改用户），违规 400 并提示规则。
- **审计导出**：`GET /api/audit/export?from=&to=&action=&actor=` 导出 CSV（仅 admin），带筛选与时间范围。
- **安全响应头**：`X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`。
- **HTTPS 部署模板**：`deploy/nginx-tls.conf`（TLS 终结/HSTS/反代），运维手册补充证书与自签/Let's Encrypt 方案。

契约见 [`docs/API_P10.md`](docs/API_P10.md)。

![P11.0 在线会话与强制下线](docs/screenshots/p11-user-sessions.png)

## P11.0 能力（刷新令牌 / 会话吊销）

- **短效 access + 轮换 refresh**：登录返回 access（默认 1h）与 refresh（默认 7d）；`POST /api/auth/refresh` 轮换，旧 refresh 立即失效。
- **主动吊销**：`/api/auth/logout` 吊销单个 refresh；`/api/auth/logout-all` 吊销全部并递增 token 版本，已签发 access 立即失效。
- **会话管理（admin）**：`GET /api/users/{id}/sessions` 查看在线会话（IP/UA/有效期），`POST /api/users/{id}/logout` 强制下线（不能对自己，用 logout-all）。
- **密码变更即失效**：管理员重置密码会吊销该用户全部 refresh 并递增版本，旧 access 立即 401。
- 前端：401 自动 refresh 一次并重试、失败自动登出；用户页「强制下线」与在线会话数。

契约见 [`docs/API_P11.md`](docs/API_P11.md)。

![P12.0 巡检路线播放](docs/screenshots/p12-patrol-play.png)

## P12.0 能力（三维交互与巡检）

- **测距**：开启后点击地面两点，场景内连线并显示米制距离。
- **框选**：Shift+拖拽矩形选中框内相机，显示多选列表/数量并高亮。
- **巡检路线**：`/api/patrol/routes` CRUD（operator/admin 可写，创建者或 admin 可改删）；前端可播放/暂停/停止相机沿航点飞行，路线折线 + 当前航点高亮，联动相机高亮。
- 审计：`patrol.create/update/delete`。

契约见 [`docs/API_P12.md`](docs/API_P12.md)。

![P13.0 巡检执行与异常工单](docs/screenshots/p13-execution-issue.png)

## P13.0 能力（巡检计划与执行）

- **巡检计划**：`/api/patrol/schedules` CRUD（operator/admin，创建者或 admin 可改删）；`interval_minutes` 1–10080，`next_run_at` 自动推进；调度器每 15s 扫描到期计划生成任务。
- **执行工作流**：`/api/patrol/executions` 列表/筛选；认领（pending→running）、完成（running→done）；`result=issue` 自动创建「巡检异常 · 路线」工单（assignee=提交人，status=open，带执行 id），响应返回 `order_id`。
- **立即执行**：`POST /api/patrol/schedules/{id}/run` 手动生成任务；WS 推送 `patrol_execution`。
- 审计：patrol.schedule.create/update/delete/run、patrol.execution.claim/complete、order.created。

契约见 [`docs/API_P13.md`](docs/API_P13.md)。

![P14.0 巡检统计与历史清理](docs/screenshots/p14-stats.png)

## P14.0 能力（巡检报表与归档）

- **统计**：`GET /api/patrol/stats?from=&to=` 返回总数/待执行/进行中/已完成/异常、完成率、异常率、平均时长，以及按路线/按人汇总。
- **导出**：`GET /api/patrol/executions/export` 导出 CSV（operator/admin，最多 1 万行，支持 time/status 过滤）。
- **归档**：`DELETE /api/patrol/executions?before=&status=` 仅 admin 可清理已结束记录（done/issue），返回删除条数并写审计 `patrol.executions.purge`。
- 前端：执行 tab 增加统计卡片、按路线/按人汇总、导出 CSV、admin 清理历史（before 日期 + 二次确认）。

契约见 [`docs/API_P14.md`](docs/API_P14.md)。

![P15.0 告警联动巡检执行](docs/screenshots/p15-execution.png)

## P15.0 能力（告警联动巡检）

- 升级设置新增 `auto_patrol`：告警首次升级/再升级时自动生成待执行巡检任务。
- 路由匹配：优先取 `camera_ids` 含该相机的路线，否则取航点与相机 XZ 距离最近的路线；无路线则跳过（审计 `patrol.execution.skipped`）。
- 执行记录新增 `event_id`，任务备注「告警联动：<类型> · <相机>」；同一事件已有 pending/running 任务时去重。
- 前端：设置页「告警联动巡检」开关；执行卡片显示关联告警，支持按事件筛选。

契约见 [`docs/API_P15.md`](docs/API_P15.md)。

![P16.0 巡检任务取消](docs/screenshots/p16-cancelled-card.png)

## P16.0 能力（巡检任务超时治理与取消）

- **人工取消**：`POST /api/patrol/executions/{id}/cancel`：pending 由 operator/admin 取消；running 仅认领人或 admin；写 finished_at/note，审计 `patrol.execution.cancel`。
- **超时自动关闭**：调度器扫描将超过 `PATROL_PENDING_TIMEOUT_MINUTES`（默认 60）的待认领、超过 `PATROL_RUNNING_TIMEOUT_MINUTES`（默认 120）的进行中任务置为 `cancelled`（note「超时未认领/未完成，自动关闭」），审计 `patrol.execution.expired`，0=关闭。
- **统计/归档**：stats 新增 `cancelled`；归档 purge 允许 `cancelled`。
- 前端：执行列表「已取消」状态/筛选、取消按钮（二次确认）、统计卡片「已取消」。

契约见 [`docs/API_P16.md`](docs/API_P16.md)。

## P17.0 能力（PostgreSQL 生产持久化验证与运维脚本）

- **PostgreSQL 全链路验证**：镜像 `postgis/postgis:16-3.4`（镜像源可配），迁移 001–012 自动应用；263 项 smoke 全量跑通（详见 `docs/ACCEPTANCE.md`）。
- **修复 4 个 PG-only 缺陷**：escalation RETURNING 列缺失、巡检工单 event_id 外键、用户 token_version 未持久化、通知 attempts 未落库；并新增 `internal/store/postgres_integration_test.go`（`PG_TEST_URL` 环境变量启用）做存储契约回归。
- **备份/恢复**：`scripts/backup.sh`、`scripts/restore.sh` 与 `make db-backup` / `make db-restore`（pg_dump -Fc / pg_restore --clean --if-exists）。
- **运维手册**更新：备份恢复流程、迁移幂等说明、Postgres 连接与验证步骤。

契约参见 `docs/OPERATIONS.md`。

## P18.0 能力（持续集成）

- **GitHub Actions**：`.github/workflows/ci.yml` 五个 job —— api（build/vet/test）、web（typecheck/build）、simulator（py_compile + bash -n）、smoke-memory、smoke-postgres（PostGIS service + 存储契约测试 + 全量 smoke）。
- **本地等价**：`scripts/ci_local.sh`（`--quick` 跳过 PG）按相同顺序跑完全部阶段并输出汇总表，自动清理临时 API 进程与 PG 容器。
- **文档**：`docs/CI.md`（流水线任务、本地跑法、失败排查）。

详见 [`docs/CI.md`](docs/CI.md)。

## P21.0 能力（补测与门禁提升）

- 新增 `internal/auth`、`internal/store` 单元测试（登录/JWT/吊销/用户 CRUD/密码策略/权限矩阵；相机/事件/工单/轨迹/审计/设置/值班/用户/刷新令牌）。
- 覆盖率 **48.8% → 56.9%**，CI 门禁由 45% 提升至 **55%**。

## P22.0 能力（错误语义统一）

- `store.ErrNotFound` / `store.ErrConflict` 与 `auth.ErrUserNotFound` / `auth.ErrUsernameTaken` 统一别名为 `model` 包哨兵，`errors.Is` 跨实现可用。
- API 层改用 `store.IsNotFound` / `store.IsConflict`（支持包装错误）。
- 新增 `internal/store/errors_test.go` 校验哨兵同一性与内存实现返回值。
- 约定文档：`docs/OPERATIONS.md` §9。

## P23.0 能力（补齐 WebSocket/配置/模型单测）

- `internal/realtime`（握手/问候/广播/背压丢弃）、`internal/config`（默认值与覆盖）、`internal/model`（默认值/严重级别）、`internal/store/seed`（种子一致性）新增单测。
- 三包覆盖率从 0% 提升至 **92–100%**；总覆盖率 57.5%。

## P24.0 能力（store 覆盖率攻坚）

- 新增巡检路线/计划/执行、事件处置与升级/再升级、相机标定与排序、用户列表等 store 单测。
- store 覆盖率 **25.4% → 37.9%**，总覆盖率 **57.5% → 61.4%**，CI 门禁 **55% → 60%**。

## P25.0 能力（rules/metrics 单测）

- `internal/rules` 覆盖率 55.3% → **97.4%**（世界坐标三源投影、ROI、类过滤、去重窗口、剪枝、窗口钳制）。
- `internal/metrics` 覆盖率 0% → **98.6%**（指标输出格式、标签、时长 summary、空值）。
- 总覆盖率 **61.4% → 62.2%**。

## P19.0 能力（部署与扩展）

- **多副本 API**：状态集中于 PostgreSQL，副本共享；非主副本用 `INGEST_FALLBACK_URL` 把 ingest 原样转发到主副本（指标 `ingest_forwarded_total` / `ingest_fallback_failures_total`，失败 502）。
- **ingest 默认体积上限**：即使未配置 `MAX_BODY_BYTES` 也限制 1 MiB（超出 413）。
- **compose 变量**：`INGEST_FALLBACK_URL` 加入 API 环境与 `.env.example`。
- 部署/扩展说明：`docs/P19.md`。

详见 [`docs/P19.md`](docs/P19.md)。

## P20.0 能力（代码质量门禁）

- **golangci-lint**：`services/api/.golangci.yml` 启用 15 个 linter；清理了全部生产代码告警（shadow/nilness/ineffectual/unused/exitAfterDefer 等）。
- **ESLint**：`apps/web/eslint.config.js`（flat config）+ `pnpm lint`，当前 0 error / 0 warning。
- **覆盖率门禁**：CI 强制 Go 语句覆盖率 ≥ 45%（当前 ~49%），报告上传 artifact。
- **一键检查**：`./scripts/lint.sh`。
- CI 新增 `lint` job，`ci_local.sh` 同步加入 lint 与覆盖率阶段。

详见 [`docs/CI.md`](docs/CI.md)。

## 目录

```
digital-twin-platform/
├── apps/web/                 # Vue 3 + TypeScript + Vite + Three.js 三维客户端
├── services/api/             # Go API：REST + WebSocket + 规则引擎 + metrics（内存/PostgreSQL）
├── services/simulator/       # Python 模拟器（扮演 AI 推理服务）+ 相机状态切换工具
├── scripts/                  # smoke.sh / load.py
├── deploy/                   # docker-compose（postgres/api + 可选 full profile）
├── docs/                     # 范围、契约、架构、验收
└── Makefile
```

## 技术栈（与方案对齐）

| 层 | P0 | 方案目标（P1+） |
|---|---|---|
| 后端 | Go 1.23（net/http + gorilla/websocket + pgx） | Go 媒体网关 + Java/Spring 业务服务（按团队能力） |
| 数据库 | PostgreSQL 16 + PostGIS（内存模式可零依赖运行） | 同左 + TDengine/Kafka |
| 前端 | Vue 3 + TS + Vite + Three.js | 同左 + Cesium（大范围 GIS）+ UE5 大屏 |
| AI | 模拟器（HTTP 契约） | YOLO/Triton + 规则服务 + Kafka |
| 视频 | 不接真实流（相机台账 + 状态） | GB28181/ONVIF/RTSP + ZLMediaKit + WebRTC |

## 已知限制（P0）

- 不接真实视频流；相机画面、定位为模拟/近似，P1 接真实流与标定参数。
- 无鉴权（仅限本机/内网演示）；权限模型在 P2 业务闭环补齐。
- 事件总线为进程内扇出，进程重启后内存模式数据清空；用 PostgreSQL 时持久化。
- 未做多租户、审计、等保相关能力。
