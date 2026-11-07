<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { WS_STATE_LABELS, formatClock } from '../labels'
import { useTwinStore } from '../store/twin'

const { state, onlineCameras } = useTwinStore()

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
</script>

<template>
  <header class="topbar">
    <div class="brand">
      <span class="brand-mark">DT</span>
      <div class="brand-text">
        <h1>{{ state.siteName }}</h1>
        <p>园区三维安防 · P0 实时闭环</p>
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

    <div class="status-cluster">
      <div class="ws-state" :class="`ws-${wsTone}`">
        <i class="dot" />
        <span>{{ wsLabel }}</span>
      </div>
      <time class="clock">{{ clock }}</time>
    </div>
  </header>
</template>
