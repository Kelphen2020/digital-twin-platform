import { getAuthToken } from './auth/token'
import type {
  Area,
  AreaListResponse,
  AuditEntry,
  BusReplayRequest,
  BusReplayResponse,
  BusStatus,
  AuditListResponse,
  AuthUser,
  CalibrationSavePayload,
  CalibrationSolveResponse,
  Camera,
  CameraListResponse,
  CameraRules,
  CameraRulesPayload,
  CameraUpdatePayload,
  ControlPoint,
  CreateOrderPayload,
  DutyCurrent,
  DutyGroup,
  DutyGroupPayload,
  DutyListResponse,
  EventDetailResponse,
  EscalationSettings,
  EventListResponse,
  LoginResponse,
  CreateUserPayload,
  ManagedUser,
  NotificationListResponse,
  NotificationLogEntry,
  NotificationSettings,
  Order,
  OrderListResponse,
  OrderStatus,
  SiteConfig,
  StatsSummary,
  TrackListResponse,
  TrackPoint,
  TwinEvent,
  UpdateOrderPayload,
  UpdateUserPayload,
  UserListResponse,
} from './types'

/**
 * 后端源站。为空字符串时使用同源：开发环境由 Vite 把 /api、/ws 代理到 127.0.0.1:8080。
 * 若显式设置 VITE_API_BASE（如 http://127.0.0.1:8080），前端直接请求该源站。
 */
export const API_BASE = (import.meta.env.VITE_API_BASE ?? '').trim().replace(/\/+$/, '')

/** 机器/联调用的静态令牌兜底（P1 遗留）；正常使用登录会话令牌。 */
const ENV_API_TOKEN = (import.meta.env.VITE_API_TOKEN ?? '').trim()

export class ApiError extends Error {
  readonly status: number | undefined
  /** 后端错误码（响应体 error 字段，如 out_of_scope / forbidden）。 */
  readonly code: string | undefined

  constructor(message: string, status?: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

let unauthorizedHandler: (() => void) | null = null

/** 注册 401 处理（会话过期自动登出）；返回解除函数。 */
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

function authHeaders(): Record<string, string> {
  const token = getAuthToken() ?? ENV_API_TOKEN
  return token ? { Authorization: `Bearer ${token}` } : {}
}

export function apiUrl(path: string): string {
  return `${API_BASE}${path}`
}

async function parseError(response: Response): Promise<ApiError> {
  let message = `HTTP ${response.status}`
  let code: string | undefined
  try {
    const body = (await response.json()) as { error?: string; message?: string }
    code = body.error
    message = body.message ?? body.error ?? message
  } catch {
    // 错误响应不是 JSON 时保留 HTTP 状态信息
  }
  return new ApiError(message, response.status, code)
}

function notifyUnauthorized(path: string): void {
  // 登录接口的 401 是凭证错误，不应触发"会话过期"登出
  if (path.startsWith('/api/auth/login')) return
  unauthorizedHandler?.()
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiUrl(path), {
    ...init,
    headers: {
      Accept: 'application/json',
      ...authHeaders(),
      ...((init?.headers as Record<string, string> | undefined) ?? {}),
    },
  })
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized(path)
    throw error
  }
  return (await response.json()) as T
}

