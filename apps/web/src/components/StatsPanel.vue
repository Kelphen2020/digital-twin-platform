<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import {
  EVENT_TYPE_LABELS,
  EVENT_TYPE_ORDER,
  SEVERITY_COLORS,
  SEVERITY_LABELS,
  SEVERITY_ORDER,
  formatHourLabel,
} from '../labels'
import { useTwinStore } from '../store/twin'
import type { StatsRange } from '../types'

const { state, loadStats, downloadEventsCsv } = useTwinStore()

let refreshTimer: number | null = null

onMounted(() => {
  void loadStats(state.statsRange)
  // 统计页签打开期间每 30s 自动刷新
  refreshTimer = window.setInterval(() => {
    if (state.sidebarTab === 'stats') void loadStats(state.statsRange)
  }, 30_000)
})
onBeforeUnmount(() => {
  if (refreshTimer !== null) window.clearInterval(refreshTimer)
})

const ranges: { value: StatsRange; label: string }[] = [
  { value: '1h', label: '近 1 小时' },
  { value: '24h', label: '近 24 小时' },
]

const summary = computed(() => state.stats)
const totals = computed(() => summary.value?.totals ?? null)
const hasData = computed(() => (totals.value?.events ?? 0) > 0)

const severityRows = computed(() => {
  const bySeverity = totals.value?.by_severity ?? {}
  const max = Math.max(1, ...SEVERITY_ORDER.map((severity) => bySeverity[severity] ?? 0))
  return SEVERITY_ORDER.map((severity) => {
    const count = bySeverity[severity] ?? 0
    return {
      severity,
      label: SEVERITY_LABELS[severity],
      color: SEVERITY_COLORS[severity],
      count,
      percent: (count / max) * 100,
    }
  })
})

const typeRows = computed(() => {
  const byType = totals.value?.by_type ?? {}
  const max = Math.max(1, ...EVENT_TYPE_ORDER.map((type) => byType[type] ?? 0))
  return EVENT_TYPE_ORDER.map((type) => ({ type, count: byType[type] ?? 0, percent: 0, max }))
    .filter((row) => row.count > 0)
    .sort((a, b) => b.count - a.count)
    .map((row) => ({
      ...row,
      label: EVENT_TYPE_LABELS[row.type],
      percent: (row.count / row.max) * 100,
    }))
})

const cameraRows = computed(() => (summary.value?.by_camera ?? []).slice(0, 6))
const cameraMax = computed(() => Math.max(1, ...cameraRows.value.map((row) => row.count)))

const hourRows = computed(() => summary.value?.by_hour ?? [])
const hourMax = computed(() => Math.max(1, ...hourRows.value.map((row) => row.count)))

function switchRange(range: StatsRange): void {
  if (state.statsRange === range) return
  void loadStats(range)
}
</script>

<template>
  <section class="stats-panel">
    <header class="panel-head">
      <h2>事件统计</h2>
      <div class="panel-head-actions">
        <button class="ghost-btn" @click="downloadEventsCsv">导出 CSV</button>
        <button class="ghost-btn" :disabled="state.statsLoading" @click="loadStats()">刷新</button>
      </div>
    </header>

    <div class="stats-range">
      <button
        v-for="range in ranges"
        :key="range.value"
        class="range-btn"
        :class="{ active: state.statsRange === range.value }"
        @click="switchRange(range.value)"
      >
        {{ range.label }}
      </button>
    </div>

    <div class="stats-scroll">
      <div v-if="state.statsLoading && !summary" class="detail-loading">正在加载统计数据…</div>
      <p v-else-if="state.statsError" class="form-error">
        统计加载失败：{{ state.statsError }}
        <button class="link-btn" @click="loadStats()">重试</button>
      </p>

      <template v-else-if="summary">
        <div class="stats-overview">
          <div class="stat-tile">
            <span>事件总数</span>
            <strong>{{ totals?.events ?? 0 }}</strong>
          </div>
          <div class="stat-tile">
            <span>严重事件</span>
            <strong class="critical-text">{{ totals?.by_severity?.critical ?? 0 }}</strong>
          </div>
          <div class="stat-tile">
            <span>涉及相机</span>
            <strong>{{ cameraRows.length }}</strong>
          </div>
        </div>

        <section class="stats-block">
          <h3>按等级</h3>
          <div v-for="row in severityRows" :key="row.severity" class="bar-row">
            <span class="bar-label">{{ row.label }}</span>
            <div class="bar-track">
              <div class="bar-fill" :style="{ width: `${row.percent}%`, background: row.color }" />
            </div>
            <span class="bar-value mono">{{ row.count }}</span>
          </div>
        </section>

        <section class="stats-block">
          <h3>按类型</h3>
          <p v-if="typeRows.length === 0" class="stats-empty">当前时段暂无事件</p>
          <div v-for="row in typeRows" :key="row.type" class="bar-row">
            <span class="bar-label">{{ row.label }}</span>
            <div class="bar-track">
              <div class="bar-fill type-fill" :style="{ width: `${row.percent}%` }" />
            </div>
            <span class="bar-value mono">{{ row.count }}</span>
          </div>
        </section>

        <section class="stats-block">
          <h3>高发相机 TOP {{ cameraRows.length }}</h3>
          <p v-if="cameraRows.length === 0" class="stats-empty">当前时段暂无数据</p>
          <div v-for="row in cameraRows" :key="row.camera_id" class="bar-row">
            <span class="bar-label camera-label" :title="`${row.code} ${row.name}`">{{ row.code }}</span>
            <div class="bar-track">
              <div class="bar-fill camera-fill" :style="{ width: `${(row.count / cameraMax) * 100}%` }" />
            </div>
            <span class="bar-value mono">{{ row.count }}</span>
          </div>
        </section>

        <section class="stats-block">
          <h3>按小时分布</h3>
          <p v-if="hourRows.length === 0" class="stats-empty">当前时段暂无数据</p>
          <div v-else class="hour-chart">
            <div
              v-for="(row, index) in hourRows"
              :key="row.hour"
              class="hour-bar-wrap"
              :title="`${formatHourLabel(row.hour)} · ${row.count} 条`"
            >
              <div class="hour-bar" :style="{ height: `${Math.max(3, (row.count / hourMax) * 100)}%` }" />
              <span v-if="index % Math.max(1, Math.ceil(hourRows.length / 6)) === 0" class="hour-label">
                {{ formatHourLabel(row.hour) }}
              </span>
            </div>
          </div>
        </section>

        <p v-if="!hasData" class="stats-empty">当前时段暂无事件，模拟器产生事件后统计会自动刷新。</p>
      </template>
    </div>
  </section>
</template>
