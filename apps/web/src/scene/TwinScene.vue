<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import {
  CAMERA_STATUS_COLORS,
  CAMERA_STATUS_LABELS,
  CAMERA_STATUS_ORDER,
  EVENT_TYPE_LABELS,
  SEVERITY_COLORS,
  SEVERITY_LABELS,
  SEVERITY_ORDER,
  formatDateTime,
} from '../labels'
import type { Area, Camera, FocusRequest, SiteConfig, SiteModelStatus, TrackPoint, TwinEvent } from '../types'
import { createBaseMapLayer, type BaseMapLayer } from './baseMap'
import { createBuildingLayer, type BuildingLayer } from './buildings'
import { createCameraMarker, type CameraMarker } from './cameraMarkers'
import { disposeObject3D } from './dispose'
import { createEnvironment, type SceneEnvironment } from './environment'
import { EVENT_MARKER_TTL_MS, createEventMarker, type EventMarker } from './eventMarkers'
import { loadSiteModels } from './gltf'
import { DEFAULT_SITE } from './siteDefaults'
import { createTrackLineLayer, type TrackLineLayer } from './trackLines'
import { createVideoFusion, type VideoFusion } from './videoFusion'

const props = defineProps<{
  cameras: Camera[]
  events: TwinEvent[]
  selectedCameraId: string | null
  selectedEventId: string | null
  focusRequest: FocusRequest | null
  offline: boolean
  tracks: TrackPoint[]
  showTracks: boolean
  site: SiteConfig | null
  areas: Area[]
  showBaseMap: boolean
  showModelDetails: boolean
  showVideoFusion: boolean
}>()

const emit = defineEmits<{
  (event: 'select-camera', cameraId: string | null): void
  (event: 'open-event', eventId: string): void
  (event: 'fps', fps: number): void
}>()

interface SceneTooltip {
  x: number
  y: number
  title: string
  detail: string
  color: string
}

interface FocusAnimation {
  fromTarget: THREE.Vector3
  toTarget: THREE.Vector3
  fromPosition: THREE.Vector3
  toPosition: THREE.Vector3
  startedAt: number
  duration: number
}

const containerRef = ref<HTMLDivElement | null>(null)
const tooltip = ref<SceneTooltip | null>(null)
const fpsDisplay = ref(0)

const hasCameras = computed(() => props.cameras.length > 0)
const isOffline = computed(() => props.offline)

// ---- 非响应式 Three.js 句柄（组件生命周期内唯一） ----
let renderer: THREE.WebGLRenderer | null = null
let scene: THREE.Scene | null = null
let camera: THREE.PerspectiveCamera | null = null
let controls: OrbitControls | null = null
let environment: SceneEnvironment | null = null
let baseMapLayer: BaseMapLayer | null = null
let buildingLayer: BuildingLayer | null = null
let gltfGroup: THREE.Group | null = null
let fusion: VideoFusion | null = null
let modelLoadSeq = 0
let fusionSeq = 0
let cameraLayer: THREE.Group | null = null
let eventLayer: THREE.Group | null = null
let trackLayer: TrackLineLayer | null = null
let raycaster: THREE.Raycaster | null = null
let resizeObserver: ResizeObserver | null = null
let removePointerListeners: (() => void) | null = null
let frameHandle = 0
let lastPruneAt = 0
let frameCount = 0
let fpsWindowStart = 0
let pointerDownAt: { x: number; y: number } | null = null
let focusAnimation: FocusAnimation | null = null
let lastFocusSeq = -1
let tracksDirty = false
let lastTrackSyncAt = 0

const cameraMarkers = new Map<string, CameraMarker>()
const eventMarkers = new Map<string, EventMarker>()
const pointer = new THREE.Vector2()