function postJson<T>(path: string, body?: unknown): Promise<T> {
  return requestJson<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

function patchJson<T>(path: string, body: unknown): Promise<T> {
  return requestJson<T>(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

function putJson<T>(path: string, body: unknown): Promise<T> {
  return requestJson<T>(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/**
 * 把后端/网络错误转成可直接展示的中文文案。
 * 特别处理 P2 RBAC 的 401/403（forbidden / out_of_scope）。
 */
export function apiErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return '登录已过期，请重新登录'
    if (error.status === 403) {
      if (error.code === 'out_of_scope' || error.message.includes('out_of_scope')) {
        return '该相机不在您的负责区域，操作被拒绝（403 out_of_scope）'
      }
      return `当前角色无此操作权限（403）：${error.message}`
    }
    if (error.status === 400) {
      if (error.code === 'self_protected' || error.message.includes('self_protected')) {
        return '不能停用、降级或删除当前登录账号（400 self_protected）'
      }
      return `请求参数不合法（400）：${error.message}`
    }
    if (error.status === 404) return `资源不存在（404 ${error.message}）`
    if (error.status === 409) {
      if (error.code === 'username_taken' || error.message.includes('username_taken')) {
        return '用户名已存在（409 username_taken）'
      }
      if (error.code === 'bus_driver_unsupported' || error.message.includes('bus_driver_unsupported')) {
        return '当前总线驱动不支持该回放区间（409 bus_driver_unsupported）'
      }
      return `操作冲突（409）：${error.message}`
    }
    return error.message
  }
  if (error instanceof TypeError) return '无法连接后端 API'
  return error instanceof Error ? error.message : String(error)
}

// ---------------- P2 身份 ----------------

/** POST /api/auth/login → JWT + 用户信息。 */
export async function login(username: string, password: string): Promise<LoginResponse> {
  return postJson<LoginResponse>('/api/auth/login', { username, password })
}

/** GET /api/auth/me → 当前登录用户。 */
export async function fetchMe(): Promise<AuthUser> {
  return requestJson<AuthUser>('/api/auth/me')
}

// ---------------- P0 REST ----------------

/** GET /api/cameras → 相机台账。 */
export async function fetchCameras(): Promise<Camera[]> {
  const data = await requestJson<CameraListResponse>('/api/cameras')
  return data.items ?? []
}

/** PATCH /api/cameras/{id} → 修改状态/坐标/朝向，返回更新后的 Camera。 */
export async function updateCamera(cameraId: string, payload: CameraUpdatePayload): Promise<Camera> {
  return patchJson<Camera>(`/api/cameras/${encodeURIComponent(cameraId)}`, payload)
}

/** GET /api/events?limit=N → 按发生时间倒序的事件列表。 */
export async function fetchEvents(limit = 100): Promise<EventListResponse> {
  const data = await requestJson<EventListResponse>(`/api/events?limit=${limit}`)
  const items = data.items ?? []
  return { items, total: data.total ?? items.length }
}

/** POST /api/events/{id}/ack → 返回更新为 acked 的事件。 */
export async function ackEvent(eventId: string): Promise<TwinEvent> {
  const data = await requestJson<{ ok: boolean; event: TwinEvent }>(
    `/api/events/${encodeURIComponent(eventId)}/ack`,
    { method: 'POST' },
  )
  return data.event
}

// ---------------- P1 事件处置 ----------------

/** GET /api/events/{id} → 事件详情 + 处置动作时间线。 */
export async function fetchEventDetail(eventId: string): Promise<EventDetailResponse> {
  return requestJson<EventDetailResponse>(`/api/events/${encodeURIComponent(eventId)}`)
}

/** POST /api/events/{id}/close → 关闭事件（可带备注）。 */
export async function closeEvent(eventId: string, note?: string): Promise<TwinEvent> {
  const body = note?.trim() ? { note: note.trim() } : {}
  const data = await postJson<{ ok: boolean; event: TwinEvent }>(
    `/api/events/${encodeURIComponent(eventId)}/close`,
    body,
  )
  return data.event
}

/** POST /api/events/{id}/dispatch → 派单（不改变事件状态）。 */
export async function dispatchEvent(eventId: string, target: string, note?: string): Promise<TwinEvent> {
  const data = await postJson<{ ok: boolean; event: TwinEvent }>(
    `/api/events/${encodeURIComponent(eventId)}/dispatch`,
    { target: target.trim(), note: note?.trim() || undefined },
  )
  return data.event
}

// ---------------- P1 轨迹 ----------------

/** GET /api/tracks?camera_id=&limit= → 按 ts 倒序的轨迹点。 */
export async function fetchTracks(cameraId: string, limit = 500, since?: string): Promise<TrackPoint[]> {
  const params = new URLSearchParams({ camera_id: cameraId, limit: String(limit) })
  if (since) params.set('since', since)
  const data = await requestJson<TrackListResponse>(`/api/tracks?${params.toString()}`)
  return data.items ?? []
}

// ---------------- P1 统计与导出 ----------------

/** GET /api/stats/summary?from=&to= → 统计汇总。 */
export async function fetchStatsSummary(from?: string, to?: string): Promise<StatsSummary> {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  const query = params.toString()
  return requestJson<StatsSummary>(`/api/stats/summary${query ? `?${query}` : ''}`)
}

/** GET /api/stats/events.csv?from=&to= → CSV 文件内容（带鉴权头）。 */
export async function fetchEventsCsv(from?: string, to?: string): Promise<Blob> {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  const query = params.toString()
  const response = await fetch(apiUrl(`/api/stats/events.csv${query ? `?${query}` : ''}`), {
    headers: { Accept: 'text/csv', ...authHeaders() },
  })
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized('/api/stats/events.csv')
    throw error
  }
  return response.blob()
}

// ---------------- P1 标定 ----------------

/** POST /api/calibration/solve → 用 4–20 组控制点求解单应矩阵。 */
export async function solveCalibration(
  cameraId: string,
  points: ControlPoint[],
): Promise<CalibrationSolveResponse> {
  return postJson<CalibrationSolveResponse>('/api/calibration/solve', { camera_id: cameraId, points })
}

/** PUT /api/cameras/{id}/calibration → 保存标定结果，返回更新后的 Camera。 */
export async function saveCalibration(
  cameraId: string,
  payload: CalibrationSavePayload,
): Promise<Camera> {
  return requestJson<Camera>(`/api/cameras/${encodeURIComponent(cameraId)}/calibration`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

/** DELETE /api/cameras/{id}/calibration → 清除标定，返回更新后的 Camera。 */
export async function deleteCalibration(cameraId: string): Promise<Camera> {
  return requestJson<Camera>(`/api/cameras/${encodeURIComponent(cameraId)}/calibration`, {
    method: 'DELETE',
  })
}

// ---------------- P2 相机规则 ----------------

/** GET /api/cameras/{id}/rules → 相机规则（未配置时返回 P0 默认值）。 */
export async function fetchCameraRules(cameraId: string): Promise<CameraRules> {
  return requestJson<CameraRules>(`/api/cameras/${encodeURIComponent(cameraId)}/rules`)
}

/** PUT /api/cameras/{id}/rules（仅 admin）→ 保存并返回规则。 */
export async function saveCameraRules(
  cameraId: string,
  payload: CameraRulesPayload,
): Promise<CameraRules> {
  return requestJson<CameraRules>(`/api/cameras/${encodeURIComponent(cameraId)}/rules`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

// ---------------- P2 工单 ----------------

/** GET /api/orders?status=&assignee=&limit= → 工单列表。 */
export async function fetchOrders(
  params: { status?: OrderStatus; assignee?: string; limit?: number } = {},
): Promise<Order[]> {
  const search = new URLSearchParams()
  if (params.status) search.set('status', params.status)
  if (params.assignee) search.set('assignee', params.assignee)
  search.set('limit', String(params.limit ?? 100))
  const data = await requestJson<OrderListResponse>(`/api/orders?${search.toString()}`)
  return data.items ?? []
}

/** GET /api/orders/{id} → 单个工单。 */
export async function fetchOrder(orderId: string): Promise<Order> {
  return requestJson<Order>(`/api/orders/${encodeURIComponent(orderId)}`)
}

/** POST /api/orders（operator/admin）→ 创建工单。 */
export async function createOrder(payload: CreateOrderPayload): Promise<Order> {
  return postJson<Order>('/api/orders', payload)
}

/** PATCH /api/orders/{id}（operator/admin）→ 流转工单。 */
export async function updateOrder(orderId: string, payload: UpdateOrderPayload): Promise<Order> {
  return patchJson<Order>(`/api/orders/${encodeURIComponent(orderId)}`, payload)
}

// ---------------- P3 设置 / 审计 / 通知 ----------------

/** GET /api/settings/notifications（admin）→ 通知（Webhook）设置。 */
export async function fetchNotificationSettings(): Promise<NotificationSettings> {
  return requestJson<NotificationSettings>('/api/settings/notifications')
}

/** PUT /api/settings/notifications（admin）→ 保存通知设置。 */
export async function saveNotificationSettings(payload: NotificationSettings): Promise<NotificationSettings> {
  return putJson<NotificationSettings>('/api/settings/notifications', payload)
}

/** GET /api/settings/escalation（admin）→ 告警升级设置。 */
export async function fetchEscalationSettings(): Promise<EscalationSettings> {
  return requestJson<EscalationSettings>('/api/settings/escalation')
}

/** PUT /api/settings/escalation（admin）→ 保存告警升级设置。 */
export async function saveEscalationSettings(payload: EscalationSettings): Promise<EscalationSettings> {
  return putJson<EscalationSettings>('/api/settings/escalation', payload)
}

/** GET /api/audit?limit=（admin）→ 操作审计。 */
export async function fetchAudit(limit = 100): Promise<AuditEntry[]> {
  const data = await requestJson<AuditListResponse>(`/api/audit?limit=${limit}`)
  return data.items ?? []
}

/** GET /api/notifications?limit=（admin）→ 通知投递记录。 */
export async function fetchNotifications(limit = 50): Promise<NotificationLogEntry[]> {
  const data = await requestJson<NotificationListResponse>(`/api/notifications?limit=${limit}`)
  return data.items ?? []
}

// ---------------- P4 值班组 / 区域 ----------------

/** GET /api/areas → 区域列表（值班组区域多选）。 */
export async function fetchAreas(): Promise<Area[]> {
  const data = await requestJson<AreaListResponse>('/api/areas')
  return data.items ?? []
}

/** GET /api/duty/groups → 值班组列表（登录用户）。 */
export async function fetchDutyGroups(): Promise<DutyGroup[]> {
  const data = await requestJson<DutyListResponse>('/api/duty/groups')
  return data.items ?? []
}

/** GET /api/duty/current?area_id= → 指定区域当前值班组（登录用户）。 */
export async function fetchDutyCurrent(areaId: string): Promise<DutyCurrent> {
  const params = new URLSearchParams({ area_id: areaId })
  return requestJson<DutyCurrent>(`/api/duty/current?${params.toString()}`)
}

/** POST /api/duty/groups（admin）→ 创建值班组。 */
export async function createDutyGroup(payload: DutyGroupPayload): Promise<DutyGroup> {
  return postJson<DutyGroup>('/api/duty/groups', payload)
}

/** PATCH /api/duty/groups/{id}（admin）→ 更新值班组。 */
export async function updateDutyGroup(groupId: string, payload: Partial<DutyGroupPayload>): Promise<DutyGroup> {
  return patchJson<DutyGroup>(`/api/duty/groups/${encodeURIComponent(groupId)}`, payload)
}

/** DELETE /api/duty/groups/{id}（admin）→ 删除值班组。 */
export async function deleteDutyGroup(groupId: string): Promise<void> {
  const response = await fetch(apiUrl(`/api/duty/groups/${encodeURIComponent(groupId)}`), {
    method: 'DELETE',
    headers: { Accept: 'application/json', ...authHeaders() },
  })
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized('/api/duty/groups')
    throw error
  }
}

// ---------------- P5 事件总线 ----------------

/** GET /api/bus/status（admin）→ 总线运行状态。 */
export async function fetchBusStatus(): Promise<BusStatus> {
  return requestJson<BusStatus>('/api/bus/status')
}

/** POST /api/bus/replay（admin）→ 回放指定 offset 区间。 */
export async function replayBus(payload: BusReplayRequest): Promise<BusReplayResponse> {
  return postJson<BusReplayResponse>('/api/bus/replay', payload)
}

// ---------------- P6 站点配置 ----------------

/** GET /api/site（登录用户）→ 站点范围 / 模型清单 / 底图配置。 */
export async function fetchSite(): Promise<SiteConfig> {
  return requestJson<SiteConfig>('/api/site')
}

// ---------------- P7 用户管理（admin） ----------------

/** GET /api/users（admin）→ 用户列表。 */
export async function fetchUsers(): Promise<ManagedUser[]> {
  const data = await requestJson<UserListResponse>('/api/users')
  return data.items ?? []
}

/** POST /api/users（admin）→ 创建用户。 */
export async function createUser(payload: CreateUserPayload): Promise<ManagedUser> {
  return postJson<ManagedUser>('/api/users', payload)
}

/** PATCH /api/users/{id}（admin）→ 修改显示名/密码/角色/区域/启用状态。 */
export async function updateUser(userId: string, payload: UpdateUserPayload): Promise<ManagedUser> {
  return patchJson<ManagedUser>(`/api/users/${encodeURIComponent(userId)}`, payload)
}

/** DELETE /api/users/{id}（admin）→ 删除用户（不能删除自己）。 */
export async function deleteUser(userId: string): Promise<void> {
  const response = await fetch(apiUrl(`/api/users/${encodeURIComponent(userId)}`), {
    method: 'DELETE',
    headers: { Accept: 'application/json', ...authHeaders() },
  })
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized('/api/users')
    throw error
  }
}

/** 供界面展示的 API 地址说明（离线横幅/README 使用）。 */
export function describeApiBase(): string {
  return API_BASE || `${window.location.origin}（Vite 代理 → 127.0.0.1:8080）`
}
