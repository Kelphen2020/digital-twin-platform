<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { SEVERITY_LABELS, SEVERITY_ORDER } from '../labels'
import { useAdminStore } from '../store/admin'
import type { EscalationSettings, NotificationFormat, NotificationSettings, Severity } from '../types'

const { state, loadSettings, saveNotifications, saveEscalation } = useAdminStore()

const notificationForm = reactive<NotificationSettings>({
  enabled: false,
  webhook_url: '',
  format: 'generic',
  min_severity: 'warn',
  timeout_seconds: 5,
})

const escalationForm = reactive<EscalationSettings>({
  enabled: true,
  after_seconds: 300,
  severities: ['warn', 'critical'],
})

const notificationError = ref('')
const notificationSaved = ref(false)
const escalationError = ref('')
const escalationSaved = ref(false)

const formatOptions: { value: NotificationFormat; label: string }[] = [
  { value: 'generic', label: '通用 JSON（generic）' },
  { value: 'feishu', label: '飞书机器人（feishu）' },
]

const severityOptions = computed(() => SEVERITY_ORDER.map((severity) => ({ value: severity, label: SEVERITY_LABELS[severity] })))

// 设置加载/保存完成后同步本地表单
watch(
  () => state.notificationSettings,
  (settings) => {
    if (settings) Object.assign(notificationForm, settings)
  },
  { immediate: true },
)
watch(
  () => state.escalationSettings,
  (settings) => {
    if (!settings) return
    escalationForm.enabled = settings.enabled
    escalationForm.after_seconds = settings.after_seconds
    escalationForm.severities = [...settings.severities]
  },
  { immediate: true },
)

function toggleSeverity(severity: Severity): void {
  escalationForm.severities = escalationForm.severities.includes(severity)
    ? escalationForm.severities.filter((item) => item !== severity)
    : [...escalationForm.severities, severity]
}

function validateNotifications(): string | null {
  const url = notificationForm.webhook_url.trim()
  if (notificationForm.enabled && !url) return '启用通知时必须填写 Webhook URL'
  if (url && !/^https?:\/\//i.test(url)) return 'Webhook URL 需为 http/https 地址'
  if (!Number.isFinite(notificationForm.timeout_seconds) || notificationForm.timeout_seconds < 1 || notificationForm.timeout_seconds > 30) {
    return '超时时间需在 1–30 秒之间'
  }
  return null
}

function validateEscalation(): string | null {
  if (!Number.isFinite(escalationForm.after_seconds) || escalationForm.after_seconds < 0 || escalationForm.after_seconds > 86400) {
    return '升级时限需在 0–86400 秒之间'
  }
  return null
}

async function submitNotifications(): Promise<void> {
  notificationError.value = ''
  notificationSaved.value = false
  const validationError = validateNotifications()
  if (validationError) {
    notificationError.value = validationError
    return
  }
  const ok = await saveNotifications({
    enabled: notificationForm.enabled,
    webhook_url: notificationForm.webhook_url.trim(),
    format: notificationForm.format,
    min_severity: notificationForm.min_severity,
    timeout_seconds: notificationForm.timeout_seconds,
  })
  notificationSaved.value = ok
}

async function submitEscalation(): Promise<void> {
  escalationError.value = ''
  escalationSaved.value = false
  const validationError = validateEscalation()
  if (validationError) {
    escalationError.value = validationError
    return
  }
  const ok = await saveEscalation({
    enabled: escalationForm.enabled,
    after_seconds: escalationForm.after_seconds,
    severities: [...escalationForm.severities],
  })
  escalationSaved.value = ok
}

onMounted(() => {
  void loadSettings()
})
</script>

<template>
  <section class="admin-panel">
    <header class="panel-head">
      <h2>平台设置</h2>
      <div class="panel-head-actions">
        <button class="ghost-btn" :disabled="state.settingsLoading" @click="loadSettings">刷新</button>
      </div>
    </header>

    <div class="admin-scroll">
      <p v-if="state.settingsLoading && !state.notificationSettings" class="detail-loading">正在加载设置…</p>
      <p v-else-if="state.settingsError" class="form-error">
        设置加载失败：{{ state.settingsError }}
        <button class="link-btn" @click="loadSettings">重试</button>
      </p>

      <template v-else>
        <section class="settings-section">
          <header class="settings-head">
            <h3>通知（Webhook）</h3>
            <label class="switch">
              <input v-model="notificationForm.enabled" type="checkbox" />
              <span>启用</span>
            </label>
          </header>

          <label class="settings-field">
            <span>Webhook URL</span>
            <input v-model="notificationForm.webhook_url" type="url" placeholder="http://127.0.0.1:8099/hook" />
          </label>

          <div class="settings-grid">
            <label class="settings-field">
              <span>格式</span>
              <select v-model="notificationForm.format">
                <option v-for="option in formatOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
            <label class="settings-field">
              <span>最低严重级别</span>
              <select v-model="notificationForm.min_severity">
                <option v-for="option in severityOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
            <label class="settings-field">
              <span>超时（秒，1–30）</span>
              <input v-model.number="notificationForm.timeout_seconds" type="number" min="1" max="30" step="1" />
            </label>
          </div>

          <p class="form-hint">新事件（严重级别 ≥ 最低级别）与告警升级都会尝试推送；投递结果见「审计 → 通知投递」。</p>
          <p v-if="notificationError" class="form-error">{{ notificationError }}</p>
          <p v-if="notificationSaved" class="form-ok">通知设置已保存</p>

          <div class="settings-actions">
            <button class="primary-btn" :disabled="state.savingNotifications" @click="submitNotifications">
              {{ state.savingNotifications ? '保存中…' : '保存通知设置' }}
            </button>
          </div>
        </section>

        <section class="settings-section">
          <header class="settings-head">
            <h3>告警升级</h3>
            <label class="switch">
              <input v-model="escalationForm.enabled" type="checkbox" />
              <span>启用</span>
            </label>
          </header>

          <label class="settings-field">
            <span>超时未处置升级时限（秒，0–86400）</span>
            <input v-model.number="escalationForm.after_seconds" type="number" min="0" max="86400" step="10" />
          </label>

          <div class="settings-field">
            <span>适用严重级别</span>
            <div class="rules-classes">
              <label v-for="severity in SEVERITY_ORDER" :key="severity" class="rules-class">
                <input
                  type="checkbox"
                  :checked="escalationForm.severities.includes(severity)"
                  @change="toggleSeverity(severity)"
                />
                <span>{{ SEVERITY_LABELS[severity] }}</span>
              </label>
            </div>
          </div>

          <p class="form-hint">
            服务端每 15 秒扫描一次：满足条件的事件写入 escalated_at、WS 广播 event_updated 并触发一次升级通知，前端显示「已升级」标记。
          </p>
          <p v-if="escalationError" class="form-error">{{ escalationError }}</p>
          <p v-if="escalationSaved" class="form-ok">升级设置已保存</p>

          <div class="settings-actions">
            <button class="primary-btn" :disabled="state.savingEscalation" @click="submitEscalation">
              {{ state.savingEscalation ? '保存中…' : '保存升级设置' }}
            </button>
          </div>
        </section>
      </template>
    </div>
  </section>
</template>
