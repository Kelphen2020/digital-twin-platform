# P14.0 API 扩展（巡检报表与历史归档）

在 P0–P13 契约之上追加：巡检执行统计、CSV 导出与历史归档清理。

## 1. 巡检统计

### `GET /api/patrol/stats?from=&to=`（登录用户）

`from`/`to` 为 RFC3339，按执行任务 `created_at` 过滤（可选）。

```json
{
  "from": "2026-11-01T00:00:00Z",
  "to": "2026-11-30T23:59:59Z",
  "executions_total": 12,
  "pending": 2, "running": 1, "done": 7, "issue": 2,
  "completion_rate": 0.583,
  "issue_rate": 0.167,
  "avg_duration_seconds": 143.2,
  "by_route": [ { "route_id": "patrol_...", "route_name": "东门→主干道", "total": 5, "done": 3, "issue": 1 } ],
  "by_assignee": [ { "assignee": "operator", "total": 9, "done": 7, "issue": 2 } ]
}
```

- `completion_rate = done / executions_total`；`issue_rate = issue / executions_total`（保留 3 位小数）。
- `avg_duration_seconds` 仅统计同时有 `started_at`/`finished_at` 的已结束任务。

## 2. 执行导出

### `GET /api/patrol/executions/export?from=&to=&status=`（operator/admin）

- `text/csv; charset=utf-8`，`Content-Disposition: attachment; filename="patrol-executions.csv"`。
- 表头：`id,schedule_id,route_name,status,assignee,result,note,scheduled_at,started_at,finished_at,order_id`
- 上限 10000 行；`status` 可选（pending/running/done/issue）；时间过滤同统计。

## 3. 历史归档

### `DELETE /api/patrol/executions?before=<RFC3339>&status=done,issue`（仅 admin）

- 删除 **已结束**（done/issue）且 `created_at <= before` 的执行记录。
- `before` 缺省为当前时间；`status` 缺省 `done,issue`（只允许这两个状态）。
- 响应：`{ "deleted": 3 }`；审计 `patrol.executions.purge`（detail：before/status/deleted）。

## 4. 前端

巡检浮层「执行」tab 顶部统计卡片（总数/待执行/进行中/已完成/异常/完成率/异常率/平均时长）、按路线与按人汇总、导出 CSV、admin「清理历史」（before 日期 + 二次确认）。
