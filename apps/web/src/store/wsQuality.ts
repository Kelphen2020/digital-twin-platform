import { reactive } from 'vue'

/** 滚动窗口（毫秒）：展示最近 5 秒 WS 帧速率。 */
export const WS_RATE_WINDOW_MS = 5000
const MAX_FRAME_SAMPLES = 600

export interface WsQualityState {
  /** 页面加载（或上次重置）以来收到的 WS 帧总数。 */
  framesTotal: number
  /** 首次连接成功后的重连次数。 */
  reconnectCount: number
  connected: boolean
  /** 正在尝试建立连接（含退避后的重试）。 */
  reconnecting: boolean
  everConnected: boolean
  connectedAt: number | null
  lastFrameAt: number | null
  /** 最近帧时间戳（用于滚动 5s 速率；有上限）。 */
  frameTimes: number[]
}

export const wsQuality = reactive<WsQualityState>({
  framesTotal: 0,
  reconnectCount: 0,
  connected: false,
  reconnecting: false,
  everConnected: false,
  connectedAt: null,
  lastFrameAt: null,
  frameTimes: [],
})

/** 开始一次连接尝试（初始连接/重连）。 */
export function noteConnecting(): void {
  wsQuality.reconnecting = true
}

/** WebSocket 打开：首次不计数，之后每次打开计入重连次数。 */
export function noteOnline(at = Date.now()): void {
  if (wsQuality.everConnected) wsQuality.reconnectCount += 1
  wsQuality.everConnected = true
  wsQuality.connected = true
  wsQuality.reconnecting = false
  wsQuality.connectedAt = at
}

/** WebSocket 关闭：等待退避重连期间视为断开（红色）。 */
export function noteOffline(): void {
  wsQuality.connected = false
  wsQuality.reconnecting = false
}

/** 收到一帧 WS 消息（字符串帧）。 */
export function noteFrame(at = Date.now()): void {
  wsQuality.framesTotal += 1
  wsQuality.lastFrameAt = at
  wsQuality.frameTimes.push(at)
  if (wsQuality.frameTimes.length > MAX_FRAME_SAMPLES) {
    wsQuality.frameTimes.splice(0, wsQuality.frameTimes.length - MAX_FRAME_SAMPLES)
  }
}

/** 清理窗口外时间戳，避免数组无限增长（页面刷新/登出时重置）。 */
export function pruneFrameTimes(now = Date.now()): void {
  const cutoff = now - WS_RATE_WINDOW_MS
  let removeCount = 0
  while (removeCount < wsQuality.frameTimes.length && wsQuality.frameTimes[removeCount] < cutoff) {
    removeCount += 1
  }
  if (removeCount > 0) wsQuality.frameTimes.splice(0, removeCount)
}

/** 最近 5 秒窗口内的帧数。 */
export function framesInWindow(now = Date.now()): number {
  const cutoff = now - WS_RATE_WINDOW_MS
  let count = 0
  for (let index = wsQuality.frameTimes.length - 1; index >= 0; index -= 1) {
    if (wsQuality.frameTimes[index] < cutoff) break
    count += 1
  }
  return count
}

/** 登出/销毁时重置统计（重连计数从 0 开始）。 */
export function resetWsQuality(): void {
  wsQuality.framesTotal = 0
  wsQuality.reconnectCount = 0
  wsQuality.connected = false
  wsQuality.reconnecting = false
  wsQuality.everConnected = false
  wsQuality.connectedAt = null
  wsQuality.lastFrameAt = null
  wsQuality.frameTimes.length = 0
}
