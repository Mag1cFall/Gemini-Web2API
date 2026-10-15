// RequestState 对应 internal/logging 的请求状态，running 只出现在进行中的请求
export type RequestState = 'running' | 'completed' | 'tool_calls' | 'failed' | 'cancelled'

// FinishedState 是已结束请求的状态
export type FinishedState = Exclude<RequestState, 'running'>

// RequestProtocol 是公开协议入口
export type RequestProtocol =
  | 'chat'
  | 'responses'
  | 'compact'
  | 'messages'
  | 'gemini'
  | 'completions'
  | 'images'
  | 'videos'
  | 'speech'
  | 'transcriptions'
  | 'translations'
  | 'embeddings'
  | 'moderations'
  | 'realtime'
  | 'live'

// RequestUsage 对应 internal/logging 的 Usage
export interface RequestUsage {
  input_tokens: number
  reasoning_tokens: number
  reply_tokens: number
  output_tokens: number
  total_tokens: number
}

// Attempt 对应 internal/logging 的 Attempt，是一次未成功的上游尝试，status 为 0 表示没有取得响应
export interface Attempt {
  account: string
  status: number
  error: string
  duration_ms: number
}

// RequestInfo 对应 internal/logging 的 RequestInfo，可选字段为空时后端省略
export interface RequestInfo {
  id: string
  response_id?: string
  conversation_id?: string
  state: RequestState
  protocol?: RequestProtocol
  method: string
  path: string
  client_ip?: string
  model?: string
  provider_model?: string
  account?: string
  stream: boolean
  status?: number
  started_at: string
  duration_ms: number
  queue_ms?: number
  first_event_ms?: number
  stage?: string
  messages?: number
  media?: number
  tools?: number
  tool_calls?: number
  finish_reason?: string
  usage?: RequestUsage
  error?: string
  attempts?: Attempt[]
  upstream_bytes?: number
  heartbeats?: number
}

// RequestBody 对应 GET /api/requests/{id}/body，正文截断到 64 KiB，size 为原始字节数
export interface RequestBody {
  id: string
  time: string
  request: string
  request_size: number
  response: string
  response_size: number
}
