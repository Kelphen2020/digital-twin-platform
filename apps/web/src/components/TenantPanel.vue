<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useTenantStore } from '../store/tenant'
import type { Tenant } from '../types'

const { state, loadTenants, createTenant, updateTenant } = useTenantStore()

const newName = ref('')
const formError = ref('')
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

async function submitCreate(): Promise<void> {
  formError.value = ''
  const error = validateName(newName.value)
  if (error) {
    formError.value = error
    return
  }
  const tenant = await createTenant(newName.value.trim())
  if (tenant) newName.value = ''
}

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
      </div>
    </header>

    <div class="admin-scroll">
      <form class="tenant-create" @submit.prevent="submitCreate">
        <input
          v-model="newName"
          type="text"
          maxlength="64"
          placeholder="新租户名称（≤64 字符）"
          :disabled="state.saving"
        />
        <button class="secondary-btn" type="submit" :disabled="state.saving">
          {{ state.saving ? '创建中…' : '新建租户' }}
        </button>
      </form>
      <p v-if="formError" class="form-error">{{ formError }}</p>

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
            <span class="tenant-badge" :class="tenant.active ? 'tenant-active' : 'tenant-inactive'">
              {{ tenant.active ? '启用' : '已停用' }}
            </span>
          </div>
          <p class="tenant-meta mono">ID：{{ tenant.id }}</p>
          <div class="tenant-actions">
            <button class="ghost-btn" @click="openEdit(tenant)">编辑</button>
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
            <button
              v-if="isDefaultTenant(tenant)"
              class="ghost-btn"
              disabled
              title="内置租户不可停用"
            >
              停用
            </button>
          </div>
        </article>
      </template>
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
  </section>
</template>
