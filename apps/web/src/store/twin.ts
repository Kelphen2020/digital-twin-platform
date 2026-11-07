import { computed, reactive } from 'vue'
import {
  ApiError,
  ackEvent as requestAckEvent,
  apiErrorMessage,
  closeEvent as requestCloseEvent,
  createOrder as requestCreateOrder,
  deleteCalibration as requestDeleteCalibration,
  dispatchEvent as requestDispatchEvent,
  fetchCameras,
  fetchEventDetail,
  fetchEvents,
  fetchEventsCsv,
  fetchOrders,
  fetchStatsSummary,
  fetchTracks,
  saveCalibration as requestSaveCalibration,
  solveCalibration as requestSolveCalibration,
  updateCamera as requestUpdateCamera,
  updateOrder as requestUpdateOrder,
} from '../api'
import { TwinSocket } from '../ws'
import { pushToast } from './ui'
import type {
  CalibrationSavePayload,
  Camera,
  CameraUpdatePayload,
  ControlPoint,
  CreateOrderPayload,
  EventAction,
  EventFilters,
  EventType,
  FocusRequest,
  Order,
  OrderStatus,
  Severity,
  SidebarTab,
  StatsRange,
  StatsSummary,
  TrackPoint,
  TwinEvent,
  UpdateOrderPayload,
  WsMessage,
  WsState,
} from '../types'

const SITE_NAME = import.meta.env.VITE_SITE_NAME?.trim() || '数字孪生安防平台'
/** 事件流最多保留的条数（防止长时间运行无限增长）。 */
const EVENTS_LIMIT = 200
/** REST 不可用时的自动重试间隔。 */
const REST_RETRY_MS = 10_000
/** 轨迹查询条数（契约默认/上限内）。 */
const TRACKS_LIMIT = 500
/** 单条轨迹与轨迹总量上限，避免长时间运行内存膨胀。 */
const TRACKS_POINTS_PER_TRACK = 300
const TRACKS_MAX_TOTAL = 2000
/** 轨迹点保留时长（仅前端内存；视觉上更早的点已淡出）。 */
const TRACKS_RETENTION_MS = 10 * 60_000
/** 统计时间窗口。 */
const STATS_RANGES_MS: Record<StatsRange, number> = { '1h': 3_600_000, '24h': 86_400_000 }

interface TwinState {
  siteName: string
  cameras: Camera[]
  events: TwinEvent[]
  /** 服务端事件总数；WS stats 会持续校准。 */
  eventsTotal: number
  /** stats 推送的在线相机数，仅在台账为空时兜底。 */
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
  // ---- P1：事件处置 ----
  eventDetail: TwinEvent | null
  eventActions: EventAction[]
  eventDetailLoading: boolean
  eventDetailError: string | null
  actionPending: 'ack' | 'close' | 'dispatch' | null
  actionError: string | null
  exportError: string | null
  // ---- P1：轨迹 ----
  tracks: TrackPoint[]
  tracksLoading: boolean
  tracksError: string | null
  showTracks: boolean
  // ---- P1：统计 ----
  stats: StatsSummary | null
  statsRange: StatsRange
  statsLoading: boolean
  statsError: string | null
  // ---- P1：侧栏页签 ----
  sidebarTab: SidebarTab
  // ---- P2：工单 ----
  orders: Order[]
  ordersLoading: boolean
  ordersError: string | null
  orderStatusFilter: OrderStatus | 'all'
  orderActionPendingId: string | null
  orderCreatePending: boolean
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
  eventDetail: null,
  eventActions: [],
  eventDetailLoading: false,
  eventDetailError: null,
  actionPending: null,
  actionError: null,
  exportError: null,
  tracks: [],
  tracksLoading: false,
  tracksError: null,
  showTracks: true,
  stats: null,
  statsRange: '24h',
  statsLoading: false,
  statsError: null,
  sidebarTab: 'events',
  orders: [],
  ordersLoading: false,
  ordersError: null,
  orderStatusFilter: 'all',
  orderActionPendingId: null,
  orderCreatePending: false,
})

