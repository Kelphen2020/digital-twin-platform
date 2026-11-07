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

/** 推一条浮层提示（默认 4.5s 自动消失）。 */
export function pushToast(message: string, kind: ToastKind = 'info', durationMs = 4500): number {
  const id = (sequence += 1)
  state.toasts = [...state.toasts, { id, kind, message }]
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
