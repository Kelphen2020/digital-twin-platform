# P31.0 API 扩展（租户数据导出与共享限流）

在 P0–P30 契约之上追加：租户数据包导出（归档/迁移/留档）与跨副本共享的租户配额限流。

## 1. 租户数据导出

### `GET /api/tenants/{id}/export`（admin）

- 响应 `Content-Type: application/json`、`Content-Disposition: attachment; filename="tenant-<id>.json"`。
- 结构：

```json
{
  "tenant": { "id": "...", "name": "...", "active": true, "archived": false, "limits": {...} },
  "exported_at": "2026-11-11T08:00:00Z",
  "counts": { "cameras": 6, "areas": 4, "events": 12, "users": 2, "orders": 3, ... },
  "data": {
    "areas": [...], "cameras": [...], "camera_rules": [...], "settings": {...},
    "users": [ { "id","username","display_name","role","areas","active","created_at" } ],
    "events": [...], "orders": [...], "notification_log": [...],
    "patrol_routes": [...], "patrol_schedules": [...], "patrol_executions": [...]
  }
}
```

- 导出**不含**：密码哈希、刷新令牌、轨迹点（数据量大，可选关闭）、审计日志（可选 `include_audit=1` 时导出最近 1000 条）。
- 可选查询参数：`include_tracks=1`（最近 1000 个轨迹点）、`include_audit=1`。
- 审计：`tenant.export`（记录条目数估算）。

### 导出为文件（可选，P31）

`POST /api/tenants/{id}/export?download_dir=/tmp` 不落地，仅返回 JSON；文件留下由调用方保存。

## 2. 共享限流（跨副本租户配额）

- 环境变量 `RATE_STORE=memory|postgres`（默认 `memory`）：`postgres` 时租户 ingest QPS 令牌桶落库，副本间共享。

```sql
CREATE TABLE IF NOT EXISTS tenant_rate_buckets (
    tenant_id text PRIMARY KEY,
    tokens    double precision NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
```

- 算法：固定时间窗口滑动补偿（读取当前令牌 → 根据 `updated_at` 补偿 → 不足则拒绝并写回），单事务 `SELECT ... FOR UPDATE`。
- 仅当租户 `limits.ingest_qps > 0` 时生效；`RATE_STORE=postgres` 但无数据库（内存模式）→ 回退内存实现并记录一次告警日志。
- 指标：沿用 `dsh_twin_tenant_quota_exceeded_total{tenant,dimension="ingest_qps"}`。

## 3. 前端

- 租户页签：每行「导出」按钮 → 下载 `tenant-<id>.json`，toast 显示条目数；导出前可勾选「包含轨迹/审计」。
- 归档提示：归档前建议先导出（toast 提示，不强制）。