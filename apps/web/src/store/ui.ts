import { reactive } from 'vue'

export type ToastKind = 'info' | 'success' | 'warning' | 'error'

export interface ToastItem {
  id: number
  kind: ToastKind
  message: string
}

const state = reactive<{ toasts: ToastItem[] }>({ toasts: [] })

let sequence = 0
const timers = new Map<number, number>()
/** 同时最多展示的浮层数量，避免告警升级/自动派单突发时刷屏遮挡界面。 */
const MAX_VISIBLE_TOASTS = 5

/** 推一条浮层提示（默认 4.5s 自动消失；超出上限时丢弃最旧的）。 */
export function pushToast(message: string, kind: ToastKind = 'info', durationMs = 4500): number {
  const id = (sequence += 1)
  const next = [...state.toasts, { id, kind, message }]
  while (next.length > MAX_VISIBLE_TOASTS) {
    const dropped = next.shift()
    if (!dropped) break
    const droppedTimer = timers.get(dropped.id)
    if (droppedTimer !== undefined) {
      window.clearTimeout(droppedTimer)
      timers.delete(dropped.id)
    }
  }
  state.toasts = next
  const timer = window.setTimeout(() => dismissToast(id), durationMs)
  timers.set(id, timer)
  return id
}

export function dismissToast(id: number): void {
  const timer = timers.get(id)
  if (timer !== undefined) {
    window.clearTimeout(timer)
    timers.delete(id)
  }
  state.toasts = state.toasts.filter((toast) => toast.id !== id)
}

export function useUiStore() {
  return { state, pushToast, dismissToast }
}
