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
