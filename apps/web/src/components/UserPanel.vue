<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { AREA_LABELS, ROLE_COLORS, ROLE_LABELS, formatArea, formatDateTime } from '../labels'
import { useAdminStore } from '../store/admin'
import { useAuthStore } from '../store/auth'
import { useDutyStore } from '../store/duty'
import type { CreateUserPayload, ManagedUser, Role } from '../types'

const {
  state,
  loadUsers,
  createManagedUser,
  updateManagedUser,
  deleteManagedUser,
  loadUserSessions,
  forceLogoutManagedUser,
} = useAdminStore()
const { state: authState } = useAuthStore()
const duty = useDutyStore()

const roleOptions: { value: Role; label: string }[] = [
  { value: 'viewer', label: ROLE_LABELS.viewer },
  { value: 'operator', label: ROLE_LABELS.operator },
  { value: 'tenant_admin', label: ROLE_LABELS.tenant_admin },
  { value: 'admin', label: ROLE_LABELS.admin },
]

const areaChoices = computed(() => {
  if (duty.state.areas.length > 0) return duty.state.areas.map((area) => ({ id: area.id, name: area.name }))
  return Object.entries(AREA_LABELS).map(([id, name]) => ({ id, name }))
})

type DialogMode = 'create' | 'edit'

const dialogMode = ref<DialogMode | null>(null)
const editingUser = ref<ManagedUser | null>(null)
const formError = ref('')
const confirmingId = ref<string | null>(null)
const confirmingLogoutId = ref<string | null>(null)

const latestSession = computed(() => {
  if (state.userSessions.length === 0) return null
  return [...state.userSessions].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  )[0]
})

const form = reactive<{
  username: string
  password: string
  display_name: string
  role: Role
  areas: string[]
  active: boolean
}>({
  username: '',
  password: '',
  display_name: '',
  role: 'viewer',
  areas: [],
  active: true,
})

const isSelf = computed(() => !!editingUser.value && editingUser.value.username === authState.user?.username)
/** 编辑自己时不能停用或降级，否则显示 self_protected 保护提示并禁用保存。 */
const selfProtected = computed(
  () => dialogMode.value === 'edit' && isSelf.value && (!form.active || form.role !== 'admin'),
)
const passwordLongEnough = computed(() => form.password.length >= 10)
const passwordHasLetter = computed(() => /[A-Za-z]/.test(form.password))
const passwordHasDigit = computed(() => /\d/.test(form.password))
const dialogTitle = computed(() => (dialogMode.value === 'create' ? '新建用户' : '编辑用户'))

function resetForm(): void {
  form.username = ''
  form.password = ''
  form.display_name = ''
  form.role = 'viewer'
  form.areas = []
  form.active = true
  formError.value = ''
}

function openCreate(): void {
  resetForm()
  editingUser.value = null
  dialogMode.value = 'create'
}

function openEdit(user: ManagedUser): void {
  formError.value = ''
  editingUser.value = user
  void loadUserSessions(user.id)
  form.username = user.username
  form.display_name = user.display_name
  form.role = user.role
  form.areas = Array.isArray(user.areas) ? [...user.areas] : []
  form.active = user.active !== false
  form.password = ''
  dialogMode.value = 'edit'
}

function closeDialog(): void {
  dialogMode.value = null
  editingUser.value = null
  formError.value = ''
}

function toggleArea(areaId: string): void {
  form.areas = form.areas.includes(areaId) ? form.areas.filter((id) => id !== areaId) : [...form.areas, areaId]
}

function validate(): string | null {
  if (dialogMode.value === 'create') {
    const username = form.username.trim()
    if (username.length < 3 || username.length > 32) return '用户名长度需为 3–32 个字符'
    if (!/^[A-Za-z0-9_.-]+$/.test(username)) return '用户名仅支持字母、数字、下划线、点与短横线'
    if (form.password.length < 10) return '密码至少 10 个字符'
    if (!passwordHasLetter.value || !passwordHasDigit.value) return '密码需同时包含字母与数字'
  } else if (form.password.length > 0) {
    if (form.password.length < 10) return '新密码至少 10 个字符（留空表示不修改）'
    if (!passwordHasLetter.value || !passwordHasDigit.value) return '新密码需同时包含字母与数字'
  }
  if (!form.display_name.trim()) return '显示名不能为空'
  if (!roleOptions.some((option) => option.value === form.role)) return '角色不合法'
  if (selfProtected.value) return '不能停用或降级当前登录账号（self_protected）'
  return null
}

