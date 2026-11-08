import { reactive } from 'vue'
import {
  apiErrorMessage,
  createUser as requestCreateUser,
  deleteUser as requestDeleteUser,
  fetchAudit,
  fetchBusStatus,
  fetchEscalationSettings,
  fetchNotificationSettings,
  fetchNotifications,
  replayBus as requestReplayBus,
  fetchUsers,
  saveEscalationSettings,
  saveNotificationSettings,
  updateUser as requestUpdateUser,
} from '../api'
import { pushToast } from './ui'
import type {
  AuditEntry,
  BusReplayRequest,
  CreateUserPayload,
  ManagedUser,
  BusReplayResponse,
  BusStatus,
  EscalationSettings,
  NotificationLogEntry,
  NotificationSettings,
  UpdateUserPayload,
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
  // ---- P5：事件总线 ----
  busStatus: BusStatus | null
  busStatusLoading: boolean
  busStatusError: string | null
  busReplayPending: boolean
  busReplayResult: BusReplayResponse | null
  busReplayError: string | null
  // ---- P7：用户管理 ----
  users: ManagedUser[]
  usersLoading: boolean
  usersError: string | null
  userSaving: boolean
  userDeletingId: string | null
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
  busStatus: null,
  busStatusLoading: false,
  busStatusError: null,
  busReplayPending: false,
  busReplayResult: null,
  busReplayError: null,
  users: [],
  usersLoading: false,
  usersError: null,
  userSaving: false,
  userDeletingId: null,
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

/** GET /api/users（admin）。 */
async function loadUsers(): Promise<void> {
  state.usersLoading = true
  state.usersError = null
  try {
    state.users = await fetchUsers()
  } catch (error) {
    state.usersError = apiErrorMessage(error)
  } finally {
    state.usersLoading = false
  }
}

/** POST /api/users（admin）→ 新建用户；409 username_taken / 400 校验错误自动 Toast。 */
async function createManagedUser(payload: CreateUserPayload): Promise<ManagedUser | null> {
  if (state.userSaving) return null
  state.userSaving = true
  try {
    const user = await requestCreateUser(payload)
    state.users = [...state.users, user]
    pushToast('用户已创建', 'success')
    return user
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.userSaving = false
  }
}

/** PATCH /api/users/{id}（admin）→ 修改用户；400 self_protected 自动 Toast。 */
async function updateManagedUser(userId: string, payload: UpdateUserPayload): Promise<ManagedUser | null> {
  if (state.userSaving) return null
  state.userSaving = true
  try {
    const user = await requestUpdateUser(userId, payload)
    state.users = state.users.map((item) => (item.id === user.id ? user : item))
    pushToast('用户已更新', 'success')
    return user
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.userSaving = false
  }
}

/** DELETE /api/users/{id}（admin）→ 删除用户；不能删除自己（400 self_protected）。 */
async function deleteManagedUser(userId: string): Promise<boolean> {
  if (state.userDeletingId) return false
  state.userDeletingId = userId
  try {
    await requestDeleteUser(userId)
    state.users = state.users.filter((item) => item.id !== userId)
    pushToast('用户已删除', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  } finally {
    state.userDeletingId = null
  }
}

/** GET /api/bus/status（admin）。 */
async function loadBusStatus(): Promise<void> {
  state.busStatusLoading = true
  state.busStatusError = null
  try {
    state.busStatus = await fetchBusStatus()
  } catch (error) {
    state.busStatusError = apiErrorMessage(error)
  } finally {
    state.busStatusLoading = false
  }
}

/** POST /api/bus/replay（admin）；400/409 通过 Toast 提示。 */
async function replayBus(payload: BusReplayRequest): Promise<BusReplayResponse | null> {
  if (state.busReplayPending) return null
  state.busReplayPending = true
  state.busReplayError = null
  state.busReplayResult = null
  try {
    const result = await requestReplayBus(payload)
    state.busReplayResult = result
    pushToast(`回放完成：${result.replayed} 条（offset ${result.from_offset}–${result.to_offset}）`, 'success')
    await loadBusStatus()
    return result
  } catch (error) {
    const message = apiErrorMessage(error)
    state.busReplayError = message
    pushToast(message, 'error')
    return null
  } finally {
    state.busReplayPending = false
  }
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
  state.busStatus = null
  state.busStatusLoading = false
  state.busStatusError = null
  state.busReplayPending = false
  state.busReplayResult = null
  state.busReplayError = null
  state.users = []
  state.usersLoading = false
  state.usersError = null
  state.userSaving = false
  state.userDeletingId = null
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
    loadBusStatus,
    replayBus,
    loadUsers,
    createManagedUser,
    updateManagedUser,
    deleteManagedUser,
    reset,
  }
}
