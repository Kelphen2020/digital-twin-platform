import { computed, reactive } from 'vue'
import {
  ApiError,
  apiErrorMessage,
  fetchMe,
  login as requestLogin,
  logoutSession,
  setTokenRefreshHandler,
  setUnauthorizedHandler,
} from '../api'
import { clearTokens, getTenantId, loadStoredTenant, loadStoredTokens, setTenantId, setTokens } from '../auth/token'
import { pushToast } from './ui'
import type { AuthUser, LoginResponse, Permission, Role } from '../types'

interface AuthState {
  user: AuthUser | null
  token: string | null
  /** P11：refresh token 过期时间（用于会话续期提示/排查）。 */
  refreshExpiresAt: string | null
  /** P27：当前租户 id（登录返回，缺省 default）。 */
  tenantId: string | null
  /** 启动时校验本地令牌的加载态（用于避免登录页闪烁）。 */
  booting: boolean
  loggingIn: boolean
  error: string | null
  /** P10 登录锁定：锁定到期时间戳（毫秒）；为空表示未锁定。 */
  lockoutUntil: number | null
}

const state = reactive<AuthState>({
  user: null,
  token: null,
  refreshExpiresAt: null,
  tenantId: null,
  booting: true,
  loggingIn: false,
  error: null,
  lockoutUntil: null,
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

function clearSession(): void {
  clearTokens()
  state.user = null
  state.token = null
  state.refreshExpiresAt = null
  state.tenantId = null
  state.error = null
  state.lockoutUntil = null
}

/** 启动时用本地令牌换取 /api/auth/me；失败则回到登录页。 */
async function restoreSession(): Promise<void> {
  state.booting = true
  const stored = loadStoredTokens()
  if (!stored || (!stored.accessToken && !stored.refreshToken)) {
    state.booting = false
    return
  }
  state.token = stored.accessToken || null
  state.refreshExpiresAt = stored.refreshExpiresAt
  state.tenantId = loadStoredTenant()
  try {
    // fetchMe 遇 401 时由 api.ts 自动 refresh + 重试；轮换后回调同步 state.token
    const user = await fetchMe()
    state.user = user
    state.error = null
  } catch (error) {
    clearSession()
    state.error = apiErrorMessage(error)
  } finally {
    state.booting = false
  }
}

async function login(username: string, password: string, tenantId = 'default'): Promise<boolean> {
  if (state.loggingIn) return false
  state.loggingIn = true
  state.error = null
  try {
    const loginTenant = tenantId.trim() || 'default'
    const result: LoginResponse = await requestLogin(username.trim(), password, loginTenant)
    state.lockoutUntil = null
    setTokens({
      accessToken: result.token,
      refreshToken: result.refresh_token ?? null,
      refreshExpiresAt: result.refresh_expires_at ?? null,
    })
    const resolvedTenant = result.tenant_id ?? loginTenant
    setTenantId(resolvedTenant)
    state.tenantId = resolvedTenant
    state.token = result.token
    state.refreshExpiresAt = result.refresh_expires_at ?? null
    state.user = result.user
    return true
  } catch (error) {
    state.error = apiErrorMessage(error)
    if (error instanceof ApiError && error.code === 'too_many_attempts') {
      const retryAfter = error.retryAfter && error.retryAfter > 0 ? error.retryAfter : 60
      state.lockoutUntil = Date.now() + retryAfter * 1000
    } else {
      state.lockoutUntil = null
    }
    return false
  } finally {
    state.loggingIn = false
  }
}

/** P11：先吊销 refresh token（尽力而为），再清理本地会话。 */
async function logout(): Promise<void> {
  await logoutSession()
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

// P11：access token 轮换后同步内存状态（localStorage 已由 token 模块更新）
setTokenRefreshHandler((bundle) => {
  state.token = bundle.accessToken
  state.refreshExpiresAt = bundle.refreshExpiresAt
  state.tenantId = getTenantId()
})

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
