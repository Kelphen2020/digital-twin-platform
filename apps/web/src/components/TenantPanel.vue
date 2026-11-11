<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useTenantStore } from '../store/tenant'
import type { Tenant, TenantLimits } from '../types'

const {
  state,
  loadTenants,
  createTenant,
  updateTenant,
  archiveTenant,
  restoreTenant,
  deleteTenant,
  updateTenantLimits,
} = useTenantStore()

// ---- 新建租户（P29：模板复制 + 初始管理员） ----
const createOpen = ref(false)
const createName = ref('')
const createTemplate = ref('')
const adminUsername = ref('')
const adminPassword = ref('')
const createError = ref('')
const adminPasswordLongEnough = computed(() => adminPassword.value.length >= 10)
const adminPasswordHasLetter = computed(() => /[A-Za-z]/.test(adminPassword.value))
const adminPasswordHasDigit = computed(() => /\d/.test(adminPassword.value))
const templateName = computed(
  () => state.tenants.find((tenant) => tenant.id === createTemplate.value)?.name ?? '',
)

// ---- 归档 / 恢复 / 删除 / 配额（P30） ----
const confirmingArchiveId = ref<string | null>(null)
const confirmingRestoreId = ref<string | null>(null)
const deleteDialog = ref<Tenant | null>(null)
const deleteConfirmId = ref('')
const deleteError = ref('')
const quotaDialog = ref<Tenant | null>(null)
const quotaError = ref('')
const quotaForm = reactive<TenantLimits>({ max_cameras: 0, max_users: 0, max_events: 0, ingest_qps: 0 })

function limitsSummary(tenant: Tenant): string {
  const limits = tenant.limits
  if (!limits) return '未限制'
  const parts: string[] = []
  if (limits.max_cameras) parts.push(`相机 ${limits.max_cameras}`)
  if (limits.max_users) parts.push(`用户 ${limits.max_users}`)
  if (limits.max_events) parts.push(`事件 ${limits.max_events}`)
  if (limits.ingest_qps) parts.push(`ingest ${limits.ingest_qps} QPS`)
  return parts.length > 0 ? parts.join(' / ') : '未限制'
}

function requestArchive(tenant: Tenant): void {
  if (isDefaultTenant(tenant) || tenant.archived) return
  confirmingArchiveId.value = tenant.id
}

async function confirmArchive(tenant: Tenant): Promise<void> {
  confirmingArchiveId.value = null
  await archiveTenant(tenant.id)
}

function requestRestore(tenant: Tenant): void {
  if (!tenant.archived) return
  confirmingRestoreId.value = tenant.id
}

async function confirmRestore(tenant: Tenant): Promise<void> {
  confirmingRestoreId.value = null
  await restoreTenant(tenant.id)
}

function openDelete(tenant: Tenant): void {
  if (!tenant.archived || isDefaultTenant(tenant)) return
  deleteDialog.value = tenant
  deleteConfirmId.value = ''
  deleteError.value = ''
}

function closeDelete(): void {
  deleteDialog.value = null
  deleteConfirmId.value = ''
  deleteError.value = ''
}

async function confirmDelete(): Promise<void> {
  const tenant = deleteDialog.value
  if (!tenant) return
  if (deleteConfirmId.value !== tenant.id) {
    deleteError.value = '请输入完整租户 ID 以确认删除'
    return
  }
  deleteError.value = ''
  const ok = await deleteTenant(tenant.id)
  if (ok) closeDelete()
}

function openQuota(tenant: Tenant): void {
  quotaDialog.value = tenant
  quotaError.value = ''
  const limits = tenant.limits ?? {}
  quotaForm.max_cameras = limits.max_cameras ?? 0
  quotaForm.max_users = limits.max_users ?? 0
  quotaForm.max_events = limits.max_events ?? 0
  quotaForm.ingest_qps = limits.ingest_qps ?? 0
}

function closeQuota(): void {
  quotaDialog.value = null
  quotaError.value = ''
}

async function submitQuota(): Promise<void> {
  const tenant = quotaDialog.value
  if (!tenant) return
  const values = [quotaForm.max_cameras, quotaForm.max_users, quotaForm.max_events, quotaForm.ingest_qps]
  if (values.some((value) => !Number.isFinite(value) || value < 0 || !Number.isInteger(value))) {
    quotaError.value = '配额需为 ≥0 的整数（0 = 不限制）'
    return
  }
  const updated = await updateTenantLimits(tenant.id, { ...quotaForm })
  if (updated) closeQuota()
}

// ---- 编辑 / 启停 ----
const confirmingDisableId = ref<string | null>(null)
const editingTenant = ref<Tenant | null>(null)
const editName = ref('')
const editActive = ref(false)
const editError = ref('')

function isDefaultTenant(tenant: Tenant): boolean {
  return tenant.id === 'default'
}

