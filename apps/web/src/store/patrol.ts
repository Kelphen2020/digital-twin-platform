import { computed, reactive } from 'vue'
import {
  apiErrorMessage,
  claimPatrolExecution,
  completePatrolExecution,
  createPatrolRoute,
  createPatrolSchedule,
  deletePatrolRoute,
  deletePatrolSchedule,
  fetchPatrolExecutions,
  fetchPatrolRoutes,
  fetchPatrolSchedules,
  runPatrolSchedule,
  updatePatrolRoute,
  updatePatrolSchedule,
} from '../api'
import type {
  PatrolExecution,
  PatrolExecutionCompletePayload,
  PatrolExecutionStatus,
  PatrolRoute,
  PatrolRoutePayload,
  PatrolSchedule,
  PatrolSchedulePayload,
} from '../types'
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
  // ---- P13：计划 / 执行 ----
  schedules: PatrolSchedule[]
  schedulesLoading: boolean
  schedulesError: string | null
  scheduleSaving: boolean
  scheduleDeletingId: string | null
  executions: PatrolExecution[]
  executionsLoading: boolean
  executionsError: string | null
  executionFilter: PatrolExecutionStatus | ''
  executionActionId: string | null
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
  schedules: [],
  schedulesLoading: false,
  schedulesError: null,
  scheduleSaving: false,
  scheduleDeletingId: null,
  executions: [],
  executionsLoading: false,
  executionsError: null,
  executionFilter: '',
  executionActionId: null,
})

const selectedRoute = computed(
  () => state.routes.find((route) => route.id === state.selectedRouteId) ?? null,
)
/** 巡检 HUD 按钮的待执行任务角标。 */
const pendingCount = computed(() => state.executions.filter((execution) => execution.status === 'pending').length)

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

// ---------------- P13：计划 ----------------

async function loadSchedules(): Promise<void> {
  state.schedulesLoading = true
  state.schedulesError = null
  try {
    state.schedules = await fetchPatrolSchedules()
  } catch (error) {
    state.schedulesError = apiErrorMessage(error)
  } finally {
    state.schedulesLoading = false
  }
}

async function createSchedule(payload: PatrolSchedulePayload): Promise<PatrolSchedule | null> {
  if (state.scheduleSaving) return null
  state.scheduleSaving = true
  try {
    const schedule = await createPatrolSchedule(payload)
    state.schedules = [schedule, ...state.schedules]
    pushToast('巡检计划已创建', 'success')
    return schedule
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.scheduleSaving = false
  }
}

async function updateSchedule(scheduleId: string, payload: Partial<PatrolSchedulePayload>): Promise<PatrolSchedule | null> {
  if (state.scheduleSaving) return null
  state.scheduleSaving = true
  try {
    const schedule = await updatePatrolSchedule(scheduleId, payload)
    state.schedules = state.schedules.map((item) => (item.id === schedule.id ? schedule : item))
    pushToast('巡检计划已更新', 'success')
    return schedule
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.scheduleSaving = false
  }
}

async function removeSchedule(scheduleId: string): Promise<boolean> {
  if (state.scheduleDeletingId) return false
  state.scheduleDeletingId = scheduleId
  try {
    await deletePatrolSchedule(scheduleId)
    state.schedules = state.schedules.filter((schedule) => schedule.id !== scheduleId)
    pushToast('巡检计划已删除', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  } finally {
    state.scheduleDeletingId = null
  }
}

/** 立即执行：生成执行任务并刷新执行列表。 */
async function runScheduleNow(scheduleId: string): Promise<PatrolExecution | null> {
  if (state.executionActionId) return null
  state.executionActionId = scheduleId
  try {
    const execution = await runPatrolSchedule(scheduleId)
    pushToast('已生成巡检执行任务', 'success')
    await loadExecutions()
    return execution
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.executionActionId = null
  }
}

// ---------------- P13：执行 ----------------

async function loadExecutions(): Promise<void> {
  state.executionsLoading = true
  state.executionsError = null
  try {
    state.executions = await fetchPatrolExecutions({ status: state.executionFilter, limit: 100 })
  } catch (error) {
    state.executionsError = apiErrorMessage(error)
  } finally {
    state.executionsLoading = false
  }
}

function setExecutionFilter(status: PatrolExecutionStatus | ''): void {
  state.executionFilter = status
  void loadExecutions()
}

async function claimExecution(executionId: string): Promise<PatrolExecution | null> {
  if (state.executionActionId) return null
  state.executionActionId = executionId
  try {
    const execution = await claimPatrolExecution(executionId)
    state.executions = state.executions.map((item) => (item.id === execution.id ? execution : item))
    pushToast('已认领巡检任务', 'success')
    return execution
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.executionActionId = null
  }
}

/** 完成巡检：done 完成 / issue 异常（后端自动建工单，返回 order_id）。 */
async function completeExecution(
  executionId: string,
  payload: PatrolExecutionCompletePayload,
): Promise<PatrolExecution | null> {
  if (state.executionActionId) return null
  state.executionActionId = executionId
  try {
    const execution = await completePatrolExecution(executionId, payload)
    state.executions = state.executions.map((item) => (item.id === execution.id ? execution : item))
    if (payload.result === 'issue') {
      pushToast(execution.order_id ? `已创建工单：${execution.order_id}` : '巡检异常已提交，已创建工单', 'warning')
    } else {
      pushToast('巡检任务已完成', 'success')
    }
    await loadExecutions()
    return execution
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.executionActionId = null
  }
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
  state.schedules = []
  state.schedulesLoading = false
  state.schedulesError = null
  state.scheduleSaving = false
  state.scheduleDeletingId = null
  state.executions = []
  state.executionsLoading = false
  state.executionsError = null
  state.executionFilter = ''
  state.executionActionId = null
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
    pendingCount,
    loadSchedules,
    createSchedule,
    updateSchedule,
    removeSchedule,
    runScheduleNow,
    loadExecutions,
    setExecutionFilter,
    claimExecution,
    completeExecution,
    reset,
  }
}
