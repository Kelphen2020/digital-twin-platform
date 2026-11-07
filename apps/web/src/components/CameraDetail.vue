<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { CAMERA_STATUS_COLORS, CAMERA_STATUS_LABELS, formatArea, formatDateTime } from '../labels'
import { useAuthStore } from '../store/auth'
import { useTwinStore } from '../store/twin'
import { pushToast } from '../store/ui'
import type { CameraStatus } from '../types'
import CalibrationDialog from './CalibrationDialog.vue'
import CameraRulesPanel from './CameraRulesPanel.vue'
import CameraVideo from './CameraVideo.vue'

const { selectedCamera, focusCamera, clearSelection, updateCameraInfo } = useTwinStore()
const { state: authState, can, isCameraOutOfScope } = useAuthStore()

const showCalibration = ref(false)
const showRules = ref(false)
const savingCamera = ref(false)

const editForm = reactive({
  status: 'online' as CameraStatus,
  x: 0,
  y: 0,
  z: 0,
  heading: 0,
})

const canCalibrate = computed(() => can('camera:calibration'))
const canEdit = computed(() => can('camera:update') && !!selectedCamera.value)
const outOfScope = computed(() => !!selectedCamera.value && isCameraOutOfScope(selectedCamera.value))
const isViewer = computed(() => authState.user?.role === 'viewer')

const cameraStatusOptions: { value: CameraStatus; label: string }[] = [
  { value: 'online', label: CAMERA_STATUS_LABELS.online },
  { value: 'degraded', label: CAMERA_STATUS_LABELS.degraded },
  { value: 'offline', label: CAMERA_STATUS_LABELS.offline },
]

const coordinateText = computed(() => {
  const camera = selectedCamera.value
  if (!camera) return ''
  return `E ${camera.x.toFixed(1)} · N ${camera.y.toFixed(1)} · U ${camera.z.toFixed(1)} m`
})

const calibrationText = computed(() => {
  const calibration = selectedCamera.value?.calibration
  if (!calibration) return '未标定'
  return `已标定 · RMSE ${calibration.rmse.toFixed(2)} m · ${calibration.points_count} 点`
})

function syncEditForm(): void {
  const camera = selectedCamera.value
  if (!camera) return
  editForm.status = camera.status
  editForm.x = camera.x
  editForm.y = camera.y
  editForm.z = camera.z
  editForm.heading = camera.heading
}

// 相机切换/服务端数据变化（含 camera_status 推送）时同步编辑表单
watch(() => selectedCamera.value, syncEditForm, { deep: true, immediate: true })

// 切换相机时关闭弹窗，避免作用于错误相机
watch(
  () => selectedCamera.value?.id,
  () => {
    showCalibration.value = false
    showRules.value = false
  },
)

async function saveCameraInfo(): Promise<void> {
  const camera = selectedCamera.value
  if (!camera) return
  if (![editForm.x, editForm.y, editForm.z, editForm.heading].every(Number.isFinite)) {
    pushToast('坐标与朝向必须为数字', 'warning')
    return
  }
  savingCamera.value = true
  try {
    await updateCameraInfo(camera.id, {
      status: editForm.status,
      x: editForm.x,
      y: editForm.y,
      z: editForm.z,
      heading: editForm.heading,
    })
    syncEditForm()
  } finally {
    savingCamera.value = false
  }
}
</script>

