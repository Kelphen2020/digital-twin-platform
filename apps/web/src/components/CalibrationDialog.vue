<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatDateTime } from '../labels'
import { useTwinStore } from '../store/twin'
import type { CalibrationSolveResponse, Camera, ControlPoint } from '../types'

const props = defineProps<{ camera: Camera }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'saved'): void }>()

const { solveCameraCalibration, saveCameraCalibration, removeCameraCalibration } = useTwinStore()

const EXAMPLE_POINTS = [
  { px: 640, py: 540, x: -85, y: -55 },
  { px: 1200, py: 540, x: -70, y: -55 },
  { px: 640, py: 780, x: -85, y: -40 },
  { px: 1200, py: 780, x: -70, y: -40 },
]

const pointsJson = ref(JSON.stringify(EXAMPLE_POINTS, null, 2))
const parseError = ref('')
const solving = ref(false)
const solveError = ref('')
const solveResult = ref<CalibrationSolveResponse | null>(null)
const saving = ref(false)
const saveError = ref('')
const savedMessage = ref('')
const deleting = ref(false)
const deleteError = ref('')

const currentCalibration = computed(() => props.camera.calibration ?? null)
const matrixRows = computed(() => solveResult.value?.matrix ?? [])
const canSave = computed(() => solveResult.value !== null && !saving.value)

function parsePoints(): ControlPoint[] | null {
  parseError.value = ''
  try {
    const parsed: unknown = JSON.parse(pointsJson.value)
    if (!Array.isArray(parsed)) throw new Error('控制点 JSON 必须是数组')
    if (parsed.length < 4 || parsed.length > 20) throw new Error('控制点数量需为 4–20 组')
    return parsed.map((item, index) => {
      const record = item as Record<string, unknown>
      const px = Number(record.px)
      const py = Number(record.py)
      const x = Number(record.x)
      const y = Number(record.y)
      if (![px, py, x, y].every(Number.isFinite)) {
        throw new Error(`第 ${index + 1} 组控制点字段需为数字：px / py / x / y`)
      }
      return { px, py, x, y }
    })
  } catch (error) {
    parseError.value = error instanceof Error ? error.message : String(error)
    return null
  }
}

async function solve(): Promise<void> {
  solveError.value = ''
  savedMessage.value = ''
  const points = parsePoints()
  if (!points) return
  solving.value = true
  try {
    solveResult.value = await solveCameraCalibration(props.camera.id, points)
  } catch (error) {
    solveResult.value = null
    solveError.value = error instanceof Error ? error.message : String(error)
  } finally {
    solving.value = false
  }
}

async function save(): Promise<void> {
  const result = solveResult.value
  if (!result) return
  saving.value = true
  saveError.value = ''
  savedMessage.value = ''
  try {
    await saveCameraCalibration(props.camera.id, {
      matrix: result.matrix,
      rmse: result.rmse,
      max_residual: result.max_residual,
      points_count: result.points_count,
    })
    savedMessage.value = '标定已保存到相机台账'
    emit('saved')
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : String(error)
  } finally {
    saving.value = false
  }
}

async function remove(): Promise<void> {
  deleting.value = true
  deleteError.value = ''
  savedMessage.value = ''
  try {
    await removeCameraCalibration(props.camera.id)
    solveResult.value = null
    savedMessage.value = '已清除标定'
    emit('saved')
  } catch (error) {
    deleteError.value = error instanceof Error ? error.message : String(error)
  } finally {
    deleting.value = false
  }
}

function formatNumber(value: number): string {
  return value.toFixed(3)
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') emit('close')
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <section class="modal-card calibration-modal" role="dialog" aria-modal="true" aria-label="相机标定">
      <header class="modal-head">
        <h2>相机标定 · {{ camera.code }}</h2>
        <button class="ghost-btn" @click="emit('close')">关闭</button>
      </header>

      <div class="modal-body">
        <div class="calib-status">
          <span>当前状态</span>
          <template v-if="currentCalibration">
            <strong class="ok-text">已标定</strong>
            <span class="mono">
              RMSE {{ currentCalibration.rmse.toFixed(2) }} m · {{ currentCalibration.points_count }} 点
              <template v-if="currentCalibration.solved_at"> · {{ formatDateTime(currentCalibration.solved_at) }}</template>
            </span>
          </template>
          <strong v-else class="warn-text">未标定</strong>
        </div>

        <label class="calib-input">
          <span>控制点（像素 → ENU，4–20 组，单位：像素 / 米）</span>
          <textarea
            v-model="pointsJson"
            class="mono"
            rows="8"
            spellcheck="false"
            placeholder='[{"px": 640, "py": 540, "x": -85, "y": -55}, ...]'
          />
        </label>
        <p v-if="parseError" class="form-error">{{ parseError }}</p>
        <p v-if="solveError" class="form-error">求解失败：{{ solveError }}</p>

        <div v-if="solveResult" class="calib-result">
          <div class="calib-result-head">
            <strong>求解结果</strong>
            <span class="mono">
              RMSE {{ solveResult.rmse.toFixed(2) }} m · 最大残差 {{ solveResult.max_residual.toFixed(2) }} m ·
              {{ solveResult.points_count }} 点
            </span>
          </div>
          <div class="calib-matrix mono">
            <div v-for="(row, rowIndex) in matrixRows" :key="rowIndex" class="calib-matrix-row">
              <span v-for="(value, colIndex) in row" :key="colIndex">{{ formatNumber(value) }}</span>
            </div>
          </div>
        </div>

        <p v-if="saveError" class="form-error">保存失败：{{ saveError }}</p>
        <p v-if="deleteError" class="form-error">删除失败：{{ deleteError }}</p>
        <p v-if="savedMessage" class="form-ok">{{ savedMessage }}</p>

        <div class="modal-buttons">
          <button class="primary-btn" :disabled="solving" @click="solve">
            {{ solving ? '求解中…' : '求解标定' }}
          </button>
          <button class="primary-btn" :disabled="!canSave" @click="save">
            {{ saving ? '保存中…' : '保存标定' }}
          </button>
          <button v-if="currentCalibration" class="danger-btn" :disabled="deleting" @click="remove">
            {{ deleting ? '删除中…' : '删除标定' }}
          </button>
        </div>

        <p class="form-hint">
          求解调用 <span class="mono">POST /api/calibration/solve</span>，保存调用
          <span class="mono">PUT /api/cameras/{id}/calibration</span>；保存后检测坐标将优先使用标定投影（docs/API_P1.md §2）。
        </p>
      </div>
    </section>
  </div>
</template>
