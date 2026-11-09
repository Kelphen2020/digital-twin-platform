<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { formatDateTime } from '../labels'
import { useAuthStore } from '../store/auth'
import { usePatrolStore } from '../store/patrol'
import { useTwinStore } from '../store/twin'
import { pushToast } from '../store/ui'
import type {
  Camera,
  PatrolExecution,
  PatrolExecutionStatus,
  PatrolRoute,
  PatrolRoutePayload,
  PatrolSchedule,
} from '../types'

const props = defineProps<{
  boxSelectedCameraIds: string[]
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

const {
  state,
  loadRoutes,
  createRoute,
  updateRoute,
  removeRoute,
  playRoute,
  pausePlayback,
  resumePlayback,
  stopPlayback,
  loadSchedules,
  createSchedule,
  updateSchedule,
  removeSchedule,
  runScheduleNow,
  loadExecutions,
  setExecutionFilter,
  claimExecution,
  completeExecution,
  loadStats,
  exportExecutions,
  purgeHistory,
} = usePatrolStore()
const { state: twinState, selectCamera } = useTwinStore()
const { state: authState, role } = useAuthStore()

type PatrolTab = 'routes' | 'schedules' | 'executions'
type CompleteResult = 'done' | 'issue'

const EXECUTION_STATUS_LABELS: Record<PatrolExecutionStatus, string> = {
  pending: '待执行',
  running: '进行中',
  done: '已完成',
  issue: '异常',
}
const EXECUTION_STATUS_OPTIONS: { value: PatrolExecutionStatus | ''; label: string }[] = [
  { value: '', label: '全部状态' },
  { value: 'pending', label: '待执行' },
  { value: 'running', label: '进行中' },
  { value: 'done', label: '已完成' },
  { value: 'issue', label: '异常' },
]

const tab = ref<PatrolTab>('routes')
const canWrite = computed(() => role.value === 'operator' || role.value === 'admin')
const isAdmin = computed(() => role.value === 'admin')
const camerasById = computed(() => new Map(twinState.cameras.map((camera) => [camera.id, camera])))
const selectedCameraIds = computed(() => {
  if (props.boxSelectedCameraIds.length > 0) return props.boxSelectedCameraIds
  return twinState.selectedCameraId ? [twinState.selectedCameraId] : []
})
const pendingCount = computed(() => state.executions.filter((execution) => execution.status === 'pending').length)

// ---- 路线（P12） ----
const routeFormOpen = ref(false)
const editingRoute = ref<PatrolRoute | null>(null)
const routeFormError = ref('')
const confirmingId = ref<string | null>(null)
const routeFormCameraIds = ref<string[]>([])
const routeForm = reactive({ name: '', interval_seconds: 3 })

// ---- 计划（P13） ----
const scheduleFormOpen = ref(false)
const editingSchedule = ref<PatrolSchedule | null>(null)
const scheduleError = ref('')
const confirmingScheduleId = ref<string | null>(null)
const scheduleForm = reactive({ name: '', route_id: '', interval_minutes: 60, enabled: true })

// ---- 执行（P13） ----
const completeDialog = ref<PatrolExecution | null>(null)
const completeNote = ref('')
const completeResult = ref<CompleteResult>('done')
const completeError = ref('')

// ---- P14：统计 / 导出 / 清理 ----
const statsFromDate = ref('')
const statsToDate = ref('')
const purgeBeforeDate = ref(new Date().toISOString().slice(0, 10))
const purgeConfirming = ref(false)

const statsCards = computed(() => {
  const stats = state.stats
  return [
    { key: 'total', label: '总数', value: stats?.executions_total ?? '—' },
    { key: 'pending', label: '待执行', value: stats?.pending ?? '—', tone: 'pending' },
    { key: 'running', label: '进行中', value: stats?.running ?? '—', tone: 'running' },
    { key: 'done', label: '已完成', value: stats?.done ?? '—', tone: 'done' },
    { key: 'issue', label: '异常', value: stats?.issue ?? '—', tone: 'issue' },
    { key: 'completion', label: '完成率', value: stats ? `${(stats.completion_rate * 100).toFixed(1)}%` : '—' },
    { key: 'issueRate', label: '异常率', value: stats ? `${(stats.issue_rate * 100).toFixed(1)}%` : '—' },
    { key: 'duration', label: '平均时长', value: stats ? formatDuration(stats.avg_duration_seconds) : '—' },
  ]
})

function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—'
  if (seconds < 60) return `${Math.round(seconds)} 秒`
  const minutes = Math.floor(seconds / 60)
  const remainder = Math.round(seconds % 60)
  return remainder > 0 ? `${minutes} 分 ${remainder} 秒` : `${minutes} 分钟`
}

