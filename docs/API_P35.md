# P35.0 变更（多租户可观测性）

指标层扩展（无新增业务端点，`/metrics` 新增序列）：

| 指标 | 类型 | 说明 |
|---|---|---|
| `dsh_twin_tenant_requests_total{tenant}` | counter | 各租户 API 请求数（P27 起已有，本次纳入看板） |
| `dsh_twin_tenant_ingest_total{tenant}` | counter | 各租户成功 ingest 次数（新增） |
| `dsh_twin_tenant_cameras_online{tenant}` | gauge | 各租户在线相机数（新增，stats 广播与 `/metrics` 时刷新） |
| `dsh_twin_tenant_quota_exceeded_total{tenant,dimension}` | counter | 租户配额拒绝（P30 起） |
| `dsh_twin_export_jobs_purged_total` | counter | 导出任务保留清理数（P33 起） |

- 看板 `deploy/observability/grafana-dashboard.json`：新增「多租户（P35）」分组（租户总数/请求速率 Top5/在线相机/配额拒绝/ingest 速率/配额明细），并新增 `tenant` 模板变量（多选，默认 All）。
- 告警 `deploy/observability/alerts.yml`：新增分组 `digital-twin-tenant`（TenantQuotaExceeded / TenantNoOnlineCameras / TenantIngestSilent / TenantRequestSpike），带 runbook 链接。
- 处置手册：`docs/OPERATIONS.md#3-观测` 与「3.1 租户告警处置」。
