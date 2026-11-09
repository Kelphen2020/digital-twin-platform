# P11.0 API 扩展（刷新令牌 / 会话吊销 / 在线会话）

在 P0–P10 契约之上追加。目标：短效 access token + 可轮换 refresh token + 主动吊销，补齐会话安全生命周期。

## 1. 登录与刷新

### `POST /api/auth/login`（响应扩展）

```json
{
  "token": "<access jwt>", "expires_at": "2026-11-09T12:00:00Z",
  "refresh_token": "<opaque 32-byte random>", "refresh_expires_at": "2026-11-16T11:00:00Z",
  "user": { ... }
}
```

- Access token TTL：`ACCESS_TOKEN_TTL_SECONDS`（默认 3600）。
- Refresh token TTL：`REFRESH_TOKEN_TTL_SECONDS`（默认 604800，7 天）。
- Refresh token 只存 SHA-256 哈希；服务端记录 IP、User-Agent。

### `POST /api/auth/refresh`

```json
{ "refresh_token": "<opaque>" }
```

响应同登录的 token 字段（不含 user）：新的 access token + **轮换后的新 refresh token**（旧 refresh token 立即失效）。失效/过期/已轮换的 refresh token → `401 invalid_refresh_token`。

### `POST /api/auth/logout`

```json
{ "refresh_token": "<opaque>" }
```

吊销该 refresh token；幂等，成功 `204`。

### `POST /api/auth/logout-all`（登录用户）

吊销该用户全部 refresh token 并递增 token 版本（使已签发 access token 立即失效）。`204`。

## 2. 会话管理（admin）

### `GET /api/users/{id}/sessions`

```json
{ "items": [ { "id": "rft_...", "created_at": "...", "expires_at": "...",
               "ip": "127.0.0.1", "user_agent": "Mozilla/5.0 ..." } ] }
```

仅返回未吊销、未过期的 refresh 会话。

### `POST /api/users/{id}/logout`

强制该用户下线：吊销其全部 refresh token 并递增 token 版本。`204`。不能对自己使用（`400 self_protected`，请用 `/api/auth/logout-all`）。

## 3. Access token 失效规则

- 每次认证除校验 JWT 签名/过期外，还校验用户 `token_version` 与 `active`；版本不匹配或已停用 → `401 unauthorized`。
- 触发版本递增：`/api/auth/logout-all`、`/api/users/{id}/logout`、**管理员修改用户密码**。
- 修改用户密码后该用户所有已签发 access token 失效（需重新登录），UI 需处理 401 自动登出。

## 4. 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `ACCESS_TOKEN_TTL_SECONDS` | 3600 | access token 有效期 |
| `REFRESH_TOKEN_TTL_SECONDS` | 604800 | refresh token 有效期 |

## 5. 数据库

新增表 `refresh_tokens`、用户表新增 `token_version`（迁移 008）。内存模式重启后会话丢失（需重新登录），PostgreSQL 模式持久化。
