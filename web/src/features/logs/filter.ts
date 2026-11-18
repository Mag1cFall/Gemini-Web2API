import type { LogEntry, LogFieldEntry, LogLevel } from '@/features/logs/types'
import { serverText } from '@/lib/i18n'

// logText 是一条日志的消息与字段拼成的单行文字，用于搜索与时间线
export function logText(message: string, fields: LogFieldEntry[] = []) {
  return [
    serverText(message),
    ...fields.map((field) =>
      field.key ? serverText(field.key) + '=' + serverText(field.value) : serverText(field.value),
    ),
  ].join(' | ')
}

// levelRank 是日志级别的严重程度
const levelRank: Record<LogLevel, number> = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 }

// minimumLevels 是级别筛选的取值，all 显示全部
export const minimumLevels = ['all', 'INFO', 'WARN', 'ERROR'] as const

// MinimumLevel 是级别筛选的取值
export type MinimumLevel = (typeof minimumLevels)[number]

// LogFilter 是日志页的筛选条件
export interface LogFilter {
  level: MinimumLevel
  source: string
  q: string
}

// LogRow 是日志列表的一行：一条服务日志，或同一请求从开始到结束的全部事件，entry 为最新一条
export interface LogRow {
  key: string
  entry: LogEntry
  events: LogEntry[]
}

// entryKeys 为服务日志分配不随数组截断变化的行标识
const entryKeys = new WeakMap<LogEntry, string>()
let nextEntryKey = 0

// entryKey 返回服务日志对象的稳定行标识
function entryKey(entry: LogEntry) {
  let key = entryKeys.get(entry)
  if (key === undefined) {
    key = 'log:' + String(nextEntryKey++)
    entryKeys.set(entry, key)
  }
  return key
}

// parseLogFilter 从 URL 参数读取日志筛选条件
export function parseLogFilter(params: URLSearchParams): LogFilter {
  const level = params.get('level')
  return {
    level: minimumLevels.find((value) => value === level) ?? 'all',
    source: params.get('source') ?? '',
    q: params.get('q') ?? '',
  }
}

// groupLogs 按请求 ID 把请求的开始、进度与结束事件合成一行，服务日志保持原始顺序
export function groupLogs(logs: LogEntry[]): LogRow[] {
  const rows: LogRow[] = []
  const requests = new Map<string, LogRow>()
  for (const entry of logs) {
    const id = entry.request?.id
    const row = id === undefined ? undefined : requests.get(id)
    if (row) {
      row.events.push(entry)
      row.entry = entry
      continue
    }
    const next = { key: id ?? entryKey(entry), entry, events: [entry] }
    rows.push(next)
    if (id !== undefined) requests.set(id, next)
  }
  return rows
}

// involves 判断一行是否有来自 source 的事件，或请求由该账号处理、该账号参与过尝试
function involves(row: LogRow, source: string) {
  const request = row.entry.request
  return (
    row.events.some((event) => event.source === source) ||
    request?.account === source ||
    request?.attempts?.some((attempt) => attempt.account === source) === true
  )
}

// matchRow 判断一行最新事件的级别是否达到最低级别、是否涉及所选来源并命中搜索词
export function matchRow(row: LogRow, filter: LogFilter) {
  if (filter.level !== 'all' && levelRank[row.entry.level] < levelRank[filter.level]) return false
  if (filter.source && !involves(row, filter.source)) return false
  const needle = filter.q.trim().toLowerCase()
  if (!needle) return true
  const request = row.entry.request
  const fields = [
    ...row.events.flatMap((event) => [
      logText(event.message, event.fields),
      event.source,
      event.event,
    ]),
    request?.id,
    request?.model,
    request?.path,
    request?.account,
    request?.status === undefined ? undefined : String(request.status),
    request?.error === undefined ? undefined : serverText(request.error),
    request?.finish_reason,
  ]
  return fields.some((field) => field?.toLowerCase().includes(needle) === true)
}
