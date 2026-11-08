<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ROLE_COLORS, ROLE_LABELS, WS_STATE_LABELS, formatArea, formatClock } from '../labels'
import { useAuthStore } from '../store/auth'
import { useTwinStore } from '../store/twin'
import { framesInWindow, pruneFrameTimes, wsQuality } from '../store/wsQuality'

const { state, onlineCameras, toggleTracks, toggleVideoFusion, toggleModelDetails, toggleBaseMap } = useTwinStore()
const { state: authState, role, logout } = useAuthStore()

const now = ref(new Date())
let clockTimer: number | null = null

onMounted(() => {
  clockTimer = window.setInterval(() => {
    now.value = new Date()
    pruneFrameTimes(now.value.getTime())
  }, 1000)
})
onBeforeUnmount(() => {
  if (clockTimer !== null) window.clearInterval(clockTimer)
})

const clock = computed(() => formatClock(now.value))
const cameraSummary = computed(() => `${onlineCameras.value}/${state.cameras.length}`)
const wsLabel = computed(() => WS_STATE_LABELS[state.wsState])
const wsTone = computed(() =>
  state.wsState === 'online' ? 'ok' : state.wsState === 'connecting' ? 'warn' : 'bad',
)

// ---- P9：WS 连接质量（滚动 5s 帧速率 / 重连次数 / 健康色 + 详细 tooltip） ----
const wsRate = computed(() => {
  const timestamp = now.value.getTime()
  const frames = framesInWindow(timestamp)
  const elapsedSeconds = wsQuality.connectedAt
    ? Math.max(1, Math.min(5, (timestamp - wsQuality.connectedAt) / 1000))
    : 5
  return frames / elapsedSeconds
})
const wsRateText = computed(() => (wsRate.value >= 10 ? wsRate.value.toFixed(0) : wsRate.value.toFixed(1)))
const wsLastFrameAge = computed(() =>
  wsQuality.lastFrameAt ? Math.max(0, now.value.getTime() - wsQuality.lastFrameAt) : null,
)
const wsHealth = computed<'green' | 'amber' | 'red'>(() => {
  if (!wsQuality.connected) return wsQuality.reconnecting ? 'amber' : 'red'
  if (wsLastFrameAge.value === null) {
    const connectingAge = wsQuality.connectedAt ? now.value.getTime() - wsQuality.connectedAt : 0
    return connectingAge <= 5000 ? 'green' : 'amber'
  }
  return wsLastFrameAge.value <= 5000 ? 'green' : 'amber'
})
const wsHealthLabel = computed(
  () => ({ green: '良好', amber: '波动', red: '断开' })[wsHealth.value],
)
const formatDuration = (milliseconds: number): string => {
  const totalSeconds = Math.max(0, Math.floor(milliseconds / 1000))
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return minutes > 0 ? `${minutes}m${String(seconds).padStart(2, '0')}s` : `${seconds}s`
}
const wsUptimeText = computed(() =>
  wsQuality.connected && wsQuality.connectedAt
    ? formatDuration(now.value.getTime() - wsQuality.connectedAt)
    : '—',
)
const wsLastFrameText = computed(() => {
  const age = wsLastFrameAge.value
  if (age === null) return '—'
  return age < 1000 ? `${age}ms 前` : `${(age / 1000).toFixed(1)}s 前`
})
const wsQualityTooltip = computed(() =>
  [
    `WebSocket 连接质量：${wsHealthLabel.value}`,
    `帧总数：${wsQuality.framesTotal}`,
    `5s 速率：${wsRateText.value} 帧/s`,
    `重连次数（自页面加载）：${wsQuality.reconnectCount}`,
    `连接时长：${wsUptimeText.value}`,
    `最近帧：${wsLastFrameText.value}`,
  ].join('\n'),
)
const trackCount = computed(() => new Set(state.tracks.map((point) => point.track_id)).size)

