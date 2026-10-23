import type { Attempt, FinishedState, RequestProtocol } from '@/features/requests/types'

// UsageDimension 是用量可筛选与堆叠的维度
export type UsageDimension = 'model' | 'account' | 'protocol' | 'state'

// UsageGroupDimension 是用量分项的维度，比筛选维度多出 HTTP 状态码
export type UsageGroupDimension = UsageDimension | 'status'

// UsageLatency 对应 internal/requestdb 的 UsageLatency
export interface UsageLatency {
  avg_ms: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
}

// UsageStats 对应 internal/requestdb 的 UsageStats，耗时与首个输出只统计成功请求，成功包含工具调用
export interface UsageStats {
  requests: number
  succeeded: number
  failed: number
  canceled: number
  rate_limited: number
  input_tokens: number
  reasoning_tokens: number
  reply_tokens: number
  total_tokens: number
  duration: UsageLatency
  first_event: UsageLatency
  queue_avg_ms: number
  last_at?: string
}

// UsageBucket 是一个时间分桶的用量与按 Gemini API 单价估算的美元费用
export interface UsageBucket extends UsageStats {
  at: string
  cost: number
  priced_requests: number
}

// UsageGroup 是一个维度取值的用量
export interface UsageGroup extends UsageStats {
  key: string
}

// UsagePair 是账号与模型组合的用量
export interface UsagePair extends UsageStats {
  account: string
  model: string
}

// UsageSeries 是堆叠趋势中一个维度取值逐桶的请求数与 token，other 合并排名第 8 之后的取值
export interface UsageSeries {
  key: string
  other?: boolean
  requests: number[]
  tokens: number[]
}

// UsageRecent 是最近几分钟的请求与 token
export interface UsageRecent {
  minutes: number
  requests: number
  tokens: number
}

// UsageReport 对应 GET /api/usage，options 列出不受筛选影响的候选，没有取值的维度为 null
export interface UsageReport {
  from: string
  to: string
  bucket_seconds: number
  generated_at: string
  latest_at?: string
  totals: UsageStats
  previous: UsageStats
  cost: number
  previous_cost: number
  priced_requests: number
  previous_priced_requests: number
  recent: UsageRecent
  buckets: UsageBucket[]
  stack_by: UsageDimension
  series: UsageSeries[]
  groups: Record<UsageGroupDimension, UsageGroup[]>
  pairs: UsagePair[]
  options: Record<UsageDimension, string[] | null>
}

// UsageRecord 对应 internal/requestdb 的 UsageRecord，是一条已结束请求的账本记录，time 为完成时间
export interface UsageRecord {
  id: string
  response_id: string
  time: string
  protocol: RequestProtocol
  path: string
  model: string
  provider_model: string
  thinking: string
  account: string
  status: number
  state: FinishedState
  duration_ms: number
  first_event_ms: number
  queue_ms: number
  input_tokens: number
  reasoning_tokens: number
  reply_tokens: number
  total_tokens: number
  tool_calls: number
  error: string
  attempts: Attempt[]
  has_body: boolean
}

// UsageRecordPage 对应 GET /api/usage/records，next_cursor 缺失表示没有更早的记录
export interface UsageRecordPage {
  items: UsageRecord[]
  next_cursor?: string
}
