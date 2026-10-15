import { describe, expect, it } from 'vitest'

import type { Account } from '@/features/accounts/types'
import { stateTone, windowTone } from '@/features/accounts/usage'

// sampleAccount 是一条用于断言的账户样例
const sampleAccount: Account = {
  account_id: 'a@gmail.com',
  state: 'ready',
  enabled: true,
  source: 'chrome',
  profile: 'Default',
  tier: 'free',
  context_window: 1_000_000,
  models: ['gemini-3.8-flash'],
  pending: 0,
  consecutive_failures: 0,
}

describe('stateTone', () => {
  it('把账户状态映射到标签语气', () => {
    expect(stateTone(sampleAccount.state)).toBe('success')
    expect(stateTone('cooldown')).toBe('warning')
    expect(stateTone('auth_required')).toBe('error')
    expect(stateTone('unavailable')).toBe('error')
    expect(stateTone('initializing')).toBe('info')
    expect(stateTone('disabled')).toBe('neutral')
  })
})

describe('windowTone', () => {
  it('按已用比例给出语气', () => {
    expect(windowTone(0.2)).toBe('info')
    expect(windowTone(0.75)).toBe('warning')
    expect(windowTone(0.95)).toBe('error')
  })
})
