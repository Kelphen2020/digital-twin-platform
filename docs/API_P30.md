# P30.0 API 扩展（租户生命周期：归档与清理）

在 P0–P29 契约之上补齐租户的「可下线」能力：归档（可恢复的只读冻结）与彻底删除（数据清理）。

## 1. 租户归档

### `POST /api/tenants/{id}/archive`（admin）

- 归档租户：`archived=true`、`active=false`（停用），并记录 `archived_at`。
- 归档后该租户所有请求 → `403 tenant_archived`（区别于停用的 `tenant_inactive`）。
- 默认租户 `default` 不可归档 → `400 bad_request`。
- 审计 `tenant.archive`；响应返回更新后的 Tenant。

### `POST /api/tenants/{id}/restore`（admin）

- 恢复归档：`archived=false`、`active=true`、清空 `archived_at`。
- 审计 `tenant.restore`。

### Tenant 对象新增字段

```json
{ "id": "...", "name": "...", "active": true, "archived": false, "archived_at": null, ... }
```

## 2. 租户彻底删除

### `DELETE /api/tenants/{id}?confirm=<tenantId>`（admin）

- 必须 `confirm` 与路径 id 完全一致，否则 `400 bad_request`（防误操作）。
- 前置条件：租户必须已归档（`archived=true`）→ 否则 `409 invalid_state`。
- 默认租户不可删除 → `400 bad_request`。
- 清理范围（该租户全部数据）：相机、区域、相机规则、设置、事件、处置动作、工单、轨迹、审计日志（除本删除操作的 `tenant.delete` 记录外）、通知日志、值班组、巡检路线/计划/执行、刷新令牌、用户。
- 审计写入**默认租户**：`tenant.delete`（detail：删除计数、原租户名）。
- 响应：`{ "deleted": { "cameras": 6, "areas": 4, "events": 12, "users": 2, ... } }`。

## 3. 租户配额

Tenant 新增可选配额 `limits`：

```json
{ "limits": { "max_cameras": 100, "max_users": 20, "max_events": 100000, "ingest_qps": 50 } }
```

- 0 或缺省表示不限制。
- 超出时：创建相机/用户 → `429 quota_exceeded`（message 指明维度）；ingest 超过 `ingest_qps` → `429 quota_exceeded`。
- `PATCH /api/tenants/{id}` 可更新 `limits`（部分字段覆盖，0 表示清除限制）。
- 指标：`dsh_twin_tenant_quota_exceeded_total{tenant,dimension}`。

## 4. 前端

- 租户页签：归档/恢复按钮（二次确认）、删除（需先归档 + 输入租户 ID 确认）、配额编辑（相机/用户/事件/ingest QPS）。
- 错误映射：`tenant_archived` → 「租户已归档」；`quota_exceeded` → 「超出配额：…」。