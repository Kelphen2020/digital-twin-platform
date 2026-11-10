import { getAuthToken, getTenantId } from './auth/token'
import { noteConnecting, noteFrame, noteOffline, noteOnline, resetWsQuality } from './store/wsQuality'
import type { WsMessage, WsState } from './types'

/** 指数退避参数：1s、2s、4s……封顶 30s。 */
const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 30_000

/**
 * 解析 WebSocket 地址：
 * - 未配置 VITE_API_BASE：连接当前页面同源 /ws（开发环境由 Vite 代理 Upgrade）。
 * - 配置了 VITE_API_BASE：以其协议与主机推导 ws(s)://.../ws。
 * - 配置了 VITE_API_TOKEN：追加 ?token= 供服务端鉴权（docs/API_P1.md §7）。
 */
export function resolveWsUrl(): string {
  // 优先使用登录会话令牌；VITE_API_TOKEN 作为机器/联调兜底
  const token = getAuthToken() ?? (import.meta.env.VITE_API_TOKEN ?? '').trim()
  const base = (import.meta.env.VITE_API_BASE ?? '').trim().replace(/\/+$/, '')

  let url: URL
  if (base) {
    url = new URL(base, window.location.origin)
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    url.pathname = `${url.pathname.replace(/\/+$/, '')}/ws`
  } else {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    url = new URL(`${protocol}//${window.location.host}/ws`)
  }

  url.search = ''
  url.hash = ''
  if (token) url.searchParams.set('token', token)
  // P27：浏览器 WebSocket 无法自定义 header，租户以查询参数传给后端（token 中的 tnt 仍是最终校验）
  const tenant = getTenantId()
  if (tenant) url.searchParams.set('tenant_id', tenant)
  return url.toString()
}

const WS_MESSAGE_TYPES = new Set([
  'hello',
  'event',
  'event_updated',
  'camera_status',
  'track_point',
  'order_updated',
  'event_replay',
  'patrol_execution',
  'stats',
])

/** 运行时校验最小消息结构，避免异常帧把状态机打乱。 */
function parseMessage(raw: string): WsMessage | null {
  try {
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return null
    const candidate = parsed as { type?: unknown; data?: unknown }
    if (typeof candidate.type !== 'string' || !WS_MESSAGE_TYPES.has(candidate.type)) return null
    if (typeof candidate.data !== 'object' || candidate.data === null) return null
    return parsed as WsMessage
  } catch {
    return null
  }
}

/**
 * 事件总线 WebSocket 客户端。
 * 服务端只推送、客户端不发送；断线后指数退避重连，连接成功后重置退避次数。
 */
export class TwinSocket {
  private socket: WebSocket | null = null
  private reconnectTimer: number | null = null
  private attempts = 0
  private stopped = false

  constructor(
    private readonly onMessage: (message: WsMessage) => void,
    private readonly onState: (state: WsState) => void,
  ) {}

  connect(): void {
    this.stopped = false
    this.open()
  }

  close(): void {
    this.stopped = true
    resetWsQuality()
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    const socket = this.socket
    this.socket = null
    if (socket) {
      socket.onopen = null
      socket.onmessage = null
      socket.onerror = null
      socket.onclose = null
      socket.close()
    }
    this.onState('offline')
  }

  private open(): void {
    if (this.stopped) return
    noteConnecting()
    this.onState('connecting')

    let socket: WebSocket
    try {
      socket = new WebSocket(resolveWsUrl())
    } catch {
      this.scheduleReconnect()
      return
    }
    this.socket = socket

    socket.onopen = () => {
      this.attempts = 0
      noteOnline()
      this.onState('online')
    }

    socket.onmessage = (event: MessageEvent) => {
      if (typeof event.data !== 'string') return
      noteFrame()
      const message = parseMessage(event.data)
      if (message) this.onMessage(message)
    }

    socket.onclose = () => {
      if (this.socket === socket) this.socket = null
      noteOffline()
      this.onState('offline')
      this.scheduleReconnect()
    }

    // onerror 后浏览器一定会触发 onclose，重连统一由 onclose 负责
    socket.onerror = () => {}
  }

  private scheduleReconnect(): void {
    if (this.stopped || this.reconnectTimer !== null) return
    const delay = Math.min(RECONNECT_BASE_MS * 2 ** this.attempts, RECONNECT_MAX_MS)
    this.attempts += 1
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      this.open()
    }, delay)
  }
}
