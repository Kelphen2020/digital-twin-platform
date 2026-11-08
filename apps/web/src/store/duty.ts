import { reactive } from 'vue'
import {
  apiErrorMessage,
  createDutyGroup,
  deleteDutyGroup,
  fetchAreas,
  fetchDutyCurrent,
  fetchDutyGroups,
  updateDutyGroup,
} from '../api'
import { pushToast } from './ui'
import type { Area, DutyCurrent, DutyGroup, DutyGroupPayload } from '../types'

interface DutyState {
  groups: DutyGroup[]
  groupsLoading: boolean
  groupsError: string | null
  current: DutyCurrent | null
  currentLoading: boolean
  currentError: string | null
  areas: Area[]
  areasLoading: boolean
  areasError: string | null
}

const state = reactive<DutyState>({
  groups: [],
  groupsLoading: false,
  groupsError: null,
  current: null,
  currentLoading: false,
  currentError: null,
  areas: [],
  areasLoading: false,
  areasError: null,
})

let areaRequestSeq = 0

/** 拉取值班组列表。 */
async function loadGroups(): Promise<void> {
  state.groupsLoading = true
  state.groupsError = null
  try {
    state.groups = await fetchDutyGroups()
  } catch (error) {
    state.groupsError = apiErrorMessage(error)
  } finally {
    state.groupsLoading = false
  }
}

/** 拉取区域列表（值班组区域多选；失败时组件回退到本地区域字典）。 */
async function loadAreas(): Promise<void> {
  state.areasLoading = true
  state.areasError = null
  try {
    state.areas = await fetchAreas()
  } catch (error) {
    state.areasError = apiErrorMessage(error)
  } finally {
    state.areasLoading = false
  }
}

/** 值班面板打开：值班组 + 区域。 */
async function loadDuty(): Promise<void> {
  await Promise.all([loadGroups(), loadAreas()])
}

/** 给定相机区域解析当前值班组。 */
async function loadCurrent(areaId: string | null): Promise<void> {
  const seq = (areaRequestSeq += 1)
  if (!areaId) {
    state.current = null
    state.currentLoading = false
    state.currentError = null
    return
  }
  state.currentLoading = true
  state.currentError = null
  try {
    const current = await fetchDutyCurrent(areaId)
    if (seq === areaRequestSeq) state.current = current
  } catch (error) {
    if (seq === areaRequestSeq) state.currentError = apiErrorMessage(error)
  } finally {
    if (seq === areaRequestSeq) state.currentLoading = false
  }
}

/** CRUD 后刷新当前值班组（若已解析区域）。 */
function refreshCurrent(): void {
  if (state.current?.area_id) void loadCurrent(state.current.area_id)
}

async function createGroup(payload: DutyGroupPayload): Promise<boolean> {
  try {
    const group = await createDutyGroup(payload)
    state.groups = [group, ...state.groups]
    pushToast('值班组已创建', 'success')
    refreshCurrent()
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  }
}

async function updateGroup(groupId: string, payload: Partial<DutyGroupPayload>): Promise<boolean> {
  try {
    const group = await updateDutyGroup(groupId, payload)
    state.groups = state.groups.map((item) => (item.id === group.id ? group : item))
    pushToast('值班组已更新', 'success')
    refreshCurrent()
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  }
}

async function removeGroup(groupId: string): Promise<boolean> {
  try {
    await deleteDutyGroup(groupId)
    state.groups = state.groups.filter((item) => item.id !== groupId)
    pushToast('值班组已删除', 'success')
    refreshCurrent()
    return true
  } catch (error) {
    pushToast(apiErrorMessage(error), 'error')
    return false
  }
}

function reset(): void {
  areaRequestSeq += 1
  state.groups = []
  state.groupsLoading = false
  state.groupsError = null
  state.current = null
  state.currentLoading = false
  state.currentError = null
  state.areas = []
  state.areasLoading = false
  state.areasError = null
}

export function useDutyStore() {
  return {
    state,
    loadDuty,
    loadGroups,
    loadAreas,
    loadCurrent,
    createGroup,
    updateGroup,
    removeGroup,
    reset,
  }
}