let socket: TwinSocket | null = null
let retryTimer: number | null = null
let loadingPromise: Promise<void> | null = null
let started = false
let focusSeq = 0
let trackRequestSeq = 0
let eventDetailSeq = 0

// ---------------- 派生状态 ----------------

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

const filteredOrders = computed(() =>
  state.orders.filter((order) => state.orderStatusFilter === 'all' || order.status === state.orderStatusFilter),
)

function matchesFilters(event: TwinEvent): boolean {
  return (
    (state.filters.type === 'all' || event.type === state.filters.type) &&
    (state.filters.severity === 'all' || event.severity === state.filters.severity)
  )
}

// ---------------- 基础数据 ----------------

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
      if (state.selectedCameraId) void loadTracks(state.selectedCameraId)
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

function applyEventUpdate(event: TwinEvent): void {
  const index = state.events.findIndex((item) => item.id === event.id)
  if (index >= 0) {
    const next = state.events.slice()
    next[index] = event
    state.events = next
  }
  if (state.selectedEventId === event.id) state.eventDetail = event
}

/** 新事件插入；数组替换以触发场景增量同步。 */
function upsertEvent(event: TwinEvent): void {
  const index = state.events.findIndex((item) => item.id === event.id)
  if (index >= 0) {
    applyEventUpdate(event)
    return
  }
  state.events = [event, ...state.events].slice(0, EVENTS_LIMIT)
  state.eventsTotal += 1
}

function applyCameraUpdate(camera: Camera): void {
  state.cameras = state.cameras.map((item) => (item.id === camera.id ? camera : item))
}

/** PATCH /api/cameras/{id}：admin 全量；operator 仅负责区域（区域外提示 403 out_of_scope）。 */
async function updateCameraInfo(cameraId: string, payload: CameraUpdatePayload): Promise<boolean> {
  try {
    const camera = await requestUpdateCamera(cameraId, payload)
    applyCameraUpdate(camera)
    pushToast('相机信息已更新', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  }
}

// ---------------- P2：工单 ----------------

function applyOrderUpdate(order: Order): void {
  const index = state.orders.findIndex((item) => item.id === order.id)
  if (index >= 0) {
    const next = state.orders.slice()
    next[index] = order
    state.orders = next
  } else {
    state.orders = [order, ...state.orders]
  }
}

/** 拉取工单列表（P2 数据量小，一次拉取后前端按状态过滤）。 */
async function loadOrders(): Promise<void> {
  state.ordersLoading = true
  state.ordersError = null
  try {
    state.orders = await fetchOrders({ limit: 100 })
  } catch (error) {
    state.ordersError = apiErrorMessage(error)
  } finally {
    state.ordersLoading = false
  }
}

/** 从事件创建工单（operator/admin）。 */
async function createOrderFromEvent(
  eventId: string,
  payload: Omit<CreateOrderPayload, 'event_id'>,
): Promise<Order | null> {
  if (state.orderCreatePending) return null
  state.orderCreatePending = true
  try {
    const order = await requestCreateOrder({ event_id: eventId, ...payload })
    applyOrderUpdate(order)
    pushToast('工单已创建', 'success')
    return order
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.orderCreatePending = false
  }
}

/** PATCH /api/orders/{id}：状态/负责人/备注流转。 */
async function updateOrderFields(orderId: string, payload: UpdateOrderPayload): Promise<boolean> {
  if (state.orderActionPendingId) return false
  state.orderActionPendingId = orderId
  try {
    const order = await requestUpdateOrder(orderId, payload)
    applyOrderUpdate(order)
    pushToast('工单已更新', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  } finally {
    state.orderActionPendingId = null
  }
}

function setOrderStatusFilter(status: OrderStatus | 'all'): void {
  state.orderStatusFilter = status
}

// ---------------- 轨迹 ----------------

/** 去重、按时间升序、剔除过期点、限制单轨与总量。 */
function trimTrackPoints(points: TrackPoint[]): TrackPoint[] {
  const now = Date.now()
  const sorted = points
    .filter((point) => {
      const ts = Date.parse(point.ts)
      return !Number.isFinite(ts) || now - ts <= TRACKS_RETENTION_MS
    })
    .sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts))

  const perTrack = new Map<string, number>()
  const kept: TrackPoint[] = []
  for (let i = sorted.length - 1; i >= 0; i -= 1) {
    const point = sorted[i]
    const count = perTrack.get(point.track_id) ?? 0
    if (count >= TRACKS_POINTS_PER_TRACK) continue
    perTrack.set(point.track_id, count + 1)
    kept.push(point)
  }
  kept.reverse()
  return kept.slice(-TRACKS_MAX_TOTAL)
}