const roleLabel = computed(() => (role.value ? ROLE_LABELS[role.value] : '未登录'))
const roleColor = computed(() => (role.value ? ROLE_COLORS[role.value] : '#94a3b8'))
const userInitial = computed(() => authState.user?.display_name?.slice(0, 1) ?? '?')
const areaText = computed(() => {
  const areas = authState.user?.areas ?? []
  return areas.length > 0 ? areas.map(formatArea).join('、') : '全部区域'
})
const userTitle = computed(() =>
  authState.user ? `${authState.user.username} · ${roleLabel.value} · 负责区域：${areaText.value}` : '',
)
</script>

<template>
  <header class="topbar">
    <div class="brand">
      <span class="brand-mark">DT</span>
      <div class="brand-text">
        <h1>{{ state.siteName }}</h1>
        <p>园区三维安防 · P9.0 连接质量 / 性能观测</p>
      </div>
    </div>

    <div class="metrics">
      <div class="metric">
        <span class="metric-label">在线相机</span>
        <strong>{{ cameraSummary }}</strong>
      </div>
      <div class="metric">
        <span class="metric-label">事件总数</span>
        <strong>{{ state.eventsTotal }}</strong>
      </div>
      <div class="metric">
        <span class="metric-label">帧率</span>
        <strong>{{ state.fps }}<em> fps</em></strong>
      </div>
    </div>

    <div class="scene-toolbar" role="group" aria-label="三维显示开关">
      <button
        class="toolbar-toggle"
        :class="{ active: state.showTracks }"
        :aria-pressed="state.showTracks"
        title="显示/隐藏选中相机的历史轨迹"
        @click="toggleTracks"
      >
        <span class="toolbar-dot" />轨迹
        <span v-if="state.showTracks && trackCount > 0" class="track-count">{{ trackCount }}</span>
      </button>
      <button
        class="toolbar-toggle"
        :class="{ active: state.showVideoFusion }"
        :aria-pressed="state.showVideoFusion"
        title="在选中相机视锥内融合 HLS 视频纹理"
        @click="toggleVideoFusion"
      >
        <span class="toolbar-dot" />视频融合
      </button>
      <button
        class="toolbar-toggle"
        :class="{ active: state.showModelDetails }"
        :aria-pressed="state.showModelDetails"
        title="建筑 LOD 细节 / 简化盒体切换"
        @click="toggleModelDetails"
      >
        <span class="toolbar-dot" />模型细节
      </button>
      <button
        class="toolbar-toggle"
        :class="{ active: state.showBaseMap }"
        :aria-pressed="state.showBaseMap"
        title="2.5D 程序化矢量底图开关"
        @click="toggleBaseMap"
      >
        <span class="toolbar-dot" />2.5D底图
      </button>
    </div>

    <div class="status-cluster">
      <div class="ws-state" :class="`ws-${wsTone}`">
        <i class="dot" />
        <span>{{ wsLabel }}</span>
      </div>
      <div
        class="ws-quality"
        :class="`quality-${wsHealth}`"
        :data-ws-health="wsHealth"
        :data-ws-frames="wsQuality.framesTotal"
        :data-ws-reconnects="wsQuality.reconnectCount"
        :data-ws-rate="wsRateText"
        :title="wsQualityTooltip"
      >
        <i class="quality-dot" />
        <span class="quality-rate">{{ wsRateText }}<em>/s</em></span>
        <span class="quality-reconnect" title="自页面加载以来重连次数">↻{{ wsQuality.reconnectCount }}</span>
      </div>
      <time class="clock">{{ clock }}</time>

      <div class="user-chip" :title="userTitle">
        <span class="user-avatar" :style="{ borderColor: roleColor, color: roleColor }">{{ userInitial }}</span>
        <span class="user-meta">
          <strong>{{ authState.user?.display_name ?? '未登录' }}</strong>
          <em :style="{ color: roleColor }">{{ roleLabel }} · {{ areaText }}</em>
        </span>
        <button class="ghost-btn logout-btn" title="退出登录" @click="logout">退出</button>
      </div>
    </div>
  </header>
</template>
