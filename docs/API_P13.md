# P13.0 API 扩展（巡检计划与执行）

在 P0–P12 契约之上追加：把巡检路线变成可排程、可执行、可留痕的任务，并与工单联动。

## 巡检计划 PatrolSchedule

```json
{
  "id": "psc_...", "name": "东门夜巡", "route_id": "patrol_...",
  "interval_minutes": 60, "enabled": true,
  "created_by": "operator",
  "created_at": "...", "updated_at": "...", "next_run_at": "..."
}
```

- `interval_minutes`：1–10080（7 天）。
- 创建时 `next_run_at = now + interval_minutes`。
- 调度器每 15 秒检查一次：`enabled && next_run_at <= now` → 生成待执行任务，并把 `next_run_at` 推进一个间隔。

## 巡检执行 PatrolExecution

```json
{
  "id": "pex_...", "schedule_id": "psc_...", "route_id": "patrol_...",
  "route_name": "东门→主干道", "status": "pending",
  "assignee": "", "result": "", "note": "", "order_id": "",
  "scheduled_at": "...", "created_at": "...",
  "started_at": null, "finished_at": null
}
```

状态：`pending → running → done`，或 `running → issue`（发现异常，自动创建工单）。
`schedule_id` 为空表示「立即执行」产生的临时任务。

## 接口

| 方法/路径 | 权限 | 说明 |
|---|---|---|
| `GET /api/patrol/schedules` | 登录 | 计划列表 |
| `POST /api/patrol/schedules` | operator/admin | 创建：name、route_id、interval_minutes、enabled |
| `PATCH /api/patrol/schedules/{id}` | 创建者/admin | 修改 name/interval/enabled/route_id |
| `DELETE /api/patrol/schedules/{id}` | 创建者/admin | 删除，`204` |
| `POST /api/patrol/schedules/{id}/run` | operator/admin | 立即生成一条执行任务，`201` 返回执行 |
| `GET /api/patrol/executions?status=&schedule_id=&limit=` | 登录 | 执行列表（默认 100，上限 500） |
| `POST /api/patrol/executions/{id}/claim` | operator/admin | pending → running，assignee=当前用户 |
| `POST /api/patrol/executions/{id}/complete` | 认领人/admin | body：`{"result":"done"\|"issue","note":"..."}`；issue 时创建工单 |

### 完成与工单联动

- `result=done`：状态 `done`，记录 note、finished_at。
- `result=issue`：状态 `issue`，自动创建工单：
  - title：`巡检异常 · <route_name>`
  - priority：`normal`，status：`open`，assignee：提交人
  - note：`巡检发现异常（执行 <execution_id>）：<note>`
  - camera_id：路线关联的第一台相机（无则空）
  - 响应体带 `order_id`。

## 审计

`patrol.schedule.create/update/delete/run`、`patrol.execution.claim/complete`、`order.created`（巡检异常）。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `PATROL_TICK_SECONDS` | 15 | 巡检调度扫描周期 |
