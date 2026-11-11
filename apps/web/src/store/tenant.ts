import { reactive } from 'vue'
import {
  apiErrorMessage,
  archiveTenant as requestArchiveTenant,
  createTenant as requestCreateTenant,
  deleteTenant as requestDeleteTenant,
  fetchTenants,
  restoreTenant as requestRestoreTenant,
  updateTenant as requestUpdateTenant,
} from '../api'
import type {
  Tenant,
  TenantCreatePayload,
  TenantLimits,
  TenantSeeded,
  TenantUpdatePayload,
} from '../types'
import { pushToast } from './ui'

interface TenantState {
  tenants: Tenant[]
  loading: boolean
  error: string | null
  saving: boolean
  updatingId: string | null
}

const state = reactive<TenantState>({
  tenants: [],
  loading: false,
  error: null,
  saving: false,
  updatingId: null,
})

async function loadTenants(): Promise<void> {
  state.loading = true
  state.error = null
  try {
    state.tenants = await fetchTenants()
  } catch (error) {
    state.error = apiErrorMessage(error)
  } finally {
    state.loading = false
  }
}

function seededSummary(seeded: TenantSeeded | undefined): string {
  if (!seeded) return '租户已创建'
  const parts: string[] = []
  if (seeded.cameras > 0) parts.push(`已复制 ${seeded.cameras} 台相机`)
  if (seeded.areas > 0) parts.push(`${seeded.areas} 个区域`)
  if (seeded.rules > 0) parts.push(`${seeded.rules} 条规则`)
  if (seeded.settings > 0) parts.push(`${seeded.settings} 项设置`)
  if (seeded.admin) parts.push(`管理员 ${seeded.admin}`)
  return parts.length > 0 ? `租户已创建：${parts.join(' / ')}` : '租户已创建'
}

async function createTenant(payload: TenantCreatePayload): Promise<Tenant | null> {
  if (state.saving) return null
  state.saving = true
  try {
    const tenant = await requestCreateTenant(payload)
    state.tenants = [...state.tenants, tenant]
    pushToast(seededSummary(tenant.seeded), 'success')
    return tenant
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.saving = false
  }
}

async function updateTenant(tenantId: string, payload: TenantUpdatePayload): Promise<Tenant | null> {
  if (state.updatingId) return null
  state.updatingId = tenantId
  try {
    const tenant = await requestUpdateTenant(tenantId, payload)
    state.tenants = state.tenants.map((item) => (item.id === tenant.id ? tenant : item))
    pushToast('租户已更新', 'success')
    return tenant
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.updatingId = null
  }
}

async function archiveTenant(tenantId: string): Promise<Tenant | null> {
  if (state.updatingId) return null
  state.updatingId = tenantId
  try {
    const tenant = await requestArchiveTenant(tenantId)
    state.tenants = state.tenants.map((item) => (item.id === tenant.id ? tenant : item))
    pushToast('租户已归档', 'success')
    return tenant
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.updatingId = null
  }
}

async function restoreTenant(tenantId: string): Promise<Tenant | null> {
  if (state.updatingId) return null
  state.updatingId = tenantId
  try {
    const tenant = await requestRestoreTenant(tenantId)
    state.tenants = state.tenants.map((item) => (item.id === tenant.id ? tenant : item))
    pushToast('租户已恢复', 'success')
    return tenant
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.updatingId = null
  }
}

const DELETE_LABELS: Record<string, string> = {
  cameras: '相机',
  areas: '区域',
  rules: '规则',
  camera_rules: '规则',
  settings: '设置',
  events: '事件',
  event_actions: '处置动作',
  orders: '工单',
  tracks: '轨迹',
  audit_logs: '审计日志',
  audit: '审计日志',
  notifications: '通知日志',
  notification_logs: '通知日志',
  duty_groups: '值班组',
  patrol_routes: '巡检路线',
  patrol_schedules: '巡检计划',
  patrol_executions: '巡检执行',
  refresh_tokens: '刷新令牌',
  users: '用户',
}

async function deleteTenant(tenantId: string): Promise<boolean> {
  if (state.updatingId) return false
  state.updatingId = tenantId
  try {
    const result = await requestDeleteTenant(tenantId)
    state.tenants = state.tenants.filter((item) => item.id !== tenantId)
    const counts = Object.entries(result.deleted ?? {})
      .filter(([, count]) => count > 0)
      .map(([key, count]) => `${DELETE_LABELS[key] ?? key} ${count}`)
      .join(' / ')
    pushToast(counts ? `租户已删除：${counts}` : '租户已删除', 'success')
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  } finally {
    state.updatingId = null
  }
}

async function updateTenantLimits(tenantId: string, limits: TenantLimits): Promise<Tenant | null> {
  if (state.updatingId) return null
  state.updatingId = tenantId
  try {
    const tenant = await requestUpdateTenant(tenantId, { limits })
    state.tenants = state.tenants.map((item) => (item.id === tenant.id ? tenant : item))
    pushToast('租户配额已更新', 'success')
    return tenant
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  } finally {
    state.updatingId = null
  }
}

function reset(): void {
  state.tenants = []
  state.loading = false
  state.error = null
  state.saving = false
  state.updatingId = null
}

export function useTenantStore() {
  return {
    state,
    loadTenants,
    createTenant,
    updateTenant,
    archiveTenant,
    restoreTenant,
    deleteTenant,
    updateTenantLimits,
    reset,
  }
}
