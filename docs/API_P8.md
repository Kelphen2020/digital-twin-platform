# P8.0 API 扩展（值班排班 / 工单去重与轮转 / 超时再升级）

在 P0–P7 契约之上追加，补全「值班 → 派单 → 升级」的运营闭环。

## 1. 值班班次

DutyGroup 新增字段：

```json
{
  "rotation": "group",
  "shifts": [
    { "id": "shift_xxx", "name": "早班", "start": "08:00", "end": "16:00",
      "weekdays": [1, 2, 3, 4, 5], "operators": ["operator"] }
  ]
}
```

- `rotation`：`group`（默认，派单给班组名）或 `operator`（按班次成员轮转）。
- `shifts`：为空时沿用 `operators` 全天值班；非空时**只有落在班次内的时间才算值班**（无人班次 → 无值班）。
- `weekdays`：ISO 周几（1=周一 … 7=周日）；`start`/`end` 为 `HH:MM`，`end=00:00` 表示当天结束（等效 24:00，如 08:00–00:00 为 16 小时班）；其余 `end <= start` 视为跨夜（如 22:00–06:00）。
- 新班次不需要传 `id`（服务端生成）；编辑时传已有 `id` 更新、缺失则新增。
- 校验：名称非空、时间格式、weekdays 1–7、operators 必须是已知用户。

### `GET /api/duty/current?area_id=area_gate&at=<RFC3339>`

`at` 可选（默认当前时间，跨时区以服务端 Asia/Shanghai 解释）。响应新增 `shift` 与 `operators`：

```json
{ "area_id": "area_gate", "group": DutyGroup, "shift": { "id": "...", "name": "早班", "start": "08:00", "end": "16:00" } | null,
  "operators": ["operator"] }
```

## 2. 自动派单：去重与轮转

- **工单去重**：同一事件已有未关闭工单（`open`/`assigned`/`processing`）时再次触发自动派单 → 跳过，审计 `order.auto_dispatch_skipped`（detail.reason=`open_order`），指标 `dsh_twin_orders_deduped_total` +1。
- **接手人轮转**：`rotation=operator` 且当前班次有成员时，派单 assignee 按班次成员轮转（进程内轮询，按班组+班次记录）；否则 assignee=班组名（保持 P4 行为）。
- 工单 `note` 追加班组/班次信息：`系统自动派单（告警升级） · <班组> · <班次>`。

## 3. 超时再升级

升级设置新增字段：

```json
{ "enabled": true, "after_seconds": 300, "severities": ["warn","critical"],
  "auto_dispatch": true, "second_after_seconds": 0 }
```

- `second_after_seconds` ∈ [0, 604800]，0 表示关闭。
- 升级后再超过该时长仍未关闭（status ≠ closed）→ 写入 `re_escalated_at`，WS `event_updated`，通知（kind=`escalation`），审计 `event.re_escalate`，指标 `dsh_twin_re_escalations_total`。
- 再升级同样触发自动派单，但受上面的工单去重限制（同事件已有未关闭工单 → 跳过并记 deduped）。

Event 新增字段：`re_escalated_at`（可空）。

## 4. 指标追加

```
dsh_twin_orders_deduped_total 3
dsh_twin_re_escalations_total 1
```

## 5. 审计新增

`order.auto_dispatch_skipped`（reason=`open_order` 或 `no_duty`）、`event.re_escalate`。
