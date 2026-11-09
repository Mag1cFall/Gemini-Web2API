import { describe, expect, test } from 'vitest'

import {
  claudeEnv,
  codexEnv,
  curlAnthropic,
  curlGemini,
  curlOpenai,
  magpieImport,
  ompEnv,
  ompModels,
} from './snippets'

const input = {
  origin: 'http://127.0.0.1:8007',
  key: `a b$c&d"e'f`,
  model: `mo"del'x`,
  smallModel: 'small',
  models: [],
}

describe('环境变量命令', () => {
  test('POSIX shell 用单引号字面量，单引号写成结束引号、转义单引号与重新开始', () => {
    expect(codexEnv(input, 'unix')).toBe(`export GEMINI_WEB2API_KEY='a b$c&d"e'\\''f'\ncodex`)
    expect(claudeEnv(input, 'unix').split('\n')[0]).toBe(
      `export ANTHROPIC_BASE_URL='http://127.0.0.1:8007'`,
    )
  })

  test('PowerShell 用单引号字面量，单引号写两遍', () => {
    expect(codexEnv(input, 'windows')).toBe(`$env:GEMINI_WEB2API_KEY = 'a b$c&d"e''f'\ncodex`)
  })

  test('PowerShell 把弯单引号当作单引号，同样写两遍', () => {
    expect(codexEnv({ ...input, key: 'x\u2019y\u2018z' }, 'windows')).toBe(
      `$env:GEMINI_WEB2API_KEY = 'x\u2019\u2019y\u2018\u2018z'\ncodex`,
    )
  })
})

describe('curl 命令', () => {
  const key = `a b$c&d"e'\\''f`

  test('OpenAI 的地址、key 与请求体都是 POSIX 字面量，模型按 JSON 转义', () => {
    expect(curlOpenai(input).split('\n')).toEqual([
      `curl 'http://127.0.0.1:8007/v1/chat/completions' \\`,
      `  -H 'Authorization: Bearer ${key}' \\`,
      '  -H "Content-Type: application/json" \\',
      `  -d '{"model": "mo\\"del'\\''x", "stream": true, "messages": [{"role": "user", "content": "Hello"}]}'`,
    ])
  })

  test('Anthropic 的 key 与模型都是 POSIX 字面量', () => {
    const lines = curlAnthropic(input).split('\n')
    expect(lines[1]).toBe(`  -H 'x-api-key: ${key}' \\`)
    expect(lines[4]).toContain(`'{"model": "mo\\"del'\\''x", "max_tokens": 1024,`)
  })

  test('Gemini 的地址与 key 都是 POSIX 字面量', () => {
    const lines = curlGemini(input).split('\n')
    expect(lines[0]).toBe(
      `curl 'http://127.0.0.1:8007/v1beta/models/mo"del'\\''x:generateContent' \\`,
    )
    expect(lines[1]).toBe(`  -H 'x-goog-api-key: ${key}' \\`)
  })
})

describe('agent 接入配置', () => {
  const agent = {
    origin: 'http://127.0.0.1:8007',
    key: 'k',
    model: 'gemini-3.1-pro',
    smallModel: 'small',
    models: [{ id: 'gemini-3.1-pro', name: '3.1 Pro' }],
  }

  test('omp 经 Gemini 协议接入，模型写入配置，key 取自环境变量', () => {
    expect(ompModels(agent)).toContain('baseUrl: http://127.0.0.1:8007/v1beta')
    expect(ompModels(agent)).toContain('api: google-generative-ai')
    expect(ompModels(agent)).toContain('- id: gemini-3.1-pro')
    expect(ompEnv(agent, 'unix')).toBe(
      "export GEMINI_WEB2API_KEY='k'\nomp --provider gemini-web2api --model gemini-3.1-pro",
    )
  })

  test('Magpie 导入链接带四种协议的地址与 key', () => {
    const link = new URL(magpieImport(agent).slice("magpie import '".length, -1))
    expect(link.protocol).toBe('magpie:')
    expect(link.searchParams.get('chat')).toBe('http://127.0.0.1:8007/v1')
    expect(link.searchParams.get('anthropic')).toBe('http://127.0.0.1:8007')
    expect(link.searchParams.get('gemini')).toBe('http://127.0.0.1:8007/v1beta')
    expect(link.searchParams.get('key')).toBe('k')
  })
})
