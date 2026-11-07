# P3.0 API 扩展（数据级权限 / 告警升级 / 通知与设置）

在 `docs/API.md`、`API_P1.md`、`API_P2.md` 之上追加。

## 1. 数据级权限（读取过滤）

非 admin 且 `areas` 非空时，以下只读接口只返回其负责区域内的相机数据；admin 与 `areas` 为空的用户不受限：

| 接口 | 过滤规则 |
|---|---|
| `GET /api/cameras` | 仅 areas 内相机 |
| `GET /api/cameras/{id}`、`GET /api/cameras/{id}/rules` | 区域外 → `403 out_of_scope` |
| `GET /api/events`、`GET /api/events/{id}` | 仅 areas 内相机的事件；详情区域外 → `403 out_of_scope` |
| `GET /api/tracks` | 仅 areas 内相机 |
| `GET /api/orders`、`GET /api/orders/{id}` | 仅 areas 内相机的工单；详情区域外 → `403 out_of_scope` |
| `GET /api/stats/summary`、`GET /api/stats/events.csv` | 仅 areas 内相机 |
| `GET /api/audit` | 仅 admin（其他角色 `403 forbidden`） |
| `GET /api/notifications` | 仅 admin |

事件新增字段 `escalated_at`（可空）。

## 2. 告警升级

升级设置（key `escalation`，默认值）：

```json
{ "enabled": true, "after_seconds": 300, "severities": ["warn", "critical"] }
```

- 服务端每 15 秒扫描一次：`status != closed`、`escalated_at` 为空、`occurred_at < now - after_seconds` 且严重级别匹配的事件 → 写入 `escalated_at`，通过 WS 广播 `event_updated`，并触发一次 `escalation` 通知。
- 已升级事件在列表中带 `escalated_at`；前端显示「已升级」。

### `GET /api/settings/escalation`（admin）

返回当前设置。

### `PUT /api/settings/escalation`（admin）

```json
{ "enabled": true, "after_seconds": 1, "severities": ["warn", "critical"] }
```

约束：`after_seconds` ∈ [0, 86400]；`severities` 限定 `info|warn|critical`。响应 `200` 为保存后设置。

## 3. 通知（Webhook）

通知设置（key `notifications`，默认值）：

```json
{ "enabled": false, "webhook_url": "", "format": "generic",
  "min_severity": "warn", "timeout_seconds": 5 }
```

- `format`：`generic`（POST 事件 JSON）或 `feishu`（飞书自定义机器人格式 `{"msg_type":"text","content":{"text":"..."}}`）。
- 触发时机：新事件产生（严重级别 ≥ `min_severity`）与告警升级。
- 投递记录保留最近 1000 条，`GET /api/notifications?limit=50`（admin）返回：

```json
{ "items": [ { "id": "ntf_...", "at": "...", "kind": "event", "event_id": "evt_...",
              "url": "http://127.0.0.1:8099/hook", "format": "generic",
              "ok": true, "status_code": 200, "error": "" } ] }
```

### `GET /api/settings/notifications`（admin）

### `PUT /api/settings/notifications`（admin）

```json
{ "enabled": true, "webhook_url": "http://127.0.0.1:8099/hook",
  "format": "generic", "min_severity": "warn", "timeout_seconds": 5 }
```

校验：`webhook_url` 为 http/https；`format` ∈ `generic|feishu`；`min_severity` ∈ `info|warn|critical`；`timeout_seconds` ∈ [1,30]。响应 `200` 为保存后设置，写入审计 `settings.notifications.put`。

## 4. WebSocket

沿用 `event_updated`（升级时携带 `escalated_at`）。

## 5. 审计新增

`event.escalate`（actor=`system`）、`settings.notifications.put`、`settings.escalation.put`。