function submit(): void {
  const validationError = validate()
  if (validationError) {
    formError.value = validationError
    return
  }
  formError.value = ''
  if (dialogMode.value === 'create') {
    const payload: CreateUserPayload = {
      username: form.username.trim(),
      password: form.password,
      display_name: form.display_name.trim(),
      role: form.role,
      areas: [...form.areas],
      active: form.active,
    }
    void createManagedUser(payload).then((user) => {
      if (user) closeDialog()
    })
    return
  }

  const editing = editingUser.value
  if (!editing) return
  const payload = {
    display_name: form.display_name.trim(),
    role: form.role,
    areas: [...form.areas],
    active: form.active,
    ...(form.password.length > 0 ? { password: form.password } : {}),
  }
  void updateManagedUser(editing.id, payload).then((user) => {
    if (user) closeDialog()
  })
}

function requestDelete(user: ManagedUser): void {
  if (user.username === authState.user?.username) return
  confirmingId.value = user.id
}

async function confirmDelete(user: ManagedUser): Promise<void> {
  if (user.username === authState.user?.username) return
  confirmingId.value = null
  await deleteManagedUser(user.id)
}

function requestForceLogout(user: ManagedUser): void {
  if (user.username === authState.user?.username) return
  confirmingLogoutId.value = user.id
}

async function confirmForceLogout(user: ManagedUser): Promise<void> {
  if (user.username === authState.user?.username) return
  confirmingLogoutId.value = null
  await forceLogoutManagedUser(user.id)
  if (editingUser.value?.id === user.id) void loadUserSessions(user.id)
}

function userAreas(user: ManagedUser): string {
  if (!Array.isArray(user.areas) || user.areas.length === 0) return '全部区域'
  return user.areas.map((id) => areaChoices.value.find((area) => area.id === id)?.name ?? formatArea(id)).join('、')
}

function isSelfUser(user: ManagedUser): boolean {
  return user.username === authState.user?.username
}

onMounted(() => {
  void loadUsers()
  void duty.loadAreas()
})
</script>