// ---- P6：站点/模型/视频融合状态（暴露为 data-* 便于验收） ----
const modelStatus = ref<SiteModelStatus>('idle')
const modelMessage = ref('')
const fusionActive = ref(false)
const selectedCamera = computed<Camera | null>(
  () => props.cameras.find((item) => item.id === props.selectedCameraId) ?? null,
)
const modelStatusLabel = computed(() => {
  switch (modelStatus.value) {
    case 'loading':
      return '加载中…'
    case 'loaded':
      return '已加载'
    case 'partial':
      return '部分加载'
    case 'fallback':
      return '回退·程序化建筑'
    default:
      return '—'
  }
})
const modelStatusClass = computed(() => `model-${modelStatus.value}`)

function currentSite(): SiteConfig {
  return props.site ?? DEFAULT_SITE
}

// ---- P6：场景层重建 ----

function rebuildEnvironment(): void {
  if (!scene) return
  environment?.dispose()
  environment = createEnvironment(currentSite())
  scene.add(environment.group)
}

function rebuildBaseMap(): void {
  if (!scene) return
  baseMapLayer?.dispose()
  baseMapLayer = createBaseMapLayer(currentSite(), props.areas, props.cameras)
  baseMapLayer.mesh.visible = props.showBaseMap
  scene.add(baseMapLayer.mesh)
}

function rebuildBuildingLayer(): void {
  if (!scene) return
  buildingLayer?.dispose()
  buildingLayer = createBuildingLayer(props.showModelDetails ? 'lod' : 'simple', gltfGroup)
  scene.add(buildingLayer.group)
}

function disposeGltfGroup(): void {
  if (!gltfGroup) return
  gltfGroup.parent?.remove(gltfGroup)
  disposeObject3D(gltfGroup)
  gltfGroup = null
}

/** 加载 GET /api/site models；失败/缺失保持程序化建筑并在 HUD 显示回退状态。 */
async function reloadModels(): Promise<void> {
  const seq = (modelLoadSeq += 1)
  disposeGltfGroup()
  rebuildBuildingLayer()

  const models = currentSite().models ?? []
  if (models.length === 0) {
    modelStatus.value = 'fallback'
    modelMessage.value = '站点未配置模型，使用程序化建筑'
    return
  }

  modelStatus.value = 'loading'
  modelMessage.value = `正在加载 ${models.length} 个站点模型…`
  try {
    const result = await loadSiteModels(models)
    if (seq !== modelLoadSeq) {
      for (const loaded of result.loaded) disposeObject3D(loaded.object)
      return
    }
    if (result.loaded.length === 0) {
      modelStatus.value = 'fallback'
      modelMessage.value = `模型加载失败（${result.failed.length}），使用程序化建筑`
      return
    }
    const group = new THREE.Group()
    group.name = 'site-models'
    for (const loaded of result.loaded) group.add(loaded.object)
    gltfGroup = group
    modelStatus.value = result.failed.length > 0 ? 'partial' : 'loaded'
    modelMessage.value =
      result.failed.length > 0
        ? `已加载 ${result.loaded.length} 个模型，${result.failed.length} 个失败（回退程序化建筑）`
        : `已加载：${result.loaded.map((item) => item.name).join('、')}`
    rebuildBuildingLayer()
  } catch (error) {
    if (seq !== modelLoadSeq) return
    modelStatus.value = 'fallback'
    modelMessage.value = `模型加载异常：${error instanceof Error ? error.message : String(error)}`
  }
}

// ---- P6：视频纹理融合（独立于右侧 CameraVideo 播放器） ----

function disposeFusion(): void {
  fusion?.dispose()
  fusion = null
  fusionActive.value = false
}

async function syncVideoFusion(): Promise<void> {
  const seq = (fusionSeq += 1)
  disposeFusion()
  if (!props.showVideoFusion) return
  const target = selectedCamera.value
  if (!target || !target.stream_url) return
  const created = await createVideoFusion(target)
  if (seq !== fusionSeq) {
    created?.dispose()
    return
  }
  if (!created) return
  fusion = created
  fusionActive.value = true
  scene?.add(created.group)
}

function easeInOutCubic(t: number): number {
  return t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2
}

// ---- 标记同步 ----

