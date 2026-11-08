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

## 8. P3.0 已落地（数据权限 / 升级 / 通知）

| 能力 | 实现位置 | 说明 |
|---|---|---|
| 数据级权限 | API 过滤层 + `visibleCameras` | 按用户 areas 过滤列表与统计；单资源越权 403；审计/通知仅 admin |
| 告警升级 | `RunEscalation` 工作协程 + `EscalateStale` | 15s 扫描 + `escalated_at` + WS + 审计 + 通知；阈值运行时可调 |
| Webhook 通知 | `deliverNotification` | `generic`/`feishu` 两种负载、严重级别门槛、超时、投递记录 |
| 运行时设置 | `settings` 表 + 内存缓存 | 管理员热更新，无需重启；写审计 |

设计取舍：设置缓存于 Host 内存（单实例部署；多实例需改为带失效通知的共享存储）；通知异步投递、失败只记录不阻塞 ingest；升级以「扫描而非定时器」实现，避免大量事件各自建定时器。

## 9. P4.0 已落地（值班 / 自动派单 / 通知可靠性 / 指标）

| 能力 | 实现位置 | 说明 |
|---|---|---|
| 值班组 | `duty_groups` 表 + `/api/duty/*` | 区域优先 + 全局兜底解析；admin 维护，全员可读 |
| 自动派单 | `autoDispatch`（升级工作协程内） | 升级即建 urgent 工单并 WS/通知/审计；无值班组时跳过并审计 |
| 通知可靠性 | `deliverNotification` | 指数退避重试、静默窗口、投递记录含 attempts；指标计数 |
| 可观测性 | `metrics` + `deploy/observability` | Prometheus 抓取 + Grafana 看板（事件速率/升级/通知/派单/延迟） |

设计取舍：值班解析采用「区域精确匹配优先、全局兜底」，不做时间排班（P5 可扩展班次）；自动派单与通知均异步/幂等友好（工单每次升级只建一次，因为升级只发生一次）。

## 10. P5.0 已落地（可回放事件总线）

| 能力 | 实现位置 | 说明 |
|---|---|---|
| 总线抽象 | `internal/bus` | `Driver` 接口 + memory 环形缓冲 + jsonl 持久日志；发布同步分发订阅者 |
| 总线驱动链路 | ingest / escalation | 事件写入总线，由订阅者统一做 WS 推送与通知；写库仍同步 |
| 回放 | `POST /api/bus/replay` | 按 offset 区间重放；WS `event_replay`；可选重复通知；写审计 |
| 观测量 | `/metrics` + Grafana | published/replayed/errors/lag 指标与看板面板 |

演进：`jsonl` 是单机可回放的中间形态；P6 把 `Driver` 换成 Kafka/Redpanda 生产者-消费者（接口不变），并拆出独立规则服务消费总线。回放默认不重复通知，避免告警风暴。

## 11. P6.0 已落地（三维增强）

| 能力 | 实现位置 | 说明 |
|---|---|---|
| 站点配置 | `model.Site` + `GET /api/site` | ENU 范围/模型清单/底图样式；`SITE_CONFIG_FILE` 覆盖 |
| 2.5D 底图 | `apps/web` CanvasTexture | 建筑轮廓/道路/区域/相机标注/指北针/比例尺 |
| LOD | `apps/web` THREE.LOD | 近/中/远三级，距离阈值切换 |
| glTF | `apps/web` GLTFLoader | 示例模型 `tools/make_site_model.py` → `public/models/site.gltf` |
| 视频融合 | `apps/web` VideoTexture | 选中相机 HLS 流贴到视锥内视频面 |

## 12. P1 建议优先级

1. 真实 RTSP 接入（1–2 路）→ ZLMediaKit → WebRTC 播放器（验证时延 ≤500ms）。
2. 单目标定工具（地面点采集 + 单应矩阵）→ 事件定位精度验收。
3. Kafka 替换进程内总线；事件规则服务化。
4. 三维：glTF 模型加载 + LOD + 相机视锥与视频纹理。
