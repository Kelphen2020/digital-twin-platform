import { reactive } from 'vue'
import {
  apiErrorMessage,
  fetchAudit,
  fetchEscalationSettings,
  fetchNotificationSettings,
  fetchNotifications,
  saveEscalationSettings,
  saveNotificationSettings,
} from '../api'
import { pushToast } from './ui'
import type {
  AuditEntry,
  EscalationSettings,
  NotificationLogEntry,
  NotificationSettings,
} from '../types'

interface AdminState {
  notificationSettings: NotificationSettings | null
  escalationSettings: EscalationSettings | null
  settingsLoading: boolean
  settingsError: string | null
  savingNotifications: boolean
  savingEscalation: boolean
  audit: AuditEntry[]
  auditLoading: boolean
  auditError: string | null
  notifications: NotificationLogEntry[]
  notificationsLoading: boolean
  notificationsError: string | null
}

const state = reactive<AdminState>({
  notificationSettings: null,
  escalationSettings: null,
  settingsLoading: false,
  settingsError: null,
  savingNotifications: false,
  savingEscalation: false,
  audit: [],
  auditLoading: false,
  auditError: null,
  notifications: [],
  notificationsLoading: false,
  notificationsError: null,
})

/** 拉取通知 + 升级两组设置（admin）。 */
async function loadSettings(): Promise<void> {
  state.settingsLoading = true
  state.settingsError = null
  try {
    const [notifications, escalation] = await Promise.all([
      fetchNotificationSettings(),
      fetchEscalationSettings(),
    ])
    state.notificationSettings = notifications
    state.escalationSettings = escalation
  } catch (error) {
    state.settingsError = apiErrorMessage(error)
  } finally {
    state.settingsLoading = false
  }
}

/** PUT /api/settings/notifications。 */
async function saveNotifications(payload: NotificationSettings): Promise<boolean> {
  if (state.savingNotifications) return false
  state.savingNotifications = true
  try {
    state.notificationSettings = await saveNotificationSettings(payload)
    pushToast('通知设置已保存', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  } finally {
    state.savingNotifications = false
  }
}

/** PUT /api/settings/escalation。 */
async function saveEscalation(payload: EscalationSettings): Promise<boolean> {
  if (state.savingEscalation) return false
  state.savingEscalation = true
  try {
    state.escalationSettings = await saveEscalationSettings(payload)
    pushToast('告警升级设置已保存', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  } finally {
    state.savingEscalation = false
  }
}

/** GET /api/audit?limit=100。 */
async function loadAudit(limit = 100): Promise<void> {
  state.auditLoading = true
  state.auditError = null
  try {
    state.audit = await fetchAudit(limit)
  } catch (error) {
    state.auditError = apiErrorMessage(error)
  } finally {
    state.auditLoading = false
  }
}

/** GET /api/notifications?limit=50。 */
async function loadNotifications(limit = 50): Promise<void> {
  state.notificationsLoading = true
  state.notificationsError = null
  try {
    state.notifications = await fetchNotifications(limit)
  } catch (error) {
    state.notificationsError = apiErrorMessage(error)
  } finally {
    state.notificationsLoading = false
  }
}

/** 审计面板刷新：同时拉取审计与通知投递记录。 */
async function refreshAudit(): Promise<void> {
  await Promise.all([loadAudit(), loadNotifications()])
}

function reset(): void {
  state.notificationSettings = null
  state.escalationSettings = null
  state.settingsLoading = false
  state.settingsError = null
  state.savingNotifications = false
  state.savingEscalation = false
  state.audit = []
  state.auditLoading = false
  state.auditError = null
  state.notifications = []
  state.notificationsLoading = false
  state.notificationsError = null
}

export function useAdminStore() {
  return {
    state,
    loadSettings,
    saveNotifications,
    saveEscalation,
    loadAudit,
    loadNotifications,
    refreshAudit,
    reset,
  }
}
