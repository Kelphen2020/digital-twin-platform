<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CAMERA_STATUS_COLORS, CAMERA_STATUS_LABELS, formatDateTime } from '../labels'
import { useTwinStore } from '../store/twin'
import CalibrationDialog from './CalibrationDialog.vue'
import CameraVideo from './CameraVideo.vue'

const { selectedCamera, focusCamera, clearSelection } = useTwinStore()
const showCalibration = ref(false)

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

// 切换相机时关闭标定弹窗，避免作用于错误相机
watch(
  () => selectedCamera.value?.id,
  () => {
    showCalibration.value = false
  },
)
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
          <span>{{ selectedCamera.name }} · {{ selectedCamera.area_id }}</span>
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
            <button class="link-btn" @click="showCalibration = true">管理标定</button>
          </dd>
        </div>
      </dl>
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
</template>
