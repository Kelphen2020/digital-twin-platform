# 基于视频监控系统的数字孪生安防平台（P0 → P39）

> 一个**可直接运行**的视频监控数字孪生平台开源实现：检测数据接入 → 规则引擎 → 三维态势一张图 →
> 告警处置/工单/值班/巡检 → 多租户隔离 → 可观测性，全链路端到端验证。

[![CI](https://github.com/Kelphen2020/digital-twin-platform/actions/workflows/ci.yml/badge.svg)](https://github.com/Kelphen2020/digital-twin-platform/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23-00ADD8?logo=go)](services/api/go.mod)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vue.js)](apps/web/package.json)
[![Three.js](https://img.shields.io/badge/Three.js-r170-000000?logo=three.js)](apps/web/package.json)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16%2BPostGIS-336791?logo=postgresql)](deploy/docker-compose.yml)

| 快速索引 | 内容 |
|---|---|
| 文档地图 | [`docs/README_MAP.md`](docs/README_MAP.md)（所有文档的分组索引） |
| 里程碑索引 | [`docs/MILESTONES.md`](docs/MILESTONES.md)（P0→P36 契约文档导航） |
| 当前状态 | `make smoke` **263/263 PASS**（内存 + PostgreSQL 双跑）；`go test ./...` 全绿；覆盖率 **64.8%**（门禁 62%，含 PG 阶段 72%） |
| API 规模 | **93 个** REST/WS 端点、19 张表、17 个幂等迁移 |
| 验收记录 | [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md)（P0–P36 逐阶段真实输出） |
| 安全须知 | [`SECURITY.md`](SECURITY.md)（**部署前必读**：替换默认密钥/口令、启用 HTTPS） |
| 贡献指南 | [`CONTRIBUTING.md`](CONTRIBUTING.md) |
| 许可协议 | [MIT](LICENSE) |

## 它是什么 / 不是什么

**是**：一个覆盖「接入 → 规则 → 三维 → 处置 → 巡检 → 多租户 → 观测」全链路的参考实现，
每一轮功能都附带真实的验收记录、截图与端到端回归，可直接 `make` 起来跑通。

**不是**：开箱即用的生产系统。当前视频源与检测数据来自**模拟器**；尚未实现 RTSP/GB28181 真实取流、
真实 AI 推理、BIM/CAD 资产导入与 GPU 编排。详见文末「已知限制」。

## 快速开始（3 条命令）

```bash
git clone https://github.com/Kelphen2020/digital-twin-platform.git && cd REPO

make dev-api    # ① 终端 1：启动 API（内存存储，:8080，自动种子 6 台相机/3 个账号）
make dev-web    # ② 终端 2：启动前端（:5173）
make sim        # ③ 终端 3：模拟摄像头推送检测数据（可选，看事件流滚动起来）
```

打开 <http://127.0.0.1:5173>，用演示账号登录：

| 账号 | 口令 | 角色 |
|---|---|---|
| `admin` | `Admin@123456` | 管理员（全部功能 + 租户管理） |
| `operator` | `Operator@123` | 值班员（事件处置/工单，限授权区域） |
| `viewer` | `Viewer@123` | 观察员（只读） |

> 演示口令仅用于本机体验，**生产必须替换**。

```bash
make smoke               # 263 项端到端回归（约 2 分钟）
./scripts/ci_local.sh    # 8 阶段本地 CI（含 PostgreSQL，需 Docker）
make obs-up              # Prometheus :9090 + Grafana :3000（看板/告警）
```

## 里程碑速览（P0 → P36）

| 阶段 | 主题 |
|---|---|
| P0 | 最小可运行 MVP：ingest → 规则引擎 → 事件流 → 三维场景 → 处置 |
| P1 | 相机标定、轨迹、处置动作、统计、HLS 视频 |
| P2–P4 | JWT RBAC、处置规则、工单、区域数据权限、通知 webhook、值班与自动派单、重试/静默 |
| P5–P7 | 可回放事件总线、2.5D 地图/glTF/视频融合、用户管理 |
| P8–P10 | 值班班次与轮转、派单去重、二次升级；背压/采样/限流与压测；登录防爆破、密码策略、审计导出、安全头、HTTPS |
| P11–P13 | 刷新令牌与会话吊销；三维测距/框选/巡检路线；巡检计划与执行（异常建单） |
| P14–P16 | 巡检报表/导出/归档；告警联动巡检；任务取消与超时治理 |
| P17–P19 | PostgreSQL 全链路验证与修复、备份恢复；CI 流水线；多副本 ingest 回落 |
| P20–P26 | 代码质量门禁（golangci-lint/eslint/覆盖率）与逐包补测 |
| P27–P28 | 多租户隔离基础（租户表/作用域存储/JWT `tnt`/租户 CRUD）；值班组与轨迹按租户 |
| P29–P31 | 租户模板与租户管理员、归档/删除/配额、数据导出与共享限流 |
| P32–P33 | 异步导出任务；导出任务持久化与保留策略 |
| P34–P35 | PG 契约测试与覆盖率门禁；多租户可观测性（租户指标/看板/告警） |
| P36 | 可观测性端到端（Grafana 自动配置、总线 lag 与 ApiDown 误报修复） |
| P37 | 租户运维概览面板（聚合接口 + 前端概览页签）+ 前端版本同步 |
| P38 | 多副本导出任务领取（原子 claim + 心跳 + 孤儿回收；迁移 018） |
| P39 | 导出产物可插拔存储（多副本共享目录）+ 并发迁移 advisory lock 加固 |

完整逐轮记录见 [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md) 与 [`docs/MILESTONES.md`](docs/MILESTONES.md)。

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.23（stdlib `net/http` + gorilla/websocket + pgx v5），无重型框架 |
| 前端 | Vue 3 + TypeScript + Vite + Three.js（2.5D 场景/glTF/视频融合） |
| 存储 | PostgreSQL 16 + PostGIS（生产）/ 内存实现（开发与测试） |
| 观测 | Prometheus 指标 + Grafana 看板 + Alertmanager 规则 |
| 模拟器 | Python 3 标准库（无第三方依赖） |
| 部署 | Docker Compose + Nginx（含 TLS 配置） |

## 已知限制（现状，P36）

| 缺口 | 说明 |
|---|---|
| 真实视频接入 | 未实现 RTSP/GB28181 取流、H.264 解码与 WebRTC 播放；视频源为模拟器 + 示例视频 |
| 真实 AI 推理 | 检测结果由模拟器按 HTTP 契约推送；未集成 YOLO/Triton 等推理服务 |
| 真实三维资产 | 示例 glTF 模型；未实现 BIM/CAD/正射影像导入与 LOD 优化 |
| 生产环境验证 | 压测与部署验证均基于本机 Docker；K8s 清单、真实容量数据尚未提供 |
| 可观测性闭环 | Prometheus 规则与看板已验证，但未接 Alertmanager 通知通道（飞书/邮件） |
| 前端版本标记 | 前端界面版本号停在 P33.0，与后端 P36 不同步（无功能差异） |

> 这些缺口是后续演进方向，欢迎在 Issue 中讨论你关心的部分。

## 路线图

- **接入层**：RTSP/GB28181 取流、ZLMediaKit/WebRTC 播放、真实推理服务对接
- **资产层**：BIM/CAD 导入、正射影像贴图、大场景 LOD
- **生产化**：K8s 部署清单、Alertmanager 通知、多副本任务领取（`SKIP LOCKED`）
- **体验**：前端版本同步、指标内嵌面板

## 贡献

见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。发现问题请按 [Issue 模板](.github/ISSUE_TEMPLATE) 提交；
安全问题请按 [`SECURITY.md`](SECURITY.md) 私下报告。

## 许可

[MIT](LICENSE) © 2026 Digital Twin Platform contributors
