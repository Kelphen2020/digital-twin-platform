# P10.0 API 扩展（安全加固：登录防爆破 / 密码策略 / 审计导出 / 安全头 / HTTPS）

在 P0–P9 契约之上追加，不改既有业务接口形状。

## 1. 登录防爆破

- 同一 `username + 客户端 IP` 连续失败达到 `LOGIN_MAX_FAILURES`（默认 5）后锁定 `LOGIN_LOCKOUT_SECONDS`（默认 60 秒）。
- 锁定期内登录返回：

```json
HTTP 429
{ "error": "too_many_attempts", "message": "too many failed sign-in attempts; retry later" }
```

并带 `Retry-After: <seconds>` 响应头。
- 成功登录清零计数；锁定到期自动恢复。
- 客户端 IP 取 `X-Forwarded-For` 首个地址（反向代理场景），否则 `RemoteAddr`。

## 2. 密码策略（创建/修改用户）

- 长度 ≥ **10**；至少包含一个字母与一个数字；其余字符不限。
- 违反返回 `400 bad_request`，消息说明规则。
- 内置种子口令满足该策略（Admin@123456 / Operator@123 / Viewer@123）。

## 3. 审计导出

### `GET /api/audit/export?from=&to=&action=&actor=&limit=5000`（仅 admin）

- `text/csv; charset=utf-8`，`Content-Disposition: attachment; filename="audit.csv"`。
- 表头：`id,at,actor,action,resource,detail`
- `detail` 为 JSON 字符串（CSV 转义）；`limit` 默认 5000、上限 20000；`from`/`to` 为 RFC3339。

## 4. 安全响应头（所有 API 响应）

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: no-referrer
Permissions-Policy: geolocation=(), microphone=(), camera=()
```

## 5. HTTPS 部署

- 新增 `deploy/nginx-tls.conf`：TLS 终结 + HSTS、静态站点/API/WS/HLS 反代、证书路径占位（`/etc/nginx/certs/`）。
- `docs/OPERATIONS.md` 增加「HTTPS 与证书」章节（自签/Let's Encrypt 两种路径）。

## 6. 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `LOGIN_MAX_FAILURES` | 5 | 连续失败锁定阈值（0=关闭） |
| `LOGIN_LOCKOUT_SECONDS` | 60 | 锁定时长 |
