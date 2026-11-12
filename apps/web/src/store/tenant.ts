import { reactive } from 'vue'
import {
  apiErrorMessage,
  archiveTenant as requestArchiveTenant,
  createTenant as requestCreateTenant,
  deleteTenant as requestDeleteTenant,
  createExportJob as requestCreateExportJob,
  deleteExportJob as requestDeleteExportJob,
  downloadExportJob as requestDownloadExportJob,
  fetchExportJobs as requestFetchExportJobs,
  fetchTenants,
  restoreTenant as requestRestoreTenant,
  updateTenant as requestUpdateTenant,
} from '../api'
import type {
  Tenant,
  TenantCreatePayload,
  ExportJob,
  TenantLimits,
  TenantSeeded,
  TenantUpdatePayload,
} from '../types'
import { pushToast } from './ui'

interface TenantState {
  tenants: Tenant[]
  exportJobs: Record<string, ExportJob[]>
  loading: boolean
  error: string | null
  saving: boolean
  updatingId: string | null
}

const state = reactive<TenantState>({
  tenants: [],
  exportJobs: {},
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

// startExport creates an asynchronous export job (P32) and tracks it locally.
async function startExport(
  tenantId: string,
  options: { includeTracks?: boolean; includeAudit?: boolean } = {},
): Promise<ExportJob | null> {
  try {
    const job = await requestCreateExportJob(tenantId, options)
    state.exportJobs[tenantId] = [job, ...(state.exportJobs[tenantId] ?? [])]
    pushToast('导出任务已创建，正在后台生成…', 'info')
    return job
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return null
  }
}

// loadExportJobs refreshes one tenant's job list (newest first).
async function loadExportJobs(tenantId: string): Promise<void> {
  try {
    state.exportJobs[tenantId] = await requestFetchExportJobs(tenantId)
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
  }
}

// applyExportJob merges a job frame pushed over WS.
function applyExportJob(job: ExportJob): void {
  const list = state.exportJobs[job.tenant_id] ?? []
  const index = list.findIndex((item) => item.id === job.id)
  if (index >= 0) list[index] = { ...list[index], ...job }
  else list.unshift(job)
  state.exportJobs[job.tenant_id] = list
  if (job.status === 'done') pushToast(`数据集已生成，可下载（${formatBytes(job.size_bytes)}）`, 'success')
  if (job.status === 'failed') pushToast(`导出失败：${job.error}`, 'error')
}

async function downloadExport(tenantId: string, job: ExportJob): Promise<void> {
  try {
    const blob = await requestDownloadExportJob(tenantId, job.id)
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `tenant-${tenantId}-${job.id}.json`
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
  }
}

async function removeExport(tenantId: string, job: ExportJob): Promise<void> {
  try {
    await requestDeleteExportJob(tenantId, job.id)
    state.exportJobs[tenantId] = (state.exportJobs[tenantId] ?? []).filter((item) => item.id !== job.id)
    pushToast('导出任务已删除', 'success')
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
  }
}

function formatBytes(size: number): string {
  if (size <= 0) return '0 B'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(1)} MB`
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
    startExport,
    loadExportJobs,
    applyExportJob,
    downloadExport,
    removeExport,
    restoreTenant,
    deleteTenant,
    updateTenantLimits,
    reset,
  }
}
