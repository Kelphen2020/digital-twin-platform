/**
 * 与 docs/API.md + docs/API_P1.md + docs/API_P2.md 对齐的数据模型与 WS 消息类型。
 * 坐标统一为局部 ENU 米制：x 东、y 北、z 上；时间统一 RFC3339 UTC 字符串。
 */

export type CameraStatus = 'online' | 'offline' | 'degraded'

export type EventType = 'person' | 'vehicle' | 'intrusion' | 'smoke' | 'no_helmet' | 'other'

export type Severity = 'info' | 'warn' | 'critical'

export type EventStatus = 'new' | 'acked' | 'closed'

/** 相机单应矩阵标定结果（P1，可空）。 */
export interface CameraCalibration {
  /** 3×3 单应矩阵：像素 → ENU。 */
  matrix: number[][]
  rmse: number
  max_residual?: number | null
  points_count: number
  solved_at?: string | null
}

export interface Camera {
  id: string
  code: string
  name: string
  area_id: string
  x: number
  y: number
  z: number
  heading: number
  fov_h: number
  fov_v: number
  range_m: number
  status: CameraStatus
  stream_url: string | null
  calibration?: CameraCalibration | null
  updated_at: string
}

/** PATCH /api/cameras/{id} 允许修改的字段（P0 契约）。 */
export interface CameraUpdatePayload {
  status?: CameraStatus
  x?: number
  y?: number
  z?: number
  heading?: number
}

export interface TwinEvent {
  id: string
  camera_id: string
  camera_code: string
  camera_name: string
  type: EventType
  severity: Severity
  confidence: number
  x: number
  y: number
  z: number
  occurred_at: string
  status: EventStatus
  closed_at?: string | null
  /** 告警升级时间（P3，可空）；非空表示超时未处置已升级。 */
  escalated_at?: string | null
  payload: Record<string, unknown> | null
}

/** 事件处置动作（审计时间线）。 */
export type EventActionType = 'ack' | 'close' | 'dispatch'

export interface EventAction {
  id: string
  event_id: string
  action: EventActionType
  actor: string
  target: string | null
  note: string | null
  at: string
}

/** 轨迹点（P1）。class 与事件类型取值一致。 */
export interface TrackPoint {
  track_id: string
  camera_id: string
  class: EventType
  x: number
  y: number
  z: number
  ts: string
}

/** 相机标定控制点（像素坐标 + 世界 ENU）。 */
export interface ControlPoint {
  px: number
  py: number
  x: number
  y: number
}

export interface CalibrationSolveResponse {
  matrix: number[][]
  rmse: number
  max_residual: number
  points_count: number
}

export interface CalibrationSavePayload {
  matrix: number[][]
  rmse: number
  max_residual?: number | null
  points_count: number
}

export interface CameraListResponse {
  items: Camera[]
}

export interface EventListResponse {
  items: TwinEvent[]
  total: number
}

export interface EventDetailResponse {
  event: TwinEvent
  actions: EventAction[]
}

export interface TrackListResponse {
  items: TrackPoint[]
}

export interface StatsTotals {
  events: number
  by_severity: Partial<Record<Severity, number>>
  by_type: Partial<Record<EventType, number>>
}

export interface StatsCameraRow {
  camera_id: string
  code: string
  name: string
  count: number
}

export interface StatsHourRow {
  hour: string
  count: number
}

/** GET /api/stats/summary 响应（P1）。 */
export interface StatsSummary {
  from: string
  to: string
  totals: StatsTotals
  by_camera: StatsCameraRow[]
  by_hour: StatsHourRow[]
}

export type StatsRange = '1h' | '24h'

// ---------------- P2：身份与 RBAC ----------------

export type Role = 'viewer' | 'operator' | 'admin'

export interface AuthUser {
  id: string
  username: string
  display_name: string
  role: Role
  /** 负责区域；admin/viewer 可能为 null（表示全部/只读）。 */
  areas: string[] | null
}

export interface LoginResponse {
  token: string
  expires_at: string
  user: AuthUser
}