function dateInputToIso(dateValue: string, endOfDay = false): string {
  if (!dateValue) return ''
  const time = endOfDay ? 'T23:59:59' : 'T00:00:00'
  const parsed = new Date(`${dateValue}${time}`)
  return Number.isNaN(parsed.getTime()) ? '' : parsed.toISOString()
}

function refreshStats(): void {
  void loadStats(dateInputToIso(statsFromDate.value), dateInputToIso(statsToDate.value, true))
}

async function downloadExecutionsCsv(): Promise<void> {
  const blob = await exportExecutions()
  if (!blob) return
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'patrol-executions.csv'
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
  pushToast('巡检执行 CSV 已导出', 'success')
}

function requestPurge(): void {
  if (!isAdmin.value) return
  purgeConfirming.value = true
}

async function confirmPurge(): Promise<void> {
  const before = dateInputToIso(purgeBeforeDate.value, true)
  purgeConfirming.value = false
  if (!before) return
  await purgeHistory(before)
}


function canManageRoute(route: PatrolRoute): boolean {
  return isAdmin.value || route.created_by === authState.user?.username
}

function canManageSchedule(schedule: PatrolSchedule): boolean {
  return isAdmin.value || schedule.created_by === authState.user?.username
}

function cameraName(cameraId: string): string {
  return camerasById.value.get(cameraId)?.code ?? cameraId
}

function routeCameraNames(route: PatrolRoute): string {
  if (route.camera_ids.length === 0) return '未关联相机'
  return route.camera_ids.map(cameraName).join('、')
}

function routeNameById(routeId: string): string {
  return state.routes.find((route) => route.id === routeId)?.name ?? routeId
}

function executionStatusClass(status: PatrolExecutionStatus): string {
  return `exec-${status}`
}

function canComplete(execution: PatrolExecution): boolean {
  if (execution.status !== 'running') return false
  if (isAdmin.value) return true
  return execution.assignee === authState.user?.username
}

function waypointsFromCameraIds(cameraIds: string[]): PatrolRoutePayload['waypoints'] {
  return cameraIds
    .map((id) => camerasById.value.get(id))
    .filter((camera): camera is Camera => !!camera)
    .map((camera) => ({ x: camera.x, y: camera.y, z: camera.z, heading: camera.heading }))
}

const previewWaypoints = computed(() => waypointsFromCameraIds(routeFormCameraIds.value))

// ---- 路线表单 ----
function resetRouteForm(): void {
  routeFormOpen.value = false
  editingRoute.value = null
  routeFormError.value = ''
  routeForm.name = ''
  routeForm.interval_seconds = 3
  routeFormCameraIds.value = []
}

function openCreateRoute(): void {
  resetRouteForm()
  routeFormOpen.value = true
  useSelection()
}

function openEditRoute(route: PatrolRoute): void {
  resetRouteForm()
  editingRoute.value = route
  routeForm.name = route.name
  routeForm.interval_seconds = route.interval_seconds
  routeFormCameraIds.value = route.camera_ids.filter((id) => camerasById.value.has(id))
  routeFormOpen.value = true
}

function toggleCamera(cameraId: string): void {
  routeFormCameraIds.value = routeFormCameraIds.value.includes(cameraId)
    ? routeFormCameraIds.value.filter((id) => id !== cameraId)
    : [...routeFormCameraIds.value, cameraId]
}

function useSelection(): void {
  const ids = selectedCameraIds.value.filter((id) => camerasById.value.has(id))
  routeFormCameraIds.value = [...new Set([...routeFormCameraIds.value, ...ids])]
}

function togglePlay(route: PatrolRoute): void {
  if (state.selectedRouteId !== route.id) {
    playRoute(route)
    return
  }
  if (state.playing) pausePlayback()
  else resumePlayback()
}

function requestDeleteRoute(route: PatrolRoute): void {
  if (!canManageRoute(route)) return
  confirmingId.value = route.id
}

