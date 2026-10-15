import { describe, expect, test } from 'vitest'

import {
  formatBytes,
  formatDateTime,
  formatDuration,
  formatPercent,
  formatRelative,
} from '@/lib/format'

describe('formatDuration', () => {
  test('按量级选择单位', () => {
    expect(formatDuration(850, 'en')).toBe('850ms')
    expect(formatDuration(1234, 'en')).toBe('1.23s')
    expect(formatDuration(42_500, 'en')).toBe('42.5s')
    expect(formatDuration(150_000, 'en')).toBe('2.5m')
    expect(formatDuration(5_400_000, 'en')).toBe('1.5h')
  })
})

describe('formatPercent', () => {
  test('整数百分比不带小数，其余保留一位', () => {
    expect(formatPercent(0.5, 'en')).toBe('50%')
    expect(formatPercent(0.123, 'en')).toBe('12.3%')
    expect(formatPercent(1, 'en')).toBe('100%')
    expect(formatPercent(0.0004, 'en')).toBe('0%')
  })
})

describe('formatBytes', () => {
  test('以 1024 进位', () => {
    expect(formatBytes(512, 'en')).toBe('512 byte')
    expect(formatBytes(65_536, 'en')).toBe('64 kB')
    expect(formatBytes(1_572_864, 'en')).toBe('1.5 MB')
  })
})

describe('formatRelative', () => {
  const now = new Date('2026-10-14T12:00:00Z')
  test('过去与将来都换算到最大的整单位', () => {
    expect(formatRelative(new Date('2026-10-14T11:57:00Z'), now, 'en')).toBe('3 minutes ago')
    expect(formatRelative(new Date('2026-10-14T14:00:00Z'), now, 'en')).toBe('in 2 hours')
    expect(formatRelative(new Date('2026-10-12T12:00:00Z'), now, 'en')).toBe('2 days ago')
    expect(formatRelative(new Date('2026-10-14T11:59:30Z'), now, 'zh-CN')).toBe('30秒钟前')
  })
})

describe('formatDateTime', () => {
  test('同一天只输出时间，跨天带日期', () => {
    const now = new Date(2026, 9, 14, 18, 0, 0)
    expect(formatDateTime(new Date(2026, 9, 14, 9, 5, 7), now, 'en')).toBe('09:05:07')
    expect(formatDateTime(new Date(2026, 9, 13, 23, 0, 0), now, 'en')).toBe('10/13, 23:00:00')
  })
})
