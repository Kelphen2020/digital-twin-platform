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
import type { EventType, ReplayFeedItem, Severity, TwinEvent } from '../types'

type FeedEntry =
  | { kind: 'live'; key: string; event: TwinEvent }
  | { kind: 'replay'; key: string; event: TwinEvent; replay: ReplayFeedItem }

const { can } = useAuthStore()
const canHandle = computed(() => can('event:handle'))

const {
  state,
  filteredEvents,
  focusEvent,
  openReplayEvent,
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
const replayCount = computed(() => state.replayEvents.length)

function matchesFilters(event: TwinEvent): boolean {
  return (
    (state.filters.type === 'all' || event.type === state.filters.type) &&
    (state.filters.severity === 'all' || event.severity === state.filters.severity)
  )
}

/** 回放条目在前（最新回放优先），实时事件在后；回放不进入实时事件/三维标记。 */
const feedItems = computed<FeedEntry[]>(() => {
  const replayEntries: FeedEntry[] = state.replayEvents
    .filter((item) => matchesFilters(item.event))
    .map((item) => ({ kind: 'replay', key: item.key, event: item.event, replay: item }))
  const liveEntries: FeedEntry[] = filteredEvents.value.map((event) => ({ kind: 'live', key: event.id, event }))
  return [...replayEntries, ...liveEntries]
})

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

function onItemClick(entry: FeedEntry): void {
  if (entry.kind === 'replay') openReplayEvent(entry.replay)
  else focusEvent(entry.event.id)
}

// 场景中点击事件标记/点击回放条目后，把对应条目滚动到可视区域
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
        <span v-if="replayCount > 0" class="replay-count" title="总线回放条目（不视为新告警）">
          回放 {{ replayCount }}
        </span>
        <span class="feed-count">
          {{ feedItems.length
          }}<template v-if="isFiltered"> / {{ state.events.length + replayCount }}</template>
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
      <div v-if="feedItems.length === 0" class="feed-empty">
        <p>
          {{ state.events.length === 0 && replayCount === 0 ? (state.backendOnline ? '暂无事件' : '等待后端连接…') : '当前筛选条件下无事件' }}
        </p>
        <span>
          {{
            state.events.length === 0 && replayCount === 0
              ? '模拟器产生事件后将实时出现在这里，并在三维场景中上图。'
              : '调整类型 / 等级筛选查看其他事件；总线回放条目会带「回放」徽标。'
          }}
        </span>
      </div>

      <article
        v-for="item in feedItems"
        :key="item.key"
        class="event-item"
        :class="[
          `sev-${item.event.severity}`,
          {
            selected: item.event.id === state.selectedEventId,
            closed: item.event.status === 'closed',
            acked: item.event.status === 'acked',
            replay: item.kind === 'replay',
          },
        ]"
        :data-event-id="item.event.id"
        @click="onItemClick(item)"
      >
        <span class="severity-bar" :style="{ background: SEVERITY_COLORS[item.event.severity] }" />
        <div class="event-main">
          <div class="event-head">
            <span class="event-type">{{ EVENT_TYPE_LABELS[item.event.type] }}</span>
            <span class="event-severity" :style="{ color: SEVERITY_COLORS[item.event.severity] }">
              {{ SEVERITY_LABELS[item.event.severity] }}
            </span>
            <span
              class="status-chip-sm"
              :style="{ color: EVENT_STATUS_COLORS[item.event.status], borderColor: EVENT_STATUS_COLORS[item.event.status] }"
            >
              {{ EVENT_STATUS_LABELS[item.event.status] }}
            </span>
            <span v-if="item.kind === 'replay'" class="replay-chip" title="总线回放事件（不视为新告警）">
              回放 · offset {{ item.replay.offset }}
            </span>
            <span v-if="item.event.escalated_at" class="escalation-chip" title="超过处置时限，已由系统升级">已升级</span>
            <span
              v-if="item.event.re_escalated_at"
              class="re-escalation-chip"
              title="升级后超时仍未关闭，已二次升级"
            >
              再升级
            </span>
            <time>{{ formatRelativeTime(item.event.occurred_at, nowTick) }}</time>
          </div>
          <p class="event-camera">{{ item.event.camera_code }} · {{ item.event.camera_name }}</p>
          <p class="event-meta">
            置信度 {{ (item.event.confidence * 100).toFixed(0) }}% · {{ formatDateTime(item.event.occurred_at) }}
          </p>
        </div>
        <button
          v-if="item.kind === 'live'"
          class="ack-btn"
          :class="{ done: item.event.status !== 'new' || !canHandle }"
          :disabled="!canHandle || item.event.status !== 'new' || pendingIds.has(item.event.id)"
          :title="canHandle ? '' : '观察员无处置权限'"
          @click.stop="acknowledgeEvent(item.event.id)"
        >
          {{ ackLabel(item.event) }}
        </button>
        <span v-else class="replay-flag" title="回放条目不可处置">历史</span>
      </article>
    </div>

    <footer class="feed-foot">点击事件查看处置详情并定位相机 · 回放条目来自事件总线，不计入实时告警</footer>
  </section>
</template>