/** 前端可判定的操作权限键（与后端角色矩阵一致）。 */
export type Permission =
  | 'event:handle'
  | 'order:create'
  | 'order:update'
  | 'camera:update'
  | 'camera:calibration'
  | 'camera:rules'

// ---------------- P2：相机规则 ----------------

export interface CameraRules {
  camera_id: string
  confidence_threshold: number
  dedupe_seconds: number
  enabled_classes: EventType[]
  /** 归一化图像坐标多边形；null 表示全画面。 */
  roi: [number, number][] | null
  updated_at: string
}

export type CameraRulesPayload = Pick<
  CameraRules,
  'confidence_threshold' | 'dedupe_seconds' | 'enabled_classes' | 'roi'
>

// ---------------- P2：工单 ----------------

export type OrderStatus = 'open' | 'assigned' | 'processing' | 'done' | 'cancelled'

export type OrderPriority = 'low' | 'normal' | 'high' | 'urgent'

export interface Order {
  id: string
  event_id: string
  camera_id: string
  title: string
  status: OrderStatus
  priority: OrderPriority
  assignee: string
  note: string
  created_at: string
  updated_at: string
  closed_at?: string | null
}

export interface CreateOrderPayload {
  event_id: string
  title?: string
  priority?: OrderPriority
  assignee?: string
  note?: string
}

export interface UpdateOrderPayload {
  status?: OrderStatus
  assignee?: string
  note?: string
}

export interface OrderListResponse {
  items: Order[]
}

// ---------------- WS ----------------

/** WebSocket 推送消息（服务端 → 客户端，P0 + P1 + P2）。 */
export interface WsHelloMessage {
  type: 'hello'
  data: { server_time: string; cameras: number }
}

export interface WsEventMessage {
  type: 'event'
  data: TwinEvent
}

export interface WsEventUpdatedMessage {
  type: 'event_updated'
  data: TwinEvent
}

export interface WsCameraStatusMessage {
  type: 'camera_status'
  data: { id: string; code: string; status: CameraStatus }
}

export interface WsTrackPointMessage {
  type: 'track_point'
  data: TrackPoint
}

export interface WsOrderUpdatedMessage {
  type: 'order_updated'
  data: Order
}

export interface WsStatsMessage {
  type: 'stats'
  data: { events_total: number; online_cameras: number; ws_clients: number }
}

export type WsMessage =
  | WsHelloMessage
  | WsEventMessage
  | WsEventUpdatedMessage
  | WsCameraStatusMessage
  | WsTrackPointMessage
  | WsOrderUpdatedMessage
  | WsStatsMessage

export type WsState = 'connecting' | 'online' | 'offline'

export interface EventFilters {
  type: EventType | 'all'
  severity: Severity | 'all'
}

// ---------------- P3：设置 / 审计 / 通知投递 ----------------

export type NotificationFormat = 'generic' | 'feishu'

/** GET/PUT /api/settings/notifications（admin）。 */
export interface NotificationSettings {
  enabled: boolean
  webhook_url: string
  format: NotificationFormat
  min_severity: Severity
  timeout_seconds: number
}

/** GET/PUT /api/settings/escalation（admin）。 */
export interface EscalationSettings {
  enabled: boolean
  after_seconds: number
  severities: Severity[]
}

/** GET /api/audit 审计条目。 */
export interface AuditEntry {
  id: string
  at: string
  actor: string
  action: string
  resource: string
  detail: Record<string, unknown> | null
}

export interface AuditListResponse {
  items: AuditEntry[]
}

/** GET /api/notifications 通知投递记录。 */
export interface NotificationLogEntry {
  id: string
  at: string
  kind: string
  event_id: string
  url: string
  format: string
  ok: boolean
  status_code: number
  error: string
}

export interface NotificationListResponse {
  items: NotificationLogEntry[]
}

export type SidebarTab = 'events' | 'stats' | 'orders' | 'settings' | 'audit'

/** 场景视角聚焦请求（由事件流/详情面板触发，场景消费后播放运镜动画）。 */
export interface FocusRequest {
  cameraId: string
  seq: number
}
