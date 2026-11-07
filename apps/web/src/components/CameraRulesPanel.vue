<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { fetchCameraRules, saveCameraRules, apiErrorMessage } from '../api'
import { EVENT_TYPE_LABELS, EVENT_TYPE_ORDER, ROLE_LABELS, formatDateTime } from '../labels'
import { useAuthStore } from '../store/auth'
import { pushToast } from '../store/ui'
import type { Camera, CameraRules, EventType } from '../types'

const props = defineProps<{ camera: Camera }>()
const emit = defineEmits<{ (event: 'close'): void }>()

const { state: authState, can } = useAuthStore()
const editable = computed(() => can('camera:rules'))

const loading = ref(true)
const loadError = ref('')
const rules = ref<CameraRules | null>(null)

const confidenceThreshold = ref(0.6)
const dedupeSeconds = ref(5)
const enabledClasses = ref<EventType[]>([])
const roi = ref<[number, number][] | null>(null)

const saving = ref(false)
const saveError = ref('')
const savedMessage = ref('')

function applyRules(next: CameraRules): void {
  rules.value = next
  confidenceThreshold.value = next.confidence_threshold
  dedupeSeconds.value = next.dedupe_seconds
  enabledClasses.value = [...next.enabled_classes]
  roi.value = next.roi ? next.roi.map((point) => [point[0], point[1]] as [number, number]) : null
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    applyRules(await fetchCameraRules(props.camera.id))
  } catch (error) {
    loadError.value = apiErrorMessage(error)
  } finally {
    loading.value = false
  }
}

function isClassEnabled(type: EventType): boolean {
  return enabledClasses.value.includes(type)
}

function toggleClass(type: EventType): void {
  enabledClasses.value = isClassEnabled(type)
    ? enabledClasses.value.filter((item) => item !== type)
    : [...enabledClasses.value, type]
}

function setFullFrame(): void {
  roi.value = null
}

function useDefaultRoi(): void {
  roi.value = [
    [0.05, 0.05],
    [0.95, 0.05],
    [0.95, 0.95],
    [0.05, 0.95],
  ]
}

function addRoiPoint(): void {
  if (!roi.value) {
    useDefaultRoi()
    return
  }
  roi.value = [...roi.value, [0.5, 0.5]]
}

function removeRoiPoint(index: number): void {
  if (!roi.value) return
  if (roi.value.length <= 3) {
    pushToast('ROI 多边形至少需要 3 个点', 'warning')
    return
  }
  roi.value = roi.value.filter((_, pointIndex) => pointIndex !== index)
}

function validate(): string | null {
  if (!Number.isFinite(confidenceThreshold.value) || confidenceThreshold.value < 0 || confidenceThreshold.value > 1) {
    return '置信度阈值需在 0–1 之间'
  }
  if (!Number.isFinite(dedupeSeconds.value) || dedupeSeconds.value < 0 || dedupeSeconds.value > 300) {
    return '去重窗口需在 0–300 秒之间'
  }
  if (roi.value) {
    if (roi.value.length < 3) return 'ROI 多边形至少需要 3 个点'
    for (const point of roi.value) {
      if (point[0] < 0 || point[0] > 1 || point[1] < 0 || point[1] > 1) {
        return 'ROI 坐标需为归一化值（0–1）'
      }
    }
  }
  return null
}

async function save(): Promise<void> {
  if (!editable.value) return
  saveError.value = ''
  savedMessage.value = ''
  const validationError = validate()
  if (validationError) {
    saveError.value = validationError
    return
  }
  saving.value = true
  try {
    const updated = await saveCameraRules(props.camera.id, {
      confidence_threshold: confidenceThreshold.value,
      dedupe_seconds: dedupeSeconds.value,
      enabled_classes: [...enabledClasses.value],
      roi: roi.value ? roi.value.map((point) => [point[0], point[1]] as [number, number]) : null,
    })
    applyRules(updated)
    savedMessage.value = '规则已保存'
    pushToast('相机规则已保存', 'success')
  } catch (error) {
    saveError.value = apiErrorMessage(error)
    pushToast(apiErrorMessage(error), 'error')
  } finally {
    saving.value = false
  }
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') emit('close')
}

