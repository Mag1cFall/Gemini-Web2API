import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import type { Plugin } from 'vite'
import { compression, defineAlgorithm } from 'vite-plugin-compression2'
import { defineConfig } from 'vitest/config'

const serviceTarget = 'http://127.0.0.1:8007'

// GenerateBundle 是插件 generateBundle 钩子的函数形式
type GenerateBundle = Extract<NonNullable<Plugin['generateBundle']>, (...args: never[]) => unknown>

// compileHintFirst 在 vite 往块开头写入预加载依赖表之后、gzip 压缩之前，于各块第一行写入 V8 显式编译提示，块在流式解析时于后台编译全部函数；V8 只采用位于脚本开头的编译提示
function compileHintFirst(): Plugin {
  return {
    name: 'compile-hint-first',
    apply: 'build',
    configResolved(config) {
      const analysis = config.plugins.find((plugin) => plugin.name === 'vite:build-import-analysis')
      if (!analysis) throw new Error('vite:build-import-analysis not found')
      const generate = analysis.generateBundle as GenerateBundle
      analysis.generateBundle = async function (options, bundle, isWrite) {
        await generate.call(this, options, bundle, isWrite)
        for (const chunk of Object.values(bundle)) {
          if (chunk.type === 'chunk')
            chunk.code = `//# allFunctionsCalledOnLoad
${chunk.code}`
        }
      }
    },
  }
}

// defineConfig 定义前端构建、本地联调与单元测试入口
export default defineConfig({
  base: './',
  plugins: [
    react(),
    tailwindcss(),
    compileHintFirst(),
    compression({
      algorithms: [defineAlgorithm('gzip', { level: 9 })],
      exclude: /index\.html$/,
      deleteOriginalAssets: true,
    }),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@server-messages': fileURLToPath(new URL('../internal/i18n/messages', import.meta.url)),
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
