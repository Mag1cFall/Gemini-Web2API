import { describe, expect, it } from 'vitest'

import { groupLogs, matchRow, parseLogFilter } from '@/features/logs/filter'
import type { LogEntry } from '@/features/logs/types'
import type { RequestInfo } from '@/features/requests/types'

// sample 构造一条服务日志样例
function sample(overrides: Partial<LogEntry>): LogEntry {
  return {
    time: '2026-10-14T10:00:00Z',
    level: 'INFO',
    source: 'service',
    message: '服务已启动 | 地址=http://127.0.0.1:8007',
    event: 'runtime.message',
    ...overrides,
  }
}

// request 构造一条请求快照样例
function request(overrides: Partial<RequestInfo>): RequestInfo {
  return {
    id: 'req_1',
    state: 'running',
    method: 'POST',
    path: '/v1/chat/completions',
    stream: true,
    started_at: '2026-10-14T10:00:00Z',
    duration_ms: 0,
    ...overrides,
  }
}

// lifecycle 是一个请求从开始、进度到结束的三条事件
const lifecycle: LogEntry[] = [
  sample({
    source: 'request',
    message: '请求开始 | chat | gemini-3.8-flash',
    event: 'request.started',
    request: request({ model: 'gemini-3.8-flash' }),
  }),
  sample({
    level: 'WARN',
    source: 'alice-gmail-com',
    message: '上游首事件等待 | 已等待=15s',
    event: 'request.progress',
    request: request({ model: 'gemini-3.8-flash', account: 'alice-gmail-com' }),
  }),
  sample({
    source: 'alice-gmail-com',
    message: '请求完成 | 200 | gemini-3.8-flash',
    event: 'request.finished',
    request: request({
      model: 'gemini-3.8-flash',
      account: 'alice-gmail-com',
      state: 'completed',
      status: 200,
    }),
  }),
]

describe('parseLogFilter', () => {
  it('从 URL 参数读取筛选条件，非法级别回到 all', () => {
    const filter = parseLogFilter(new URLSearchParams('level=WARN&source=request&q=pro'))
    expect(filter).toEqual({ level: 'WARN', source: 'request', q: 'pro' })
    expect(parseLogFilter(new URLSearchParams('level=bogus')).level).toBe('all')
  })
})

describe('groupLogs', () => {
  it('同一请求的事件合成一行，位置在开始处，entry 为最新事件', () => {
    const before = sample({ message: 'a' })
    const between = sample({ message: 'b' })
    const logs = [before, lifecycle[0], between, lifecycle[1], lifecycle[2]].filter(
      (entry): entry is LogEntry => entry !== undefined,
    )
    const rows = groupLogs(logs)
    expect(rows.map((row) => row.entry.message)).toEqual([
      'a',
      '请求完成 | 200 | gemini-3.8-flash',
      'b',
    ])
    expect(rows[1]?.key).toBe('req_1')
    expect(rows[1]?.events).toHaveLength(3)
    expect(rows[1]?.entry.request?.state).toBe('completed')
  })

  it('服务日志的行标识在数组截断后保持不变', () => {
    const first = sample({ message: 'a' })
    const second = sample({ message: 'b' })
    const keys = groupLogs([first, second]).map((row) => row.key)
    expect(groupLogs([second]).map((row) => row.key)).toEqual([keys[1]])
  })
})

describe('matchRow', () => {
  const [row] = groupLogs(lifecycle)
  if (!row) throw new Error('lifecycle 应合成一行')

  it('按最新事件的级别过滤，进度中的警告不改变已完成请求的级别', () => {
    const warn = groupLogs([sample({ level: 'WARN' })])[0]
    expect(warn && matchRow(warn, { level: 'WARN', source: '', q: '' })).toBe(true)
    expect(warn && matchRow(warn, { level: 'ERROR', source: '', q: '' })).toBe(false)
    expect(matchRow(row, { level: 'all', source: '', q: '' })).toBe(true)
    expect(matchRow(row, { level: 'INFO', source: '', q: '' })).toBe(true)
    expect(matchRow(row, { level: 'WARN', source: '', q: '' })).toBe(false)
  })

  it('任一事件来自该来源即命中', () => {
    expect(matchRow(row, { level: 'all', source: 'request', q: '' })).toBe(true)
    expect(matchRow(row, { level: 'all', source: 'alice-gmail-com', q: '' })).toBe(true)
    expect(matchRow(row, { level: 'all', source: 'service', q: '' })).toBe(false)
  })

  it('搜索消息、请求 ID、模型与状态码', () => {
    expect(matchRow(row, { level: 'all', source: '', q: '首事件' })).toBe(true)
    expect(matchRow(row, { level: 'all', source: '', q: 'REQ_1' })).toBe(true)
    expect(matchRow(row, { level: 'all', source: '', q: '3.8-flash' })).toBe(true)
    expect(matchRow(row, { level: 'all', source: '', q: '200' })).toBe(true)
    expect(matchRow(row, { level: 'all', source: '', q: 'absent' })).toBe(false)
  })
})
