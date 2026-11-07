/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 后端 API 源站（如 http://127.0.0.1:8080）。为空时使用同源，开发环境由 Vite 代理 /api 与 /ws。 */
  readonly VITE_API_BASE?: string
  /** 顶栏站点标题。 */
  readonly VITE_SITE_NAME?: string
  /** 可选鉴权令牌：REST 加 Authorization: Bearer，WS 加 ?token=。 */
  readonly VITE_API_TOKEN?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
