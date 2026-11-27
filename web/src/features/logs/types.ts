import type { RequestInfo } from '@/features/requests/types'

// LogLevel 对应 internal/logging 的日志级别
export type LogLevel = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR'

// LogEvent 对应 internal/logging 的稳定事件名
export type LogEvent =
  'runtime.message' | 'request.started' | 'request.progress' | 'request.finished'

// LogFieldEntry 对应 internal/logging 的 Field，key 为空时只有取值
export interface LogFieldEntry {
  key: string
  value: string
}

// LogEntry 对应 internal/logging 的 Event，seq 为服务本次运行内递增的发布序号，fields 为按顺序排列的字段，request 是请求相关事件发生时的请求快照
export interface LogEntry {
  seq: number
  time: string
  level: LogLevel
  source: string
  message: string
  fields?: LogFieldEntry[]
  event: LogEvent
  request?: RequestInfo
}

// SavedLog 对应 internal/requestdb 的 Log，seq 为写入序号
export interface SavedLog extends LogEntry {
  seq: number
}

// LogPage 对应 GET /api/logs 的一页已保存日志，next_before 为下一页的 before
export interface LogPage {
  items: SavedLog[]
  next_before?: number
}