/** 相机切换时重新拉取该相机的历史轨迹点。 */
async function loadTracks(cameraId: string | null): Promise<void> {
  const seq = (trackRequestSeq += 1)
  state.tracks = []
  state.tracksError = null
  state.tracksLoading = false
  if (!cameraId) return

  state.tracksLoading = true
  try {
    const points = await fetchTracks(cameraId, TRACKS_LIMIT)
    if (seq !== trackRequestSeq) return
    state.tracks = trimTrackPoints(points)
  } catch (error) {
    if (seq === trackRequestSeq) {
      state.tracksError = error instanceof Error ? error.message : String(error)
    }
  } finally {
    if (seq === trackRequestSeq) state.tracksLoading = false
  }
}

/** WS track_point 实时追加（仅当前选中相机）。 */
function appendTrackPoint(point: TrackPoint): void {
  if (!point || point.camera_id !== state.selectedCameraId) return
  const ts = Date.parse(point.ts)
  if (!Number.isFinite(ts)) return

  // 乱序保护：该 track 已有更新时间的点则忽略；允许同一 ts 的多点（模拟器按批次推送）
  let latestTs = Number.NEGATIVE_INFINITY
  for (let i = state.tracks.length - 1; i >= 0; i -= 1) {
    if (state.tracks[i].track_id === point.track_id) {
      latestTs = Date.parse(state.tracks[i].ts)
      break
    }
  }
  if (latestTs > ts) return
  state.tracks = trimTrackPoints([...state.tracks, point])
}

// ---------------- 事件详情与处置 ----------------

function resetEventDetail(): void {
  eventDetailSeq += 1
  state.eventDetail = null
  state.eventActions = []
  state.eventDetailLoading = false
  state.eventDetailError = null
  state.actionPending = null
  state.actionError = null
}

async function loadEventDetail(eventId: string | null = state.selectedEventId): Promise<void> {
  if (!eventId) {
    resetEventDetail()
    return
  }
  const seq = (eventDetailSeq += 1)
  state.eventDetailLoading = true
  state.eventDetailError = null
  try {
    const detail = await fetchEventDetail(eventId)
    if (seq !== eventDetailSeq) return
    state.eventDetail = detail.event
    state.eventActions = detail.actions ?? []
    applyEventUpdate(detail.event)
  } catch (error) {
    if (seq === eventDetailSeq) {
      state.eventDetailError = error instanceof Error ? error.message : String(error)
    }
  } finally {
    if (seq === eventDetailSeq) state.eventDetailLoading = false
  }
}

async function runEventAction(
  action: 'ack' | 'close' | 'dispatch',
  eventId: string,
  request: () => Promise<TwinEvent>,
): Promise<boolean> {
  if (state.actionPending) return false
  state.actionPending = action
  state.actionError = null
  state.ackError = null
  try {
    const updated = await request()
    applyEventUpdate(updated)
    if (state.selectedEventId === eventId) await loadEventDetail(eventId)
    return true
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    state.actionError = message
    if (action === 'ack') state.ackError = message
    // RBAC 权限错误额外浮层提示（out_of_scope / forbidden / 会话过期）
    if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
      pushToast(apiErrorMessage(error), 'error')
    }
    return false
  } finally {
    state.actionPending = null
  }
}