function rebuildCameraMarkers(): void {
  if (!cameraLayer) return
  for (const marker of cameraMarkers.values()) {
    cameraLayer.remove(marker.group)
    marker.dispose()
  }
  cameraMarkers.clear()
  for (const item of props.cameras) {
    const marker = createCameraMarker(item, item.id === props.selectedCameraId)
    cameraMarkers.set(item.id, marker)
    cameraLayer.add(marker.group)
  }
}

/** 增量同步事件标记：新事件建标记、到期/移出列表的销毁标记。 */
function syncEventMarkers(): void {
  if (!eventLayer) return
  const now = Date.now()
  const recentIds = new Set<string>()

  for (const item of props.events) {
    const parsedTime = Date.parse(item.occurred_at)
    const occurredAt = Number.isFinite(parsedTime) ? parsedTime : now
    if (now - occurredAt > EVENT_MARKER_TTL_MS) continue
    recentIds.add(item.id)

    const existing = eventMarkers.get(item.id)
    if (existing) {
      existing.setSelected(item.id === props.selectedEventId)
      continue
    }
    const marker = createEventMarker(item, now)
    if (!marker) continue
    marker.setSelected(item.id === props.selectedEventId)
    eventMarkers.set(item.id, marker)
    eventLayer.add(marker.group)
  }

  for (const [id, marker] of eventMarkers) {
    if (recentIds.has(id)) continue
    eventLayer.remove(marker.group)
    marker.dispose()
    eventMarkers.delete(id)
  }
}

function applyEventSelection(): void {
  for (const [id, marker] of eventMarkers) {
    marker.setSelected(id === props.selectedEventId)
  }
}

function pruneExpiredEventMarkers(now: number): void {
  if (!eventLayer) return
  for (const [id, marker] of eventMarkers) {
    if (now - marker.occurredAt > EVENT_MARKER_TTL_MS) {
      eventLayer.remove(marker.group)
      marker.dispose()
      eventMarkers.delete(id)
    }
  }
}

// ---- 拾取 / 悬停 / 点击 ----

function pickInteractive(event: PointerEvent): { kind: 'camera' | 'event'; id: string } | null {
  if (!renderer || !camera || !raycaster) return null
  const rect = renderer.domElement.getBoundingClientRect()
  if (rect.width === 0 || rect.height === 0) return null

  pointer.x = ((event.clientX - rect.left) / rect.width) * 2 - 1
  pointer.y = -((event.clientY - rect.top) / rect.height) * 2 + 1
  raycaster.setFromCamera(pointer, camera)

  const targets: THREE.Object3D[] = []
  for (const marker of cameraMarkers.values()) targets.push(...marker.interactive)
  for (const marker of eventMarkers.values()) targets.push(...marker.interactive)

  for (const hit of raycaster.intersectObjects(targets, false)) {
    const data = hit.object.userData as { kind?: string; cameraId?: string; eventId?: string }
    if (data.kind === 'camera' && data.cameraId) return { kind: 'camera', id: data.cameraId }
    if (data.kind === 'event' && data.eventId) return { kind: 'event', id: data.eventId }
  }
  return null
}

function handlePointerMove(event: PointerEvent): void {
  if (!renderer) return
  const hit = pickInteractive(event)
  const rect = renderer.domElement.getBoundingClientRect()

  if (!hit) {
    tooltip.value = null
    renderer.domElement.style.cursor = 'grab'
    return
  }
  renderer.domElement.style.cursor = 'pointer'

  const x = Math.min(event.clientX - rect.left + 16, Math.max(12, rect.width - 300))
  const y = Math.min(event.clientY - rect.top + 16, Math.max(12, rect.height - 140))

  if (hit.kind === 'camera') {
    const item = props.cameras.find((entry) => entry.id === hit.id)
    if (!item) {
      tooltip.value = null
      return
    }
    tooltip.value = {
      x,
      y,
      title: `${item.code} · ${item.name}`,
      detail: `${CAMERA_STATUS_LABELS[item.status]} · 方位 ${Math.round(item.heading)}° · 视场 ${Math.round(
        item.fov_h,
      )}° · 量程 ${Math.round(item.range_m)} m`,
      color: CAMERA_STATUS_COLORS[item.status],
    }
    return
  }

  const twinEvent = props.events.find((entry) => entry.id === hit.id)
  if (!twinEvent) {
    tooltip.value = null
    return
  }
  tooltip.value = {
    x,
    y,
    title: `${EVENT_TYPE_LABELS[twinEvent.type]}事件 · ${SEVERITY_LABELS[twinEvent.severity]}`,
    detail: `${twinEvent.camera_code} ${twinEvent.camera_name} · 置信度 ${Math.round(
      twinEvent.confidence * 100,
    )}% · ${formatDateTime(twinEvent.occurred_at)}`,
    color: SEVERITY_COLORS[twinEvent.severity],
  }
}

