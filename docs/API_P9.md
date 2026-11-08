# P9.0 扩展（性能、背压与运维交付）

P9 不改业务 API 形状，新增**运行时配置、背压/采样、限流与可观测性**，并补齐部署与运维交付物。

## 1. 新增环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `HUB_CLIENT_BUFFER` | 256 | 每个 WebSocket 客户端的发送队列长度；满时丢弃该帧并计数 |
| `WS_TRACK_SAMPLE` | 1 | 轨迹点 WS 广播采样率（1=全部，2=每 2 个广播 1 个；落库不受影响） |
| `INGEST_RATE_LIMIT` | 0 | ingest 每秒请求上限（0=关闭）；超出返回 HTTP 429 |
| `INGEST_BURST` | 200 | 限流令牌桶容量 |
| `MAX_BODY_BYTES` | 1048576 | 请求体上限（字节），超限返回 413 |

## 2. 限流响应

```json
HTTP 429
{ "error": "rate_limited", "message": "ingest rate limit exceeded" }
```

超限请求计入 `dsh_twin_ingest_rejected_total`。

## 3. 指标追加

```
dsh_twin_ws_frames_total 12345
dsh_twin_ws_dropped_total 0
dsh_twin_track_points_written_total 456
dsh_twin_track_points_broadcast_total 456
dsh_twin_ingest_rejected_total 0
```

## 4. 部署与运维交付物（非运行时 API）

- `deploy/Dockerfile.web` + `deploy/nginx.conf`：前端静态站点 + `/api` 反代 + `/ws` 升级 + `/hls` 静态，gzip 与 SPA 回退。
- `deploy/docker-compose.yml` 新增 `web` 服务（8081:80，依赖 api）。
- `deploy/observability/alerts.yml`：Prometheus 告警规则（API down、高 5xx、WS 丢弃、总线 lag、升级积压、通知失败）。
- `deploy/observability/grafana-dashboard.json`：新增 WS 帧/丢弃、轨迹写入、限流拒绝面板。
- `docs/OPERATIONS.md`：启停、健康检查、指标/告警、备份恢复、升级与排障。
- `docs/PERFORMANCE.md`：压测矩阵（QPS/延迟/采样/丢弃）与结论。