/** 确认事件：POST /api/events/{id}/ack。 */
async function acknowledgeEvent(eventId: string): Promise<boolean> {
  if (state.ackPendingIds.includes(eventId)) return false
  state.ackPendingIds = [...state.ackPendingIds, eventId]
  try {
    return await runEventAction('ack', eventId, () => requestAckEvent(eventId))
  } finally {
    state.ackPendingIds = state.ackPendingIds.filter((id) => id !== eventId)
  }
}

/** 关闭事件：POST /api/events/{id}/close（可带备注）。 */
function closeEvent(eventId: string, note?: string): Promise<boolean> {
  return runEventAction('close', eventId, () => requestCloseEvent(eventId, note))
}

/** 派单：POST /api/events/{id}/dispatch（不改变事件状态，仅记录动作）。 */
function dispatchEvent(eventId: string, target: string, note?: string): Promise<boolean> {
  if (!target.trim()) {
    state.actionError = '派单目标不能为空'
    return Promise.resolve(false)
  }
  return runEventAction('dispatch', eventId, () => requestDispatchEvent(eventId, target, note))
}

function clearActionError(): void {
  state.actionError = null
}

function clearAckError(): void {
  state.ackError = null
}

function clearExportError(): void {
  state.exportError = null
}

// ---------------- 统计与导出 ----------------

async function loadStats(range: StatsRange = state.statsRange): Promise<void> {
  state.statsRange = range
  state.statsLoading = true
  state.statsError = null
  const to = new Date()
  const from = new Date(to.getTime() - STATS_RANGES_MS[range])
  try {
    const summary = await fetchStatsSummary(from.toISOString(), to.toISOString())
    if (state.statsRange === range) state.stats = summary
  } catch (error) {
    if (state.statsRange === range) {
      state.statsError = error instanceof Error ? error.message : String(error)
    }
  } finally {
    if (state.statsRange === range) state.statsLoading = false
  }
}

function exportRange(): { from: string; to: string } {
  const to = new Date()
  const from = new Date(to.getTime() - STATS_RANGES_MS[state.statsRange])
  return { from: from.toISOString(), to: to.toISOString() }
}

/** 导出事件 CSV（带鉴权头，触发浏览器下载）。 */
async function downloadEventsCsv(): Promise<void> {
  state.exportError = null
  const { from, to } = exportRange()
  try {
    const blob = await fetchEventsCsv(from, to)
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `events_${to.slice(0, 19).replace(/[:T]/g, '-')}.csv`
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
  } catch (error) {
    state.exportError = error instanceof Error ? error.message : String(error)
  }
}

// ---------------- 标定（P1，可选） ----------------

function solveCameraCalibration(cameraId: string, points: ControlPoint[]) {
  return requestSolveCalibration(cameraId, points)
}

async function saveCameraCalibration(cameraId: string, payload: CalibrationSavePayload): Promise<Camera> {
  const camera = await requestSaveCalibration(cameraId, payload)
  applyCameraUpdate(camera)
  return camera
}

async function removeCameraCalibration(cameraId: string): Promise<Camera> {
  const camera = await requestDeleteCalibration(cameraId)
  applyCameraUpdate(camera)
  return camera
}

