<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  EVENT_STATUS_COLORS,
  EVENT_STATUS_LABELS,
  EVENT_TYPE_LABELS,
  EVENT_TYPE_ORDER,
  SEVERITY_COLORS,
  SEVERITY_LABELS,
  SEVERITY_ORDER,
  formatDateTime,
  formatRelativeTime,
} from '../labels'
import { useAuthStore } from '../store/auth'
import { useTwinStore } from '../store/twin'
import type { EventType, Severity } from '../types'

const { can } = useAuthStore()
const canHandle = computed(() => can('event:handle'))

const {
  state,
  filteredEvents,
  focusEvent,
  acknowledgeEvent,
  clearAckError,
  clearExportError,
  downloadEventsCsv,
  setTypeFilter,
  setSeverityFilter,
} = useTwinStore()

const listRef = ref<HTMLDivElement | null>(null)
const nowTick = ref(Date.now())
let clockTimer: number | null = null

onMounted(() => {
  // 相对时间只需要低频刷新
  clockTimer = window.setInterval(() => {
    nowTick.value = Date.now()
  }, 10_000)
})
onBeforeUnmount(() => {
  if (clockTimer !== null) window.clearInterval(clockTimer)
})

const typeOptions: { value: EventType | 'all'; label: string }[] = [
  { value: 'all', label: '全部类型' },
  ...EVENT_TYPE_ORDER.map((type) => ({ value: type, label: EVENT_TYPE_LABELS[type] })),
]

const severityOptions: { value: Severity | 'all'; label: string }[] = [
  { value: 'all', label: '全部等级' },
  ...SEVERITY_ORDER.map((severity) => ({ value: severity, label: SEVERITY_LABELS[severity] })),
]

const pendingIds = computed(() => new Set(state.ackPendingIds))
const isFiltered = computed(() => state.filters.type !== 'all' || state.filters.severity !== 'all')

function onTypeChange(event: Event): void {
  setTypeFilter((event.target as HTMLSelectElement).value as EventType | 'all')
}

function onSeverityChange(event: Event): void {
  setSeverityFilter((event.target as HTMLSelectElement).value as Severity | 'all')
}

function ackLabel(event: { status: string; id: string }): string {
  if (!canHandle.value) return '只读'
  if (pendingIds.value.has(event.id)) return '确认中…'
  if (event.status === 'closed') return '已关闭'
  if (event.status === 'acked') return '已确认'
  return '确认'
}

// 场景中点击事件标记后，把对应条目滚动到可视区域
watch(
  () => state.selectedEventId,
  async (eventId) => {
    if (!eventId) return
    await nextTick()
    const item = listRef.value?.querySelector<HTMLElement>(`[data-event-id="${CSS.escape(eventId)}"]`)
    item?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  },
)
</script>

<template>
  <section class="event-feed">
    <header class="panel-head">
      <h2>实时事件</h2>
      <div class="panel-head-actions">
        <span class="feed-count">
          {{ filteredEvents.length }}<template v-if="isFiltered"> / {{ state.events.length }}</template>
        </span>
        <button class="ghost-btn" :disabled="state.exportError !== null" @click="downloadEventsCsv">导出 CSV</button>
      </div>
    </header>

    <div class="feed-filters">
      <label>
        <span>类型</span>
        <select :value="state.filters.type" @change="onTypeChange">
          <option v-for="option in typeOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </label>
      <label>
        <span>等级</span>
        <select :value="state.filters.severity" @change="onSeverityChange">
          <option v-for="option in severityOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </option>
        </select>
      </label>
    </div>

    <div v-if="state.ackError" class="feed-error">
      <span>确认失败：{{ state.ackError }}</span>
      <button class="ghost-btn" @click="clearAckError">关闭</button>
    </div>
    <div v-if="state.exportError" class="feed-error">
      <span>CSV 导出失败：{{ state.exportError }}</span>
      <button class="ghost-btn" @click="clearExportError">关闭</button>
    </div>

    <div ref="listRef" class="feed-list">
      <div v-if="filteredEvents.length === 0" class="feed-empty">
        <p>
          {{ state.events.length === 0 ? (state.backendOnline ? '暂无事件' : '等待后端连接…') : '当前筛选条件下无事件' }}
        </p>
        <span>
          {{
            state.events.length === 0
              ? '模拟器产生事件后将实时出现在这里，并在三维场景中上图。'
              : '调整类型 / 等级筛选查看其他事件。'
          }}
        </span>
      </div>

      <article
        v-for="event in filteredEvents"
        :key="event.id"
        class="event-item"
        :class="[
          `sev-${event.severity}`,
          { selected: event.id === state.selectedEventId, closed: event.status === 'closed', acked: event.status === 'acked' },
        ]"
        :data-event-id="event.id"
        @click="focusEvent(event.id)"
      >
        <span class="severity-bar" :style="{ background: SEVERITY_COLORS[event.severity] }" />
        <div class="event-main">
          <div class="event-head">
            <span class="event-type">{{ EVENT_TYPE_LABELS[event.type] }}</span>
            <span class="event-severity" :style="{ color: SEVERITY_COLORS[event.severity] }">
              {{ SEVERITY_LABELS[event.severity] }}
            </span>
            <span
              class="status-chip-sm"
              :style="{ color: EVENT_STATUS_COLORS[event.status], borderColor: EVENT_STATUS_COLORS[event.status] }"
            >
              {{ EVENT_STATUS_LABELS[event.status] }}
            </span>
            <span v-if="event.escalated_at" class="escalation-chip" title="超过处置时限，已由系统升级">已升级</span>
            <time>{{ formatRelativeTime(event.occurred_at, nowTick) }}</time>
          </div>
          <p class="event-camera">{{ event.camera_code }} · {{ event.camera_name }}</p>
          <p class="event-meta">
            置信度 {{ (event.confidence * 100).toFixed(0) }}% · {{ formatDateTime(event.occurred_at) }}
          </p>
        </div>
        <button
          class="ack-btn"
          :class="{ done: event.status !== 'new' || !canHandle }"
          :disabled="!canHandle || event.status !== 'new' || pendingIds.has(event.id)"
          :title="canHandle ? '' : '观察员无处置权限'"
          @click.stop="acknowledgeEvent(event.id)"
        >
          {{ ackLabel(event) }}
        </button>
      </article>
    </div>

    <footer class="feed-foot">点击事件查看处置详情并定位相机 · 三维标记约 60 秒后自动淡出</footer>
  </section>
</template>
