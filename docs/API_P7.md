# P7.0 API 扩展（用户与账号管理）

在 P0–P6 契约之上追加：把 P2 的内置种子账号升级为**可持久化、可由管理员维护的账号体系**（仍为单租户；多租户留待后续）。

## 用户对象

```json
{
  "id": "usr_...",
  "username": "operator2",
  "display_name": "值班员二",
  "role": "operator",
  "areas": ["area_gate"],
  "active": true,
  "created_at": "...",
  "updated_at": "..."
}
```

- 密码永远不返回；`active=false` 的账号无法登录（`401 invalid_credentials`）。
- 角色与区域沿用 P2 语义：`admin` / `operator` / `viewer`，`areas` 为空表示全部区域（仅 admin/观察员常用）。

## 接口（均仅 admin，`403 forbidden` 其他角色）

### `GET /api/users`

```json
{ "items": [ User ] }
```

### `POST /api/users`

```json
{ "username": "operator2", "password": "Passw0rd!", "display_name": "值班员二",
  "role": "operator", "areas": ["area_gate"], "active": true }
```

- `username` 唯一（重复 `409 username_taken`）、3–32 字符；`password` ≥ 8 字符；`role` 合法；`areas` 必须是已知区域。
- 响应 `201` User。

### `PATCH /api/users/{id}`

可修改 `display_name`、`password`、`role`、`areas`、`active`；不能停用/降级自己（`400 self_protected`）。响应 `200` User。

### `DELETE /api/users/{id}`

不能删除自己（`400 self_protected`）；响应 `204`。

## 认证变化

- 登录校验改为从用户库读取（bcrypt/PBKDF2 兼容现有种子口令哈希格式）；JWT 内容不变。
- 审计新增：`user.create`、`user.update`、`user.delete`（actor=当前管理员）。
- 内置种子账号（admin/operator/viewer）在用户表为空时自动写入；之后由管理员维护。

## 环境变量

无新增。