function handlePointerLeave(): void {
  tooltip.value = null
  if (renderer) renderer.domElement.style.cursor = 'grab'
}

function handlePointerDown(event: PointerEvent): void {
  if (event.button !== 0) return
  pointerDownAt = { x: event.clientX, y: event.clientY }
}

function handlePointerUp(event: PointerEvent): void {
  if (event.button !== 0) {
    pointerDownAt = null
    return
  }
  const start = pointerDownAt
  pointerDownAt = null
  if (!start) return
  // 拖动旋转视角时不触发选择
  if (Math.hypot(event.clientX - start.x, event.clientY - start.y) > 6) return

  const hit = pickInteractive(event)
  if (!hit) {
    emit('select-camera', null)
  } else if (hit.kind === 'camera') {
    emit('select-camera', hit.id)
  } else {
    emit('open-event', hit.id)
  }
}

// ---- 尺寸 / 渲染循环 ----

function resizeRenderer(): void {
  const container = containerRef.value
  if (!container || !renderer || !camera) return
  const width = container.clientWidth
  const height = container.clientHeight
  if (width === 0 || height === 0) return
  renderer.setSize(width, height)
  camera.aspect = width / height
  camera.updateProjectionMatrix()
}

function animate(now: number): void {
  frameHandle = requestAnimationFrame(animate)
  if (!renderer || !scene || !camera || !controls) return

  // 聚焦运镜（rAF 时间戳与 performance.now 同域）
  if (focusAnimation) {
    const progress = Math.min(1, (now - focusAnimation.startedAt) / focusAnimation.duration)
    const eased = easeInOutCubic(progress)
    controls.target.lerpVectors(focusAnimation.fromTarget, focusAnimation.toTarget, eased)
    camera.position.lerpVectors(focusAnimation.fromPosition, focusAnimation.toPosition, eased)
    if (progress >= 1) focusAnimation = null
  }
  controls.update()

  // 事件标记使用墙上时钟（Date.now）计算年龄，避免与 rAF 时间域混用
  const nowMs = Date.now()
  for (const marker of eventMarkers.values()) marker.animate(nowMs)
  if (now - lastPruneAt > 1000) {
    lastPruneAt = now
    pruneExpiredEventMarkers(nowMs)
  }

  // 轨迹：显示开关 + 数据/淡出刷新（最多每秒重建一次，避免高频动画开销）
  if (trackLayer) {
    trackLayer.group.visible = props.showTracks
    if (props.showTracks && (tracksDirty || now - lastTrackSyncAt > 1000)) {
      trackLayer.update(props.tracks, nowMs)
      tracksDirty = false
      lastTrackSyncAt = now
    }
  }

  renderer.render(scene, camera)

  // FPS：每 500ms 刷新一次，避免高频触发响应式更新
  frameCount += 1
  const elapsed = now - fpsWindowStart
  if (elapsed >= 500) {
    const fps = (frameCount * 1000) / elapsed
    fpsDisplay.value = Math.round(fps)
    emit('fps', fps)
    frameCount = 0
    fpsWindowStart = now
  }
}

// ---- 初始化 / 销毁 ----

