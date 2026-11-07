import { computed, reactive } from 'vue'
import { ackEvent as requestAckEvent, fetchCameras, fetchEvents } from '../api'
import { TwinSocket } from '../ws'
import type {
  Camera,
  EventFilters,
  EventType,
  FocusRequest,
  Severity,
  TwinEvent,
  WsMessage,
  WsState,
} from '../types'

const SITE_NAME = import.meta.env.VITE_SITE_NAME?.trim() || '数字孪生安防平台'
/** 事件流最多保留的条数（防止长时间运行无限增长）。 */
const EVENTS_LIMIT = 200
/** REST 不可用时的自动重试间隔。 */
const REST_RETRY_MS = 10_000

interface TwinState {
  siteName: string
  cameras: Camera[]
  events: TwinEvent[]
  /** 服务端事件总数；WS stats 会持续校准。 */
  eventsTotal: number
  /** stats 推送的在线相机数，优先于本地统计。 */
  onlineCamerasFromStats: number | null
  wsState: WsState
  backendOnline: boolean
  loading: boolean
  lastError: string | null
  ackError: string | null
  selectedCameraId: string | null
  selectedEventId: string | null
  filters: EventFilters
  ackPendingIds: string[]
  focusRequest: FocusRequest | null
  fps: number
}

const state = reactive<TwinState>({
  siteName: SITE_NAME,
  cameras: [],
  events: [],
  eventsTotal: 0,
  onlineCamerasFromStats: null,
  wsState: 'connecting',
  backendOnline: false,
  loading: true,
  lastError: null,
  ackError: null,
  selectedCameraId: null,
  selectedEventId: null,
  filters: { type: 'all', severity: 'all' },
  ackPendingIds: [],
  focusRequest: null,
  fps: 0,
})

let socket: TwinSocket | null = null
let retryTimer: number | null = null
let loadingPromise: Promise<void> | null = null
let started = false
let focusSeq = 0

const selectedCamera = computed<Camera | null>(
  () => state.cameras.find((camera) => camera.id === state.selectedCameraId) ?? null,
)

const selectedEvent = computed<TwinEvent | null>(
  () => state.events.find((event) => event.id === state.selectedEventId) ?? null,
)

const onlineCameras = computed(() => {
  // 相机台账已加载时以本地状态为准（camera_status 推送后立即生效）；否则退回 stats 统计值
  if (state.cameras.length > 0) {
    return state.cameras.filter((camera) => camera.status === 'online').length
  }
  return state.onlineCamerasFromStats ?? 0
})

const filteredEvents = computed(() =>
  state.events.filter(
    (event) =>
      (state.filters.type === 'all' || event.type === state.filters.type) &&
      (state.filters.severity === 'all' || event.severity === state.filters.severity),
  ),
)

function matchesFilters(event: TwinEvent): boolean {
  return (
    (state.filters.type === 'all' || event.type === state.filters.type) &&
    (state.filters.severity === 'all' || event.severity === state.filters.severity)
  )
}

function scheduleRestRetry(): void {
  if (retryTimer !== null) return
  retryTimer = window.setTimeout(() => {
    retryTimer = null
    if (!state.backendOnline) void loadInitial()
  }, REST_RETRY_MS)
}

/** 启动时拉取全部基础数据；失败时保持场景可用并进入自动重试。 */
async function loadInitial(): Promise<void> {
  if (loadingPromise) return loadingPromise
  state.loading = true
  loadingPromise = (async () => {
    try {
      const [cameras, events] = await Promise.all([fetchCameras(), fetchEvents(EVENTS_LIMIT)])
      state.cameras = cameras
      state.events = events.items
      state.eventsTotal = Math.max(events.total, events.items.length)
      state.backendOnline = true
      state.lastError = null
      // 默认选中第一台相机，右侧详情面板开屏即有内容
      if (!state.selectedCameraId && cameras.length > 0) {
        state.selectedCameraId = cameras[0].id
      }
    } catch (error) {
      state.backendOnline = false
      state.lastError = error instanceof Error ? error.message : String(error)
      scheduleRestRetry()
    } finally {
      state.loading = false
      loadingPromise = null
    }
  })()
  return loadingPromise
}

