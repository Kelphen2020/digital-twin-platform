import type { Camera, CameraListResponse, EventListResponse, TwinEvent } from './types'

/**
 * 后端源站。为空字符串时使用同源：开发环境由 Vite 把 /api、/ws 代理到 127.0.0.1:8080。
 * 若显式设置 VITE_API_BASE（如 http://127.0.0.1:8080），前端直接请求该源站。
 */
export const API_BASE = (import.meta.env.VITE_API_BASE ?? '').trim().replace(/\/+$/, '')

export class ApiError extends Error {
  readonly status: number | undefined

  constructor(message: string, status?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

function resolveUrl(path: string): string {
  return `${API_BASE}${path}`
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(resolveUrl(path), {
    ...init,
    headers: { Accept: 'application/json', ...init?.headers },
  })
  if (!response.ok) {
    let message = `HTTP ${response.status}`
    try {
      const body = (await response.json()) as { error?: string; message?: string }
      message = body.message ?? body.error ?? message
    } catch {
      // 错误响应不是 JSON 时保留 HTTP 状态信息
    }
    throw new ApiError(message, response.status)
  }
  return (await response.json()) as T
}

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

/** 供界面展示的 API 地址说明（离线横幅/README 使用）。 */
export function describeApiBase(): string {
  return API_BASE || `${window.location.origin}（Vite 代理 → 127.0.0.1:8080）`
}
