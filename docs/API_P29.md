# P29.0 API 扩展（租户初始化模板 / 租户管理员 / 总线租户隔离）

在 P0–P28 契约之上追加三块能力。

## 1. 租户初始化模板

### `POST /api/tenants` 新增字段

```json
{ "name": "XX园区", "template": "default", "admin": { "username": "admin", "password": "Admin@123456" } }
```

- `template`（可选）：租户 ID，复制该租户的 **相机、区域、相机规则、设置**（不含事件/工单/审计/用户/巡检）。
- `template` 不存在 → `400 bad_request`（"unknown template"）。
- `admin`（可选）：创建该租户的初始管理员账号（username/password 走 P10 密码策略；冲突 → `409 conflict`）。用户名在同一租户内唯一，因此不同租户可用相同用户名。
- 响应新增 `seeded` 字段：`{ "cameras": 6, "areas": 2, "rules": 6, "settings": 2, "admin": "admin" }`（未复制则计数为 0，未建管理员则为空字符串）。
- 审计 `tenant.create`（detail 增加 template/seeded）。

## 2. 租户管理员角色

- 新角色 `tenant-admin`（`auth.RoleTenantAdmin = "tenant_admin"`）：
  - 可访问「用户管理」与「租户设置」相关接口，但**只能操作本租户**（租户来自请求头/JWT，天然隔离）；
  - 不可访问 `GET/POST/PATCH /api/tenants`（跨租户管理）→ `403 forbidden`；
  - 具备 operator 之外的管理权限：用户 CRUD、审计、用户会话/强制下线（本租户内）。
- `PermUserAdmin`（用户管理）与 `PermAuditRead` 允许 `admin` 与 `tenant_admin`；`PermTenantAdmin` 仅 `admin`。
- 前端：角色下拉新增「租户管理员」；租户管理员登录后显示「用户/审计」页签，隐藏「租户」页签。

## 3. 事件总线租户隔离

- `Envelope` 新增 `tenant_id`；发布时打标当前租户；`ReadRange` 支持按租户过滤。
- `GET /api/bus/status` 返回 `tenants` 计数（可选）；`POST /api/bus/replay` 默认只回放**调用者租户**的事件；`all_tenants: true`（仅 admin）可回放全部。
- jsonl 驱动：新行带 `tenant_id`；旧行（无字段）视为 `default`。
- 响应 `replayed` 列表中每条带 `tenant_id`。

## 4. 测试与兼容

- 无租户头/默认租户行为不变；smoke 263 项必须保持全绿。
- 迁移：无需新表/列（模板复制走存储接口；bus jsonl 行格式向后兼容）。