<template>
  <section class="admin-panel">
    <header class="panel-head">
      <h2>用户管理</h2>
      <div class="panel-head-actions">
        <span class="feed-count">{{ state.users.length }}</span>
        <button class="ghost-btn" :disabled="state.usersLoading" @click="loadUsers">刷新</button>
        <button class="secondary-btn" @click="openCreate">新建用户</button>
      </div>
    </header>

    <div class="admin-scroll">
      <p v-if="state.usersLoading && state.users.length === 0" class="detail-loading">正在加载用户…</p>
      <p v-else-if="state.usersError" class="form-error">
        用户加载失败：{{ state.usersError }}
        <button class="link-btn" @click="loadUsers">重试</button>
      </p>
      <div v-else-if="state.users.length === 0" class="feed-empty">
        <p>暂无用户</p>
        <span>点击「新建用户」创建账号。</span>
      </div>

      <template v-else>
        <article v-for="user in state.users" :key="user.id" class="user-card">
          <div class="user-card-head">
            <span class="user-avatar-sm" :style="{ borderColor: ROLE_COLORS[user.role], color: ROLE_COLORS[user.role] }">
              {{ user.display_name.slice(0, 1) || '?' }}
            </span>
            <div class="user-card-title">
              <strong>{{ user.display_name }}</strong>
              <span class="user-username mono">@{{ user.username }}</span>
            </div>
            <span class="user-role" :style="{ color: ROLE_COLORS[user.role], borderColor: ROLE_COLORS[user.role] }">
              {{ ROLE_LABELS[user.role] }}
            </span>
          </div>

          <div class="user-badges">
            <span v-if="isSelfUser(user)" class="user-badge user-badge-self">当前用户</span>
            <span v-if="user.active === false" class="user-badge user-badge-inactive">已停用</span>
            <span v-else class="user-badge user-badge-active">启用中</span>
          </div>

          <p class="user-meta">负责区域：{{ userAreas(user) }}</p>
          <p class="user-meta">
            创建时间：{{ user.created_at ? formatDateTime(user.created_at) : '—' }}
            <template v-if="user.updated_at"> · 更新：{{ formatDateTime(user.updated_at) }}</template>
          </p>

          <div class="user-actions">
            <button class="ghost-btn" @click="openEdit(user)">编辑</button>
            <template v-if="confirmingId !== user.id">
              <button
                class="ghost-btn"
                :disabled="isSelfUser(user)"
                :title="isSelfUser(user) ? '不能删除当前登录账号（self_protected）' : ''"
                @click="requestDelete(user)"
              >
                删除
              </button>
            </template>
            <template v-else>
              <button class="danger-btn compact-btn" :disabled="state.userDeletingId !== null" @click="confirmDelete(user)">
                确认删除
              </button>
              <button class="ghost-btn" @click="confirmingId = null">取消</button>
            </template>
            <template v-if="!isSelfUser(user)">
              <button
                v-if="confirmingLogoutId !== user.id"
                class="ghost-btn"
                :title="'强制下线：吊销该用户全部会话（' + user.username + '）'"
                @click="requestForceLogout(user)"
              >
                强制下线
              </button>
              <template v-else>
                <button
                  class="danger-btn compact-btn"
                  :disabled="state.forceLogoutId !== null"
                  @click="confirmForceLogout(user)"
                >
                  确认下线
                </button>
                <button class="ghost-btn" @click="confirmingLogoutId = null">取消</button>
              </template>
            </template>
          </div>
        </article>
      </template>
    </div>

    <!-- 新建 / 编辑用户弹窗 -->
    <div v-if="dialogMode" class="modal-backdrop" @click.self="closeDialog">
      <section class="modal-card user-modal" role="dialog" aria-modal="true" :aria-label="dialogTitle">
        <header class="modal-head">
          <h2>{{ dialogTitle }}</h2>
          <button class="ghost-btn" @click="closeDialog">关闭</button>
        </header>

        <div class="modal-body">
          <label class="settings-field">
            <span>用户名 *{{ dialogMode === 'edit' ? '（不可修改）' : '（3–32 字符）' }}</span>
            <input v-model="form.username" type="text" :disabled="dialogMode === 'edit'" placeholder="operator2" autocomplete="off" />
          </label>

          <label class="settings-field">
            <span>{{ dialogMode === 'create' ? '密码 *（至少 10 字符，含字母与数字）' : '新密码（留空表示不修改，至少 10 字符，含字母与数字）' }}</span>
            <input v-model="form.password" type="password" placeholder="••••••••" autocomplete="new-password" />
          </label>
          <div class="password-policy" :class="{ 'password-policy-empty': dialogMode === 'edit' && !form.password }">
            <span class="password-policy-title">
              密码策略{{ dialogMode === 'edit' ? '（留空不修改；如需修改需满足）' : '' }}
            </span>
            <span class="password-rule" :class="{ met: passwordLongEnough }">≥ 10 字符</span>
            <span class="password-rule" :class="{ met: passwordHasLetter }">含字母</span>
            <span class="password-rule" :class="{ met: passwordHasDigit }">含数字</span>
          </div>

          <label class="settings-field">
            <span>显示名 *</span>
            <input v-model="form.display_name" type="text" placeholder="值班员二" />
          </label>

          <div class="settings-grid">
            <label class="settings-field">
              <span>角色</span>
              <select v-model="form.role" :disabled="isSelf">
                <option v-for="option in roleOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
            <label class="switch settings-switch user-active-switch">
              <input v-model="form.active" type="checkbox" :disabled="isSelf" />
              <span>启用账号</span>
            </label>
          </div>

          <p v-if="form.role === 'tenant_admin'" class="form-hint">
            租户管理员仅能管理本租户用户与配置（含用户 CRUD、审计、用户会话/强制下线），不可跨租户管理租户。
          </p>

          <p v-if="isSelf" class="role-notice">
            当前登录账号受保护：不能停用或降级自己（保存时触发 self_protected）。可修改显示名/密码/区域。
          </p>

          <div class="settings-field">
            <span>负责区域（不选 = 全部区域）</span>
            <div class="rules-classes">
              <label v-for="area in areaChoices" :key="area.id" class="rules-class">
                <input
                  type="checkbox"
                  :checked="form.areas.includes(area.id)"
                  @change="toggleArea(area.id)"
                />
                <span>{{ area.name }}</span>
              </label>
            </div>
          </div>

          <div v-if="dialogMode === 'edit' && editingUser" class="user-sessions">
            <span class="user-sessions-title">在线会话</span>
            <span v-if="state.userSessionsLoading" class="user-sessions-meta">正在加载会话…</span>
            <span v-else-if="state.userSessionsError" class="form-error">
              会话加载失败：{{ state.userSessionsError }}
            </span>
            <template v-else-if="state.userSessions.length === 0">
              <span class="user-sessions-meta">无有效会话</span>
            </template>
            <template v-else>
              <span class="user-sessions-meta">{{ state.userSessions.length }} 个会话</span>
              <span v-if="latestSession" class="user-sessions-meta">
                最近 IP：<b class="mono">{{ latestSession.ip }}</b> ·
                最近创建：{{ formatDateTime(latestSession.created_at) }}
              </span>
              <span v-if="latestSession?.user_agent" class="user-sessions-ua" :title="latestSession.user_agent">
                {{ latestSession.user_agent }}
              </span>
            </template>
          </div>

          <p v-if="formError" class="form-error">{{ formError }}</p>
          <p v-if="selfProtected" class="form-error">不能停用或降级当前登录账号（self_protected）。</p>

          <div class="modal-buttons">
            <button
              class="primary-btn"
              :disabled="state.userSaving || selfProtected"
              @click="submit"
            >
              {{ state.userSaving ? '保存中…' : '保存' }}
            </button>
            <button class="ghost-btn" @click="closeDialog">取消</button>
          </div>
        </div>
      </section>
    </div>
  </section>
</template>