async function confirmDeleteRoute(route: PatrolRoute): Promise<void> {
  confirmingId.value = null
  await removeRoute(route.id)
}

function submitRoute(): void {
  routeFormError.value = ''
  const name = routeForm.name.trim()
  if (!name) {
    routeFormError.value = '路线名称不能为空'
    return
  }
  if (previewWaypoints.value.length < 2 || previewWaypoints.value.length > 50) {
    routeFormError.value = '请选择 2–50 个相机航点（按选择顺序飞行）'
    return
  }
  if (!Number.isFinite(routeForm.interval_seconds) || routeForm.interval_seconds < 1 || routeForm.interval_seconds > 60) {
    routeFormError.value = '航点间隔需为 1–60 秒'
    return
  }
  const payload: PatrolRoutePayload = {
    name,
    waypoints: previewWaypoints.value,
    camera_ids: [...routeFormCameraIds.value],
    interval_seconds: Math.round(routeForm.interval_seconds),
  }
  const action = editingRoute.value ? updateRoute(editingRoute.value.id, payload) : createRoute(payload)
  void action.then((route) => {
    if (route) resetRouteForm()
  })
}

// ---- 计划表单 ----
function resetScheduleForm(): void {
  scheduleFormOpen.value = false
  editingSchedule.value = null
  scheduleError.value = ''
  scheduleForm.name = ''
  scheduleForm.route_id = state.routes[0]?.id ?? ''
  scheduleForm.interval_minutes = 60
  scheduleForm.enabled = true
}

function openCreateSchedule(): void {
  resetScheduleForm()
  scheduleFormOpen.value = true
}

function openEditSchedule(schedule: PatrolSchedule): void {
  resetScheduleForm()
  editingSchedule.value = schedule
  scheduleForm.name = schedule.name
  scheduleForm.route_id = schedule.route_id
  scheduleForm.interval_minutes = schedule.interval_minutes
  scheduleForm.enabled = schedule.enabled
  scheduleFormOpen.value = true
}

function submitSchedule(): void {
  scheduleError.value = ''
  const name = scheduleForm.name.trim()
  if (!name) {
    scheduleError.value = '计划名称不能为空'
    return
  }
  if (!scheduleForm.route_id) {
    scheduleError.value = '请选择巡检路线'
    return
  }
  if (
    !Number.isFinite(scheduleForm.interval_minutes) ||
    scheduleForm.interval_minutes < 1 ||
    scheduleForm.interval_minutes > 10080
  ) {
    scheduleError.value = '执行间隔需为 1–10080 分钟'
    return
  }
  const payload = {
    name,
    route_id: scheduleForm.route_id,
    interval_minutes: Math.round(scheduleForm.interval_minutes),
    enabled: scheduleForm.enabled,
  }
  const action = editingSchedule.value
    ? updateSchedule(editingSchedule.value.id, payload)
    : createSchedule(payload)
  void action.then((schedule) => {
    if (schedule) resetScheduleForm()
  })
}

function requestDeleteSchedule(schedule: PatrolSchedule): void {
  if (!canManageSchedule(schedule)) return
  confirmingScheduleId.value = schedule.id
}

async function confirmDeleteSchedule(schedule: PatrolSchedule): Promise<void> {
  confirmingScheduleId.value = null
  await removeSchedule(schedule.id)
}

async function runNow(schedule: PatrolSchedule): Promise<void> {
  await runScheduleNow(schedule.id)
}

// ---- 执行 ----
function openComplete(execution: PatrolExecution): void {
  if (!canComplete(execution)) return
  completeDialog.value = execution
  completeNote.value = ''
  completeResult.value = 'done'
  completeError.value = ''
}

function closeComplete(): void {
  completeDialog.value = null
  completeNote.value = ''
  completeError.value = ''
}

async function submitComplete(): Promise<void> {
  const execution = completeDialog.value
  if (!execution) return
  completeError.value = ''
  if (completeResult.value === 'issue' && !completeNote.value.trim()) {
    completeError.value = '异常结果需填写备注'
    return
  }
  const result = await completeExecution(execution.id, {
    result: completeResult.value,
    note: completeNote.value.trim(),
  })
  if (result) closeComplete()
}

async function refreshAll(): Promise<void> {
  await Promise.all([loadRoutes(), loadSchedules(), loadExecutions()])
}

watch(tab, (next) => {
  if (next === 'schedules') void loadSchedules()
  if (next === 'executions') {
    void loadExecutions()
    void loadStats()
  }
})