// ---------------- WS ----------------

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
    case 'event_updated': {
      applyEventUpdate(message.data)
      if (state.selectedEventId === message.data.id) void loadEventDetail(message.data.id)
      break
    }
    case 'camera_status': {
      const camera = state.cameras.find((item) => item.id === message.data.id || item.code === message.data.code)
      if (camera) camera.status = message.data.status
      break
    }
    case 'track_point': {
      appendTrackPoint(message.data)
      break
    }
    case 'order_updated': {
      applyOrderUpdate(message.data)
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

// ---------------- 选择与交互 ----------------

function selectCamera(cameraId: string | null): void {
  state.selectedCameraId = cameraId
  if (cameraId) {
    state.selectedEventId = null
    resetEventDetail()
  }
  void loadTracks(cameraId)
}

/** 点击事件标记/列表：打开事件详情；若被筛选条件隐藏则重置筛选，保证列表可见。 */
function selectEvent(eventId: string | null): void {
  state.selectedEventId = eventId
  state.actionError = null
  if (!eventId) {
    resetEventDetail()
    return
  }
  const event = state.events.find((item) => item.id === eventId)
  if (event && !matchesFilters(event)) {
    state.filters = { type: 'all', severity: 'all' }
  }
  void loadEventDetail(eventId)
}

function requestFocus(cameraId: string): void {
  focusSeq += 1
  state.focusRequest = { cameraId, seq: focusSeq }
}

/** 相机详情/场景中的「定位」：选中相机、加载轨迹并请求运镜。 */
function focusCamera(cameraId: string): void {
  state.selectedCameraId = cameraId
  state.selectedEventId = null
  resetEventDetail()
  void loadTracks(cameraId)
  requestFocus(cameraId)
}

/** 点击事件流条目：选中事件、聚焦其所属相机并打开处置详情。 */
function focusEvent(eventId: string): void {
  const event = state.events.find((item) => item.id === eventId)
  if (!event) return
  state.selectedCameraId = event.camera_id
  state.selectedEventId = eventId
  state.actionError = null
  void loadTracks(event.camera_id)
  void loadEventDetail(eventId)
  requestFocus(event.camera_id)
}

function clearSelection(): void {
  state.selectedCameraId = null
  state.selectedEventId = null
  resetEventDetail()
  void loadTracks(null)
}

/** 登出/切换账号时清空历史数据，避免下一会话看到上一角色残留数据。 */
function reset(): void {
  stop()
  trackRequestSeq += 1
  eventDetailSeq += 1
  focusSeq = 0
  loadingPromise = null
  state.cameras = []
  state.events = []
  state.eventsTotal = 0
  state.onlineCamerasFromStats = null
  state.wsState = 'connecting'
  state.backendOnline = false
  state.loading = true
  state.lastError = null
  state.ackError = null
  state.selectedCameraId = null
  state.selectedEventId = null
  state.filters = { type: 'all', severity: 'all' }
  state.ackPendingIds = []
  state.focusRequest = null
  state.fps = 0
  state.eventDetail = null
  state.eventActions = []
  state.eventDetailLoading = false
  state.eventDetailError = null
  state.actionPending = null
  state.actionError = null
  state.exportError = null
  state.tracks = []
  state.tracksLoading = false
  state.tracksError = null
  state.showTracks = true
  state.stats = null
  state.statsRange = '24h'
  state.statsLoading = false
  state.statsError = null
  state.sidebarTab = 'events'
  state.orders = []
  state.ordersLoading = false
  state.ordersError = null
  state.orderStatusFilter = 'all'
  state.orderActionPendingId = null
  state.orderCreatePending = false
}

// ---------------- 筛选 / 页签 / 显示开关 ----------------

function setTypeFilter(type: EventType | 'all'): void {
  state.filters = { ...state.filters, type }
}

function setSeverityFilter(severity: Severity | 'all'): void {
  state.filters = { ...state.filters, severity }
}

function setSidebarTab(tab: SidebarTab): void {
  state.sidebarTab = tab
  if (tab === 'stats') void loadStats()
  if (tab === 'orders') void loadOrders()
}

function toggleTracks(): void {
  state.showTracks = !state.showTracks
}

function setFps(fps: number): void {
  state.fps = Math.round(fps)
}

export function useTwinStore() {
  return {
    state,
    selectedCamera,
    selectedEvent,
    onlineCameras,
    filteredEvents,
    filteredOrders,
    start,
    stop,
    reset,
    loadInitial,
    selectCamera,
    selectEvent,
    focusCamera,
    focusEvent,
    acknowledgeEvent,
    closeEvent,
    dispatchEvent,
    clearActionError,
    clearAckError,
    clearExportError,
    loadEventDetail,
    loadTracks,
    loadStats,
    downloadEventsCsv,
    solveCameraCalibration,
    saveCameraCalibration,
    removeCameraCalibration,
    updateCameraInfo,
    loadOrders,
    createOrderFromEvent,
    updateOrderFields,
    setOrderStatusFilter,
    setTypeFilter,
    setSeverityFilter,
    setSidebarTab,
    toggleTracks,
    setFps,
    clearSelection,
  }
}
