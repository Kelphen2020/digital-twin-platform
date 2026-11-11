# 文档地图

本仓库文档按用途分组，避免在 20+ 个阶段文档里迷路。

## 1. 入口

| 文档 | 说明 |
|---|---|
| [`README.md`](../README.md) | 项目总览、快速开始、里程碑速览 |
| [`docs/MILESTONES.md`](MILESTONES.md) | P0→P29 契约文档导航表 |

## 2. 契约（按阶段）

| 阶段 | 文档 |
|---|---|
| P0 | [`API.md`](API.md) |
| P1+ | 见 `docs/API_P1.md` … `docs/API_P16.md`、`docs/API_P27.md`（多租户）、`docs/API_P29.md`（租户模板/角色/总线）与 `docs/P19.md`（部署与扩展） |

## 3. 验收与质量

| 文档 | 说明 |
|---|---|
| [`ACCEPTANCE.md`](ACCEPTANCE.md) | 逐阶段验收记录（测试命令、结果、已知限制） |
| [`CI.md`](CI.md) | GitHub Actions、本地等价脚本、质量门禁（golangci-lint / eslint / 覆盖率 62%） |
| [`PERFORMANCE.md`](PERFORMANCE.md) | 压测矩阵、瓶颈与调优建议 |

## 4. 运行与运维

| 文档 | 说明 |
|---|---|
| [`OPERATIONS.md`](OPERATIONS.md) | 启停、环境变量、观测、备份恢复、升级、排障、错误语义约定 |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | 分层架构与各阶段落地位置 |
| [`P0_SCOPE.md`](P0_SCOPE.md) | 最初的 P0 范围定义（历史基线） |

## 5. 截图

`docs/screenshots/` 按阶段前缀命名（`p0-*.png` … `p29-*.png`），与 ACCEPTANCE 记录一一对应。
