<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { formatDateTime } from '../labels'
import { useAdminStore } from '../store/admin'
import type { AuditEntry, NotificationLogEntry } from '../types'

const { state, refreshAudit } = useAdminStore()

type AuditSubTab = 'audit' | 'notifications'

const subTab = ref<AuditSubTab>('audit')

const auditCount = computed(() => state.audit.length)
const notificationCount = computed(() => state.notifications.length)

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
        <p v-if="state.auditLoading && state.audit.length === 0" class="detail-loading">正在加载审计日志…</p>
        <p v-else-if="state.auditError" class="form-error">
          审计加载失败：{{ state.auditError }}
          <button class="link-btn" @click="refreshAudit">重试</button>
        </p>
        <div v-else-if="state.audit.length === 0" class="feed-empty">
          <p>暂无审计记录</p>
          <span>用户写操作（事件处置、相机/规则/工单/设置变更）会记录在这里。</span>
        </div>
        <template v-else>
          <article v-for="entry in state.audit" :key="entry.id" class="audit-item">
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
