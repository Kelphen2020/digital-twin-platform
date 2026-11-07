/**
 * 会话令牌的集中读写点。
 * 单独成模块是为了避免 api.ts ↔ store/auth.ts 的循环依赖：
 * - auth store 负责登录、持久化与清理；
 * - api.ts / ws.ts 只读取当前令牌。
 */
let token: string | null = null

export function setAuthToken(next: string | null): void {
  token = next
}

export function getAuthToken(): string | null {
  return token
}
