/**
 * 与 docs/API.md + docs/API_P1.md 对齐的数据模型与 WS 消息类型。
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

/** WebSocket 推送消息（服务端 → 客户端，P0 + P1）。 */
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
  | WsStatsMessage

export type WsState = 'connecting' | 'online' | 'offline'

export interface EventFilters {
  type: EventType | 'all'
  severity: Severity | 'all'
}

export type SidebarTab = 'events' | 'stats'

/** 场景视角聚焦请求（由事件流/详情面板触发，场景消费后播放运镜动画）。 */
export interface FocusRequest {
  cameraId: string
  seq: number
}