function initScene(): void {
  const container = containerRef.value
  if (!container) return

  const webglRenderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: 'high-performance' })
  webglRenderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
  webglRenderer.outputColorSpace = THREE.SRGBColorSpace
  webglRenderer.toneMapping = THREE.ACESFilmicToneMapping
  webglRenderer.toneMappingExposure = 1.05
  webglRenderer.shadowMap.enabled = true
  webglRenderer.shadowMap.type = THREE.PCFShadowMap
  container.appendChild(webglRenderer.domElement)
  renderer = webglRenderer

  const sceneInstance = new THREE.Scene()
  sceneInstance.fog = new THREE.Fog(0x0d2440, 300, 900)
  scene = sceneInstance

  const cameraInstance = new THREE.PerspectiveCamera(50, 1, 0.5, 2400)
  cameraInstance.position.set(185, 165, 235)
  camera = cameraInstance

  const controlsInstance = new OrbitControls(cameraInstance, webglRenderer.domElement)
  controlsInstance.target.set(0, 0, 0)
  controlsInstance.enableDamping = true
  controlsInstance.dampingFactor = 0.08
  controlsInstance.minDistance = 25
  controlsInstance.maxDistance = 700
  controlsInstance.maxPolarAngle = THREE.MathUtils.degToRad(88)
  controlsInstance.update()
  controls = controlsInstance

  environment = createEnvironment(currentSite())
  sceneInstance.add(environment.group)

  baseMapLayer = createBaseMapLayer(currentSite(), props.areas, props.cameras)
  baseMapLayer.mesh.visible = props.showBaseMap
  sceneInstance.add(baseMapLayer.mesh)

  buildingLayer = createBuildingLayer(props.showModelDetails ? 'lod' : 'simple', null)
  sceneInstance.add(buildingLayer.group)

  cameraLayer = new THREE.Group()
  cameraLayer.name = 'camera-markers'
  sceneInstance.add(cameraLayer)

  eventLayer = new THREE.Group()
  eventLayer.name = 'event-markers'
  sceneInstance.add(eventLayer)

  trackLayer = createTrackLineLayer()
  trackLayer.group.visible = props.showTracks
  sceneInstance.add(trackLayer.group)

  raycaster = new THREE.Raycaster()

  rebuildCameraMarkers()
  syncEventMarkers()
  void reloadModels()
  void syncVideoFusion()

  resizeObserver = new ResizeObserver(() => resizeRenderer())
  resizeObserver.observe(container)
  resizeRenderer()

  const dom = webglRenderer.domElement
  dom.addEventListener('pointermove', handlePointerMove)
  dom.addEventListener('pointerdown', handlePointerDown)
  dom.addEventListener('pointerup', handlePointerUp)
  dom.addEventListener('pointerleave', handlePointerLeave)
  removePointerListeners = () => {
    dom.removeEventListener('pointermove', handlePointerMove)
    dom.removeEventListener('pointerdown', handlePointerDown)
    dom.removeEventListener('pointerup', handlePointerUp)
    dom.removeEventListener('pointerleave', handlePointerLeave)
  }

  fpsWindowStart = performance.now()
  frameHandle = requestAnimationFrame(animate)
}

function disposeScene(): void {
  if (frameHandle) cancelAnimationFrame(frameHandle)
  frameHandle = 0

  resizeObserver?.disconnect()
  resizeObserver = null
  removePointerListeners?.()
  removePointerListeners = null

  for (const marker of cameraMarkers.values()) marker.dispose()
  cameraMarkers.clear()
  for (const marker of eventMarkers.values()) marker.dispose()
  eventMarkers.clear()

  trackLayer?.dispose()
  trackLayer = null

  disposeFusion()
  buildingLayer?.dispose()
  buildingLayer = null
  baseMapLayer?.dispose()
  baseMapLayer = null
  disposeGltfGroup()

  environment?.dispose()
  environment = null

  controls?.dispose()
  controls = null

  if (renderer) {
    renderer.dispose()
    const dom = renderer.domElement
    dom.parentElement?.removeChild(dom)
    renderer = null
  }

  scene = null
  camera = null
  raycaster = null
  tooltip.value = null
}

