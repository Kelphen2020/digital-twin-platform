<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { apiErrorMessage, fetchTenantOverview } from '../api'
import { useAuthStore } from '../store/auth'
import type { TenantOverview } from '../types'

const auth = useAuthStore()
const overview = ref<TenantOverview | null>(null)
/** 显式布尔条件：避免 v-else-if 直接对 ref 求值时的解包边界问题（P37）。 */
const hasOverview = computed(() => overview.value !== null)
const loading = ref(false)
const error = ref<string | null>(null)
const lastLoadedAt = ref<number | null>(null)
let timer: number | null = null

const tenantId = computed(() => auth.state.tenantId || 'default')

async function loadOverview() {
  if (loading.value) return
  loading.value = true
  error.value = null
  try {
    const fetched = await fetchTenantOverview(tenantId.value)
    overview.value = fetched
    lastLoadedAt.value = Date.now()
  } catch (err) {
    error.value = apiErrorMessage(err)
  } finally {
    loading.value = false
  }
}

const severityRows = computed(() => {
  const entries = Object.entries(overview.value?.events.by_severity ?? {})
  const order = ['critical', 'warn', 'info']
  return order
    .filter((key) => key in Object.fromEntries(entries))
    .map((key) => [key, Object.fromEntries(entries)[key]] as [string, number])
    .concat(entries.filter(([key]) => !order.includes(key)))
})

const severityLabel: Record<string, string> = {
  critical: '严重',
  warn: '警告',
  info: '提示',
}

const hourlyBars = computed(() => {
  const rows = overview.value?.recent_hourly ?? []
  const max = Math.max(1, ...rows.map((row) => row.count))
  return rows.slice(-12).map((row) => ({
    label: row.hour.slice(11, 16) || row.hour,
    count: row.count,
    height: Math.max(4, Math.round((row.count / max) * 100)),
  }))
})

const storeModeLabel = computed(() => (overview.value?.health.store_mode === 'postgres' ? 'PostgreSQL' : '内存'))

function formatTime(ts: number | null): string {
  if (!ts) return '—'
  const date = new Date(ts)
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}:${String(date.getSeconds()).padStart(2, '0')}`
}

onMounted(() => {
  void loadOverview()
  timer = window.setInterval(() => void loadOverview(), 30000)
})

onUnmounted(() => {
  if (timer !== null) window.clearInterval(timer)
})
</script>

<template>
  <section class="admin-panel">
    <header class="panel-head">
      <h2>运维概览</h2>
      <div class="panel-head-actions">
        <span class="panel-hint">每 30 秒自动刷新 · {{ formatTime(lastLoadedAt) }}</span>
        <button class="ghost-btn" :disabled="loading" @click="loadOverview">刷新</button>
      </div>
    </header>

    <div class="admin-scroll">
      <p v-if="loading && !overview" class="detail-loading">正在读取运维概览…</p>
      <p v-else-if="error" class="form-error">
        概览读取失败：{{ error }}
        <button class="link-btn" @click="loadOverview">重试</button>
      </p>

      <template v-else-if="hasOverview && overview">
        <div class="overview-kpis">
          <div class="overview-kpi">
            <span class="overview-label">在线相机</span>
            <strong class="overview-value">{{ overview.cameras.online }}<small>/ {{ overview.cameras.total }}</small></strong>
            <span class="overview-foot">离线 {{ overview.cameras.offline }}</span>
          </div>
          <div class="overview-kpi">
            <span class="overview-label">事件（近 24h）</span>
            <strong class="overview-value">{{ overview.events.last_24h }}</strong>
            <span class="overview-foot">累计 {{ overview.events.total }}</span>
          </div>
          <div class="overview-kpi">
            <span class="overview-label">待处理工单</span>
            <strong class="overview-value">{{ overview.open_orders }}</strong>
            <span class="overview-foot">值班 / 派单状态见工单页</span>
          </div>
          <div class="overview-kpi">
            <span class="overview-label">巡检进行中</span>
            <strong class="overview-value">{{ overview.patrol.in_progress }}</strong>
            <span class="overview-foot">待执行 {{ overview.patrol.pending }}</span>
          </div>
          <div class="overview-kpi">
            <span class="overview-label">导出任务</span>
            <strong class="overview-value">{{ overview.exports.active }}</strong>
            <span class="overview-foot">完成 {{ overview.exports.done }} / 失败 {{ overview.exports.failed }}</span>
          </div>
          <div class="overview-kpi">
            <span class="overview-label">实时连接</span>
            <strong class="overview-value">{{ overview.health.ws_clients }}</strong>
            <span class="overview-foot">WebSocket 客户端</span>
          </div>
        </div>

        <div class="overview-grid">
          <section class="overview-card">
            <h3>近 12 小时事件</h3>
            <div v-if="hourlyBars.length === 0" class="overview-empty">暂无数据</div>
            <div v-else class="overview-bars">
              <div v-for="bar in hourlyBars" :key="bar.label" class="overview-bar">
                <span class="overview-bar-count">{{ bar.count }}</span>
                <div class="overview-bar-track">
                  <div class="overview-bar-fill" :style="{ height: `${bar.height}%` }"></div>
                </div>
                <span class="overview-bar-label">{{ bar.label }}</span>
              </div>
            </div>
          </section>

          <section class="overview-card">
            <h3>按严重级别</h3>
            <div v-if="severityRows.length === 0" class="overview-empty">暂无数据</div>
            <ul v-else class="overview-list">
              <li v-for="[severity, count] in severityRows" :key="severity">
                <span class="severity-chip" :class="`severity-${severity}`">{{ severityLabel[severity] ?? severity }}</span>
                <strong>{{ count }}</strong>
              </li>
            </ul>
            <h3 style="margin-top: 16px">运行状态</h3>
            <ul class="overview-list overview-health">
              <li><span>存储</span><strong>{{ storeModeLabel }}</strong></li>
              <li><span>事件总线</span><strong>{{ overview.health.bus_driver }}</strong></li>
              <li><span>总线偏移</span><strong>{{ overview.health.bus_next_offset }}</strong></li>
              <li><span>ingest 接收</span><strong>{{ overview.ingest.accepted }}</strong></li>
            </ul>
          </section>
        </div>

        <p class="overview-note">
          本面板聚合当前租户的运营数据；指标口径（Prometheus/Grafana）见「运维手册」，租户 {{ overview.tenant }} ·
          刷新于 {{ new Date(overview.generated_at).toLocaleString() }}
        </p>
      </template>
    </div>
  </section>
</template>