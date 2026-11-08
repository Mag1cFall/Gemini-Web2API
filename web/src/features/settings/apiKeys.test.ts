import { describe, expect, test } from 'vitest'

import { maskKey, modelsForKey, parseKeyAccounts } from './apiKeys'

describe('maskKey', () => {
  test('不超过 12 个字符时完整显示', () => {
    expect(maskKey('123456789012')).toBe('123456789012')
  })

  test('更长时只显示首尾各 4 个字符', () => {
    expect(maskKey('1234567890123')).toBe('1234…0123')
  })
})

describe('parseKeyAccounts', () => {
  test('按最后一个冒号分开 key 与账号，账号以竖线分隔', () => {
    expect(parseKeyAccounts('a:b:acc-1|acc-2,c:acc-3')).toEqual(
      new Map([
        ['a:b', ['acc-1', 'acc-2']],
        ['c', ['acc-3']],
      ]),
    )
  })

  test('空串没有绑定', () => {
    expect(parseKeyAccounts('').size).toBe(0)
  })
})

describe('modelsForKey', () => {
  const models = [
    { id: 'only-a', accounts: ['acc-a'] },
    { id: 'both', accounts: ['acc-a', 'acc-b'] },
  ]

  test('绑定了账号的 key 只保留这些账号提供的模型', () => {
    expect(modelsForKey(models, ['acc-b']).map((model) => model.id)).toEqual(['both'])
  })

  test('未绑定的 key 保留全部模型', () => {
    expect(modelsForKey(models, undefined)).toEqual(models)
  })
})