function validateName(name: string): string | null {
  const trimmed = name.trim()
  if (!trimmed) return '租户名称不能为空'
  if (trimmed.length > 64) return '租户名称不能超过 64 个字符'
  return null
}

function resetCreate(): void {
  createName.value = ''
  createTemplate.value = ''
  adminUsername.value = ''
  adminPassword.value = ''
  createError.value = ''
}

function openCreate(): void {
  resetCreate()
  createOpen.value = true
}

function closeCreate(): void {
  createOpen.value = false
  createError.value = ''
}

function validateCreate(): string | null {
  const nameError = validateName(createName.value)
  if (nameError) return nameError
  const username = adminUsername.value.trim()
  if (username) {
    if (username.length < 3 || username.length > 32) return '初始管理员用户名需为 3–32 个字符'
    if (!adminPassword.value) return '填写初始管理员用户名时必须设置密码'
    if (adminPassword.value.length < 10) return '初始管理员密码至少 10 个字符'
    if (!adminPasswordHasLetter.value || !adminPasswordHasDigit.value) {
      return '初始管理员密码需同时包含字母与数字'
    }
  } else if (adminPassword.value) {
    return '填写密码时请同时填写初始管理员用户名'
  }
  return null
}

async function submitCreate(): Promise<void> {
  createError.value = ''
  const error = validateCreate()
  if (error) {
    createError.value = error
    return
  }
  const username = adminUsername.value.trim()
  const tenant = await createTenant({
    name: createName.value.trim(),
    ...(createTemplate.value ? { template: createTemplate.value } : {}),
    ...(username ? { admin: { username, password: adminPassword.value } } : {}),
  })
  if (tenant) closeCreate()
}

// ---- 编辑 ----
function openEdit(tenant: Tenant): void {
  editingTenant.value = tenant
  editName.value = tenant.name
  editActive.value = tenant.active
  editError.value = ''
}

function closeEdit(): void {
  editingTenant.value = null
  editError.value = ''
}

async function submitEdit(): Promise<void> {
  const tenant = editingTenant.value
  if (!tenant) return
  editError.value = ''
  const error = validateName(editName.value)
  if (error) {
    editError.value = error
    return
  }
  const updated = await updateTenant(tenant.id, {
    name: editName.value.trim(),
    active: isDefaultTenant(tenant) ? true : editActive.value,
  })
  if (updated) closeEdit()
}

function requestDisable(tenant: Tenant): void {
  if (isDefaultTenant(tenant) || !tenant.active) return
  confirmingDisableId.value = tenant.id
}

async function confirmDisable(tenant: Tenant): Promise<void> {
  if (isDefaultTenant(tenant)) return
  confirmingDisableId.value = null
  await updateTenant(tenant.id, { active: false })
}

async function enableTenant(tenant: Tenant): Promise<void> {
  if (tenant.active) return
  await updateTenant(tenant.id, { active: true })
}

onMounted(() => {
  void loadTenants()
})
</script>

