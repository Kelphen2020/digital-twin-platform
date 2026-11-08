<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { AREA_LABELS, formatArea, formatDateTime } from '../labels'
import { useAuthStore } from '../store/auth'
import { useDutyStore } from '../store/duty'
import { useTwinStore } from '../store/twin'
import type { DutyGroup, DutyGroupPayload, DutyRotation, DutyShift } from '../types'

const { state, loadDuty, loadCurrent, createGroup, updateGroup, removeGroup } = useDutyStore()
const { selectedCamera } = useTwinStore()
const { state: authState } = useAuthStore()

/** P4.0 种子用户（后端校验 operators 必须是已知用户名）。 */
const KNOWN_OPERATORS: { username: string; role: string }[] = [
  { username: 'operator', role: '值班员' },
  { username: 'viewer', role: '观察员' },
  { username: 'admin', role: '管理员' },
]

/** ISO 周几：1=周一 … 7=周日。 */
const WEEKDAYS: { value: number; label: string }[] = [
  { value: 1, label: '一' },
  { value: 2, label: '二' },
  { value: 3, label: '三' },
  { value: 4, label: '四' },
  { value: 5, label: '五' },
  { value: 6, label: '六' },
  { value: 7, label: '日' },
]

interface ShiftForm {
  key: string
  id?: string
  name: string
  start: string
  end: string
  weekdays: number[]
  operators: string[]
}

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
const shiftSeq = ref(0)
const form = reactive<{
  name: string
  areas: string[]
  operators: string[]
  active: boolean
  rotation: DutyRotation
  shifts: ShiftForm[]
}>({
  name: '',
  areas: [],
  operators: [],
  active: true,
  rotation: 'group',
  shifts: [],
})

const currentGroup = computed(() => state.current?.group ?? null)
const currentShift = computed(() => state.current?.shift ?? null)
const currentOperators = computed(() => state.current?.operators ?? [])

const TIME_PATTERN = /^([01]\d|2[0-3]):[0-5]\d$/

function areaNames(areaIds: string[]): string {
  if (areaIds.length === 0) return '全局兜底'
  return areaIds.map((id) => areaChoices.value.find((area) => area.id === id)?.name ?? formatArea(id)).join('、')
}

function operatorRole(username: string): string {
  return KNOWN_OPERATORS.find((operator) => operator.username === username)?.role ?? '用户'
}

function rotationLabel(rotation: DutyRotation | undefined): string {
  return rotation === 'operator' ? '班次成员轮转' : '班组名'
}

function shiftSummary(group: DutyGroup): string {
  const shifts = group.shifts ?? []
  if (shifts.length === 0) return '未配置班次（值班人员全天）'
  return shifts.map((shift) => `${shift.name} ${shift.start}–${shift.end}`).join('；')
}

function weekdayText(weekdays: number[]): string {
  if (weekdays.length === 0) return '未选周几'
  if (weekdays.length === 7) return '每天'
  if (weekdays.length === 5 && [1, 2, 3, 4, 5].every((day) => weekdays.includes(day))) return '工作日'
  return weekdays
    .slice()
    .sort((a, b) => a - b)
    .map((day) => `周${WEEKDAYS.find((item) => item.value === day)?.label ?? day}`)
    .join('、')
}

function newShift(index: number): ShiftForm {
  shiftSeq.value += 1
  return {
    key: `shift-${index}-${Date.now()}-${shiftSeq.value}`,
    name: `班次 ${index + 1}`,
    start: '08:00',
    end: '16:00',
    weekdays: [1, 2, 3, 4, 5],
    operators: [],
  }
}

function resetForm(): void {
  formOpen.value = false
  editingId.value = null
  formError.value = ''
  form.name = ''
  form.areas = []
  form.operators = []
  form.active = true
  form.rotation = 'group'
  form.shifts = []
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
  form.rotation = group.rotation ?? 'group'
  form.shifts = (group.shifts ?? []).map((shift, index) => ({
    key: shift.id ?? `shift-${index}-${Date.now()}`,
    id: shift.id,
    name: shift.name,
    start: shift.start,
    end: shift.end,
    weekdays: [...shift.weekdays],
    operators: [...shift.operators],
  }))
}

function addShift(): void {
  form.shifts = [...form.shifts, newShift(form.shifts.length)]
}

function removeShift(index: number): void {
  form.shifts = form.shifts.filter((_, shiftIndex) => shiftIndex !== index)
}

function toggleShiftWeekday(shift: ShiftForm, day: number): void {
  shift.weekdays = shift.weekdays.includes(day)
    ? shift.weekdays.filter((item) => item !== day)
    : [...shift.weekdays, day]
}

