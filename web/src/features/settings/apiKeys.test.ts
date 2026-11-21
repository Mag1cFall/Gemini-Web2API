import { describe, expect, test } from 'vitest'

import { formatAccountBindings, modelsForKey, parseAccountBindings } from './apiKeys'

describe('parseAccountBindings', () => {
  test('按最后一个冒号分开 key 与账号，账号以竖线分隔', () => {
    expect(parseAccountBindings('a:b:acc-1|acc-2,c:acc-3')).toEqual(
      new Map([
        ['a:b', ['acc-1', 'acc-2']],
        ['c', ['acc-3']],
      ]),
    )
  })

  test('空串没有绑定', () => {
    expect(parseAccountBindings('').size).toBe(0)
  })
})

describe('formatAccountBindings', () => {
  test('按名称排序写成 名称:账号|账号，没有账号的名称不写', () => {
    expect(
      formatAccountBindings(
        new Map([
          ['gemini-4-carbon', ['acc-2', 'acc-1']],
          ['gemini-4-hydrogen', ['acc-3']],
          ['gemini-4-argon', []],
        ]),
      ),
    ).toBe('gemini-4-carbon:acc-2|acc-1,gemini-4-hydrogen:acc-3')
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
