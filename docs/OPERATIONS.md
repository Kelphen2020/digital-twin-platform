# 运维手册（P9.0）

面向部署/值班同学：如何在单机或 compose 环境启动、观测、备份、升级与排障。

## 1. 进程与端口

| 组件 | 端口 | 启动 |
|---|---|---|
| API | 8080 | `make dev-api` 或 compose `api` |
| Web 开发服务器 | 5173 | `make dev-web` |
| Web 静态站点（compose） | 8081 | `docker compose up -d --build web` |
| PostgreSQL | 5432 | `make db-up` |
| Prometheus / Grafana | 9090 / 3000 | `make obs-up` |

健康检查：`curl -s http://127.0.0.1:8080/api/health`，期望 `{"status":"ok"}`。
WS 健康：浏览器 DevTools Network → `ws://…/ws`（101）。

## 2. 关键环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `AUTH_MODE` | on | 关闭仅用于本机联调 |
| `JWT_SECRET` | dev-secret-change-me | 共享环境必须替换 |
| `DATABASE_URL` | 空（内存） | 生产必须配置 PostgreSQL |
| `BUS_DRIVER` / `BUS_DIR` | jsonl / data/bus | 事件总线持久化 |
| `INGEST_RATE_LIMIT` / `INGEST_BURST` | 0 / 200 | 接入限流 |
| `WS_TRACK_SAMPLE` | 1 | 轨迹广播采样 |
| `HUB_CLIENT_BUFFER` | 256 | 单客户端 WS 队列 |
| `MAX_BODY_BYTES` | 1048576 | 请求体上限 |
| `ACCESS_TOKEN_TTL_SECONDS` | 3600 | access token 有效期 |
| `REFRESH_TOKEN_TTL_SECONDS` | 604800 | refresh token 有效期（7 天） |
| `SITE_CONFIG_FILE` | 空 | 站点/模型配置覆盖 |
| `NOTIFY_*` | 运行时设置 | 通知 webhook 由管理页配置 |

## 3. 观测

- 指标：`GET /metrics`（Prometheus 文本）。关键指标：
  - 业务：`dsh_twin_events_total`、`dsh_twin_cameras_online`、`dsh_twin_orders_total`、`dsh_twin_escalations_total`
  - 链路：`dsh_twin_ws_frames_total`、`dsh_twin_ws_dropped_total`、`dsh_twin_track_points_written_total`
  - 限流/通知：`dsh_twin_ingest_rejected_total`、`dsh_twin_notifications_total{result}`
  - 总线：`dsh_twin_bus_published_total`、`dsh_twin_bus_lag`
- 告警：`deploy/observability/alerts.yml`（API down、无在线相机、WS 丢帧、总线积压、通知失败、限流、升级激增）。
- 看板：`make obs-up` → http://127.0.0.1:3000（admin/admin 首次登录改密）。

## 4. 备份与恢复（PostgreSQL）

```bash
# 备份（默认写入 backups/twin-<时间>.dump）
make db-backup
# 或指定路径
./scripts/backup.sh backups/manual.dump

# 恢复（破坏性，先停 API）
docker compose -f deploy/docker-compose.yml stop api   # 或 kill 本地 API 进程
make db-restore DUMP=backups/twin-20261110-093000.dump
docker compose -f deploy/docker-compose.yml start api
```

- 容器名/用户/库可用 `PG_CONTAINER`/`PG_USER`/`PG_DB` 覆盖（compose 与本机容器一致默认 `dtp-postgres`）。
- 备份为 `pg_dump -Fc` 自定义格式；恢复使用 `pg_restore --clean --if-exists`。
- 迁移在 API 启动时自动幂等重放（`migrations/*.sql` 全部 `IF NOT EXISTS`），恢复旧备份后启动 API 会补齐新列/表。

内存模式不持久化业务数据（重启回到种子），仅用于演示/联调。

## 5. 升级

1. `git pull` + `make build-api && make build-web`（或 compose `up -d --build`）。
2. 迁移在 API 启动时自动执行（`migrations/*.sql` 幂等）。
3. 升级前备份数据库；升级后验证 `/api/health`、登录、相机列表、事件流。
4. 回滚：切回上一个 commit + 镜像 tag，恢复数据库备份（迁移只增列/建表，向后兼容）。

