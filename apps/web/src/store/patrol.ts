import { computed, reactive } from 'vue'
import {
  apiErrorMessage,
  createPatrolRoute,
  deletePatrolRoute,
  fetchPatrolRoutes,
  updatePatrolRoute,
} from '../api'
import type { PatrolRoute, PatrolRoutePayload } from '../types'
import { pushToast } from './ui'

interface PatrolState {
  routes: PatrolRoute[]
  loading: boolean
  error: string | null
  selectedRouteId: string | null
  playing: boolean
  /** 当前巡检航点索引（播放时随时间推进）。 */
  currentIndex: number
  saving: boolean
  deletingId: string | null
}

const state = reactive<PatrolState>({
  routes: [],
  loading: false,
  error: null,
  selectedRouteId: null,
  playing: false,
  currentIndex: 0,
  saving: false,
  deletingId: null,
})

const selectedRoute = computed(
  () => state.routes.find((route) => route.id === state.selectedRouteId) ?? null,
)

let playbackTimer: number | null = null

function clearPlaybackTimer(): void {
  if (playbackTimer !== null) {
    window.clearInterval(playbackTimer)
    playbackTimer = null
  }
}

function startPlaybackTimer(route: PatrolRoute): void {
  clearPlaybackTimer()
  const intervalMs = Math.max(1, Math.min(60, route.interval_seconds)) * 1000
  playbackTimer = window.setInterval(() => {
    const current = selectedRoute.value
    if (!current || current.waypoints.length === 0) return
    state.currentIndex = (state.currentIndex + 1) % current.waypoints.length
  }, intervalMs)
}

async function loadRoutes(): Promise<void> {
  state.loading = true
  state.error = null
  try {
    state.routes = await fetchPatrolRoutes()
  } catch (error) {
    state.error = apiErrorMessage(error)
  } finally {
    state.loading = false
  }
}

async function createRoute(payload: PatrolRoutePayload): Promise<PatrolRoute | null> {
  if (state.saving) return null
  state.saving = true
  try {
    const route = await createPatrolRoute(payload)
    state.routes = [route, ...state.routes]
    pushToast('巡检路线已创建', 'success')
    return route
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.saving = false
  }
}

async function updateRoute(routeId: string, payload: Partial<PatrolRoutePayload>): Promise<PatrolRoute | null> {
  if (state.saving) return null
  state.saving = true
  try {
    const route = await updatePatrolRoute(routeId, payload)
    state.routes = state.routes.map((item) => (item.id === route.id ? route : item))
    pushToast('巡检路线已更新', 'success')
    return route
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.saving = false
  }
}

async function removeRoute(routeId: string): Promise<boolean> {
  if (state.deletingId) return false
  state.deletingId = routeId
  try {
    await deletePatrolRoute(routeId)
    state.routes = state.routes.filter((route) => route.id !== routeId)
    if (state.selectedRouteId === routeId) stopPlayback()
    pushToast('巡检路线已删除', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  } finally {
    state.deletingId = null
  }
}

function selectRoute(routeId: string | null): void {
  if (state.selectedRouteId === routeId) return
  stopPlayback()
  state.selectedRouteId = routeId
}

function playRoute(route: PatrolRoute): void {
  if (route.waypoints.length === 0) return
  state.selectedRouteId = route.id
  state.currentIndex = 0
  state.playing = true
  startPlaybackTimer(route)
}

function pausePlayback(): void {
  state.playing = false
  clearPlaybackTimer()
}

function resumePlayback(): void {
  const route = selectedRoute.value
  if (!route || state.playing) return
  state.playing = true
  startPlaybackTimer(route)
}

function stopPlayback(): void {
  state.playing = false
  state.currentIndex = 0
  state.selectedRouteId = null
  clearPlaybackTimer()
}

function reset(): void {
  clearPlaybackTimer()
  state.routes = []
  state.loading = false
  state.error = null
  state.selectedRouteId = null
  state.playing = false
  state.currentIndex = 0
  state.saving = false
  state.deletingId = null
}

export function usePatrolStore() {
  return {
    state,
    selectedRoute,
    loadRoutes,
    createRoute,
    updateRoute,
    removeRoute,
    selectRoute,
    playRoute,
    pausePlayback,
    resumePlayback,
    stopPlayback,
    reset,
  }
}