<template>
  <section class="camera-panel">
    <header class="panel-head">
      <h2>相机详情</h2>
      <div v-if="selectedCamera" class="panel-head-actions">
        <button class="ghost-btn" @click="focusCamera(selectedCamera.id)">定位</button>
        <button class="ghost-btn" @click="clearSelection">取消选择</button>
      </div>
    </header>

    <div v-if="selectedCamera" class="camera-body">
      <CameraVideo :camera="selectedCamera" />

      <div class="camera-title">
        <span
          class="status-dot"
          :style="{ background: CAMERA_STATUS_COLORS[selectedCamera.status], color: CAMERA_STATUS_COLORS[selectedCamera.status] }"
        />
        <div>
          <strong>{{ selectedCamera.code }}</strong>
          <span>{{ selectedCamera.name }} · {{ formatArea(selectedCamera.area_id) }}</span>
        </div>
        <span
          class="status-chip"
          :style="{ color: CAMERA_STATUS_COLORS[selectedCamera.status], borderColor: CAMERA_STATUS_COLORS[selectedCamera.status] }"
        >
          {{ CAMERA_STATUS_LABELS[selectedCamera.status] }}
        </span>
      </div>

      <dl class="detail-grid">
        <div>
          <dt>世界坐标</dt>
          <dd class="mono">{{ coordinateText }}</dd>
        </div>
        <div>
          <dt>方位角</dt>
          <dd class="mono">{{ selectedCamera.heading.toFixed(1) }}°</dd>
        </div>
        <div>
          <dt>有效量程</dt>
          <dd class="mono">{{ selectedCamera.range_m.toFixed(0) }} m</dd>
        </div>
        <div>
          <dt>水平视场</dt>
          <dd class="mono">{{ selectedCamera.fov_h.toFixed(1) }}°</dd>
        </div>
        <div>
          <dt>垂直视场</dt>
          <dd class="mono">{{ selectedCamera.fov_v.toFixed(1) }}°</dd>
        </div>
        <div class="wide">
          <dt>更新时间</dt>
          <dd class="mono">{{ formatDateTime(selectedCamera.updated_at) }}</dd>
        </div>
        <div class="wide">
          <dt>单应标定</dt>
          <dd class="calib-row">
            <span :class="selectedCamera.calibration ? 'ok-text' : 'pending-text'">{{ calibrationText }}</span>
            <button v-if="canCalibrate" class="link-btn" @click="showCalibration = true">管理标定</button>
            <span v-else class="text-faint">仅管理员可标定</span>
          </dd>
        </div>
        <div class="wide">
          <dt>检测规则</dt>
          <dd class="calib-row">
            <span class="text-faint">阈值 / 去重 / 类别 / ROI</span>
            <button class="link-btn" @click="showRules = true">
              {{ can('camera:rules') ? '规则配置' : '查看规则' }}
            </button>
          </dd>
        </div>
      </dl>

      <p v-if="isViewer" class="role-notice">观察员只读：设备状态/坐标与检测规则不可编辑。</p>

      <details v-if="canEdit" class="camera-edit">
        <summary>设备编辑（状态 / 坐标 / 朝向）</summary>
        <p v-if="outOfScope" class="form-warn">
          该相机不在您的负责区域，保存会被后端拒绝（403 out_of_scope）。
        </p>
        <div class="camera-edit-grid">
          <label>
            <span>状态</span>
            <select v-model="editForm.status">
              <option v-for="option in cameraStatusOptions" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </label>
          <label>
            <span>朝向 °</span>
            <input v-model.number="editForm.heading" type="number" step="1" />
          </label>
          <label>
            <span>E (m)</span>
            <input v-model.number="editForm.x" type="number" step="0.1" />
          </label>
          <label>
            <span>N (m)</span>
            <input v-model.number="editForm.y" type="number" step="0.1" />
          </label>
          <label>
            <span>U (m)</span>
            <input v-model.number="editForm.z" type="number" step="0.1" />
          </label>
        </div>
        <button class="primary-btn" :disabled="savingCamera" @click="saveCameraInfo">
          {{ savingCamera ? '保存中…' : '保存设备信息' }}
        </button>
      </details>
    </div>

    <div v-else class="camera-empty">
      <p>未选择相机</p>
      <span>在三维场景中点击相机，或点击右侧事件定位到所属相机。</span>
    </div>
  </section>

  <CalibrationDialog
    v-if="selectedCamera && showCalibration"
    :camera="selectedCamera"
    @close="showCalibration = false"
    @saved="showCalibration = false"
  />
  <CameraRulesPanel
    v-if="selectedCamera && showRules"
    :camera="selectedCamera"
    @close="showRules = false"
  />
</template>
