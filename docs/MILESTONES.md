# 里程碑索引（P0 → P41）

每个阶段都有独立契约文档、验收记录与提交；本页是快速导航。

| 阶段 | 主题 | 契约文档 |
|---|---|---|
| P0 | 事件链路/规则引擎/三维首屏 | `docs/API.md` |
| P1 | 标定/轨迹/处置/统计/视频 | `docs/API_P1.md` |
| P2 | JWT RBAC/工单 | `docs/API_P2.md` |
| P3 | 区域数据权限/通知/升级 | `docs/API_P3.md` |
| P4 | 值班与自动派单/重试静默 | `docs/API_P4.md` |
| P5 | 可回放事件总线 | `docs/API_P5.md` |
| P6 | 2.5D 地图/glTF/视频融合 | `docs/API_P6.md` |
| P7 | 用户管理 | `docs/API_P7.md` |
| P8 | 值班班次/轮转/去重/二次升级 | `docs/API_P8.md` |
| P9 | 背压/采样/限流/压测/运维 | `docs/API_P9.md` |
| P10 | 登录防爆破/密码策略/审计导出/安全头/HTTPS | `docs/API_P10.md` |
| P11 | 刷新令牌/会话吊销/强制下线 | `docs/API_P11.md` |
| P12 | 三维测距/框选/巡检路线 | `docs/API_P12.md` |
| P13 | 巡检计划与执行(异常建单) | `docs/API_P13.md` |
| P14 | 巡检报表/导出/归档 | `docs/API_P14.md` |
| P15 | 告警联动巡检 | `docs/API_P15.md` |
| P16 | 任务取消/超时治理 | `docs/API_P16.md` |
| P17 | PostgreSQL 全链路验证与修复/备份恢复 | — |
| P18 | CI 流水线 | — |
| P19 | 多副本 ingest 回落 | — |
| P20 | 代码质量门禁 | — |
| P21 | auth/store 补测 | — |
| P22 | 错误哨兵统一 | — |
| P23 | realtime/config/model 补测 | — |
| P24 | store 巡检/事件/标定补测 | — |
| P25 | rules/metrics 补测 | — |
| P26 | API 读路径/错误分支补测 | — |
| P27 | 多租户隔离基础 | `docs/API_P27.md` |

## 验收与运维文档

| 文档 | 内容 |
|---|---|
| `docs/ACCEPTANCE.md` | 逐阶段验收记录（测试、smoke、E2E、已知限制） |
| `docs/ARCHITECTURE.md` | 架构与各阶段落地位置 |
| `docs/OPERATIONS.md` | 部署/备份恢复/升级/排障/错误语义约定 |
| `docs/PERFORMANCE.md` | 压测矩阵与调优建议 |
| `docs/CI.md` | CI 流水线、质量门禁、本地等价脚本 |
| `docs/API_P8.md` … `docs/API_P27.md` | 各阶段 API 增量契约 |
| `docs/screenshots/` | 各阶段界面截图 |
