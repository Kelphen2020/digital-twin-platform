# P12.0 API 扩展（巡检路线）

在 P0–P11 契约之上追加：三维场景的巡检路线持久化（交互与播放由前端实现）。

## 巡检路线对象

```json
{
  "id": "patrol_...",
  "name": "东门→主干道→停车场",
  "waypoints": [
    { "x": -90, "y": -60, "z": 12, "heading": 90 },
    { "x": 0, "y": 0, "z": 14, "heading": 225 }
  ],
  "camera_ids": ["cam_001", "cam_006", "cam_005"],
  "interval_seconds": 3,
  "created_by": "admin",
  "created_at": "...",
  "updated_at": "..."
}
```

- `waypoints`：2–50 个 ENU 米制点，`heading` 为视角方位角（度，可省略默认 0）。
- `camera_ids`：0–50 个相机 id（用于前端高亮联动），必须存在。
- `interval_seconds`：航点间飞行/停留秒数，1–60。

## 接口

### `GET /api/patrol/routes`（登录用户）

```json
{ "items": [ PatrolRoute ] }
```

### `POST /api/patrol/routes`（operator/admin）

请求：`name`、`waypoints`、`camera_ids`、`interval_seconds`。校验失败 `400 bad_request`；相机不存在 `400 unknown_camera`。响应 `201`。

### `PATCH /api/patrol/routes/{id}`（创建者或 admin）

可修改 `name`、`waypoints`、`camera_ids`、`interval_seconds`。响应 `200`；越权 `403 forbidden`。

### `DELETE /api/patrol/routes/{id}`（创建者或 admin）

`204`。

## 审计

`patrol.create`、`patrol.update`、`patrol.delete`（actor=用户名）。

## 前端（P12 交互，非 API）

- **测距**：开启后点击地面两点 → 场景内连线 + 距离标签（米），可清除。
- **框选**：Shift+拖拽矩形 → 选中框内相机，显示多选列表与数量。
- **巡检播放**：选择路线 → 播放/暂停/停止，相机沿航点飞行并按 `interval_seconds` 停留；路线以折线显示，当前航点高亮。