function toggleShiftOperator(shift: ShiftForm, username: string): void {
  shift.operators = shift.operators.includes(username)
    ? shift.operators.filter((item) => item !== username)
    : [...shift.operators, username]
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

function validateShifts(): string | null {
  for (let index = 0; index < form.shifts.length; index += 1) {
    const shift = form.shifts[index]
    if (!shift.name.trim()) return `第 ${index + 1} 个班次名称不能为空`
    if (!TIME_PATTERN.test(shift.start)) return `班次「${shift.name}」开始时间需为 HH:MM`
    if (!TIME_PATTERN.test(shift.end)) return `班次「${shift.name}」结束时间需为 HH:MM`
    if (shift.weekdays.some((day) => day < 1 || day > 7)) return `班次「${shift.name}」周几需在 1–7`
  }
  return null
}

function toPayload(): DutyGroupPayload {
  return {
    name: form.name.trim(),
    areas: [...form.areas],
    operators: [...form.operators],
    active: form.active,
    rotation: form.rotation,
    shifts: form.shifts.map<DutyShift>((shift) => ({
      ...(shift.id ? { id: shift.id } : {}),
      name: shift.name.trim(),
      start: shift.start,
      end: shift.end,
      weekdays: [...shift.weekdays].sort((a, b) => a - b),
      operators: [...shift.operators],
    })),
  }
}

async function submit(): Promise<void> {
  formError.value = ''
  if (!form.name.trim()) {
    formError.value = '值班组名称不能为空'
    return
  }
  const shiftError = validateShifts()
  if (shiftError) {
    formError.value = shiftError
    return
  }
  saving.value = true
  try {
    const payload = toPayload()
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
          <p class="duty-current-meta">区域：{{ areaNames(currentGroup.areas) }} · 派单对象：{{ rotationLabel(currentGroup.rotation) }}</p>
          <p v-if="currentShift" class="duty-current-shift">
            当前班次：<strong>{{ currentShift.name }}</strong> {{ currentShift.start }}–{{ currentShift.end }}
            <template v-if="currentShift.id"> · {{ currentShift.id }}</template>
          </p>
          <p v-else-if="(currentGroup.shifts ?? []).length > 0" class="duty-current-shift duty-current-shift-off">
            当前无生效班次（非值班时间，告警升级不会自动派单）
          </p>
          <p v-else class="duty-current-meta">未配置班次：按值班人员全天值班。</p>
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
            <span v-if="currentOperators.length === 0" class="duty-current-meta">当前无值班人员</span>
          </div>
        </template>
        <p v-else class="duty-current-meta">该区域无匹配值班组，告警升级时不会自动派单。</p>
      </div>

      <div v-if="isAdmin" class="duty-actions">
        <span class="duty-hint">值班解析：优先区域匹配组，其次全局兜底组；班次内才值班</span>
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
              <input type="checkbox" :checked="form.areas.includes(area.id)" @change="toggleArea(area.id)" />
              <span>{{ area.name }}</span>
            </label>
          </div>
        </div>

        <div class="settings-field">
          <span>值班人员（已知用户名；无班次时全天值班）</span>
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

        <label class="settings-field">
          <span>派单对象（rotation）</span>
          <select v-model="form.rotation">
            <option value="group">班组名（group）</option>
            <option value="operator">班次成员轮转（operator）</option>
          </select>
        </label>
        <p class="form-hint">
          rotation=operator 时自动派单在班次成员间轮转（assignee 为操作员用户名）；班次为空时沿用值班人员全天。
        </p>

        <div class="settings-field">
          <div class="shift-editor-head">
            <span>班次（可选；非空时只有班次内时间才值班）</span>
            <button class="tiny-btn" type="button" @click="addShift">添加班次</button>
          </div>

          <div v-if="form.shifts.length === 0" class="shift-empty">未配置班次：值班人员全天值班。</div>
          <div v-else class="shift-editor">
            <div v-for="(shift, index) in form.shifts" :key="shift.key" class="shift-editor-row">
              <div class="shift-row-head">
                <input v-model="shift.name" class="shift-name" type="text" placeholder="早班" />
                <input v-model="shift.start" type="time" :aria-label="'班次开始时间'" />
                <span class="shift-time-sep">–</span>
                <input v-model="shift.end" type="time" :aria-label="'班次结束时间'" />
                <button class="ghost-btn shift-remove" type="button" title="删除该班次" @click="removeShift(index)">
                  删除
                </button>
              </div>
              <div class="weekday-toggles">
                <label
                  v-for="day in WEEKDAYS"
                  :key="day.value"
                  class="weekday-toggle"
                  :class="{ active: shift.weekdays.includes(day.value) }"
                >
                  <input
                    type="checkbox"
                    :checked="shift.weekdays.includes(day.value)"
                    @change="toggleShiftWeekday(shift, day.value)"
                  />
                  <span>周{{ day.label }}</span>
                </label>
              </div>
              <div class="shift-operators">
                <span class="shift-operators-label">班次成员：</span>
                <label v-for="operator in KNOWN_OPERATORS" :key="operator.username" class="rules-class">
                  <input
                    type="checkbox"
                    :checked="shift.operators.includes(operator.username)"
                    @change="toggleShiftOperator(shift, operator.username)"
                  />
                  <span>{{ operator.username }}</span>
                </label>
                <span v-if="shift.operators.length === 0" class="text-faint">未配置成员（该班次不派单）</span>
              </div>
              <p v-if="shift.weekdays.length > 0" class="shift-summary">
                {{ shift.name }}：{{ weekdayText(shift.weekdays) }} {{ shift.start }}–{{ shift.end }}
                <template v-if="shift.end <= shift.start">（跨夜）</template>
              </p>
            </div>
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
          {{ isAdmin ? '创建值班组后，告警升级时可自动派单到对应区域的值班组/班次成员。' : '管理员尚未配置值班组。' }}
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
          <div class="duty-badges">
            <span class="shift-chip">{{ rotationLabel(group.rotation) }}</span>
            <span v-if="(group.shifts ?? []).length > 0" class="shift-chip shift-chip-alt">
              {{ (group.shifts ?? []).length }} 个班次
            </span>
          </div>
          <p class="duty-group-meta">区域：{{ areaNames(group.areas) }}</p>
          <div class="operator-chips">
            <span v-for="username in group.operators" :key="username" class="operator-chip" :title="operatorRole(username)">
              {{ username }}
            </span>
            <span v-if="group.operators.length === 0" class="duty-current-meta">未配置值班人员</span>
          </div>
          <p class="duty-group-meta">班次：{{ shiftSummary(group) }}</p>
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
