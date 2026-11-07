import type {
  CalibrationSavePayload,
  CalibrationSolveResponse,
  Camera,
  CameraListResponse,
  ControlPoint,
  EventDetailResponse,
  EventListResponse,
  StatsSummary,
  TrackListResponse,
  TrackPoint,
  TwinEvent,
} from './types'

/**
 * 后端源站。为空字符串时使用同源：开发环境由 Vite 把 /api、/ws 代理到 127.0.0.1:8080。
 * 若显式设置 VITE_API_BASE（如 http://127.0.0.1:8080），前端直接请求该源站。
 */
export const API_BASE = (import.meta.env.VITE_API_BASE ?? '').trim().replace(/\/+$/, '')

/** 可选鉴权令牌（docs/API_P1.md §7）；默认关闭。 */
export const API_TOKEN = (import.meta.env.VITE_API_TOKEN ?? '').trim()

export class ApiError extends Error {
  readonly status: number | undefined

  constructor(message: string, status?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

function authHeaders(): Record<string, string> {
  return API_TOKEN ? { Authorization: `Bearer ${API_TOKEN}` } : {}
}

export function apiUrl(path: string): string {
  return `${API_BASE}${path}`
}

async function parseError(response: Response): Promise<ApiError> {
  let message = `HTTP ${response.status}`
  try {
    const body = (await response.json()) as { error?: string; message?: string }
    message = body.message ?? body.error ?? message
  } catch {
    // 错误响应不是 JSON 时保留 HTTP 状态信息
  }
  return new ApiError(message, response.status)
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
  if (!response.ok) throw await parseError(response)
  return (await response.json()) as T
}

function postJson<T>(path: string, body?: unknown): Promise<T> {
  return requestJson<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

// ---------------- P0 REST ----------------

/** GET /api/cameras → 相机台账。 */
export async function fetchCameras(): Promise<Camera[]> {
  const data = await requestJson<CameraListResponse>('/api/cameras')
  return data.items ?? []
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
  if (!response.ok) throw await parseError(response)
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

/** 供界面展示的 API 地址说明（离线横幅/README 使用）。 */
export function describeApiBase(): string {
  return API_BASE || `${window.location.origin}（Vite 代理 → 127.0.0.1:8080）`
}
