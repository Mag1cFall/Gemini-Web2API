import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

const serviceTarget = 'http://127.0.0.1:8007'

// defineConfig 定义前端构建、本地联调与单元测试入口
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: fileURLToPath(new URL('../internal/webui/dist', import.meta.url)),
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': { target: serviceTarget, changeOrigin: false },
      '/v1': serviceTarget,
      '/v1beta': serviceTarget,
      '/readyz': serviceTarget,
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
  },
})
