/**
 * 与 docs/API.md 对齐的数据模型与 WS 消息类型。
 * 坐标统一为局部 ENU 米制：x 东、y 北、z 上；时间统一 RFC3339 UTC 字符串。
 */

export type CameraStatus = 'online' | 'offline' | 'degraded'

export type EventType = 'person' | 'vehicle' | 'intrusion' | 'smoke' | 'no_helmet' | 'other'

export type Severity = 'info' | 'warn' | 'critical'

export type EventStatus = 'new' | 'acked'

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
  payload: Record<string, unknown> | null
}

export interface CameraListResponse {
  items: Camera[]
}

export interface EventListResponse {
  items: TwinEvent[]
  total: number
}

/** WebSocket 推送消息（服务端 → 客户端）。 */
export interface WsHelloMessage {
  type: 'hello'
  data: { server_time: string; cameras: number }
}

export interface WsEventMessage {
  type: 'event'
  data: TwinEvent
}

export interface WsCameraStatusMessage {
  type: 'camera_status'
  data: { id: string; code: string; status: CameraStatus }
}

export interface WsStatsMessage {
  type: 'stats'
  data: { events_total: number; online_cameras: number; ws_clients: number }
}

export type WsMessage = WsHelloMessage | WsEventMessage | WsCameraStatusMessage | WsStatsMessage

export type WsState = 'connecting' | 'online' | 'offline'

export interface EventFilters {
  type: EventType | 'all'
  severity: Severity | 'all'
}

/** 场景视角聚焦请求（由事件流/详情面板触发，场景消费后播放运镜动画）。 */
export interface FocusRequest {
  cameraId: string
  seq: number
}