onMounted(() => {
  void loadRoutes()
  void loadSchedules()
  void loadExecutions()
  void loadStats()
})
</script>

<template>
  <section class="patrol-overlay" @click.stop>
    <header class="patrol-head">
      <div class="patrol-title">
        <h3>巡检</h3>
        <span class="feed-count">{{ state.routes.length }}</span>
      </div>
      <div class="panel-head-actions">
        <button class="ghost-btn" :disabled="state.loading || state.schedulesLoading || state.executionsLoading" @click="refreshAll">刷新</button>
        <button v-if="tab === 'routes' && canWrite" class="secondary-btn" @click="openCreateRoute">新建路线</button>
        <button v-if="tab === 'schedules' && canWrite" class="secondary-btn" @click="openCreateSchedule">新建计划</button>
        <button class="ghost-btn" @click="emit('close')">关闭</button>
      </div>
    </header>

    <div class="patrol-tabs">
      <button class="patrol-tab" :class="{ active: tab === 'routes' }" @click="tab = 'routes'">
        路线 <span class="patrol-count">{{ state.routes.length }}</span>
      </button>
      <button class="patrol-tab" :class="{ active: tab === 'schedules' }" @click="tab = 'schedules'">
        计划 <span class="patrol-count">{{ state.schedules.length }}</span>
      </button>
      <button class="patrol-tab" :class="{ active: tab === 'executions' }" @click="tab = 'executions'">
        执行 <span class="patrol-count">{{ pendingCount > 0 ? `待 ${pendingCount}` : state.executions.length }}</span>
      </button>
    </div>

    <div class="patrol-body">
      <!-- 路线 -->
      <template v-if="tab === 'routes'">
        <p v-if="state.loading && state.routes.length === 0" class="detail-loading">正在加载巡检路线…</p>
        <p v-else-if="state.error" class="form-error">
          巡检路线加载失败：{{ state.error }}
          <button class="link-btn" @click="loadRoutes">重试</button>
        </p>
        <div v-else-if="state.routes.length === 0" class="feed-empty">
          <p>暂无巡检路线</p>
          <span>{{ canWrite ? '点击「新建路线」，从框选/当前选中的相机生成航点。' : '管理员或值班员创建后即可在此查看并播放。' }}</span>
        </div>
        <template v-else>
          <article
            v-for="route in state.routes"
            :key="route.id"
            class="patrol-route"
            :class="{ 'patrol-route-active': state.selectedRouteId === route.id }"
          >
            <div class="patrol-route-head">
              <span class="patrol-route-name">{{ route.name }}</span>
              <span class="patrol-chip">{{ route.waypoints.length }} 航点</span>
              <span class="patrol-chip">{{ route.interval_seconds }}s</span>
            </div>
            <p class="patrol-meta">相机：{{ routeCameraNames(route) }}</p>
            <p class="patrol-meta">
              创建者：{{ route.created_by }} · {{ formatDateTime(route.updated_at || route.created_at) }}
              <template v-if="state.selectedRouteId === route.id && state.playing">
                · 播放中 航点 {{ state.currentIndex + 1 }}/{{ route.waypoints.length }}
              </template>
            </p>
            <div class="patrol-actions">
              <button class="ghost-btn" @click="togglePlay(route)">
                {{ state.selectedRouteId === route.id && state.playing ? '暂停' : state.selectedRouteId === route.id ? '继续' : '播放' }}
              </button>
              <button v-if="state.selectedRouteId === route.id" class="ghost-btn" @click="stopPlayback">停止</button>
              <button class="ghost-btn" :disabled="route.camera_ids.length === 0" @click="selectCamera(route.camera_ids[0] ?? '')">定位首站</button>
              <template v-if="canManageRoute(route)">
                <button class="ghost-btn" @click="openEditRoute(route)">编辑</button>
                <button v-if="confirmingId !== route.id" class="ghost-btn" @click="requestDeleteRoute(route)">删除</button>
                <template v-else>
                  <button class="danger-btn compact-btn" :disabled="state.deletingId !== null" @click="confirmDeleteRoute(route)">确认删除</button>
                  <button class="ghost-btn" @click="confirmingId = null">取消</button>
                </template>
              </template>
            </div>
          </article>
        </template>
      </template>

      <!-- 计划 -->
      <template v-else-if="tab === 'schedules'">
        <p v-if="state.schedulesLoading && state.schedules.length === 0" class="detail-loading">正在加载巡检计划…</p>
        <p v-else-if="state.schedulesError" class="form-error">
          巡检计划加载失败：{{ state.schedulesError }}
          <button class="link-btn" @click="loadSchedules">重试</button>
        </p>
        <div v-else-if="state.schedules.length === 0" class="feed-empty">
          <p>暂无巡检计划</p>
          <span>{{ canWrite ? '点击「新建计划」，把巡检路线排成周期性任务。' : '管理员或值班员创建计划后即可在此查看。' }}</span>
        </div>
        <template v-else>
          <article v-for="schedule in state.schedules" :key="schedule.id" class="patrol-route" :data-schedule-id="schedule.id">
            <div class="patrol-route-head">
              <span class="patrol-route-name">{{ schedule.name }}</span>
              <span class="exec-status" :class="schedule.enabled ? 'exec-running' : 'exec-done'">
                {{ schedule.enabled ? '启用' : '停用' }}
              </span>
              <span class="patrol-chip">{{ schedule.interval_minutes }} 分钟</span>
            </div>
            <p class="patrol-meta">路线：{{ routeNameById(schedule.route_id) }}</p>
            <p class="patrol-meta">下次执行：{{ formatDateTime(schedule.next_run_at) }} · 创建者：{{ schedule.created_by }}</p>
            <div class="patrol-actions">
              <button
                v-if="canWrite"
                class="ghost-btn"
                :disabled="state.executionActionId !== null"
                @click="runNow(schedule)"
              >
                立即执行
              </button>
              <template v-if="canManageSchedule(schedule)">
                <button class="ghost-btn" @click="openEditSchedule(schedule)">编辑</button>
                <button v-if="confirmingScheduleId !== schedule.id" class="ghost-btn" @click="requestDeleteSchedule(schedule)">删除</button>
                <template v-else>
                  <button class="danger-btn compact-btn" :disabled="state.scheduleDeletingId !== null" @click="confirmDeleteSchedule(schedule)">确认删除</button>
                  <button class="ghost-btn" @click="confirmingScheduleId = null">取消</button>
                </template>
              </template>
            </div>
          </article>
        </template>
      </template>

      <!-- 执行 -->
      <template v-else>
        <div class="patrol-stats-grid" :data-stats-total="state.stats?.executions_total ?? ''">
          <div v-for="card in statsCards" :key="card.key" class="patrol-stat-card" :class="card.tone ? `stat-${card.tone}` : ''">
            <span class="patrol-stat-label">{{ card.label }}</span>
            <strong class="patrol-stat-value">{{ card.value }}</strong>
          </div>
        </div>
        <p v-if="state.statsError" class="form-error">统计加载失败：{{ state.statsError }}</p>

        <div class="patrol-stats-controls">
          <label class="patrol-date-field">
            <span>起</span>
            <input v-model="statsFromDate" type="date" />
          </label>
          <label class="patrol-date-field">
            <span>止</span>
            <input v-model="statsToDate" type="date" />
          </label>
          <button class="ghost-btn" :disabled="state.statsLoading" @click="refreshStats">刷新统计</button>
          <button v-if="canWrite" class="secondary-btn" :disabled="state.exporting" @click="downloadExecutionsCsv">
            {{ state.exporting ? '导出中…' : '导出 CSV' }}
          </button>
        </div>

        <div class="patrol-summaries">
          <div class="patrol-summary">
            <h4>按路线</h4>
            <div class="patrol-summary-row patrol-summary-head">
              <span>路线</span><span>总数</span><span>完成</span><span>异常</span>
            </div>
            <div v-for="row in state.stats?.by_route ?? []" :key="row.route_id" class="patrol-summary-row">
              <span class="patrol-summary-name" :title="row.route_name">{{ row.route_name }}</span>
              <span>{{ row.total }}</span><span>{{ row.done }}</span><span>{{ row.issue }}</span>
            </div>
            <p v-if="(state.stats?.by_route ?? []).length === 0" class="patrol-meta">暂无数据</p>
          </div>
          <div class="patrol-summary">
            <h4>按人</h4>
            <div class="patrol-summary-row patrol-summary-head">
              <span>人员</span><span>总数</span><span>完成</span><span>异常</span>
            </div>
            <div v-for="row in state.stats?.by_assignee ?? []" :key="row.assignee || 'unassigned'" class="patrol-summary-row">
              <span class="patrol-summary-name" :title="row.assignee">{{ row.assignee || '未认领' }}</span>
              <span>{{ row.total }}</span><span>{{ row.done }}</span><span>{{ row.issue }}</span>
            </div>
            <p v-if="(state.stats?.by_assignee ?? []).length === 0" class="patrol-meta">暂无数据</p>
          </div>
        </div>

        <div v-if="isAdmin" class="patrol-purge">
          <span class="patrol-purge-title">清理历史</span>
          <label class="patrol-date-field">
            <span>早于</span>
            <input v-model="purgeBeforeDate" type="date" />
          </label>
          <button v-if="!purgeConfirming" class="ghost-btn" :disabled="state.purging" @click="requestPurge">清理历史</button>
          <template v-else>
            <button class="danger-btn compact-btn" :disabled="state.purging" @click="confirmPurge">
              {{ state.purging ? '清理中…' : '确认清理' }}
            </button>
            <button class="ghost-btn" @click="purgeConfirming = false">取消</button>
          </template>
          <span class="patrol-meta">仅删除已结束（done/issue）且创建时间早于所选日期的记录</span>
        </div>

        <div class="patrol-filter">
          <span>状态筛选</span>
          <select
            :value="state.executionFilter"
            @change="setExecutionFilter(($event.target as HTMLSelectElement).value as PatrolExecutionStatus | '')"
          >
            <option v-for="option in EXECUTION_STATUS_OPTIONS" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
          <span class="patrol-meta">共 {{ state.executions.length }} 条 · 待执行 {{ pendingCount }}</span>
        </div>
        <p v-if="state.executionsLoading && state.executions.length === 0" class="detail-loading">正在加载执行任务…</p>
        <p v-else-if="state.executionsError" class="form-error">
          执行任务加载失败：{{ state.executionsError }}
          <button class="link-btn" @click="loadExecutions">重试</button>
        </p>
        <div v-else-if="state.executions.length === 0" class="feed-empty">
          <p>暂无执行任务</p>
          <span>计划到达执行时间或点击「立即执行」后会生成任务。</span>
        </div>
        <template v-else>
          <article v-for="execution in state.executions" :key="execution.id" class="patrol-route execution-card" :data-execution-id="execution.id" :data-status="execution.status">
            <div class="patrol-route-head">
              <span class="patrol-route-name">{{ execution.route_name }}</span>
              <span class="exec-status" :class="executionStatusClass(execution.status)">{{ EXECUTION_STATUS_LABELS[execution.status] }}</span>
            </div>
            <p class="patrol-meta">
              任务 {{ execution.id }} · {{ execution.schedule_id ? `计划 ${execution.schedule_id}` : '立即执行' }} · 计划于 {{ formatDateTime(execution.scheduled_at) }}
            </p>
            <p class="patrol-meta">
              认领人：{{ execution.assignee || '—' }}
              <template v-if="execution.started_at"> · 开始 {{ formatDateTime(execution.started_at) }}</template>
              <template v-if="execution.finished_at"> · 完成 {{ formatDateTime(execution.finished_at) }}</template>
            </p>
            <p v-if="execution.note" class="patrol-meta">备注：{{ execution.note }}</p>
            <p v-if="execution.order_id" class="patrol-meta">关联工单：<span class="mono">{{ execution.order_id }}</span></p>
            <div v-if="canWrite" class="patrol-actions">
              <button
                v-if="execution.status === 'pending'"
                class="ghost-btn"
                :disabled="state.executionActionId !== null"
                @click="claimExecution(execution.id)"
              >
                认领
              </button>
              <button
                v-if="canComplete(execution)"
                class="secondary-btn"
                :disabled="state.executionActionId !== null"
                @click="openComplete(execution)"
              >
                完成巡检
              </button>
            </div>
          </article>
        </template>
      </template>
    </div>

    <!-- 路线表单 -->
    <form v-if="routeFormOpen && canWrite" class="patrol-form" @submit.prevent="submitRoute">
      <header class="settings-head"><h3>{{ editingRoute ? '编辑巡检路线' : '新建巡检路线' }}</h3></header>
      <div class="settings-grid">
        <label class="settings-field">
          <span>名称 *</span>
          <input v-model="routeForm.name" type="text" placeholder="东门→主干道→停车场" />
        </label>
        <label class="settings-field">
          <span>航点间隔（秒，1–60）</span>
          <input v-model.number="routeForm.interval_seconds" type="number" min="1" max="60" step="1" />
        </label>
      </div>
      <div class="settings-field">
        <div class="shift-editor-head">
          <span>航点（按相机选择顺序；需 2–50 个）</span>
          <button class="tiny-btn" type="button" @click="useSelection">使用框选/当前选中相机（{{ selectedCameraIds.length }}）</button>
        </div>
        <div class="rules-classes">
          <label v-for="camera in twinState.cameras" :key="camera.id" class="rules-class">
            <input type="checkbox" :checked="routeFormCameraIds.includes(camera.id)" @change="toggleCamera(camera.id)" />
            <span>{{ camera.code }} · {{ camera.name }}</span>
          </label>
        </div>
        <p class="form-hint">已选 {{ previewWaypoints.length }} 个航点；相机坐标将作为 ENU 航点（含朝向）。</p>
      </div>
      <p v-if="routeFormError" class="form-error">{{ routeFormError }}</p>
      <div class="duty-form-actions">
        <button class="primary-btn" type="submit" :disabled="state.saving">{{ state.saving ? '保存中…' : editingRoute ? '保存修改' : '创建路线' }}</button>
        <button class="ghost-btn" type="button" @click="resetRouteForm">取消</button>
      </div>
    </form>

    <!-- 计划表单 -->
    <form v-if="scheduleFormOpen && canWrite" class="patrol-form" @submit.prevent="submitSchedule">
      <header class="settings-head"><h3>{{ editingSchedule ? '编辑巡检计划' : '新建巡检计划' }}</h3></header>
      <div class="settings-grid">
        <label class="settings-field">
          <span>名称 *</span>
          <input v-model="scheduleForm.name" type="text" placeholder="东门夜巡" />
        </label>
        <label class="settings-field">
          <span>执行间隔（分钟，1–10080）</span>
          <input v-model.number="scheduleForm.interval_minutes" type="number" min="1" max="10080" step="1" />
        </label>
      </div>
      <label class="settings-field">
        <span>巡检路线 *</span>
        <select v-model="scheduleForm.route_id">
          <option v-for="route in state.routes" :key="route.id" :value="route.id">{{ route.name }}（{{ route.waypoints.length }} 航点）</option>
        </select>
      </label>
      <label class="switch">
        <input v-model="scheduleForm.enabled" type="checkbox" />
        <span>启用（到达 next_run_at 时自动生成执行任务）</span>
      </label>
      <p v-if="scheduleError" class="form-error">{{ scheduleError }}</p>
      <div class="duty-form-actions">
        <button class="primary-btn" type="submit" :disabled="state.scheduleSaving">{{ state.scheduleSaving ? '保存中…' : editingSchedule ? '保存修改' : '创建计划' }}</button>
        <button class="ghost-btn" type="button" @click="resetScheduleForm">取消</button>
      </div>
    </form>

    <!-- 完成巡检表单 -->
    <form v-if="completeDialog" class="patrol-form" @submit.prevent="submitComplete">
      <header class="settings-head"><h3>完成巡检 · {{ completeDialog.route_name }}</h3></header>
      <p class="patrol-meta">任务 {{ completeDialog.id }} · 认领人 {{ completeDialog.assignee || '—' }}</p>
      <label class="settings-field">
        <span>巡检结果</span>
        <select v-model="completeResult">
          <option value="done">正常完成（done）</option>
          <option value="issue">发现异常（issue，自动创建工单）</option>
        </select>
      </label>
      <label class="settings-field">
        <span>备注{{ completeResult === 'issue' ? ' *' : '' }}</span>
        <textarea v-model="completeNote" rows="2" placeholder="巡检情况说明" />
      </label>
      <p v-if="completeError" class="form-error">{{ completeError }}</p>
      <div class="duty-form-actions">
        <button class="primary-btn" type="submit" :disabled="state.executionActionId !== null">
          {{ state.executionActionId !== null ? '提交中…' : completeResult === 'issue' ? '提交异常并建单' : '提交完成' }}
        </button>
        <button class="ghost-btn" type="button" @click="closeComplete">取消</button>
      </div>
    </form>
  </section>
</template>
