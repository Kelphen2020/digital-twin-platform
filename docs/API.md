# P0 API 契约（前后端/模拟器共同遵守）

Base URL：`http://127.0.0.1:8080`（可用环境变量 `API_BASE` 覆盖）
WebSocket：`ws://127.0.0.1:8080/ws`
所有时间为 RFC3339 UTC 字符串。坐标：**局部 ENU 米制**（x 东、y 北、z 上，站心原点由项目定义；CGCS2000 换算在 P1 标定模块接入）。

## 0. 数据模型

### Camera（相机/摄像机）

```json
{
  "id": "cam_001",
  "code": "CAM-001",
  "name": "东门入口",
  "area_id": "area_gate",
  "x": -90.0,
  "y": -60.0,
  "z": 6.0,
  "heading": 90.0,
  "fov_h": 60.0,
  "fov_v": 34.0,
  "range_m": 120.0,
  "status": "online",
  "stream_url": null,
  "updated_at": "2026-11-07T12:00:00Z"
}
```

- `heading`：相机光轴方位角，度，正北为 0、顺时针为正。
- `fov_h` / `fov_v`：水平/垂直视场角（度）。
- `status`：`online` | `offline` | `degraded`。

### Event（事件）

```json
{
  "id": "evt_7f3c...",
  "camera_id": "cam_001",
  "camera_code": "CAM-001",
  "camera_name": "东门入口",
  "type": "intrusion",
  "severity": "warn",
  "confidence": 0.92,
  "x": -85.0,
  "y": -55.0,
  "z": 0.0,
  "occurred_at": "2026-11-07T12:00:00Z",
  "status": "new",
  "payload": { "class": "person", "bbox": [0.41, 0.32, 0.58, 0.77] }
}
```

- `type`：`person` | `vehicle` | `intrusion` | `smoke` | `no_helmet` | `other`（P0 由 class 映射）。
- `severity`：`info` | `warn` | `critical`。
- `status`：`new` | `acked`。

### 规则（P0 规则引擎）

1. `confidence < 0.6` 的检测丢弃。
2. 同一相机 + 同一 class 在 **5 秒**内只产生一条事件（去重窗口）。
3. class → type/severity 映射：`person→person/info`、`vehicle→vehicle/info`、`intrusion→intrusion/warn`、`smoke→smoke/critical`、`no_helmet→no_helmet/warn`。
4. 事件世界坐标优先取检测里的 `world`；缺省时用相机位置 + 检测框中心反投影的近似值（P0 用相机前方 30m 处）。

## 1. REST

### `GET /api/health`

```json
{ "status": "ok", "time": "2026-11-07T12:00:00Z", "version": "0.1.0" }
```

### `GET /api/areas`

```json
{ "items": [ { "id": "area_gate", "name": "东门", "type": "zone", "polygon": [[-120,-100],[-60,-100],[-60,-20],[-120,-20]] } ] }
```

P0 可返回空数组，前端不依赖。

### `GET /api/cameras`

```json
{ "items": [ Camera, ... ] }
```

### `GET /api/cameras/{id}`

返回单个 Camera；不存在返回 `404 {"error":"camera_not_found"}`。

### `PATCH /api/cameras/{id}`

允许修改 `status`、`x`、`y`、`z`、`heading`；返回更新后的 Camera。修改状态后需向所有 WS 客户端广播 `camera_status`。

```json
{ "status": "offline" }
```

### `GET /api/events?limit=50&status=new&type=intrusion&since=2026-11-07T00:00:00Z`

按 `occurred_at` 倒序返回。

```json
{ "items": [ Event, ... ], "total": 123 }
```

### `POST /api/events/{id}/ack`

```json
{ "ok": true, "event": Event }
```

### `POST /api/ingest/detections`（模拟器 / AI 推理服务入口）

请求：

```json
{
  "camera_code": "CAM-001",
  "ts": "2026-11-07T12:00:00Z",
  "detections": [
    { "class": "person", "confidence": 0.92, "bbox": [0.41, 0.32, 0.58, 0.77],
      "world": { "x": -85.0, "y": -55.0, "z": 0.0 } }
  ]
}
```

- `ts`、`world` 可省略。
- 响应 `202`：

```json
{ "accepted": 1, "dropped": 0, "events": [ Event ] }
```

- 相机不存在返回 `404 {"error":"camera_not_found"}`。
- 规则命中时通过 WS 广播 `event`。

### `GET /metrics`（Prometheus 文本格式）

必须包含：

```
dsh_twin_events_total{severity="warn"} 12
dsh_twin_detections_total 100
dsh_twin_events_dropped_total 4
dsh_twin_ws_clients 1
dsh_twin_ingest_duration_seconds_sum 0.12
dsh_twin_ingest_duration_seconds_count 100
dsh_twin_cameras_online 5
```

## 2. WebSocket

`ws://127.0.0.1:8080/ws`，服务端推送 JSON 文本帧：

```json
{ "type": "hello", "data": { "server_time": "...", "cameras": 6 } }
{ "type": "event", "data": Event }
{ "type": "camera_status", "data": { "id": "cam_001", "code": "CAM-001", "status": "offline" } }
{ "type": "stats", "data": { "events_total": 42, "online_cameras": 5, "ws_clients": 2 } }
```

- `hello`：连接建立后立即发送一次。
- `stats`：每 5 秒一次。
- 客户端不需要发送任何消息；断线由客户端指数退避重连。

## 3. 错误格式

```json
{ "error": "bad_request", "message": "camera_code is required" }
```

HTTP 状态：400 参数错误 / 404 资源不存在 / 500 内部错误。
