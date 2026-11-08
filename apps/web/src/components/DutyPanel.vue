<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { AREA_LABELS, formatArea, formatDateTime } from '../labels'
import { useAuthStore } from '../store/auth'
import { useDutyStore } from '../store/duty'
import { useTwinStore } from '../store/twin'
import type { DutyGroup, DutyGroupPayload } from '../types'

const { state, loadDuty, loadCurrent, createGroup, updateGroup, removeGroup } = useDutyStore()
const { selectedCamera } = useTwinStore()
const { state: authState } = useAuthStore()

/** P4.0 种子用户（后端校验 operators 必须是已知用户名）。 */
const KNOWN_OPERATORS: { username: string; role: string }[] = [
  { username: 'operator', role: '值班员' },
  { username: 'viewer', role: '观察员' },
  { username: 'admin', role: '管理员' },
]

const isAdmin = computed(() => authState.user?.role === 'admin')
const selectedAreaId = computed(() => selectedCamera.value?.area_id ?? null)
const areaChoices = computed(() => {
  if (state.areas.length > 0) return state.areas.map((area) => ({ id: area.id, name: area.name }))
  return Object.entries(AREA_LABELS).map(([id, name]) => ({ id, name }))
})

const formOpen = ref(false)
const editingId = ref<string | null>(null)
const formError = ref('')
const saving = ref(false)
const confirmingId = ref<string | null>(null)
const form = reactive<DutyGroupPayload>({ name: '', areas: [], operators: [], active: true })

const currentGroup = computed(() => state.current?.group ?? null)
const currentOperators = computed(() => state.current?.operators ?? [])

function areaNames(areaIds: string[]): string {
  if (areaIds.length === 0) return '全局兜底'
  return areaIds.map((id) => areaChoices.value.find((area) => area.id === id)?.name ?? formatArea(id)).join('、')
}

function operatorRole(username: string): string {
  return KNOWN_OPERATORS.find((operator) => operator.username === username)?.role ?? '用户'
}

function resetForm(): void {
  formOpen.value = false
  editingId.value = null
  formError.value = ''
  form.name = ''
  form.areas = []
  form.operators = []
  form.active = true
}

function startCreate(): void {
  resetForm()
  formOpen.value = true
}

function startEdit(group: DutyGroup): void {
  editingId.value = group.id
  formOpen.value = true
  formError.value = ''
  form.name = group.name
  form.areas = [...group.areas]
  form.operators = [...group.operators]
  form.active = group.active
}

function toggleArea(areaId: string): void {
  form.areas = form.areas.includes(areaId)
    ? form.areas.filter((id) => id !== areaId)
    : [...form.areas, areaId]
}

function toggleOperator(username: string): void {
  form.operators = form.operators.includes(username)
    ? form.operators.filter((item) => item !== username)
    : [...form.operators, username]
}

async function submit(): Promise<void> {
  formError.value = ''
  if (!form.name.trim()) {
    formError.value = '值班组名称不能为空'
    return
  }
  saving.value = true
  try {
    const payload: DutyGroupPayload = {
      name: form.name.trim(),
      areas: [...form.areas],
      operators: [...form.operators],
      active: form.active,
    }
    const ok = editingId.value ? await updateGroup(editingId.value, payload) : await createGroup(payload)
    if (ok) resetForm()
  } finally {
    saving.value = false
  }
}

async function confirmDelete(group: DutyGroup): Promise<void> {
  confirmingId.value = null
  await removeGroup(group.id)
}

async function refresh(): Promise<void> {
  await loadDuty()
  void loadCurrent(selectedAreaId.value)
}

onMounted(() => {
  void loadDuty()
  void loadCurrent(selectedAreaId.value)
})

watch(selectedAreaId, (areaId) => {
  void loadCurrent(areaId)
})
</script>