onMounted(() => {
  void load()
  window.addEventListener('keydown', onKeydown)
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <section class="modal-card rules-modal" role="dialog" aria-modal="true" aria-label="相机规则配置">
      <header class="modal-head">
        <h2>相机规则 · {{ camera.code }} {{ camera.name }}</h2>
        <button class="ghost-btn" @click="emit('close')">关闭</button>
      </header>

      <div class="modal-body">
        <p v-if="!editable" class="role-notice">
          当前角色（{{ authState.user ? ROLE_LABELS[authState.user.role] : '未登录' }}）为只读——仅管理员可修改检测规则。
        </p>
        <p v-if="loading" class="detail-loading">正在加载规则…</p>
        <p v-else-if="loadError" class="form-error">
          规则加载失败：{{ loadError }}
          <button class="link-btn" @click="load">重试</button>
        </p>

        <template v-else-if="rules">
          <div class="rules-status">
            <span>最近更新</span>
            <strong class="mono">{{ formatDateTime(rules.updated_at) }}</strong>
            <span class="mono text-faint">camera_id: {{ rules.camera_id }}</span>
          </div>

          <div class="rules-grid">
            <label>
              <span>置信度阈值（0–1）</span>
              <input v-model.number="confidenceThreshold" type="number" min="0" max="1" step="0.01" :disabled="!editable" />
            </label>
            <label>
              <span>去重窗口（秒，0–300）</span>
              <input v-model.number="dedupeSeconds" type="number" min="0" max="300" step="1" :disabled="!editable" />
            </label>
          </div>

          <div class="rules-block">
            <h3>启用类别</h3>
            <div class="rules-classes">
              <label v-for="type in EVENT_TYPE_ORDER" :key="type" class="rules-class">
                <input
                  type="checkbox"
                  :checked="isClassEnabled(type)"
                  :disabled="!editable"
                  @change="toggleClass(type)"
                />
                <span>{{ EVENT_TYPE_LABELS[type] }}</span>
              </label>
            </div>
            <p class="form-hint">未勾选的类别会被规则引擎直接丢弃。</p>
          </div>

          <div class="rules-block">
            <div class="rules-block-head">
              <h3>ROI 多边形（归一化图像坐标 0–1）</h3>
              <span class="text-faint">{{ roi ? `${roi.length} 个点` : '全画面' }}</span>
            </div>

            <p v-if="!roi" class="rules-roi-empty">当前为全画面 ROI（不裁剪检测区域）。</p>
            <div v-else class="roi-rows">
              <div v-for="(point, index) in roi" :key="index" class="roi-row">
                <span class="roi-index">P{{ index + 1 }}</span>
                <label>
                  <span>x</span>
                  <input v-model.number="point[0]" type="number" min="0" max="1" step="0.01" :disabled="!editable" />
                </label>
                <label>
                  <span>y</span>
                  <input v-model.number="point[1]" type="number" min="0" max="1" step="0.01" :disabled="!editable" />
                </label>
                <button v-if="editable" class="ghost-btn" :disabled="roi.length <= 3" @click="removeRoiPoint(index)">
                  删除
                </button>
              </div>
            </div>

            <div v-if="editable" class="rules-buttons">
              <button class="tiny-btn" @click="addRoiPoint">添加点</button>
              <button class="tiny-btn" @click="useDefaultRoi">使用 4 点默认 ROI</button>
              <button class="tiny-btn" @click="setFullFrame">恢复全画面</button>
            </div>
          </div>

          <p v-if="saveError" class="form-error">{{ saveError }}</p>
          <p v-if="savedMessage" class="form-ok">{{ savedMessage }}</p>

          <div v-if="editable" class="modal-buttons">
            <button class="primary-btn" :disabled="saving" @click="save">
              {{ saving ? '保存中…' : '保存规则' }}
            </button>
            <button class="ghost-btn" @click="load">重新加载</button>
          </div>
          <p v-else class="form-hint">只读视图：规则由管理员统一配置，值班员/观察员可在相机详情中查看当前生效值。</p>
        </template>
      </div>
    </section>
  </div>
</template>
