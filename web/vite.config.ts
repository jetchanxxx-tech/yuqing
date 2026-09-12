import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    // echarts 为独立懒加载块（仅图表渲染时按需下载），体积上限放宽
    chunkSizeWarningLimit: 600,
  },
  server: {
    proxy: {
      // 开发环境将 /api 代理到本地 platform API（yuqing-server :8080）
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
})
