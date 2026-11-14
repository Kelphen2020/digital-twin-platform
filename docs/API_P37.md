# P37.0 API 扩展（租户运维概览）

新增一个供前端内嵌面板使用的聚合接口，把 Prometheus 里最关键的一组租户指标以 JSON 形式暴露给管理界面。

## `GET /api/tenants/{id}/overview`（admin）

响应：

```json
{
  "tenant": "default",
  "generated_at": "2026-11-14T03:18:15Z",
  "events": { "total": 12, "last_24h": 12, "by_severity": { "warn": 10, "critical": 2 } },
  "cameras": { "total": 6, "online": 5, "offline": 1 },
  "open_orders": 3,
  "patrol": { "routes": 2, "schedules": 4, "pending": 1, "in_progress": 1 },
  "exports": { "jobs": 5, "done": 4, "failed": 0, "active": 1 },
  "ingest": { "accepted": 120, "rejected": 0 },
  "health": { "bus_driver": "jsonl", "bus_next_offset": 5864, "ws_clients": 1, "store_mode": "memory" },
  "recent_hourly": [ { "hour": "2026-11-14T03:00:00Z", "count": 4 } ]
}
```

- 权限：仅 `admin`（`PermTenantAdmin`）；非 admin → 403，未知租户 → 404 `not_found`。
- `events.total` 为该租户累计事件数；`last_24h` 与 `recent_hourly` 为近 24 小时口径。
- `ingest.accepted` 来自进程内计数（`dsh_twin_tenant_ingest_total` 同源）。
- `health.bus_driver` 在未挂载总线时返回 `"none"`。

## 前端

- 新增「概览」页签（仅 admin，位于「统计」之后）：6 个 KPI + 近 12 小时事件柱图 + 严重级别分布 + 运行状态，每 30 秒自动刷新。
- 版本标记同步至 **P37.0**（此前前端停留在 P33.0）。
