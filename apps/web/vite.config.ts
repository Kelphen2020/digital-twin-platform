import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

/**
 * P0 Web 客户端构建配置。
 *
 * 开发/预览服务器默认把同源的 `/api` 与 `/ws` 代理到 Go API（http://127.0.0.1:8080），
 * 前端代码本身使用相对路径，因此无需处理跨域。
 * 如前后端分离部署，可通过 VITE_API_BASE 指定后端源站：此时代理目标与前端请求地址都指向该源站。
 */
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const proxyTarget = env.VITE_API_BASE || 'http://127.0.0.1:8080'

  const proxy = {
    '/api': { target: proxyTarget, changeOrigin: true },
    // ws: true 让 Vite 处理 WebSocket Upgrade，前端统一连接同源 /ws
    '/ws': { target: proxyTarget, changeOrigin: true, ws: true },
  }

  return {
    plugins: [vue()],
    server: {
      host: '127.0.0.1',
      port: 5173,
      strictPort: true,
      proxy,
    },
    preview: {
      host: '127.0.0.1',
      port: 4173,
      strictPort: true,
      proxy,
    },
  }
})
