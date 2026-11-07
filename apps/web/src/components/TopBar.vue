<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ROLE_COLORS, ROLE_LABELS, WS_STATE_LABELS, formatArea, formatClock } from '../labels'
import { useAuthStore } from '../store/auth'
import { useTwinStore } from '../store/twin'

const { state, onlineCameras, toggleTracks } = useTwinStore()
const { state: authState, role, logout } = useAuthStore()

const now = ref(new Date())
let clockTimer: number | null = null

onMounted(() => {
  clockTimer = window.setInterval(() => {
    now.value = new Date()
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
        <p>园区三维安防 · P3.0 升级 / 设置 / 审计</p>
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

    <button
      class="track-toggle"
      :class="{ active: state.showTracks }"
      :aria-pressed="state.showTracks"
      title="显示/隐藏选中相机的历史轨迹"
      @click="toggleTracks"
    >
      <span class="track-toggle-dot" />
      显示轨迹
      <span v-if="state.showTracks && trackCount > 0" class="track-count">{{ trackCount }}</span>
    </button>

    <div class="status-cluster">
      <div class="ws-state" :class="`ws-${wsTone}`">
        <i class="dot" />
        <span>{{ wsLabel }}</span>
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