<template>
  <section class="duty-panel">
    <header class="panel-head">
      <h2>值班</h2>
      <div class="panel-head-actions">
        <span class="feed-count">{{ state.groups.length }}</span>
        <button class="ghost-btn" :disabled="state.groupsLoading" @click="refresh">刷新</button>
      </div>
    </header>

    <div class="duty-scroll">
      <div class="duty-current">
        <div class="duty-current-head">
          <h3>当前值班</h3>
          <span class="duty-current-area">
            {{ selectedCamera ? `${selectedCamera.code} · ${formatArea(selectedCamera.area_id)}` : '未选择相机' }}
          </span>
        </div>
        <p v-if="!selectedCamera" class="duty-current-meta">选中相机后显示该相机区域的值班组。</p>
        <p v-else-if="state.currentLoading" class="duty-current-meta">正在解析值班组…</p>
        <p v-else-if="state.currentError" class="duty-current-meta">解析失败：{{ state.currentError }}</p>
        <template v-else-if="currentGroup">
          <p class="duty-current-name">{{ currentGroup.name }}</p>
          <p class="duty-current-meta">区域：{{ areaNames(currentGroup.areas) }}</p>
          <div class="operator-chips">
            <span
              v-for="username in currentOperators"
              :key="username"
              class="operator-chip"
              :class="{ self: username === authState.user?.username }"
              :title="operatorRole(username)"
            >
              {{ username }}
            </span>
            <span v-if="currentOperators.length === 0" class="duty-current-meta">无值班人员</span>
          </div>
        </template>
        <p v-else class="duty-current-meta">该区域无匹配值班组，告警升级时不会自动派单。</p>
      </div>

      <div v-if="isAdmin" class="duty-actions">
        <span class="duty-hint">值班解析：优先区域匹配组，其次全局兜底组</span>
        <button class="secondary-btn" @click="startCreate">新建值班组</button>
      </div>
      <p v-else class="role-notice">值班组为只读；如需调整请联系管理员。</p>

      <form v-if="formOpen && isAdmin" class="duty-form" @submit.prevent="submit">
        <header class="settings-head">
          <h3>{{ editingId ? '编辑值班组' : '新建值班组' }}</h3>
        </header>

        <label class="settings-field">
          <span>名称 *</span>
          <input v-model="form.name" type="text" placeholder="东门值班组" />
        </label>

        <div class="settings-field">
          <span>负责区域（不选表示全局兜底组）</span>
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

        <div class="settings-field">
          <span>值班人员（已知用户名）</span>
          <div class="rules-classes">
            <label v-for="operator in KNOWN_OPERATORS" :key="operator.username" class="rules-class">
              <input
                type="checkbox"
                :checked="form.operators.includes(operator.username)"
                @change="toggleOperator(operator.username)"
              />
              <span>{{ operator.username }}（{{ operator.role }}）</span>
            </label>
          </div>
        </div>

        <label class="switch">
          <input v-model="form.active" type="checkbox" />
          <span>启用（参与值班解析与自动派单）</span>
        </label>

        <p v-if="formError" class="form-error">{{ formError }}</p>

        <div class="duty-form-actions">
          <button class="primary-btn" type="submit" :disabled="saving">
            {{ saving ? '保存中…' : editingId ? '保存修改' : '创建值班组' }}
          </button>
          <button class="ghost-btn" type="button" @click="resetForm">取消</button>
        </div>
      </form>

      <p v-if="state.groupsLoading && state.groups.length === 0" class="detail-loading">正在加载值班组…</p>
      <p v-else-if="state.groupsError" class="form-error">
        值班组加载失败：{{ state.groupsError }}
        <button class="link-btn" @click="refresh">重试</button>
      </p>
      <div v-else-if="state.groups.length === 0" class="feed-empty">
        <p>暂无值班组</p>
        <span>
          {{
            isAdmin
              ? '创建值班组后，告警升级时可自动派单到对应区域的值班组。'
              : '管理员尚未配置值班组。'
          }}
        </span>
      </div>

      <template v-else>
        <article v-for="group in state.groups" :key="group.id" class="duty-group">
          <div class="duty-group-head">
            <span class="duty-group-name">{{ group.name }}</span>
            <span
              class="status-chip-sm"
              :style="{ color: group.active ? '#22c55e' : '#94a3b8', borderColor: group.active ? '#22c55e' : '#94a3b8' }"
            >
              {{ group.active ? '值班中' : '停用' }}
            </span>
          </div>
          <p class="duty-group-meta">区域：{{ areaNames(group.areas) }}</p>
          <div class="operator-chips">
            <span v-for="username in group.operators" :key="username" class="operator-chip" :title="operatorRole(username)">
              {{ username }}
            </span>
            <span v-if="group.operators.length === 0" class="duty-current-meta">未配置值班人员</span>
          </div>
          <p class="duty-group-meta">更新：{{ formatDateTime(group.updated_at) }}</p>

          <div v-if="isAdmin" class="duty-group-actions">
            <button class="ghost-btn" @click="startEdit(group)">编辑</button>
            <button v-if="confirmingId !== group.id" class="ghost-btn" @click="confirmingId = group.id">删除</button>
            <template v-else>
              <button class="danger-btn compact-btn" @click="confirmDelete(group)">确认删除</button>
              <button class="ghost-btn" @click="confirmingId = null">取消</button>
            </template>
          </div>
        </article>
      </template>
    </div>
  </section>
</template>
