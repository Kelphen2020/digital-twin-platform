<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { apiErrorMessage, exportAuditCsv } from '../api'
import { formatDateTime } from '../labels'
import { useAdminStore } from '../store/admin'
import { useAuthStore } from '../store/auth'
import { pushToast } from '../store/ui'
import type { AuditEntry, NotificationLogEntry } from '../types'

const { state, refreshAudit } = useAdminStore()
const { role } = useAuthStore()
const isAdmin = computed(() => role.value === 'admin')

type AuditSubTab = 'audit' | 'notifications'

const subTab = ref<AuditSubTab>('audit')

const auditCount = computed(() => state.audit.length)
const notificationCount = computed(() => state.notifications.length)

// ---- P10：审计筛选 + CSV 导出 ----
const filterAction = ref('')
const filterActor = ref('')
const filterFrom = ref('')
const filterTo = ref('')
const exporting = ref(false)

const filteredAudit = computed(() => {
  const action = filterAction.value.trim().toLowerCase()
  const actor = filterActor.value.trim().toLowerCase()
  const from = filterFrom.value ? new Date(filterFrom.value).getTime() : null
  const to = filterTo.value ? new Date(filterTo.value).getTime() : null
  return state.audit.filter((entry) => {
    if (action && !entry.action.toLowerCase().includes(action)) return false
    if (actor && !entry.actor.toLowerCase().includes(actor)) return false
    const at = new Date(entry.at).getTime()
    if (from !== null && Number.isFinite(from) && at < from) return false
    if (to !== null && Number.isFinite(to) && at > to) return false
    return true
  })
})

function clearFilters(): void {
  filterAction.value = ''
  filterActor.value = ''
  filterFrom.value = ''
  filterTo.value = ''
}

function toIsoOrUndefined(value: string): string | undefined {
  if (!value) return undefined
  const timestamp = new Date(value)
  if (Number.isNaN(timestamp.getTime())) return undefined
  return timestamp.toISOString()
}

async function exportAudit(): Promise<void> {
  if (!isAdmin.value || exporting.value) return
  exporting.value = true
  try {
    const blob = await exportAuditCsv({
      from: toIsoOrUndefined(filterFrom.value),
      to: toIsoOrUndefined(filterTo.value),
      action: filterAction.value.trim() || undefined,
      actor: filterActor.value.trim() || undefined,
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = 'audit.csv'
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
    pushToast('审计 CSV 已导出（audit.csv）', 'success')
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
  } finally {
    exporting.value = false
  }
}

function formatDetail(detail: Record<string, unknown> | null): string {
  if (!detail) return ''
  try {
    const text = JSON.stringify(detail)
    return text.length > 160 ? `${text.slice(0, 160)}…` : text
  } catch {
    return ''
  }
}

function auditRow(entry: AuditEntry): string {
  return formatDetail(entry.detail)
}

function deliverySummary(entry: NotificationLogEntry): string {
  return entry.error || (entry.ok ? '投递成功' : '投递失败')
}

onMounted(() => {
  void refreshAudit()
})
</script>

<template>
  <section class="admin-panel">
    <header class="panel-head">
      <h2>审计</h2>
      <div class="panel-head-actions">
        <button class="ghost-btn" :disabled="state.auditLoading || state.notificationsLoading" @click="refreshAudit">
          刷新
        </button>
        <button
          v-if="isAdmin && subTab === 'audit'"
          class="secondary-btn"
          :disabled="exporting"
          title="按当前筛选导出 CSV（GET /api/audit/export）"
          @click="exportAudit"
        >
          {{ exporting ? '导出中…' : '导出 CSV' }}
        </button>
      </div>
    </header>

    <div class="audit-subtabs">
      <button class="range-btn" :class="{ active: subTab === 'audit' }" @click="subTab = 'audit'">
        操作审计 <span class="audit-count">{{ auditCount }}</span>
      </button>
      <button class="range-btn" :class="{ active: subTab === 'notifications' }" @click="subTab = 'notifications'">
        通知投递 <span class="audit-count">{{ notificationCount }}</span>
      </button>
    </div>

    <div class="audit-list">
      <template v-if="subTab === 'audit'">
        <div class="audit-filters">
          <input v-model="filterAction" type="text" class="audit-filter-input" placeholder="action 关键字" />
          <input v-model="filterActor" type="text" class="audit-filter-input" placeholder="actor" />
          <label class="audit-filter-time">
            <span>起</span>
            <input v-model="filterFrom" type="datetime-local" />
          </label>
          <label class="audit-filter-time">
            <span>止</span>
            <input v-model="filterTo" type="datetime-local" />
          </label>
          <button class="ghost-btn" @click="clearFilters">清除筛选</button>
          <span class="audit-filter-count">{{ filteredAudit.length }} 条</span>
        </div>
        <p v-if="state.auditLoading && state.audit.length === 0" class="detail-loading">正在加载审计日志…</p>
        <p v-else-if="state.auditError" class="form-error">
          审计加载失败：{{ state.auditError }}
          <button class="link-btn" @click="refreshAudit">重试</button>
        </p>
        <div v-else-if="filteredAudit.length === 0" class="feed-empty">
          <p>暂无匹配审计记录</p>
          <span>调整筛选条件或清除筛选后重试。</span>
        </div>
        <template v-else>
          <article v-for="entry in filteredAudit" :key="entry.id" class="audit-item">
            <div class="audit-row">
              <span class="audit-actor">{{ entry.actor }}</span>
              <span class="audit-action mono">{{ entry.action }}</span>
              <time>{{ formatDateTime(entry.at) }}</time>
            </div>
            <p class="audit-resource mono">{{ entry.resource }}</p>
            <p v-if="auditRow(entry)" class="audit-detail mono">{{ auditRow(entry) }}</p>
          </article>
        </template>
      </template>

      <template v-else>
        <p v-if="state.notificationsLoading && state.notifications.length === 0" class="detail-loading">
          正在加载通知投递记录…
        </p>
        <p v-else-if="state.notificationsError" class="form-error">
          通知记录加载失败：{{ state.notificationsError }}
          <button class="link-btn" @click="refreshAudit">重试</button>
        </p>
        <div v-else-if="state.notifications.length === 0" class="feed-empty">
          <p>暂无通知投递记录</p>
          <span>启用通知后，新事件与告警升级的 Webhook 投递结果会出现在这里。</span>
        </div>
        <template v-else>
          <article v-for="entry in state.notifications" :key="entry.id" class="audit-item">
            <div class="audit-row">
              <span :class="entry.ok ? 'ok-text' : 'warn-text'">{{ deliverySummary(entry) }}</span>
              <span class="mono">{{ entry.status_code }}</span>
              <time>{{ formatDateTime(entry.at) }}</time>
            </div>
            <p class="audit-resource mono">
              {{ entry.kind }} · {{ entry.event_id }} · {{ entry.format }}
              <span v-if="entry.attempts > 0" class="attempts-chip">尝试 {{ entry.attempts }} 次</span>
            </p>
            <p class="audit-detail mono">{{ entry.url }}</p>
            <p v-if="entry.error" class="form-error">{{ entry.error }}</p>
          </article>
        </template>
      </template>
    </div>
  </section>
</template>
