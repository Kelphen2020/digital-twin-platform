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
