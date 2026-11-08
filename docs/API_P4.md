# P4.0 API 扩展（值班组 / 自动派单 / 通知可靠性 / 指标）

在 `docs/API.md`、`API_P1.md`、`API_P2.md`、`API_P3.md` 之上追加。

## 1. 值班组

```json
{
  "id": "duty_...", "name": "东门值班组",
  "areas": ["area_gate", "area_parking"],
  "operators": ["operator", "viewer"],
  "active": true,
  "created_at": "...", "updated_at": "..."
}
```

- `areas` 为空表示「全局值班组」（兜底）；`active=false` 不参与值班解析。
- 值班解析规则：给定相机区域，优先匹配包含该区域且 `active` 的值班组（同一区域多个组时按创建时间取第一个）；否则使用 `areas` 为空的全局组；都没有则无值班。

### `GET /api/duty/groups`（登录用户）

```json
{ "items": [ DutyGroup ] }
```

### `POST /api/duty/groups`（admin）

```json
{ "name": "东门值班组", "areas": ["area_gate", "area_parking"], "operators": ["operator"], "active": true }
```

响应 `201` DutyGroup。`name` 必填；`areas` 中每个区域必须存在；`operators` 必须是已知用户名。

### `PATCH /api/duty/groups/{id}`（admin）

可修改 `name`、`areas`、`operators`、`active`；响应 `200` DutyGroup。

### `DELETE /api/duty/groups/{id}`（admin）

`204`。

### `GET /api/duty/current?area_id=area_gate`（登录用户）

```json
{ "area_id": "area_gate", "group": DutyGroup | null, "operators": ["operator"] }
```

无匹配时 `group` 为 `null`、`operators` 为 `[]`。

## 2. 升级自动派单

升级设置新增字段：

```json
{ "enabled": true, "after_seconds": 300, "severities": ["warn", "critical"],
  "auto_dispatch": true }
```

- 升级发生时，若 `auto_dispatch=true`：按事件相机区域解析值班组 → 自动创建工单（`status=open`、`priority=urgent`、`assignee=<值班组名>`、`title=告警升级 · <type> · <camera_code> <camera_name>`、`note=系统自动派单（告警升级）`），WS 广播 `order_updated`，审计 `order.auto_dispatch`（actor=`system`）。
- 无值班组时跳过并审计 `order.auto_dispatch_skipped`。
- 通知新增类型 `dispatch`（webhook 负载包含 `order` 字段）。

## 3. 通知可靠性

通知设置新增字段（默认值）：

```json
{ "enabled": false, "webhook_url": "", "format": "generic", "min_severity": "warn",
  "timeout_seconds": 5, "max_attempts": 3, "backoff_seconds": 2, "silence_seconds": 30 }
```

- `max_attempts` ∈ [1,5]：失败按指数退避重试（`backoff_seconds × 2^(n-1)`）。
- `silence_seconds` ∈ [0,3600]：同一相机+类型+严重级别在该窗口内只投递一次（抑制计数进指标）。
- 投递记录新增 `attempts`（实际尝试次数，成功或最终失败）。

`GET /api/notifications` 返回项新增 `attempts`。

## 4. 指标（`GET /metrics` 追加）

```
dsh_twin_notifications_total{result="ok"} 3
dsh_twin_notifications_total{result="failed"} 0
dsh_twin_notifications_suppressed_total 1
dsh_twin_escalations_total 2
dsh_twin_orders_total 4
dsh_twin_duty_dispatches_total 1
dsh_twin_duty_skipped_total 1
```

## 5. 审计新增

`duty.create`、`duty.update`、`duty.delete`、`order.auto_dispatch`、`order.auto_dispatch_skipped`。
