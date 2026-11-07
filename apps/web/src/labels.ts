import type { CameraStatus, EventActionType, EventStatus, EventType, Severity, WsState } from './types'

/** 事件类型中文标签与展示顺序。 */
export const EVENT_TYPE_LABELS: Record<EventType, string> = {
  person: '人员',
  vehicle: '车辆',
  intrusion: '入侵',
  smoke: '烟雾',
  no_helmet: '未戴安全帽',
  other: '其他',
}

export const EVENT_TYPE_ORDER: EventType[] = ['person', 'vehicle', 'intrusion', 'smoke', 'no_helmet', 'other']

/** 严重程度中文标签、排序与颜色（同一份颜色同时用于 3D 标记与列表）。 */
export const SEVERITY_LABELS: Record<Severity, string> = {
  info: '提示',
  warn: '警告',
  critical: '严重',
}

export const SEVERITY_ORDER: Severity[] = ['info', 'warn', 'critical']

export const SEVERITY_COLORS: Record<Severity, string> = {
  info: '#38bdf8',
  warn: '#f59e0b',
  critical: '#ef4444',
}

/** 事件处置状态中文标签与颜色（P1：new → acked → closed）。 */
export const EVENT_STATUS_LABELS: Record<EventStatus, string> = {
  new: '待处置',
  acked: '已确认',
  closed: '已关闭',
}

export const EVENT_STATUS_COLORS: Record<EventStatus, string> = {
  new: '#38bdf8',
  acked: '#22c55e',
  closed: '#64748b',
}

/** 处置动作中文标签。 */
export const EVENT_ACTION_LABELS: Record<EventActionType, string> = {
  ack: '确认',
  close: '关闭',
  dispatch: '派单',
}

/** 相机状态中文标签与颜色。 */
export const CAMERA_STATUS_LABELS: Record<CameraStatus, string> = {
  online: '在线',
  offline: '离线',
  degraded: '异常',
}

export const CAMERA_STATUS_COLORS: Record<CameraStatus, string> = {
  online: '#22c55e',
  offline: '#ef4444',
  degraded: '#f59e0b',
}

export const CAMERA_STATUS_ORDER: CameraStatus[] = ['online', 'degraded', 'offline']

/** 轨迹颜色：按检测 class 区分，暖/冷色交替便于围观。 */
export const TRACK_CLASS_COLORS: Record<EventType, string> = {
  person: '#38bdf8',
  vehicle: '#a78bfa',
  intrusion: '#f59e0b',
  smoke: '#ef4444',
  no_helmet: '#f472b6',
  other: '#94a3b8',
}

/** WebSocket 连接状态中文标签。 */
export const WS_STATE_LABELS: Record<WsState, string> = {
  online: '已连接',
  connecting: '连接中',
  offline: '已断开',
}

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

/** 本地时间 HH:mm:ss（顶栏时钟）。 */
export function formatClock(date: Date = new Date()): string {
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

/** 本地时间 YYYY-MM-DD HH:mm:ss。 */
export function formatDateTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  )
}

/** 本地时间 HH:mm（统计图小时标签）。 */
export function formatHourLabel(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return `${pad(date.getHours())}:00`
}

/** 相对时间（事件流展示），未来时间/无法解析时安全降级。 */
export function formatRelativeTime(iso: string, now: number = Date.now()): string {
  const ts = Date.parse(iso)
  if (!Number.isFinite(ts)) return '—'
  const seconds = Math.floor(Math.max(0, now - ts) / 1000)
  if (seconds < 10) return '刚刚'
  if (seconds < 60) return `${seconds} 秒前`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return formatDateTime(iso)
}
