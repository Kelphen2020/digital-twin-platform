import { computed, reactive } from 'vue'
import { login as requestLogin, fetchMe, setUnauthorizedHandler, apiErrorMessage } from '../api'
import { setAuthToken } from '../auth/token'
import { pushToast } from './ui'
import type { AuthUser, LoginResponse, Permission, Role } from '../types'

const STORAGE_KEY = 'twin.auth.token'

interface AuthState {
  user: AuthUser | null
  token: string | null
  /** 启动时校验本地令牌的加载态（用于避免登录页闪烁）。 */
  booting: boolean
  loggingIn: boolean
  error: string | null
}

const state = reactive<AuthState>({
  user: null,
  token: null,
  booting: true,
  loggingIn: false,
  error: null,
})

const isAuthenticated = computed(() => state.user !== null && state.token !== null)
const role = computed<Role | null>(() => state.user?.role ?? null)

/** 角色 → 权限矩阵（与 docs/API_P2.md §1 一致；后端仍做最终校验）。 */
const ROLE_PERMISSIONS: Record<Permission, Role[]> = {
  'event:handle': ['operator', 'admin'],
  'order:create': ['operator', 'admin'],
  'order:update': ['operator', 'admin'],
  'camera:update': ['operator', 'admin'],
  'camera:calibration': ['admin'],
  'camera:rules': ['admin'],
}

function can(permission: Permission): boolean {
  const currentRole = state.user?.role
  return currentRole ? ROLE_PERMISSIONS[permission].includes(currentRole) : false
}

/** operator 对区域外相机：UI 提示 + 由后端返回 403 out_of_scope。 */
function isCameraOutOfScope(camera: { area_id: string }): boolean {
  const user = state.user
  return !!user && user.role === 'operator' && !(user.areas ?? []).includes(camera.area_id)
}

function persistToken(token: string): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, token)
  } catch {
    // localStorage 不可用时退化为仅内存会话
  }
}

function readStoredToken(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

function clearPersistedToken(): void {
  try {
    window.localStorage.removeItem(STORAGE_KEY)
  } catch {
    // ignore
  }
}

function clearSession(): void {
  clearPersistedToken()
  setAuthToken(null)
  state.user = null
  state.token = null
  state.error = null
}

/** 启动时用本地令牌换取 /api/auth/me；失败则回到登录页。 */
async function restoreSession(): Promise<void> {
  state.booting = true
  const stored = readStoredToken()
  if (!stored) {
    state.booting = false
    return
  }
  setAuthToken(stored)
  try {
    const user = await fetchMe()
    state.user = user
    state.token = stored
    state.error = null
  } catch (error) {
    clearSession()
    state.error = apiErrorMessage(error)
  } finally {
    state.booting = false
  }
}

async function login(username: string, password: string): Promise<boolean> {
  if (state.loggingIn) return false
  state.loggingIn = true
  state.error = null
  try {
    const result: LoginResponse = await requestLogin(username.trim(), password)
    persistToken(result.token)
    setAuthToken(result.token)
    state.token = result.token
    state.user = result.user
    return true
  } catch (error) {
    state.error = apiErrorMessage(error)
    return false
  } finally {
    state.loggingIn = false
  }
}

function logout(): void {
  clearSession()
}

/** 任意 REST 请求返回 401 时自动登出（会话过期）。 */
function handleUnauthorized(): void {
  const wasAuthenticated = state.user !== null
  clearSession()
  if (wasAuthenticated) pushToast('登录已过期，请重新登录', 'warning')
}

// 模块级注册一次：api.ts 收到 401 时回调登出
setUnauthorizedHandler(handleUnauthorized)

export function useAuthStore() {
  return {
    state,
    isAuthenticated,
    role,
    can,
    isCameraOutOfScope,
    restoreSession,
    login,
    logout,
  }
}
