import { clearTokens, getAuthToken, getRefreshToken, getTenantId, setTokens } from './auth/token'
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
  PatrolExecution,
  PatrolExecutionCompletePayload,
  PatrolExecutionListResponse,
  PatrolExecutionStatus,
  PatrolRoute,
  PatrolRouteListResponse,
  PatrolRoutePayload,
  PatrolSchedule,
  PatrolSchedulePayload,
  PatrolStats,
  RefreshResponse,
  SiteConfig,
  StatsSummary,
  TrackListResponse,
  Tenant,
  TenantCreatePayload,
  TenantDeleteResult,
  TenantListResponse,
  TenantUpdatePayload,
  TrackPoint,
  TwinEvent,
  UpdateOrderPayload,
  UpdateUserPayload,
  UserListResponse,
  UserSession,
  UserSessionsResponse,
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
  /** 429 响应的 Retry-After（秒；P10 登录锁定）。 */
  readonly retryAfter: number | undefined

  constructor(message: string, status?: number, code?: string, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

let unauthorizedHandler: (() => void) | null = null

/** 注册 401 处理（会话过期自动登出）；返回解除函数。 */
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

function authHeaders(): Record<string, string> {
  const token = getAuthToken() ?? ENV_API_TOKEN
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = `Bearer ${token}`
  // P27：所有 API 请求携带当前租户（缺省不发送，由后端按 default 处理）
  const tenant = getTenantId()
  if (tenant) headers['X-Tenant-ID'] = tenant
  return headers
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
  const retryAfterHeader = response.headers.get('Retry-After')
  const parsedRetryAfter = retryAfterHeader ? Number.parseInt(retryAfterHeader, 10) : NaN
  return new ApiError(
    message,
    response.status,
    code,
    Number.isFinite(parsedRetryAfter) ? parsedRetryAfter : undefined,
  )
}

function notifyUnauthorized(path: string): void {
  // 登录接口的 401 是凭证错误，不应触发"会话过期"登出
  if (path.startsWith('/api/auth/login')) return
  unauthorizedHandler?.()
}

// ---------------- P11：access token 自动刷新（单飞，避免刷新风暴/死循环） ----------------

/** 登录/刷新/登出接口本身不触发 refresh。 */
const NO_REFRESH_PATHS = ['/api/auth/login', '/api/auth/refresh', '/api/auth/logout', '/api/auth/logout-all']

let refreshPromise: Promise<boolean> | null = null
let tokenRefreshHandler:
  | ((bundle: { accessToken: string; refreshToken: string | null; refreshExpiresAt: string | null }) => void)
  | null = null

/** 注册 access token 轮换回调（auth store 同步内存中的 token）。 */
export function setTokenRefreshHandler(handler: typeof tokenRefreshHandler): void {
  tokenRefreshHandler = handler
}

/** POST /api/auth/refresh：成功则持久化轮换后的 access+refresh。 */
async function refreshAccessToken(): Promise<boolean> {
  const currentRefresh = getRefreshToken()
  if (!currentRefresh) return false
  if (refreshPromise) return refreshPromise
  refreshPromise = (async () => {
    try {
      const tenant = getTenantId()
      const response = await fetch(apiUrl('/api/auth/refresh'), {
        method: 'POST',
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          ...(tenant ? { 'X-Tenant-ID': tenant } : {}),
        },
        body: JSON.stringify({ refresh_token: currentRefresh }),
      })
      if (!response.ok) {
        clearTokens()
        return false
      }
      const data = (await response.json()) as RefreshResponse
      if (!data.token) {
        clearTokens()
        return false
      }
      const nextBundle = {
        accessToken: data.token,
        refreshToken: data.refresh_token || currentRefresh,
        refreshExpiresAt: data.refresh_expires_at ?? null,
      }
      setTokens(nextBundle)
      tokenRefreshHandler?.(nextBundle)
      return true
    } catch {
      clearTokens()
      return false
    } finally {
      refreshPromise = null
    }
  })()
  return refreshPromise
}

