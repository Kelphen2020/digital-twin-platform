# P33.0 API 扩展（导出任务持久化 + 保留策略）

P32 的导出任务只存在于进程内存，重启即丢、多副本不共享、产物无清理。P33 将其持久化并加入保留策略。

## 1. 持久化（迁移 016）

```sql
CREATE TABLE IF NOT EXISTS export_jobs (
    id            text PRIMARY KEY,
    tenant_id     text NOT NULL,
    status        text NOT NULL,
    include_tracks boolean NOT NULL DEFAULT false,
    include_audit  boolean NOT NULL DEFAULT false,
    created_by    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL,
    started_at    timestamptz,
    finished_at   timestamptz,
    size_bytes    bigint NOT NULL DEFAULT 0,
    counts        jsonb NOT NULL DEFAULT '{}'::jsonb,
    error         text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS export_jobs_tenant_created_idx ON export_jobs (tenant_id, created_at DESC);
```

- 存储接口新增：`CreateExportJob / UpdateExportJob / GetExportJob / ListExportJobs(tenantID, limit) / DeleteExportJob / PurgeExportJobs(before)`，内存与 PostgreSQL 均实现（PG 用 jsonb 存 counts）。
- `Store` 方法**不带** `WithTenant` 依赖（导出任务按显式 tenant_id 存取，admin 可见全租户），与现有租户作用域互不干扰。

## 2. 行为

| 场景 | 行为 |
|---|---|
| API 重启 | 任务列表从存储恢复；`pending/running` 状态在启动时标记为 `failed`（reason `interrupted by restart`） |
| 多副本 | 任务行共享；worker 领取用 `UPDATE ... SET status='running' WHERE id=$1 AND status='pending'`，抢不到则跳过（避免重复生成） |
| 列表 | `GET /api/tenants/{id}/exports?limit=50` 从存储读取（默认 50，最大 200） |
| 删除 | 同时删任务行与产物文件 |
| 租户删除（P30 `DELETE /api/tenants/{id}`） | 一并清理该租户的导出任务行与产物文件 |

## 3. 保留策略

- 环境变量 `EXPORT_RETENTION`（默认 `168h`，`0` 表示不清理）、`EXPORT_MAX_JOBS`（默认 `200`，按租户保留最新 N 条，`0` 表示不限）。
- 后台清理任务：每 10 分钟执行一次，删除超过保留期或超过上限的已完成/失败任务（含产物文件），运行中任务不清理。
- 指标：`dsh_twin_export_jobs_purged_total`。
- 手动清理：`POST /api/tenants/{id}/exports/purge?before=<RFC3339>`（admin）立即清理该租户早于 before 的已完成/失败任务，响应 `{"purged": n}`。

## 4. 前端

- 导出记录列表直接读持久化数据（重启后仍在）；`failed` 任务显示错误原因与「重试」按钮（重新创建任务）。
- 清理入口：租户导出记录区「清理历史」按钮 → 调用 purge（默认 7 天前）。