<template>
  <section class="admin-panel">
    <header class="panel-head">
      <h2>租户</h2>
      <div class="panel-head-actions">
        <span class="feed-count">{{ state.tenants.length }}</span>
        <button class="ghost-btn" :disabled="state.loading" @click="loadTenants">刷新</button>
        <button class="secondary-btn" @click="openCreate">新建租户</button>
      </div>
    </header>

    <div class="admin-scroll">
      <p v-if="state.loading && state.tenants.length === 0" class="detail-loading">正在加载租户…</p>
      <p v-else-if="state.error" class="form-error">
        租户加载失败：{{ state.error }}
        <button class="link-btn" @click="loadTenants">重试</button>
      </p>
      <div v-else-if="state.tenants.length === 0" class="feed-empty">
        <p>暂无租户</p>
        <span>创建租户后，用户可在登录页选择租户（缺省 default）。</span>
      </div>

      <template v-else>
        <article v-for="tenant in state.tenants" :key="tenant.id" class="tenant-card">
          <div class="tenant-card-head">
            <span class="tenant-name">{{ tenant.name }}</span>
            <span v-if="isDefaultTenant(tenant)" class="tenant-badge tenant-builtin">内置</span>
            <span v-if="tenant.archived" class="tenant-badge tenant-archived">已归档</span>
            <span v-else class="tenant-badge" :class="tenant.active ? 'tenant-active' : 'tenant-inactive'">
              {{ tenant.active ? '启用' : '已停用' }}
            </span>
          </div>
          <p class="tenant-meta mono">ID：{{ tenant.id }}</p>
          <p class="tenant-meta">配额：{{ limitsSummary(tenant) }}</p>
          <p v-if="tenant.archived && tenant.archived_at" class="tenant-meta">归档时间：{{ tenant.archived_at }}</p>
          <div class="tenant-actions">
            <button class="ghost-btn" @click="openEdit(tenant)">编辑</button>
            <button class="ghost-btn" @click="openQuota(tenant)">配额</button>
            <template v-if="tenant.archived">
              <button
                v-if="confirmingRestoreId !== tenant.id"
                class="ghost-btn"
                :disabled="state.updatingId !== null"
                @click="requestRestore(tenant)"
              >
                恢复
              </button>
              <template v-else>
                <button class="secondary-btn compact-btn" :disabled="state.updatingId !== null" @click="confirmRestore(tenant)">
                  确认恢复
                </button>
                <button class="ghost-btn" @click="confirmingRestoreId = null">取消</button>
              </template>
              <button
                class="danger-btn compact-btn"
                :disabled="state.updatingId !== null"
                @click="openDelete(tenant)"
              >
                删除
              </button>
            </template>
            <template v-else>
              <template v-if="!isDefaultTenant(tenant)">
                <button
                  v-if="confirmingArchiveId !== tenant.id"
                  class="ghost-btn"
                  :disabled="state.updatingId !== null"
                  @click="requestArchive(tenant)"
                >
                  归档
                </button>
                <template v-else>
                  <button class="danger-btn compact-btn" :disabled="state.updatingId !== null" @click="confirmArchive(tenant)">
                    确认归档
                  </button>
                  <button class="ghost-btn" @click="confirmingArchiveId = null">取消</button>
                </template>
              </template>
              <template v-if="tenant.active && !isDefaultTenant(tenant)">
                <button
                  v-if="confirmingDisableId !== tenant.id"
                  class="ghost-btn"
                  :disabled="state.updatingId !== null"
                  @click="requestDisable(tenant)"
                >
                  停用
                </button>
                <template v-else>
                  <button class="danger-btn compact-btn" :disabled="state.updatingId !== null" @click="confirmDisable(tenant)">
                    确认停用
                  </button>
                  <button class="ghost-btn" @click="confirmingDisableId = null">取消</button>
                </template>
              </template>
              <button
                v-if="!tenant.active"
                class="ghost-btn"
                :disabled="state.updatingId !== null"
                @click="enableTenant(tenant)"
              >
                启用
              </button>
              <button class="ghost-btn" disabled title="仅归档后可删除">删除</button>
              <button
                v-if="isDefaultTenant(tenant)"
                class="ghost-btn"
                disabled
                title="内置租户不可归档/删除"
              >
                归档
              </button>
            </template>
          </div>
        </article>
      </template>
    </div>

    <!-- 新建租户对话框：模板复制 + 初始管理员 -->
    <div v-if="createOpen" class="modal-backdrop" @click.self="closeCreate">
      <section class="modal-card tenant-modal" role="dialog" aria-modal="true" aria-label="新建租户">
        <header class="modal-head">
          <h2>新建租户</h2>
          <button class="ghost-btn" @click="closeCreate">关闭</button>
        </header>
        <div class="modal-body">
          <label class="settings-field">
            <span>租户名称 *（≤64 字符）</span>
            <input v-model="createName" type="text" maxlength="64" placeholder="XX园区" />
          </label>

          <label class="settings-field">
            <span>复制模板（可选）</span>
            <select v-model="createTemplate">
              <option value="">不复制（空租户）</option>
              <option v-for="tenant in state.tenants" :key="tenant.id" :value="tenant.id">
                {{ tenant.name }}（{{ tenant.id }}）
              </option>
            </select>
          </label>
          <p class="form-hint">
            {{ createTemplate
              ? `将复制「${templateName}」的相机、区域、相机规则与设置（不含事件/工单/审计/用户/巡检）`
              : '不复制模板：新租户为空；可通过初始管理员开始配置。' }}
          </p>

          <div class="tenant-form-section">
            <span class="tenant-form-title">初始管理员（可选）</span>
            <div class="settings-grid">
              <label class="settings-field">
                <span>用户名</span>
                <input v-model="adminUsername" type="text" autocomplete="off" placeholder="admin" />
              </label>
              <label class="settings-field">
                <span>密码</span>
                <input v-model="adminPassword" type="password" autocomplete="new-password" placeholder="••••••••" />
              </label>
            </div>
            <p class="form-hint">
              密码策略：至少 10 个字符，且同时包含字母与数字；用户名在同一租户内唯一。
            </p>
            <div v-if="adminUsername.trim()" class="password-policy">
              <span class="password-rule" :class="{ met: adminPasswordLongEnough }">≥ 10 字符</span>
              <span class="password-rule" :class="{ met: adminPasswordHasLetter }">含字母</span>
              <span class="password-rule" :class="{ met: adminPasswordHasDigit }">含数字</span>
            </div>
          </div>

          <p v-if="createError" class="form-error">{{ createError }}</p>
          <div class="modal-buttons">
            <button class="primary-btn" :disabled="state.saving" @click="submitCreate">
              {{ state.saving ? '创建中…' : '创建租户' }}
            </button>
            <button class="ghost-btn" @click="closeCreate">取消</button>
          </div>
        </div>
      </section>
    </div>

    <div v-if="editingTenant" class="modal-backdrop" @click.self="closeEdit">
      <section class="modal-card tenant-modal" role="dialog" aria-modal="true" aria-label="编辑租户">
        <header class="modal-head">
          <h2>编辑租户</h2>
          <button class="ghost-btn" @click="closeEdit">关闭</button>
        </header>
        <div class="modal-body">
          <label class="settings-field">
            <span>租户 ID（不可修改）</span>
            <input :value="editingTenant.id" type="text" disabled />
          </label>
          <label class="settings-field">
            <span>名称 *</span>
            <input v-model="editName" type="text" maxlength="64" />
          </label>
          <label class="switch" :title="isDefaultTenant(editingTenant) ? '内置租户不可停用' : ''">
            <input v-model="editActive" type="checkbox" :disabled="isDefaultTenant(editingTenant)" />
            <span>启用租户</span>
          </label>
          <p v-if="editError" class="form-error">{{ editError }}</p>
          <div class="modal-buttons">
            <button class="primary-btn" :disabled="state.updatingId !== null" @click="submitEdit">
              {{ state.updatingId !== null ? '保存中…' : '保存' }}
            </button>
            <button class="ghost-btn" @click="closeEdit">取消</button>
          </div>
        </div>
      </section>
    </div>

    <!-- 删除租户（仅归档后） -->
    <div v-if="deleteDialog" class="modal-backdrop" @click.self="closeDelete">
      <section class="modal-card tenant-modal" role="dialog" aria-modal="true" aria-label="删除租户">
        <header class="modal-head">
          <h2>删除租户</h2>
          <button class="ghost-btn" @click="closeDelete">关闭</button>
        </header>
        <div class="modal-body">
          <p class="tenant-delete-warning">
            将彻底删除「{{ deleteDialog.name }}」及其全部数据（相机/区域/规则/设置/事件/工单/轨迹/审计/通知/值班/巡检/会话/用户），不可恢复。
          </p>
          <p class="tenant-meta mono">请输入租户 ID 以确认：{{ deleteDialog.id }}</p>
          <label class="settings-field">
            <span>租户 ID *</span>
            <input v-model="deleteConfirmId" type="text" class="mono" :placeholder="deleteDialog.id" />
          </label>
          <p v-if="deleteError" class="form-error">{{ deleteError }}</p>
          <div class="modal-buttons">
            <button
              class="danger-btn"
              :disabled="state.updatingId !== null || deleteConfirmId !== deleteDialog.id"
              @click="confirmDelete"
            >
              {{ state.updatingId !== null ? '删除中…' : '确认删除租户' }}
            </button>
            <button class="ghost-btn" @click="closeDelete">取消</button>
          </div>
        </div>
      </section>
    </div>

    <!-- 配额编辑 -->
    <div v-if="quotaDialog" class="modal-backdrop" @click.self="closeQuota">
      <section class="modal-card tenant-modal" role="dialog" aria-modal="true" aria-label="编辑租户配额">
        <header class="modal-head">
          <h2>编辑租户配额 · {{ quotaDialog.name }}</h2>
          <button class="ghost-btn" @click="closeQuota">关闭</button>
        </header>
        <div class="modal-body">
          <div class="tenant-quota-grid">
            <label class="settings-field">
              <span>相机上限（max_cameras）</span>
              <input v-model.number="quotaForm.max_cameras" type="number" min="0" step="1" />
            </label>
            <label class="settings-field">
              <span>用户上限（max_users）</span>
              <input v-model.number="quotaForm.max_users" type="number" min="0" step="1" />
            </label>
            <label class="settings-field">
              <span>事件上限（max_events）</span>
              <input v-model.number="quotaForm.max_events" type="number" min="0" step="1" />
            </label>
            <label class="settings-field">
              <span>ingest QPS（ingest_qps）</span>
              <input v-model.number="quotaForm.ingest_qps" type="number" min="0" step="1" />
            </label>
          </div>
          <p class="form-hint">0 表示不限制；PATCH 会以当前值覆盖 limits（部分字段保存时以表单值为准）。</p>
          <p v-if="quotaError" class="form-error">{{ quotaError }}</p>
          <div class="modal-buttons">
            <button class="primary-btn" :disabled="state.updatingId !== null" @click="submitQuota">
              {{ state.updatingId !== null ? '保存中…' : '保存配额' }}
            </button>
            <button class="ghost-btn" @click="closeQuota">取消</button>
          </div>
        </div>
      </section>
    </div>
  </section>
</template>