// ---- 响应数据变化 ----

watch(
  () => props.cameras,
  () => rebuildCameraMarkers(),
  { deep: true },
)
watch(
  () => props.selectedCameraId,
  () => rebuildCameraMarkers(),
)
watch(
  () => props.events,
  () => syncEventMarkers(),
)
watch(
  () => props.selectedEventId,
  () => applyEventSelection(),
)
watch(
  () => props.tracks,
  () => {
    tracksDirty = true
  },
)
watch(
  () => props.showTracks,
  () => {
    tracksDirty = true
  },
)
watch(
  () => props.site,
  () => {
    rebuildEnvironment()
    rebuildBaseMap()
    void reloadModels()
  },
)
watch(
  () => props.areas,
  () => rebuildBaseMap(),
)
watch(
  () => props.cameras,
  () => rebuildBaseMap(),
)
watch(
  () => props.showBaseMap,
  () => {
    if (baseMapLayer) baseMapLayer.mesh.visible = props.showBaseMap
  },
)
watch(
  () => props.showModelDetails,
  () => rebuildBuildingLayer(),
)
watch(
  [() => props.selectedCameraId, () => props.showVideoFusion, () => selectedCamera.value?.stream_url],
  () => {
    void syncVideoFusion()
  },
)
watch(
  () => props.focusRequest,
  (request) => {
    if (!request || request.seq === lastFocusSeq) return
    if (!camera || !controls) return
    lastFocusSeq = request.seq

    const marker = cameraMarkers.get(request.cameraId)
    if (!marker) return
    const target = marker.group.position.clone()
    focusAnimation = {
      fromTarget: controls.target.clone(),
      toTarget: target,
      fromPosition: camera.position.clone(),
      toPosition: target.clone().add(new THREE.Vector3(46, 38, 46)),
      startedAt: performance.now(),
      duration: 800,
    }
  },
)

onMounted(initScene)
onBeforeUnmount(disposeScene)
</script>

<template>
  <div
    ref="containerRef"
    class="scene-root"
    :data-base-map="showBaseMap ? 'on' : 'off'"
    :data-model-detail="showModelDetails ? 'lod' : 'simple'"
    :data-video-fusion="fusionActive ? 'active' : 'off'"
    :data-model-status="modelStatus"
  >
    <div class="scene-hud">
      <span class="scene-fps">{{ fpsDisplay > 0 ? fpsDisplay : '—' }} FPS</span>
      <span class="scene-model-status" :class="modelStatusClass" :title="modelMessage">
        模型：{{ modelStatusLabel }}
      </span>
      <span class="scene-hint">左键旋转 · 右键平移 · 滚轮缩放</span>
    </div>

    <div
      v-if="tooltip"
      class="scene-tooltip"
      :style="{ left: `${tooltip.x}px`, top: `${tooltip.y}px`, borderLeftColor: tooltip.color }"
    >
      <strong :style="{ color: tooltip.color }">{{ tooltip.title }}</strong>
      <span>{{ tooltip.detail }}</span>
    </div>

    <div class="scene-legend">
      <div class="legend-row">
        <span class="legend-title">相机</span>
        <span v-for="status in CAMERA_STATUS_ORDER" :key="status" class="legend-item">
          <i :style="{ background: CAMERA_STATUS_COLORS[status] }" />{{ CAMERA_STATUS_LABELS[status] }}
        </span>
      </div>
      <div class="legend-row">
        <span class="legend-title">事件</span>
        <span v-for="severity in SEVERITY_ORDER" :key="severity" class="legend-item">
          <i :style="{ background: SEVERITY_COLORS[severity] }" />{{ SEVERITY_LABELS[severity] }}
        </span>
      </div>
    </div>

    <div v-if="!hasCameras" class="scene-empty">
      <strong>{{ isOffline ? '后端未连接' : '暂无相机数据' }}</strong>
      <span>{{ isOffline ? '场景仍可浏览，正在等待后端恢复…' : '等待相机台账同步到三维场景' }}</span>
    </div>
  </div>
</template>
