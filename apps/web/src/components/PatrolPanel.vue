<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { formatDateTime } from '../labels'
import { useAuthStore } from '../store/auth'
import { usePatrolStore } from '../store/patrol'
import { useTwinStore } from '../store/twin'
import type { Camera, PatrolRoute, PatrolRoutePayload } from '../types'

const props = defineProps<{
  boxSelectedCameraIds: string[]
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

const { state, loadRoutes, createRoute, updateRoute, removeRoute, playRoute, pausePlayback, resumePlayback, stopPlayback } =
  usePatrolStore()
const { state: twinState, selectCamera } = useTwinStore()
const { state: authState, role } = useAuthStore()

const canWrite = computed(() => role.value === 'operator' || role.value === 'admin')
const isAdmin = computed(() => role.value === 'admin')
const formOpen = ref(false)
const editingRoute = ref<PatrolRoute | null>(null)
const formError = ref('')
const confirmingId = ref<string | null>(null)
const formCameraIds = ref<string[]>([])
const form = reactive({ name: '', interval_seconds: 3 })

const camerasById = computed(() => new Map(twinState.cameras.map((camera) => [camera.id, camera])))
const selectedCameraIds = computed(() => {
  if (props.boxSelectedCameraIds.length > 0) return props.boxSelectedCameraIds
  return twinState.selectedCameraId ? [twinState.selectedCameraId] : []
})

function canManage(route: PatrolRoute): boolean {
  if (isAdmin.value) return true
  return route.created_by === authState.user?.username
}

function cameraName(cameraId: string): string {
  return camerasById.value.get(cameraId)?.code ?? cameraId
}

function routeCameraNames(route: PatrolRoute): string {
  if (route.camera_ids.length === 0) return '未关联相机'
  return route.camera_ids.map(cameraName).join('、')
}

function waypointsFromCameraIds(cameraIds: string[]): PatrolRoutePayload['waypoints'] {
  return cameraIds
    .map((id) => camerasById.value.get(id))
    .filter((camera): camera is Camera => !!camera)
    .map((camera) => ({ x: camera.x, y: camera.y, z: camera.z, heading: camera.heading }))
}

const previewWaypoints = computed(() => waypointsFromCameraIds(formCameraIds.value))

function resetForm(): void {
  formOpen.value = false
  editingRoute.value = null
  formError.value = ''
  form.name = ''
  form.interval_seconds = 3
  formCameraIds.value = []
}

function openCreate(): void {
  resetForm()
  formOpen.value = true
  useSelection()
}

function openEdit(route: PatrolRoute): void {
  resetForm()
  editingRoute.value = route
  form.name = route.name
  form.interval_seconds = route.interval_seconds
  formCameraIds.value = route.camera_ids.filter((id) => camerasById.value.has(id))
  formOpen.value = true
}

function toggleCamera(cameraId: string): void {
  formCameraIds.value = formCameraIds.value.includes(cameraId)
    ? formCameraIds.value.filter((id) => id !== cameraId)
    : [...formCameraIds.value, cameraId]
}

function useSelection(): void {
  const ids = selectedCameraIds.value.filter((id) => camerasById.value.has(id))
  formCameraIds.value = [...new Set([...formCameraIds.value, ...ids])]
}

function togglePlay(route: PatrolRoute): void {
  if (state.selectedRouteId !== route.id) {
    playRoute(route)
    return
  }
  if (state.playing) pausePlayback()
  else resumePlayback()
}

function requestDelete(route: PatrolRoute): void {
  if (!canManage(route)) return
  confirmingId.value = route.id
}

async function confirmDelete(route: PatrolRoute): Promise<void> {
  confirmingId.value = null
  await removeRoute(route.id)
}

function submit(): void {
  formError.value = ''
  const name = form.name.trim()
  if (!name) {
    formError.value = '路线名称不能为空'
    return
  }
  if (previewWaypoints.value.length < 2 || previewWaypoints.value.length > 50) {
    formError.value = '请选择 2–50 个相机航点（按选择顺序飞行）'
    return
  }
  if (!Number.isFinite(form.interval_seconds) || form.interval_seconds < 1 || form.interval_seconds > 60) {
    formError.value = '航点间隔需为 1–60 秒'
    return
  }
  const payload: PatrolRoutePayload = {
    name,
    waypoints: previewWaypoints.value,
    camera_ids: [...formCameraIds.value],
    interval_seconds: Math.round(form.interval_seconds),
  }
  const action = editingRoute.value
    ? updateRoute(editingRoute.value.id, payload)
    : createRoute(payload)
  void action.then((route) => {
    if (route) resetForm()
  })
}

onMounted(() => {
  void loadRoutes()
})
</script>

<template>
  <section class="patrol-overlay" @click.stop>
    <header class="patrol-head">
      <div class="patrol-title">
        <h3>巡检路线</h3>
        <span class="feed-count">{{ state.routes.length }}</span>
      </div>
      <div class="panel-head-actions">
        <button class="ghost-btn" :disabled="state.loading" @click="loadRoutes">刷新</button>
        <button v-if="canWrite" class="secondary-btn" @click="openCreate">新建路线</button>
        <button class="ghost-btn" @click="emit('close')">关闭</button>
      </div>
    </header>

    <div class="patrol-body">
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
            <button
              v-if="state.selectedRouteId === route.id"
              class="ghost-btn"
              @click="stopPlayback"
            >
              停止
            </button>
            <button class="ghost-btn" @click="selectCamera(route.camera_ids[0] ?? '')" :disabled="route.camera_ids.length === 0">
              定位首站
            </button>
            <template v-if="canManage(route)">
              <button class="ghost-btn" @click="openEdit(route)">编辑</button>
              <button v-if="confirmingId !== route.id" class="ghost-btn" @click="requestDelete(route)">删除</button>
              <template v-else>
                <button class="danger-btn compact-btn" :disabled="state.deletingId !== null" @click="confirmDelete(route)">
                  确认删除
                </button>
                <button class="ghost-btn" @click="confirmingId = null">取消</button>
              </template>
            </template>
          </div>
        </article>
      </template>
    </div>

    <form v-if="formOpen && canWrite" class="patrol-form" @submit.prevent="submit">
      <header class="settings-head">
        <h3>{{ editingRoute ? '编辑巡检路线' : '新建巡检路线' }}</h3>
      </header>

      <div class="settings-grid">
        <label class="settings-field">
          <span>名称 *</span>
          <input v-model="form.name" type="text" placeholder="东门→主干道→停车场" />
        </label>
        <label class="settings-field">
          <span>航点间隔（秒，1–60）</span>
          <input v-model.number="form.interval_seconds" type="number" min="1" max="60" step="1" />
        </label>
      </div>

      <div class="settings-field">
        <div class="shift-editor-head">
          <span>航点（按相机选择顺序；需 2–50 个）</span>
          <button class="tiny-btn" type="button" @click="useSelection">
            使用框选/当前选中相机（{{ selectedCameraIds.length }}）
          </button>
        </div>
        <div class="rules-classes">
          <label v-for="camera in twinState.cameras" :key="camera.id" class="rules-class">
            <input
              type="checkbox"
              :checked="formCameraIds.includes(camera.id)"
              @change="toggleCamera(camera.id)"
            />
            <span>{{ camera.code }} · {{ camera.name }}</span>
          </label>
        </div>
        <p class="form-hint">已选 {{ previewWaypoints.length }} 个航点；相机坐标将作为 ENU 航点（含朝向）。</p>
      </div>

      <p v-if="formError" class="form-error">{{ formError }}</p>

      <div class="duty-form-actions">
        <button class="primary-btn" type="submit" :disabled="state.saving">
          {{ state.saving ? '保存中…' : editingRoute ? '保存修改' : '创建路线' }}
        </button>
        <button class="ghost-btn" type="button" @click="resetForm">取消</button>
      </div>
    </form>
  </section>
</template>