## 6. 常见故障

| 现象 | 排查 |
|---|---|
| 登录 401 | 用户被停用/密码变更；管理员在「用户」页确认 `active` |
| 会话频繁掉线 | 检查是否被强制下线/改密（token 版本变更）；内存模式重启会清空 refresh 会话（需重新登录） |
| 相机为空 | 数据级权限（用户 areas）或相机全部离线 |
| 事件不上图 | WS 断连（顶栏指示器）、`event` 帧被采样丢弃、权限过滤 |
| 轨迹闪烁/缺帧 | `WS_TRACK_SAMPLE` 过大或 `HUB_CLIENT_BUFFER` 过小，查看 `ws_dropped_total` |
| 通知不发送 | 管理页通知设置未启用/URL 错误；查 `/api/notifications` 投递记录 |
| ingest 429 | 触发 `INGEST_RATE_LIMIT`，看 `ingest_rejected_total`，调高或扩容 |
| 升级不触发 | 升级设置是否启用、严重级别匹配、扫描周期 15s |
| 总线告警 | `GET /api/bus/status` 看 lag；必要时回放或检查磁盘 |

## 7. HTTPS 与证书

- 模板：`deploy/nginx-tls.conf`（80→443 跳转、HSTS、TLS1.2/1.3、静态站点 + `/api`/`/ws`/`/hls` 反代）。证书挂载到 `/etc/nginx/certs/fullchain.pem` 与 `privkey.pem`。
- 自签（内网演示）：`openssl req -x509 -nodes -newkey rsa:2048 -days 365 -keyout privkey.pem -out fullchain.pem -subj "/CN=dtp.local"`。
- Let's Encrypt：域名解析到本机后 `certbot certonly --standalone -d <domain>`，把 `/etc/letsencrypt/live/<domain>/{fullchain,privkey}.pem` 挂载到上述路径；续期用 cron/systemd timer。
- 反代后客户端 IP 通过 `X-Forwarded-For` 传递（登录防爆破按该 IP 计数）。
- 生产建议：只暴露 443；`/metrics` 与 `/api/audit*` 限制到运维网段；开启 WAF/限流。

## 8. PostgreSQL 验证清单（P17）

```bash
# 1) 启动 PostGIS（本机 5432 被占用时换端口即可；镜像源可配）
docker run -d --name dtp-postgres \
  -e POSTGRES_USER=twin -e POSTGRES_PASSWORD=twin -e POSTGRES_DB=twin \
  -p 127.0.0.1:55432:5432 -v dtp-pgdata:/var/lib/postgresql/data \
  docker.m.daocloud.io/postgis/postgis:16-3.4

# 2) 以 Postgres 存储启动 API（迁移自动应用）
cd services/api && go build -o bin/api ./cmd/server
PORT=8081 AUTH_MODE=on JWT_SECRET=dev-secret-change-me \
  DATABASE_URL='postgres://twin:twin@127.0.0.1:55432/twin?sslmode=disable' ./bin/api

# 3) 全量验证（幂等，可重复跑）
API_BASE=http://127.0.0.1:8081 bash scripts/smoke.sh

# 4) 存储契约集成测试（需独立测试库，避免污染）
docker exec dtp-postgres psql -U twin -d twin -c 'CREATE DATABASE twin_test;'
PG_TEST_URL='postgres://twin:twin@127.0.0.1:55432/twin_test?sslmode=disable' \
  go test ./internal/store/ -run TestPostgresContract -v

# 5) 重启持久化 + 备份恢复演练
./scripts/backup.sh backups/drill.dump
# 修改数据后：
kill <api-pid> && ./scripts/restore.sh backups/drill.dump
# 再启动 API 应恢复备份时数据
```

说明：PostGIS 官方镜像在 arm64 上以 amd64 模拟运行（`platform` 警告），功能验证不受影响；生产建议使用原生架构镜像或托管数据库。

## 9. 安全清单

- 替换 `JWT_SECRET` 与种子账号口令；关闭演示账号或改密。
- 生产启用 HTTPS（nginx/网关），限制 `/metrics` 访问。
- 定期轮换 webhook 密钥；审计日志（`/api/audit`）留存。
- 数据库与备份加密存储；最小权限账号。
