import type { CameraStatus, EventType, Severity, WsState } from './types'

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
