/**
 * 会话令牌的集中读写点。
 * 单独成模块是为了避免 api.ts ↔ store/auth.ts 的循环依赖：
 * - auth store 负责登录、持久化与清理；
 * - api.ts / ws.ts 只读取当前 access token，refresh 由 api.ts 单飞处理。
 *
 * P11：access token + refresh token（轮换）分开持久化，登出/失效时整体清理。
 */

const ACCESS_KEY = 'twin.auth.token'
const REFRESH_KEY = 'twin.auth.refresh'
const REFRESH_EXPIRES_KEY = 'twin.auth.refresh_expires_at'
const TENANT_KEY = 'twin.auth.tenant'

export interface TokenBundle {
  accessToken: string
  refreshToken: string | null
  refreshExpiresAt: string | null
}

let accessToken: string | null = null
let refreshToken: string | null = null
let refreshExpiresAt: string | null = null
let tenantId: string | null = null

function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string | null): void {
  try {
    if (value === null) window.localStorage.removeItem(key)
    else window.localStorage.setItem(key, value)
  } catch {
    // localStorage 不可用时退化为仅内存会话
  }
}

/** 写入完整令牌 bundle（P11：登录/刷新成功后调用，刷新令牌会轮换）。 */
export function setTokens(bundle: TokenBundle): void {
  accessToken = bundle.accessToken
  refreshToken = bundle.refreshToken
  refreshExpiresAt = bundle.refreshExpiresAt
  writeStorage(ACCESS_KEY, bundle.accessToken)
  writeStorage(REFRESH_KEY, bundle.refreshToken)
  writeStorage(REFRESH_EXPIRES_KEY, bundle.refreshExpiresAt)
}

/** 仅更新 access token（兼容旧调用点，如启动/清理）。 */
export function setAuthToken(next: string | null): void {
  accessToken = next
  writeStorage(ACCESS_KEY, next)
}

export function getAuthToken(): string | null {
  return accessToken
}

export function getRefreshToken(): string | null {
  return refreshToken
}

export function getRefreshExpiresAt(): string | null {
  return refreshExpiresAt
}

/** P27：当前租户 id（登录成功/恢复会话时写入，登出清理）。 */
export function setTenantId(next: string | null): void {
  tenantId = next
  writeStorage(TENANT_KEY, next)
}

export function getTenantId(): string | null {
  return tenantId
}

/** 从 localStorage 恢复租户到内存（应用启动时与令牌一起调用）。 */
export function loadStoredTenant(): string | null {
  tenantId = readStorage(TENANT_KEY)
  return tenantId
}

/** 从 localStorage 恢复令牌到内存（应用启动时调用）。 */
export function loadStoredTokens(): TokenBundle | null {
  const storedAccess = readStorage(ACCESS_KEY)
  const storedRefresh = readStorage(REFRESH_KEY)
  const storedRefreshExpires = readStorage(REFRESH_EXPIRES_KEY)
  if (!storedAccess && !storedRefresh) return null
  accessToken = storedAccess
  refreshToken = storedRefresh
  refreshExpiresAt = storedRefreshExpires
  return {
    accessToken: storedAccess ?? '',
    refreshToken: storedRefresh,
    refreshExpiresAt: storedRefreshExpires,
  }
}

/** 清理全部令牌（登出/刷新失败/401）。 */
export function clearTokens(): void {
  accessToken = null
  refreshToken = null
  refreshExpiresAt = null
  tenantId = null
  writeStorage(ACCESS_KEY, null)
  writeStorage(REFRESH_KEY, null)
  writeStorage(REFRESH_EXPIRES_KEY, null)
  writeStorage(TENANT_KEY, null)
}