/** 带鉴权的 fetch：401 时最多刷新一次并重试原请求；刷新失败清理会话并通知登出。 */
async function fetchWithAuth(path: string, init?: RequestInit): Promise<Response> {
  const buildInit = (): RequestInit => ({
    ...init,
    headers: {
      Accept: 'application/json',
      ...authHeaders(),
      ...((init?.headers as Record<string, string> | undefined) ?? {}),
    },
  })
  let response = await fetch(apiUrl(path), buildInit())
  if (response.status === 401 && !NO_REFRESH_PATHS.some((prefix) => path.startsWith(prefix))) {
    const refreshed = await refreshAccessToken()
    if (refreshed) response = await fetch(apiUrl(path), buildInit())
    if (response.status === 401) notifyUnauthorized(path)
  } else if (response.status === 401) {
    notifyUnauthorized(path)
  }
  return response
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetchWithAuth(path, init)
  if (!response.ok) {
    const error = await parseError(response)
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
      if (error.code === 'tenant_archived' || error.message.includes('tenant_archived')) {
        return '租户已归档'
      }
      if (error.code === 'tenant_inactive' || error.message.includes('tenant_inactive')) {
        return '租户已停用'
      }
      if (error.code === 'cross_tenant' || error.message.includes('cross_tenant')) {
        return '登录租户与请求不一致，请重新登录'
      }
      if (error.code === 'out_of_scope' || error.message.includes('out_of_scope')) {
        return '该相机不在您的负责区域，操作被拒绝（403 out_of_scope）'
      }
      return `当前角色无此操作权限（403）：${error.message}`
    }
    if (error.status === 429) {
      // P30 租户配额：error=quota_exceeded（message 含维度）
      if (error.code === 'quota_exceeded' || error.message.includes('quota_exceeded')) {
        const dimensions: [string, string][] = [
          ['cameras', '相机'],
          ['users', '用户'],
          ['events', '事件'],
          ['ingest_qps', 'ingest QPS'],
        ]
        const matched = dimensions.find(([key]) => error.message.includes(key))
        return matched ? `超出配额：${matched[1]}（${error.message}）` : `超出配额：${error.message}`
      }
      // P10 登录防爆破：error=too_many_attempts + Retry-After 秒
      if (error.code === 'too_many_attempts' || error.message.includes('too_many_attempts')) {
        return '登录失败次数过多，请稍后重试'
      }
      // P9 ingest 限流：error=rate_limited
      return '请求过于频繁，已限流'
    }
    if (error.status === 400) {
      if (error.code === 'unknown_tenant' || error.message.includes('unknown_tenant')) {
        return '租户不存在'
      }
      if (error.code === 'self_protected' || error.message.includes('self_protected')) {
        return '不能停用、降级或删除当前登录账号（400 self_protected）'
      }
      const passwordMessage = error.message.toLowerCase()
      if (
        passwordMessage.includes('password') &&
        (passwordMessage.includes('10') || passwordMessage.includes('letter') || passwordMessage.includes('digit'))
      ) {
        return '密码不符合策略：至少 10 个字符，且需同时包含字母与数字'
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

/** POST /api/auth/login（P27：tenant_id 缺省 default）→ JWT + 用户信息 + tenant_id。 */
export async function login(username: string, password: string, tenantId = 'default'): Promise<LoginResponse> {
  return requestJson<LoginResponse>('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Tenant-ID': tenantId },
    body: JSON.stringify({ username, password, tenant_id: tenantId }),
  })
}

/** GET /api/auth/me → 当前登录用户。 */
export async function fetchMe(): Promise<AuthUser> {
  return requestJson<AuthUser>('/api/auth/me')
}

/** POST /api/auth/logout：吊销当前 refresh token（幂等 204，失败静默）。 */
export async function logoutSession(): Promise<void> {
  const refresh = getRefreshToken()
  if (!refresh) return
  try {
    await fetch(apiUrl('/api/auth/logout'), {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', ...authHeaders() },
      body: JSON.stringify({ refresh_token: refresh }),
    })
  } catch {
    // 登出失败也清理本地会话
  }
}

/** GET /api/users/{id}/sessions（admin）→ 该用户有效 refresh 会话。 */
export async function fetchUserSessions(userId: string): Promise<UserSession[]> {
  const data = await requestJson<UserSessionsResponse>(`/api/users/${encodeURIComponent(userId)}/sessions`)
  return data.items ?? []
}

/** POST /api/users/{id}/logout（admin）→ 强制下线（吊销全部 refresh + token 版本递增）。 */
export async function forceLogoutUser(userId: string): Promise<void> {
  const response = await fetchWithAuth(`/api/users/${encodeURIComponent(userId)}/logout`, { method: 'POST' })
  if (!response.ok) {
    throw await parseError(response)
  }
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
  const response = await fetchWithAuth(`/api/stats/events.csv${query ? `?${query}` : ''}`, {
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
  const response = await fetchWithAuth(`/api/duty/groups/${encodeURIComponent(groupId)}`, {
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
  const response = await fetchWithAuth(`/api/users/${encodeURIComponent(userId)}`, {
    method: 'DELETE',
    headers: { Accept: 'application/json', ...authHeaders() },
  })
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized('/api/users')
    throw error
  }
}

// ---------------- P10 审计导出（admin） ----------------

export interface AuditExportParams {
  from?: string
  to?: string
  action?: string
  actor?: string
  limit?: number
}

/** GET /api/audit/export（admin）→ text/csv Blob（表头 id,at,actor,action,resource,detail）。 */
export async function exportAuditCsv(params: AuditExportParams = {}): Promise<Blob> {
  const query = new URLSearchParams()
  if (params.from) query.set('from', params.from)
  if (params.to) query.set('to', params.to)
  if (params.action) query.set('action', params.action)
  if (params.actor) query.set('actor', params.actor)
  if (params.limit) query.set('limit', String(params.limit))
  const suffix = query.toString()
  const response = await fetchWithAuth(`/api/audit/export${suffix ? `?${suffix}` : ''}`, {
    method: 'GET',
    headers: { Accept: 'text/csv', ...authHeaders() },
  })
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized('/api/audit/export')
    throw error
  }
  return response.blob()
}

// ---------------- P31 租户数据导出（admin） ----------------

export async function exportTenant(
  tenantId: string,
  options: { includeTracks?: boolean; includeAudit?: boolean } = {},
): Promise<{ blob: Blob; counts: import('./types').TenantExportCounts }> {
  const query = new URLSearchParams()
  if (options.includeTracks) query.set('include_tracks', '1')
  if (options.includeAudit) query.set('include_audit', '1')
  const suffix = query.toString()
  const response = await fetchWithAuth(`/api/tenants/${encodeURIComponent(tenantId)}/export${suffix ? `?${suffix}` : ''}`, {
    method: 'GET',
    headers: { Accept: 'application/json', ...authHeaders() },
  })
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized(`/api/tenants/${tenantId}/export`)
    throw error
  }
  const blob = await response.blob()
  let counts: import('./types').TenantExportCounts = {
    areas: 0, cameras: 0, camera_rules: 0, settings: 0, users: 0, events: 0, orders: 0,
    notification_log: 0, patrol_routes: 0, patrol_schedules: 0, patrol_executions: 0,
    tracks: 0, audit: 0,
  }
  try {
    const text = await blob.text()
    const parsed = JSON.parse(text)
    if (parsed && parsed.counts) counts = { ...counts, ...parsed.counts }
  } catch {
    // Keep the blob; a malformed payload still downloads for inspection.
  }
  return { blob, counts }
}

// ---------------- P32 异步导出任务（admin） ----------------

export async function createExportJob(
  tenantId: string,
  options: { includeTracks?: boolean; includeAudit?: boolean } = {},
): Promise<import('./types').ExportJob> {
  return postJson<import('./types').ExportJob>(`/api/tenants/${encodeURIComponent(tenantId)}/exports`, {
    include_tracks: Boolean(options.includeTracks),
    include_audit: Boolean(options.includeAudit),
  })
}

export async function fetchExportJobs(tenantId: string): Promise<import('./types').ExportJob[]> {
  const payload = await requestJson<{ items: import('./types').ExportJob[] }>(
    `/api/tenants/${encodeURIComponent(tenantId)}/exports`,
  )
  return payload.items ?? []
}

export async function downloadExportJob(tenantId: string, exportId: string): Promise<Blob> {
  const response = await fetchWithAuth(
    `/api/tenants/${encodeURIComponent(tenantId)}/exports/${encodeURIComponent(exportId)}/download`,
    { method: 'GET', headers: { Accept: 'application/json', ...authHeaders() } },
  )
  if (!response.ok) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized(`/api/tenants/${tenantId}/exports/${exportId}/download`)
    throw error
  }
  return response.blob()
}

export async function deleteExportJob(tenantId: string, exportId: string): Promise<void> {
  const response = await fetchWithAuth(
    `/api/tenants/${encodeURIComponent(tenantId)}/exports/${encodeURIComponent(exportId)}`,
    { method: 'DELETE', headers: { ...authHeaders() } },
  )
  if (!response.ok && response.status !== 204) {
    const error = await parseError(response)
    if (response.status === 401) notifyUnauthorized(`/api/tenants/${tenantId}/exports/${exportId}`)
    throw error
  }
}

// ---------------- P12 巡检路线 ----------------

/** GET /api/patrol/routes（登录用户）→ 巡检路线列表。 */
export async function fetchPatrolRoutes(): Promise<PatrolRoute[]> {
  const data = await requestJson<PatrolRouteListResponse>('/api/patrol/routes')
  return data.items ?? []
}

/** POST /api/patrol/routes（operator/admin）→ 创建巡检路线。 */
export async function createPatrolRoute(payload: PatrolRoutePayload): Promise<PatrolRoute> {
  return postJson<PatrolRoute>('/api/patrol/routes', payload)
}

/** PATCH /api/patrol/routes/{id}（创建者或 admin）→ 修改。 */
export async function updatePatrolRoute(routeId: string, payload: Partial<PatrolRoutePayload>): Promise<PatrolRoute> {
  return patchJson<PatrolRoute>(`/api/patrol/routes/${encodeURIComponent(routeId)}`, payload)
}

/** DELETE /api/patrol/routes/{id}（创建者或 admin）→ 204。 */
export async function deletePatrolRoute(routeId: string): Promise<void> {
  const response = await fetchWithAuth(`/api/patrol/routes/${encodeURIComponent(routeId)}`, { method: 'DELETE' })
  if (!response.ok) throw await parseError(response)
}

// ---------------- P13 巡检计划 / 执行 ----------------

/** GET /api/patrol/schedules（登录用户）。 */
export async function fetchPatrolSchedules(): Promise<PatrolSchedule[]> {
  const data = await requestJson<{ items: PatrolSchedule[] }>('/api/patrol/schedules')
  return data.items ?? []
}

/** POST /api/patrol/schedules（operator/admin）。 */
export async function createPatrolSchedule(payload: PatrolSchedulePayload): Promise<PatrolSchedule> {
  return postJson<PatrolSchedule>('/api/patrol/schedules', payload)
}

/** PATCH /api/patrol/schedules/{id}（创建者/admin）。 */
export async function updatePatrolSchedule(scheduleId: string, payload: Partial<PatrolSchedulePayload>): Promise<PatrolSchedule> {
  return patchJson<PatrolSchedule>(`/api/patrol/schedules/${encodeURIComponent(scheduleId)}`, payload)
}

/** DELETE /api/patrol/schedules/{id}（创建者/admin）。 */
export async function deletePatrolSchedule(scheduleId: string): Promise<void> {
  const response = await fetchWithAuth(`/api/patrol/schedules/${encodeURIComponent(scheduleId)}`, { method: 'DELETE' })
  if (!response.ok) throw await parseError(response)
}

/** POST /api/patrol/schedules/{id}/run（operator/admin）→ 立即生成执行任务。 */
export async function runPatrolSchedule(scheduleId: string): Promise<PatrolExecution> {
  return postJson<PatrolExecution>(`/api/patrol/schedules/${encodeURIComponent(scheduleId)}/run`)
}

/** GET /api/patrol/executions?status=&schedule_id=&limit=（登录用户）。 */
export async function fetchPatrolExecutions(
  params: { status?: PatrolExecutionStatus | ''; scheduleId?: string; eventId?: string; limit?: number } = {},
): Promise<PatrolExecution[]> {
  const query = new URLSearchParams()
  if (params.status) query.set('status', params.status)
  if (params.scheduleId) query.set('schedule_id', params.scheduleId)
  if (params.eventId) query.set('event_id', params.eventId)
  query.set('limit', String(params.limit ?? 100))
  const data = await requestJson<PatrolExecutionListResponse>(`/api/patrol/executions?${query.toString()}`)
  return data.items ?? []
}

/** POST /api/patrol/executions/{id}/claim（operator/admin）→ pending → running。 */
export async function claimPatrolExecution(executionId: string): Promise<PatrolExecution> {
  return postJson<PatrolExecution>(`/api/patrol/executions/${encodeURIComponent(executionId)}/claim`)
}

/** POST /api/patrol/executions/{id}/cancel（operator/admin）→ pending/running → cancelled。 */
export async function cancelPatrolExecution(executionId: string, note?: string): Promise<PatrolExecution> {
  return postJson<PatrolExecution>(
    `/api/patrol/executions/${encodeURIComponent(executionId)}/cancel`,
    note?.trim() ? { note: note.trim() } : {},
  )
}

/** POST /api/patrol/executions/{id}/complete（认领人/admin）→ done/issue；issue 时响应带 order_id。 */
export async function completePatrolExecution(
  executionId: string,
  payload: PatrolExecutionCompletePayload,
): Promise<PatrolExecution> {
  return postJson<PatrolExecution>(`/api/patrol/executions/${encodeURIComponent(executionId)}/complete`, payload)
}

// ---------------- P14 巡检统计 / 导出 / 归档 ----------------

function patrolTimeQuery(params: { from?: string; to?: string; status?: string }): string {
  const query = new URLSearchParams()
  if (params.from) query.set('from', params.from)
  if (params.to) query.set('to', params.to)
  if (params.status) query.set('status', params.status)
  return query.toString()
}

/** GET /api/patrol/stats?from=&to=（登录用户）。 */
export async function fetchPatrolStats(params: { from?: string; to?: string } = {}): Promise<PatrolStats> {
  const query = patrolTimeQuery(params)
  return requestJson<PatrolStats>(`/api/patrol/stats${query ? `?${query}` : ''}`)
}

/** GET /api/patrol/executions/export（operator/admin）→ text/csv Blob。 */
export async function exportPatrolExecutionsCsv(params: { from?: string; to?: string; status?: string } = {}): Promise<Blob> {
  const query = patrolTimeQuery(params)
  const response = await fetchWithAuth(`/api/patrol/executions/export${query ? `?${query}` : ''}`, {
    method: 'GET',
    headers: { Accept: 'text/csv' },
  })
  if (!response.ok) throw await parseError(response)
  return response.blob()
}

/** DELETE /api/patrol/executions?before=&status=（admin）→ 删除已结束历史记录。 */
export async function purgePatrolExecutions(params: { before?: string; status?: string } = {}): Promise<{ deleted: number }> {
  const query = patrolTimeQuery({ from: undefined, to: undefined, status: params.status })
  const suffix = [params.before ? `before=${encodeURIComponent(params.before)}` : '', query].filter(Boolean).join('&')
  const response = await fetchWithAuth(`/api/patrol/executions${suffix ? `?${suffix}` : ''}`, { method: 'DELETE' })
  if (!response.ok) throw await parseError(response)
  return (await response.json()) as { deleted: number }
}

// ---------------- P27 多租户（admin） ----------------

/** GET /api/tenants（admin）→ 租户列表。 */
export async function fetchTenants(): Promise<Tenant[]> {
  const data = await requestJson<TenantListResponse>('/api/tenants')
  return data.items ?? []
}

/** POST /api/tenants（admin）→ 创建租户（id 自动生成）。 */
export async function createTenant(payload: TenantCreatePayload): Promise<Tenant> {
  return postJson<Tenant>('/api/tenants', payload)
}

/** PATCH /api/tenants/{id}（admin）→ 修改名称/启用状态/配额。 */
export async function updateTenant(tenantId: string, payload: TenantUpdatePayload): Promise<Tenant> {
  return patchJson<Tenant>(`/api/tenants/${encodeURIComponent(tenantId)}`, payload)
}

/** POST /api/tenants/{id}/archive（admin）→ 归档（可恢复只读冻结）。 */
export async function archiveTenant(tenantId: string): Promise<Tenant> {
  return postJson<Tenant>(`/api/tenants/${encodeURIComponent(tenantId)}/archive`)
}

/** POST /api/tenants/{id}/restore（admin）→ 恢复归档租户。 */
export async function restoreTenant(tenantId: string): Promise<Tenant> {
  return postJson<Tenant>(`/api/tenants/${encodeURIComponent(tenantId)}/restore`)
}

/** DELETE /api/tenants/{id}?confirm=<id>（admin，仅归档后）→ 清理计数。 */
export async function deleteTenant(tenantId: string): Promise<TenantDeleteResult> {
  const response = await fetchWithAuth(
    `/api/tenants/${encodeURIComponent(tenantId)}?confirm=${encodeURIComponent(tenantId)}`,
    { method: 'DELETE' },
  )
  if (!response.ok) throw await parseError(response)
  return (await response.json()) as TenantDeleteResult
}

/** 供界面展示的 API 地址说明（离线横幅/README 使用）。 */
export function describeApiBase(): string {
  return API_BASE || `${window.location.origin}（Vite 代理 → 127.0.0.1:8080）`
}
