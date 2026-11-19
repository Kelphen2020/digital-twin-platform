<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { apiErrorMessage, fetchTenantOverview } from '../api'
import { useAuthStore } from '../store/auth'
import { useTwinStore } from '../store/twin'
import type { TenantOverview } from '../types'

const auth = useAuthStore()
const twin = useTwinStore()

/** 点击 KPI 跳转到对应页签（P40）。 */
function jumpTo(tab: 'orders' | 'duty' | 'tenant') {
  twin.setSidebarTab(tab)
}
const overview = ref<TenantOverview | null>(null)
/** 显式布尔条件：避免 v-else-if 直接对 ref 求值时的解包边界问题（P37）。 */
const hasOverview = computed(() => overview.value !== null)
const loading = ref(false)
const error = ref<string | null>(null)
const lastLoadedAt = ref<number | null>(null)
let timer: number | null = null

const tenantId = computed(() => auth.state.tenantId || 'default')

// P40：时间窗口切换（1h / 6h / 24h / 7d）
const WINDOW_OPTIONS = [
  { value: '1h', label: '1 小时' },
  { value: '6h', label: '6 小时' },
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
] as const
const selectedWindow = ref<string>('24h')

const trend = computed(() => {
  const win = overview.value?.window
  if (!win) return null
  const delta = win.current - win.previous
  const pct = win.previous > 0 ? Math.round((delta / win.previous) * 100) : null
  return { delta, pct, up: delta > 0, flat: delta === 0 }
})

async function loadOverview() {
  if (loading.value) return
  loading.value = true
  error.value = null
  try {
    const fetched = await fetchTenantOverview(tenantId.value, selectedWindow.value)
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

// P41：柱状图跟随所选窗口，标签格式随桶宽变化（分钟/小时/日期）。
const bars = computed(() => {
  const rows = overview.value?.series ?? []
  const bucketSeconds = overview.value?.bucket_seconds ?? 3600
  const max = Math.max(1, ...rows.map((row) => row.count))
  return rows.map((row) => ({
    label: formatBucketLabel(row.bucket, bucketSeconds),
    count: row.count,
    height: Math.max(2, Math.round((row.count / max) * 100)),
  }))
})

/** formatBucketLabel 输出与桶宽匹配的短标签。 */
function formatBucketLabel(iso: string, bucketSeconds: number): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  const pad = (value: number) => String(value).padStart(2, '0')
  if (bucketSeconds >= 86400) return `${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const storeModeLabel = computed(() => (overview.value?.health.store_mode === 'postgres' ? 'PostgreSQL' : '内存'))

function formatTime(ts: number | null): string {
  if (!ts) return '—'
  const date = new Date(ts)
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}:${String(date.getSeconds()).padStart(2, '0')}`
}

watch(selectedWindow, () => void loadOverview())

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
        <div class="overview-windows" role="group" aria-label="时间窗口">
          <button
            v-for="option in WINDOW_OPTIONS"
            :key="option.value"
            class="overview-window-btn"
            :class="{ active: selectedWindow === option.value }"
            @click="selectedWindow = option.value"
          >
            {{ option.label }}
          </button>
        </div>
        <span class="panel-hint">{{ formatTime(lastLoadedAt) }}</span>
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
            <span class="overview-label">事件（{{ selectedWindow }}）</span>
            <strong class="overview-value">{{ overview.window.current }}</strong>
            <span v-if="trend" class="overview-foot" :class="{ 'trend-up': trend.up, 'trend-down': !trend.up }">
              <template v-if="trend.up">▲ +{{ trend.delta }}</template>
              <template v-else-if="trend.flat">持平</template>
              <template v-else>▼ {{ trend.delta }}</template>
              <template v-if="trend.pct !== null">（{{ trend.pct }}%）</template>
              <span class="trend-ref">vs 上期 {{ overview.window.previous }}</span>
            </span>
            <span v-else class="overview-foot">累计 {{ overview.events.total }}</span>
          </div>
          <div class="overview-kpi overview-kpi-link" role="button" tabindex="0" @click="jumpTo('orders')" @keydown.enter="jumpTo('orders')">
            <span class="overview-label">待处理工单</span>
            <strong class="overview-value">{{ overview.open_orders }}</strong>
            <span class="overview-foot">值班 / 派单状态见工单页</span>
          </div>
          <div class="overview-kpi overview-kpi-link" role="button" tabindex="0" @click="jumpTo('duty')" @keydown.enter="jumpTo('duty')">
            <span class="overview-label">巡检进行中</span>
            <strong class="overview-value">{{ overview.patrol.in_progress }}</strong>
            <span class="overview-foot">待执行 {{ overview.patrol.pending }}</span>
          </div>
          <div class="overview-kpi overview-kpi-link" role="button" tabindex="0" @click="jumpTo('tenant')" @keydown.enter="jumpTo('tenant')">
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
            <h3>事件分布（{{ WINDOW_OPTIONS.find((o) => o.value === selectedWindow)?.label }}）</h3>
            <div v-if="bars.length === 0" class="overview-empty">暂无数据</div>
            <div v-else class="overview-bars" :class="{ 'overview-bars-dense': bars.length > 20 }">
              <div v-for="(bar, index) in bars" :key="index" class="overview-bar">
                <span class="overview-bar-count">{{ bar.count || '' }}</span>
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
              <li><span>导出产物</span><strong class="mono">{{ overview.exports.artifacts }}</strong></li>
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