/** 新事件插入/同 id 更新；数组替换以触发场景增量同步。 */
function upsertEvent(event: TwinEvent): void {
  const index = state.events.findIndex((item) => item.id === event.id)
  if (index >= 0) {
    const next = state.events.slice()
    next[index] = event
    state.events = next
    return
  }
  state.events = [event, ...state.events].slice(0, EVENTS_LIMIT)
  state.eventsTotal += 1
}

function handleWsMessage(message: WsMessage): void {
  switch (message.type) {
    case 'hello': {
      // WS 已连通说明后端可用；若启动时 REST 失败（例如后端比前端晚就绪）则补拉数据
      state.backendOnline = true
      state.lastError = null
      if (state.cameras.length === 0) void loadInitial()
      break
    }
    case 'event': {
      upsertEvent(message.data)
      break
    }
    case 'camera_status': {
      const camera = state.cameras.find((item) => item.id === message.data.id || item.code === message.data.code)
      if (camera) camera.status = message.data.status
      break
    }
    case 'stats': {
      state.eventsTotal = message.data.events_total
      state.onlineCamerasFromStats = message.data.online_cameras
      break
    }
  }
}

function handleWsState(next: WsState): void {
  state.wsState = next
  if (next === 'online') state.backendOnline = true
}

/** 页面挂载时启动 REST 拉取 + WS 连接。 */
function start(): void {
  if (started) return
  started = true
  void loadInitial()
  socket = new TwinSocket(handleWsMessage, handleWsState)
  socket.connect()
}

/** 页面卸载时关闭连接并清理定时器。 */
function stop(): void {
  started = false
  if (retryTimer !== null) {
    window.clearTimeout(retryTimer)
    retryTimer = null
  }
  socket?.close()
  socket = null
}

function selectCamera(cameraId: string | null): void {
  state.selectedCameraId = cameraId
  if (cameraId) state.selectedEventId = null
}

/** 点击事件标记：打开事件（若被筛选条件隐藏则重置筛选，保证列表可见）。 */
function selectEvent(eventId: string | null): void {
  state.selectedEventId = eventId
  const event = eventId ? state.events.find((item) => item.id === eventId) : null
  if (event && !matchesFilters(event)) {
    state.filters = { type: 'all', severity: 'all' }
  }
}

function requestFocus(cameraId: string): void {
  focusSeq += 1
  state.focusRequest = { cameraId, seq: focusSeq }
}

/** 相机详情/场景中的「定位」：选中相机并请求运镜。 */
function focusCamera(cameraId: string): void {
  state.selectedCameraId = cameraId
  state.selectedEventId = null
  requestFocus(cameraId)
}

/** 点击事件流条目：选中事件并聚焦其所属相机。 */
function focusEvent(eventId: string): void {
  const event = state.events.find((item) => item.id === eventId)
  if (!event) return
  state.selectedCameraId = event.camera_id
  state.selectedEventId = eventId
  requestFocus(event.camera_id)
}

/** 确认事件：POST /api/events/{id}/ack，成功后同步状态。 */
async function acknowledgeEvent(eventId: string): Promise<void> {
  if (state.ackPendingIds.includes(eventId)) return
  state.ackPendingIds = [...state.ackPendingIds, eventId]
  state.ackError = null
  try {
    const updated = await requestAckEvent(eventId)
    state.events = state.events.map((item) => (item.id === updated.id ? updated : item))
  } catch (error) {
    state.ackError = error instanceof Error ? error.message : String(error)
  } finally {
    state.ackPendingIds = state.ackPendingIds.filter((id) => id !== eventId)
  }
}

function clearAckError(): void {
  state.ackError = null
}

function setTypeFilter(type: EventType | 'all'): void {
  state.filters = { ...state.filters, type }
}

function setSeverityFilter(severity: Severity | 'all'): void {
  state.filters = { ...state.filters, severity }
}

function setFps(fps: number): void {
  state.fps = Math.round(fps)
}

function clearSelection(): void {
  state.selectedCameraId = null
  state.selectedEventId = null
}

export function useTwinStore() {
  return {
    state,
    selectedCamera,
    selectedEvent,
    onlineCameras,
    filteredEvents,
    start,
    stop,
    loadInitial,
    selectCamera,
    selectEvent,
    focusCamera,
    focusEvent,
    acknowledgeEvent,
    clearAckError,
    setTypeFilter,
    setSeverityFilter,
    setFps,
    clearSelection,
  }
}
