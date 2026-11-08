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
# 备份
docker exec dtp-postgres pg_dump -U twin -d twin -Fc > backup-$(date +%F).dump
# 恢复（先停 API）
docker compose -f deploy/docker-compose.yml stop api
docker exec -i dtp-postgres pg_restore -U twin -d twin --clean --if-exists < backup-2026-11-08.dump
docker compose -f deploy/docker-compose.yml start api
```

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
| 相机为空 | 数据级权限（用户 areas）或相机全部离线 |
| 事件不上图 | WS 断连（顶栏指示器）、`event` 帧被采样丢弃、权限过滤 |
| 轨迹闪烁/缺帧 | `WS_TRACK_SAMPLE` 过大或 `HUB_CLIENT_BUFFER` 过小，查看 `ws_dropped_total` |
| 通知不发送 | 管理页通知设置未启用/URL 错误；查 `/api/notifications` 投递记录 |
| ingest 429 | 触发 `INGEST_RATE_LIMIT`，看 `ingest_rejected_total`，调高或扩容 |
| 升级不触发 | 升级设置是否启用、严重级别匹配、扫描周期 15s |
| 总线告警 | `GET /api/bus/status` 看 lag；必要时回放或检查磁盘 |

## 7. 安全清单

- 替换 `JWT_SECRET` 与种子账号口令；关闭演示账号或改密。
- 生产启用 HTTPS（nginx/网关），限制 `/metrics` 访问。
- 定期轮换 webhook 密钥；审计日志（`/api/audit`）留存。
- 数据库与备份加密存储；最小权限账号。
