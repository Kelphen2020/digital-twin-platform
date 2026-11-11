import { reactive } from 'vue'
import {
  apiErrorMessage,
  createTenant as requestCreateTenant,
  fetchTenants,
  updateTenant as requestUpdateTenant,
} from '../api'
import type { Tenant, TenantCreatePayload, TenantSeeded, TenantUpdatePayload } from '../types'
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

function reset(): void {
  state.tenants = []
  state.loading = false
  state.error = null
  state.saving = false
  state.updatingId = null
}

export function useTenantStore() {
  return { state, loadTenants, createTenant, updateTenant, reset }
}
