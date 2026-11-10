# P16.0 API 扩展（巡检任务超时治理与取消）

在 P0–P15 契约之上追加：解决 pending/running 任务长期堆积的问题，新增取消与超时自动关闭。

## 1. 状态扩展

`PatrolExecution.status` 新增 `cancelled`（已取消/超时关闭）。

```
pending → running → done | issue
pending → cancelled（人工取消或超时未认领）
running → cancelled（超时未完成）
```

## 2. 人工取消

### `POST /api/patrol/executions/{id}/cancel`

权限：operator/admin。已认领任务仅认领人或 admin 可取消。

请求体可选：

```json
{ "note": "现场无需巡检" }
```

- pending/running → cancelled：写 `finished_at`、`note`（用户备注或默认「人工取消」）、`result="cancelled"`。
- 已完成/已取消任务取消 → `409 invalid_state`。
- 审计 `patrol.execution.cancel`；WS 推送 `patrol_execution`。

## 3. 超时自动关闭

调度器每次扫描（`PATROL_TICK_SECONDS`）同时执行超时治理：

| 状态 | 超时阈值 | 动作 |
|---|---|---|
| pending | `PATROL_PENDING_TIMEOUT_MINUTES`（默认 60） | → cancelled，note「超时未认领，自动关闭」 |
| running | `PATROL_RUNNING_TIMEOUT_MINUTES`（默认 120） | → cancelled，note「超时未完成，自动关闭」 |

- 审计 `patrol.execution.expired`（detail：previous_status、timeout_minutes）。
- 推送 WS `patrol_execution`。
- 阈值设为 0 表示关闭对应治理。

## 4. 统计与清理

- `GET /api/patrol/stats` 响应新增 `cancelled` 计数（`completion_rate`/`issue_rate` 分母仍为总数）。
- 历史归档 `DELETE /api/patrol/executions` 的 `status` 允许值扩展为 `done,issue,cancelled`（默认 `done,issue`）。

## 5. 前端

- 执行列表新增「已取消」状态 chip 与筛选。
- 待执行/进行中卡片提供「取消」按钮（二次确认；running 仅认领人或 admin 可见）；取消后刷新统计/列表。
- 统计卡片新增「已取消」。

## 6. 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `PATROL_PENDING_TIMEOUT_MINUTES` | 60 | 待认领超时（0=关闭） |
| `PATROL_RUNNING_TIMEOUT_MINUTES` | 120 | 进行中超时（0=关闭） |
