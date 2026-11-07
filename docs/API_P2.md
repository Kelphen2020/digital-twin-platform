# P2.0 API 扩展（RBAC / 规则配置 / 工单）

本文件在 `docs/API.md`（P0）与 `docs/API_P1.md`（P1）之上追加。默认 `AUTH_MODE=on`：
除特别豁免的端点外，所有 REST 需要 `Authorization: Bearer <token>`，WS 需要 `?token=<token>`。
设置 `AUTH_MODE=off` 可关闭（仅本机联调）。

## 1. 身份与角色

### `POST /api/auth/login`

```json
{ "username": "operator", "password": "Operator@123" }
```

响应 `200`：

```json
{ "token": "<jwt>", "expires_at": "2026-11-08T12:00:00Z",
  "user": { "id": "usr_operator", "username": "operator", "display_name": "值班员",
            "role": "operator", "areas": ["area_gate", "area_parking"] } }
```

错误：`401 invalid_credentials`。

### `GET /api/auth/me`

返回当前用户（同 `user` 对象）。

### 角色与权限

| 角色 | 权限 |
|---|---|
| `viewer` 观察员 | 只读：相机/事件/轨迹/统计/审计/工单/规则；WS |
| `operator` 值班员 | viewer + 事件确认/关闭/派单 + 工单创建/流转 + 相机状态与坐标修改（仅限 `areas` 内相机） |
| `admin` 管理员 | 全部，包括标定、规则配置、任意相机修改 |

未认证 `401 unauthorized`；已认证但越权 `403 forbidden`；区域外相机写操作 `403 out_of_scope`。

### 内置账号（P2.0 演示，生产必须替换）

| 用户名 | 密码 | 角色 | 区域 |
|---|---|---|---|
| `admin` | `Admin@123456` | admin | 全部 |
| `operator` | `Operator@123` | operator | `area_gate`、`area_parking` |
| `viewer` | `Viewer@123` | viewer | 全部（只读） |

## 2. 相机规则配置

### `GET /api/cameras/{id}/rules`

```json
{
  "camera_id": "cam_001",
  "confidence_threshold": 0.6,
  "dedupe_seconds": 5,
  "enabled_classes": ["person", "vehicle", "intrusion", "smoke", "no_helmet"],
  "roi": [[0.05, 0.05], [0.95, 0.05], [0.95, 0.95], [0.05, 0.95]],
  "updated_at": "2026-11-07T12:00:00Z"
}
```

- `roi` 为归一化图像坐标（0–1）多边形，`null` 表示全画面；检测框底边中点落在 ROI 外则丢弃。
- `enabled_classes` 之外的类别直接丢弃。
- 未配置时返回 P0 默认值（阈值 0.6、去重 5s、全类别、全画面）。

### `PUT /api/cameras/{id}/rules`（仅 admin）

请求同 GET 结构（`camera_id`/`updated_at` 忽略）。校验：`confidence_threshold` ∈ [0,1]、`dedupe_seconds` ∈ [0,300]、`roi` 至少 3 个点。响应 `200` 为保存后的规则。

## 3. 工单

```json
{
  "id": "ord_...", "event_id": "evt_...", "camera_id": "cam_001",
  "title": "人员入侵 · CAM-001 东门入口", "status": "open",
  "priority": "normal", "assignee": "", "note": "",
  "created_at": "...", "updated_at": "...", "closed_at": null
}
```

- 状态机：`open` → `assigned` → `processing` → `done`（或 `cancelled`）。
- `priority`：`low` | `normal` | `high` | `urgent`。

### `POST /api/orders`（operator/admin）

```json
{ "event_id": "evt_...", "title": "可选", "priority": "high", "assignee": "安保A组", "note": "5分钟内到场" }
```

响应 `201` 工单对象；同一事件允许重复建单（P2.0 不做唯一约束）。事件不存在返回 `404 event_not_found`。

### `GET /api/orders?status=&assignee=&limit=100`

```json
{ "items": [ Order ] }
```

### `GET /api/orders/{id}`

### `PATCH /api/orders/{id}`（operator/admin；区域外相机返回 `403 out_of_scope`）

```json
{ "status": "processing", "assignee": "安保B组", "note": "已到场" }
```

`done`/`cancelled` 时服务端写 `closed_at`。响应 `200` 工单对象。

## 4. WebSocket

新增帧：

```json
{ "type": "order_updated", "data": Order }
```

## 5. 鉴权豁免与机器入口

| 端点/路径 | 规则 |
|---|---|
| `GET /api/health`、`GET /metrics`、`/api/auth/login` | 无需 token |
| `POST /api/ingest/detections` | 无需用户 token；若设置 `INGEST_TOKEN` 则需 `X-Ingest-Token: <token>` |
| 其他 REST | 需要 Bearer token |
| `/ws` | 需要 `?token=` |

## 6. 审计

用户发起的写操作在 `audit_log` 中 `actor` 为用户名（不再是 `web`）：`camera.update`、`camera.calibration.*`、`camera.rules.put`、`event.ack/close/dispatch`、`order.create/update`。
