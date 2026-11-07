# P0 架构说明

本文只描述 P0 实现，并标注它与《详细实现方案》的对应关系与演进路径。

## 1. 组件

| 组件 | 路径 | 技术 | 职责 |
|---|---|---|---|
| API | `services/api` | Go 1.23 + net/http + gorilla/websocket + pgx v5 | 相机台账、事件规则、REST、WebSocket、metrics |
| Web | `apps/web` | Vue 3 + TS + Vite + Three.js | 三维态势一张图、事件面板、WS 实时更新 |
| 模拟器 | `services/simulator` | Python 3（stdlib） | 扮演 AI 推理服务：按契约持续上报检测 |
| 脚本 | `scripts` | bash / Python | 冒烟、压测 |
| 部署 | `deploy` | docker compose | PostgreSQL/PostGIS + API（可选 full：Redis/MinIO/Redpanda） |

## 2. 数据流

1. 模拟器 `POST /api/ingest/detections` 上报一批检测（相机编码 + class + 置信度 + bbox + 可选世界坐标）。
2. API `rules.Engine` 执行 P0 规则：置信度 ≥0.6、相机+class 5s 去重、class→type/severity 映射、世界坐标（有则用，无则沿光轴 30m 投影）。
3. `store` 持久化事件（PostgreSQL 或内存）。
4. `realtime.Hub` 向所有 WS 客户端广播 `event` 帧。
5. Web 端收到后：三维场景出现脉冲事件点、右侧事件流插入记录、计数更新。
6. 每 5 秒广播 `stats`；相机状态 PATCH 后广播 `camera_status`。

## 3. 规则与坐标（P0 简化）

- 去重窗口是"同相机 + 同 class"，避免同一目标每帧产生事件。
- 世界坐标为局部 ENU 米制；P0 未标定，缺省用相机位置 + 朝向外推 30m，仅用于演示"事件能落到三维空间"。
- P1 接入真实标定：地面平面/高程图 + 单应矩阵，把 bbox 底边中心映射为世界坐标，并用时间戳对齐（方案 D1）。

## 4. 与方案章节的对应

| 方案 | P0 | 演进 |
|---|---|---|
| 3.1 分层架构 | 接入（模拟）→ 规则 → 事件 → 展示 | 接入层换 GB28181/ONVIF 适配器 |
| 4 视频接入 | 相机台账 + 状态 | ZLMediaKit + WebRTC/FLV 播放；按需拉流 |
| 5 AI 分析 | 模拟器扮演推理服务 | Triton + YOLO/TensorRT；规则引擎独立服务 |
| 6 三维孪生 | Three.js 场景 / 视锥 / 事件上图 | glTF 模型、3D Tiles、标定参数、真实视频融合 |
| 7 数据平台 | PostgreSQL/PostGIS | TDengine（轨迹）、Kafka（事件）、MinIO（对象） |
| 8 服务接口 | REST + WS + OpenAPI 风格 | 网关、鉴权、限流、多租户 |
| 11 部署 | docker compose | Helm + GPU Operator + Prometheus/Grafana |
| 13 验收 | smoke/load + 打点 | 真实设备联调与性能压测 |

## 5. 关键设计决策（P0）

- **契约先行**：先冻结 `docs/API.md`，前端、模拟器、后端并行开发；P1 换实现不换契约。
- **存储双实现**：`Store` 接口 + 内存/PostgreSQL 两实现；无数据库也能跑演示与 CI。
- **事件总线可替换**：Hub 只暴露 `Broadcast(Frame)`；P1 换成 Kafka 消费者时 API 层不改。
- **坐标统一**：ENU 米制贯穿后端与前端，避免"大屏一套坐标、后端一套坐标"。
- **可观测性优先**：`/metrics` 从 P0 就有，压测数据直接可查。

## 6. P1.0 已落地

| 能力 | 实现位置 | 说明 |
|---|---|---|
| 视频播放 | `apps/web` + `scripts/make_hls.sh` | hls.js 播 `camera.stream_url`；本机用 ffmpeg 测试流，真实接入换 ZLMediaKit 地址 |
| 单目标定 | `services/api/internal/calib` | 4–20 个控制点 DLT（法方程）→ 单应矩阵 + 残差；规则引擎优先标定投影 |
| 轨迹 | `track_points` 表 + `/api/tracks` + WS `track_point` | 带 `track_id` 的检测落库，前端画轨迹线 |
| 处置闭环 | `event_actions` + `audit_log` | 确认/派单/关闭；写审计（`/api/audit`） |
| 统计导出 | `/api/stats/summary`、`/api/stats/events.csv` | 按等级/类型/相机/小时聚合；CSV 导出 |
| 可选鉴权 | `AUTH_TOKEN` + 中间件 | REST Bearer；WS `?token=`；默认关闭 |

演进备注：标定求解当前是代数最小二乘（不是 SVD），已用合成数据验证；真实场景需加鲁棒估计（RANSAC）与多相机融合；视频播放走 HLS 演示链路，WebRTC 低时延需接媒体服务器。

## 7. P2.0 已落地（运营底座）

| 能力 | 实现位置 | 说明 |
|---|---|---|
| 身份认证 | `services/api/internal/auth` | 种子用户 + PBKDF2 口令 + HS256 JWT（stdlib 实现，无外部依赖） |
| 权限模型 | 同上 + API 中间件 | 角色（admin/operator/viewer）× 区域（areas）× 操作；401/403/out_of_scope |
| 相机规则 | `camera_rules` 表 + 规则引擎 | 阈值/去重/启用类别/ROI；ROI 用归一化图像坐标 + 射线法 |
| 工单 | `orders` 表 + `/api/orders` | 事件转工单、指派、状态机（open→assigned→processing→done/cancelled） |
| 审计主体 | `audit_log.actor` | 登录用户名；机器入口单独可配 token |

安全边界（P2.0）：JWT 密钥来自 env、内置口令仅演示；生产需接企业 IdP/SSO、启用 HTTPS、轮换密钥、对 ingest 启用 token。

## 8. P1 建议优先级

1. 真实 RTSP 接入（1–2 路）→ ZLMediaKit → WebRTC 播放器（验证时延 ≤500ms）。
2. 单目标定工具（地面点采集 + 单应矩阵）→ 事件定位精度验收。
3. Kafka 替换进程内总线；事件规则服务化。
4. 三维：glTF 模型加载 + LOD + 相机视锥与视频纹理。
