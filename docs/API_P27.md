# P27.0 API 扩展（多租户：隔离基础）

在 P0–P26 契约之上引入租户维度。本轮交付「隔离基础」：账号、相机、区域、规则、设置、审计、工单、事件、巡检按租户隔离，并保证无租户头的旧客户端行为不变（默认租户）。

## 1. 租户

```json
{ "id": "default", "name": "默认园区", "active": true }
```

- 内置租户 `default`（名称「默认园区」），不可删除。
- 环境变量 `TENANT_SHARED=1` 时所有租户共享 `default` 的数据视图（兼容演示/单租户部署，默认 `0` 即严格隔离）。

### 接口（admin）

| 方法/路径 | 说明 |
|---|---|
| `GET /api/tenants` | 租户列表 `{ "items": [Tenant] }` |
| `POST /api/tenants` | 创建：`name`（必填、≤64 字符），`id` 自动生成 `tnt_...`；`active` 默认 true |
| `PATCH /api/tenants/{id}` | 修改 `name`/`active`；停用后该租户所有请求 403 `tenant_inactive` |

## 2. 租户解析与传递

- 请求头 `X-Tenant-ID` 指定租户；缺省或为空 → `default`。
- 未知租户 → `400 unknown_tenant`；租户被停用 → `403 tenant_inactive`。
- 登录响应新增 `tenant_id`；`POST /api/auth/login` 支持 body 字段 `tenant_id`（缺省 default）。JWT 声明新增 `tnt`，**跨租户的 token 无效**（认证中间件校验 `tnt` 与请求租户一致，否则 `403 cross_tenant`）。
- 管理员的租户归属即 `users.tenant_id`；非 admin 用户只能访问自身租户。

## 3. 数据隔离范围

按租户隔离（含索引与唯一约束）：`cameras`、`areas`、`events`、`orders`、`camera_rules`、`audit_log`、`notification_log`、`patrol_routes`、`patrol_schedules`、`patrol_executions`、`users`。

- 唯一约束调整：`cameras` 由全局 `code` 唯一改为 `(tenant_id, code)` 唯一；`users.username` 由全局唯一改为 `(tenant_id, username)` 唯一（不同租户可同名）。
- `settings` 主键改为 `(tenant_id, key)`；`duty_groups`、`track_points` 本轮仍为全局（后续阶段按租户迁移）。

## 4. 指标

```
dsh_twin_tenant_requests_total{tenant="default"} 128
```

## 5. 审计

`tenant.create`、`tenant.update`（actor=管理员用户名）。

## 6. 前端

顶栏显示当前租户名；admin「租户」页签（列表/新建/停用/改名），切换租户后重新登录以刷新数据视图。