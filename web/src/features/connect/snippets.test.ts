import { describe, expect, test } from 'vitest'

import { claudeEnv, codexEnv, curlAnthropic, curlGemini, curlOpenai } from './snippets'

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
