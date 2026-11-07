# P1.0 API 扩展（在 P0 契约之上追加，向后兼容）

P0 的 `docs/API.md` 全部保持不变；本文件定义 P1.0 新增能力。前端、模拟器、后端都必须按此契约实现。

## 1. 相机标定（单应矩阵）

标定把"像素坐标 → 世界 ENU 坐标"变成可计算映射：`[x,y,w]ᵀ = H · [px,py,1]ᵀ`。

相机对象新增字段（可空）：

```json
"calibration": {
  "matrix": [[1.2, 0.03, -940.1], [0.01, 1.1, -520.4], [0.0001, 0.00002, 1.0]],
  "rmse": 0.42,
  "max_residual": 1.13,
  "points_count": 6,
  "solved_at": "2026-11-07T12:00:00Z"
}
```

### `POST /api/calibration/solve`

请求（至少 4 组、至多 20 组对应点，像素坐标系为相机原始分辨率，P1 默认 1920×1080）：

```json
{
  "camera_id": "cam_001",
  "points": [
    { "px": 640.0, "py": 540.0, "x": -85.0, "y": -55.0 },
    { "px": 1200.0, "py": 540.0, "x": -70.0, "y": -55.0 }
  ]
}
```

响应 `200`：

```json
{ "matrix": [[...],[...],[...]], "rmse": 0.42, "max_residual": 1.13, "points_count": 6 }
```

错误：`400 bad_request`（点数不足/JSON 非法）、`404 camera_not_found`、`422 calibration_degenerate`（点共线或无法求解）。

### `PUT /api/cameras/{id}/calibration`

保存求解结果（通常把 `solve` 的响应原样回传；`max_residual` 可省略）：

```json
{ "matrix": [[...],[...],[...]], "rmse": 0.42, "points_count": 6 }
```

响应 `200` 为更新后的 Camera（含 `calibration`，服务端补 `solved_at`）。

### `DELETE /api/cameras/{id}/calibration`

响应 `200` 为更新后的 Camera（`calibration` 为 `null`）。

## 2. 规则升级：标定优先

`POST /api/ingest/detections` 的检测可带可选 `track_id`：

```json
{ "class": "person", "confidence": 0.93, "bbox": [0.41,0.32,0.58,0.77],
  "track_id": "trk-0007", "world": {"x": -85, "y": -55, "z": 0} }
```

世界坐标优先级：`world` 字段 > 相机 `calibration` 投影 > P0 光轴 30m 兜底。
标定投影取 bbox 底边中点：`px = (x1+x2)/2*1920`，`py = y2*1080`（P1 假设 1920×1080 画面）。
带 `track_id` 的检测（无论是否产生事件）写入轨迹点，并通过 WS 广播 `track_point`；`track_id` 的检测若只有兜底坐标则不落轨迹。

202 响应新增 `tracks` 字段（本次写入的轨迹点数），事件 `payload` 新增 `world_source`（`world`/`calibration`/`fallback`）。二者均为追加字段，客户端应忽略未知字段。

## 3. 轨迹

### `GET /api/tracks?camera_id=cam_001&since=<RFC3339>&limit=500`

```json
{ "items": [
  { "track_id": "trk-0007", "camera_id": "cam_001", "class": "person",
    "x": -85.2, "y": -55.4, "z": 0, "ts": "2026-11-07T12:00:00Z" }
] }
```

按 `ts` 倒序，`limit` 默认 500、上限 5000。

## 4. 告警处置与审计

事件新增状态：`new` → `acked` → `closed`；新增字段 `closed_at`（可空）。

### `POST /api/events/{id}/close`

```json
{ "note": "现场已处置" }
```

响应 `200`：`{ "ok": true, "event": Event }`。

### `POST /api/events/{id}/dispatch`

```json
{ "target": "安保A组", "note": "请5分钟内到场" }
```

响应 `200`：`{ "ok": true, "event": Event, "action": EventAction }`（不改变事件状态，仅记录派单）。

### `GET /api/events/{id}`

```json
{ "event": Event, "actions": [ EventAction ] }
```

`EventAction`：

```json
{ "id": "act_...", "event_id": "evt_...", "action": "dispatch|ack|close",
  "actor": "web", "target": "安保A组", "note": "...", "at": "..." }
```

### `GET /api/audit?limit=100&action=event.close`

```json
{ "items": [ { "id": "aud_...", "at": "...", "actor": "web", "action": "event.close",
              "resource": "evt_...", "detail": { "status": "closed" } } ] }
```

所有写操作（注入除外）记录审计：`camera.update`、`camera.calibration.put`、`camera.calibration.delete`、`event.ack`、`event.close`、`event.dispatch`。

## 5. 统计与导出

### `GET /api/stats/summary?from=<RFC3339>&to=<RFC3339>`

```json
{
  "from": "...", "to": "...",
  "totals": { "events": 120, "by_severity": {"info": 80, "warn": 30, "critical": 10},
              "by_type": {"person": 60, "intrusion": 30, "smoke": 10, "vehicle": 20} },
  "by_camera": [ { "camera_id": "cam_001", "code": "CAM-001", "name": "东门入口", "count": 40 } ],
  "by_hour": [ { "hour": "2026-11-07T10:00:00Z", "count": 12 } ]
}
```

`from`/`to` 可省略（默认最近 24 小时）。

### `GET /api/stats/events.csv?from=&to=`

`text/csv; charset=utf-8`，表头：`id,camera_code,camera_name,type,severity,confidence,x,y,z,occurred_at,status`。

## 6. WebSocket 新增帧

```json
{ "type": "event_updated", "data": Event }
{ "type": "track_point", "data": { "track_id": "trk-0007", "camera_id": "cam_001", "class": "person", "x": -85.2, "y": -55.4, "z": 0, "ts": "..." } }
```

## 7. 可选鉴权（默认关闭）

设置环境变量 `AUTH_TOKEN` 后：除 `GET /api/health`、`GET /metrics` 外，所有 REST 需要
`Authorization: Bearer <token>`；WS 需要 `?token=<token>`。前端用 `VITE_API_TOKEN` 配置。

## 8. 视频（P1.0 演示）

相机 `stream_url` 指向 HLS 播放地址（P1.0 用本机测试流）：

```json
"stream_url": "/hls/test.m3u8"
```

`scripts/make_hls.sh` 用 ffmpeg 生成测试流到 `apps/web/public/hls/`，由 Vite 直接提供；
前端在相机详情里用 hls.js 播放。真实接入时换成 ZLMediaKit 输出的 WebRTC/FLV/HLS 地址即可。
