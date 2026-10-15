import type { RequestInfo } from '@/features/requests/types'

// LogLevel 对应 internal/logging 的日志级别
export type LogLevel = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR'

// LogEvent 对应 internal/logging 的稳定事件名
export type LogEvent =
  'runtime.message' | 'request.started' | 'request.progress' | 'request.finished'

// LogEntry 对应 internal/logging 的 Event，request 是请求相关事件发生时的请求快照
export interface LogEntry {
  time: string
  level: LogLevel
  source: string
  message: string
  event: LogEvent
  request?: RequestInfo
}
