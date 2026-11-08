# P5.0 API 扩展（可回放事件总线）

在 P0–P4 契约之上追加。目标：把「事件产生 → 展示/通知」从进程内直调改为**可持久化、可回放的事件总线**（对应方案 ADR-03 的落地第一步）。

## 1. 总线模型

事件信封（Envelope）：

```json
{
  "offset": 42,
  "kind": "event_created | event_escalated",
  "at": "2026-11-08T01:00:00Z",
  "event": Event,
  "order": Order | null,
  "source": "ingest | escalation | replay"
}
```

- `offset`：从 0 开始递增的持久偏移（memory 驱动为进程内计数）。
- 驱动（`BUS_DRIVER`）：
  - `memory`（默认）：进程内环形缓冲 + 同步分发给订阅者（WS 广播、通知投递）。
  - `jsonl`：追加写入 `BUS_DIR/events.jsonl`（默认 `data/bus/events.jsonl`），重启后从文件尾部恢复 offset，可回放历史事件。
- 订阅者由服务端固定注册：WebSocket 推送与 Webhook 通知（业务写库仍同步完成，总线不替代存储）。

## 2. 管理接口（admin）

### `GET /api/bus/status`

```json
{
  "driver": "jsonl",
  "path": "data/bus/events.jsonl",
  "published": 1280,
  "replayed": 12,
  "oldest_offset": 0,
  "next_offset": 1280,
  "last_publish_at": "2026-11-08T01:00:00Z",
  "subscribers": 2
}
```

memory 驱动下 `path` 为空、`oldest_offset` 为当前缓冲起点。

### `POST /api/bus/replay`

```json
{ "from_offset": 0, "to_offset": 99, "notify": false, "limit": 500 }
```

- 从总线读取 `[from_offset, to_offset]` 区间（省略 `to_offset` 时读到当前尾部；单次最多 `limit` 条，默认 500）。
- 重放会：
  - 通过 WS 重新推送：`event_created` → 帧类型 `event_replay`（附带 `replay: true` 与 `offset`）；`event_escalated` → `event_updated`。
  - `notify=true` 时按正常规则触发 Webhook 通知（默认 `false`，避免回放刷屏）。
- 响应（`to_offset` 为**实际重放到的最后一条 offset**；请求的 `to_offset` 超过当前尾部时会被截断，没有可重放项时 `replayed=0`、`to_offset=-1`）：

```json
{ "ok": true, "replayed": 100, "from_offset": 0, "to_offset": 99 }
```

错误：`400 bad_request`（区间非法）、`409 bus_driver_unsupported`（驱动不支持回放，例如 memory 驱动且区间已滚出缓冲）。

## 3. 指标追加

```
dsh_twin_bus_published_total 1280
dsh_twin_bus_replayed_total 12
dsh_twin_bus_errors_total 0
dsh_twin_bus_lag 0
```

## 4. 审计

`bus.replay`（actor=用户名，detail 含区间与条数）。

## 5. 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `BUS_DRIVER` | `memory` | `memory` / `jsonl` |
| `BUS_DIR` | `data/bus` | jsonl 驱动日志目录 |
| `BUS_BUFFER` | `10000` | memory 驱动环形缓冲条数 |
