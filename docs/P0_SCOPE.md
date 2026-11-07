# P0 范围与验收（软件 MVP / PoC 套件）

本仓库是《基于视频监控系统的数字孪生平台·详细实现方案》的 **P0 落地**：先用最小可运行系统把方案里三件最容易翻车的事提前验证——**视频/事件链路能通、时空对齐有落点、三维首屏能达标**。

> 结论先行：P0 不追求接入真实 GB28181 设备，也不追求 AI 指标；P0 追求 **一条可演示、可压测、可扩展的真实闭环**。

## 1. P0 目标

| 编号 | 目标 | P0 验收标准 |
|---|---|---|
| P0-1 | 事件闭环 | 模拟器（扮演 AI 推理）产生检测 → API 规则引擎生成事件 → WebSocket 推送 → 三维场景实时上图，端到端 ≤ 1s（本机） |
| P0-2 | 三维底座 | 浏览器打开三维场景：地面/建筑/相机点位/视锥/事件点，首屏 ≤ 3s，1080p ≥ 30fps |
| P0-3 | 设备与数据 | 相机台账（状态/坐标/朝向/FOV/量程）入库，REST 可查改，状态变化实时推送 |
| P0-4 | 可压测 | 事件注入脚本可调 QPS，`/metrics` 暴露事件数、连接数、注入耗时 |
| P0-5 | 可交付 | `docker compose up` + 三条命令即可在 macOS/Linux 上跑起来，README 可直接给同事 |

## 2. P0 架构（对应方案章节）

| 方案章节 | P0 实现 | 说明 |
|---|---|---|
| 3.1 分层架构 | 接入模拟器 → 规则引擎 → 事件总线（进程内） → WS → Web 三维 | P1 把"进程内总线"换成 Kafka，接口不变 |
| 4 视频接入 | **不接真实流**；相机台账 + 状态模拟 | P1 接 RTSP/GB28181，媒体服务用 ZLMediaKit |
| 5 AI 分析 | `services/simulator` 扮演推理服务，按 `/api/ingest/detections` 契约上报 | P1 换真实 YOLO/Triton，契约不变 |
| 6 三维孪生 | Three.js 场景 + 视锥 + 事件上图 + 像素/世界坐标预留 | P1 接真实模型与标定参数 |
| 7 数据平台 | PostgreSQL 16 + PostGIS；轨迹/时序留接口 | P1 加 TDengine / Kafka |
| 8 服务接口 | REST + WebSocket + OpenAPI 风格文档 | 见 `docs/API.md` |
| 11 部署 | docker compose（小规模档）+ 本机运行脚本 | P1 出 Helm Chart |
| 13 验收 | 打点：首帧/首屏/帧率/端到端时延 | 见 `docs/ACCEPTANCE.md` |

## 3. 明确不做（P0 Out of Scope）

- GB28181/ONVIF/RTSP 真实接入与 ZLMediaKit 集群（P1）
- 真实 AI 模型与准确率指标（P2，需要标注数据）
- UE5 / Pixel Streaming、大屏上墙
- 权限/多租户/等保（P2 业务闭环阶段）
- 三维模型资产、现场标定、硬件采购

## 4. 验收方式（演示脚本）

1. `docker compose -f deploy/docker-compose.yml up -d postgres`（数据库）
2. `make dev-api`（后端 :8080）
3. `make dev-web`（前端 :5173）
4. `make sim`（模拟器持续上报）
5. 浏览器打开 http://127.0.0.1:5173 ：看到三维场景、相机状态、事件实时出现、点击事件定位到相机
6. `make smoke`：断言 ingest → event → list 全链路通过
7. `make load`：100 QPS 注入 5s，`/metrics` 与页面统计一致

## 5. 里程碑建议

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | 本仓库：模拟闭环 + 三维底座 + 数据台账 | 本次交付 |
| P1 | 1–2 路真实 RTSP 接入 + ZLMediaKit + WebRTC 播放 + 单目定位标定工具 | 待现场素材 |
| P2 | 告警中心/联动/检索/权限/报表 + AI 训练流水线 | 待数据与人力 |
