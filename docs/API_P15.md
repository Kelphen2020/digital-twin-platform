# P15.0 API 扩展（告警联动巡检）

在 P0–P14 契约之上追加：告警升级时自动生成就近巡检任务，把「告警 → 处置」链路延伸到现场巡检。

## 1. 升级设置新增字段

```json
{
  "enabled": true, "after_seconds": 300, "severities": ["warn", "critical"],
  "auto_dispatch": true, "second_after_seconds": 0,
  "auto_patrol": false
}
```

- `auto_patrol`（默认 false）：告警首次升级/再升级时，自动生成一条待执行巡检任务。

## 2. 任务生成规则

- 路由匹配顺序：
  1. `camera_ids` 包含该事件相机的巡检路线（多条取最近更新）；
  2. 否则取航点与相机 **XZ 平面**距离最近的路线；
  3. 无路线 → 跳过（审计 `patrol.execution.skipped`，reason=`no_route`）。
- 生成执行：

```json
{
  "id": "pex_...", "schedule_id": "", "route_id": "patrol_...", "route_name": "…",
  "event_id": "evt_...", "status": "pending", "assignee": "",
  "note": "告警联动：intrusion · CAM-001 东门",
  "scheduled_at": "...", "created_at": "..."
}
```

- 去重：同一 `event_id` 已有 `pending`/`running` 任务时跳过（审计 `patrol.execution.skipped`，reason=`duplicate`）。
- 推送 WS `patrol_execution`；审计 `patrol.execution.created`（detail.source=`event`）。

## 3. PatrolExecution 新增字段

`event_id`（string，可为空；空表示计划/手动任务）。

执行列表过滤新增：`GET /api/patrol/executions?event_id=evt_...`。

## 4. 前端

- 设置页：升级设置增加「告警联动巡检」开关 + 说明。
- 巡检执行 tab：卡片显示关联告警（事件类型/相机/事件 id，点击可定位相机）；筛选支持按事件查看。
