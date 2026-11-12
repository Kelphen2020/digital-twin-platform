# P32.0 API 扩展（异步导出任务）

在 P0–P31 契约之上把大租户导出改为后台任务，避免同步请求超时/内存峰值。

## 1. 导出任务模型

```json
{
  "id": "exp_...",
  "tenant_id": "default",
  "status": "pending|running|done|failed",
  "include_tracks": false,
  "include_audit": false,
  "created_by": "admin",
  "created_at": "...", "started_at": null, "finished_at": null,
  "size_bytes": 0,
  "counts": { "cameras": 6, "events": 12, ... },
  "error": ""
}
```

## 2. 接口

| 方法/路径 | 权限 | 说明 |
|---|---|---|
| `POST /api/tenants/{id}/exports` | admin | 创建导出任务，body 可选 `{"include_tracks":true,"include_audit":true}`；响应 `202` + 任务 |
| `GET /api/tenants/{id}/exports` | admin | 该租户任务列表（按创建时间倒序，最多 50） |
| `GET /api/tenants/{id}/exports/{exportId}` | admin | 任务详情 |
| `GET /api/tenants/{id}/exports/{exportId}/download` | admin | 任务完成后下载 JSON（`attachment; filename="tenant-<id>-<exportId>.json"`）；未完成 → `409 invalid_state` |
| `DELETE /api/tenants/{id}/exports/{exportId}` | admin | 删除任务与产物文件 |

- 任务在 API 进程内异步执行（worker goroutine），产物写入 `EXPORT_DIR`（默认 `data/exports`），文件名 `<exportId>.json`。
- 同一租户同时最多 2 个 pending/running 任务，超出 → `429 too_many_exports`。
- 任务完成后 WS 推送 `export_status`（含 id/status/counts/size_bytes）。
- 审计：`tenant.export.start`、`tenant.export.done`（detail 含 size_bytes）、`tenant.export.failed`。

## 3. 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `EXPORT_DIR` | `data/exports` | 导出产物目录 |
| `EXPORT_WORKERS` | 2 | 并发导出 worker 数 |

## 4. 前端

租户页签每行「导出」升级为：创建任务 → 列表显示状态（生成中/已完成/失败 + 大小）→ 完成自动刷新（WS `export_status`）→ 「下载」按钮。保留 `include_tracks`/`include_audit` 勾选。