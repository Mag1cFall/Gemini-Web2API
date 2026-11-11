// ServiceConfig 对应 GET /api/config 返回与 PUT /api/config 接受的字段
export interface ServiceConfig {
  proxy_api_key: string
  proxy: string
  save_history: boolean
  nonstream_keepalive: boolean
  fake_stream: boolean
  model_mapping: string
  account_cooldown: string
  session_ttl: string
  init_timeout: string
  per_account_concurrency: number
  routing_strategy: 'round-robin' | 'fill-first'
  log_level: 'debug' | 'info' | 'warn' | 'error'
  request_body_log: boolean
  log_file: boolean
  log_max_total_mb: number
  admin_auth_enabled: boolean
  admin_username: string
  admin_password_set: boolean
  listen_addr: string
  auth_states: string
  log_format: 'json' | 'text'
  // api_key_accounts 是 API key 可用账号的 .env 写法：key:账号|账号，逗号分隔
  api_key_accounts: string
  // account_order 是 fill-first 依次使用的账号 ID，逗号分隔
  account_order: string
  // model_accounts 是模型可用账号的 .env 写法：模型 ID:账号|账号，逗号分隔
  model_accounts: string
  // first_event_hedge 是首事件备用请求方式：off、auto 或等待秒数
  first_event_hedge: string
  first_event_hedge_models: string
  active_listen_addr: string
  restart_required: boolean
  restart_fields: string[]
  // invalid 是启动时取值无效、按默认值运行的 .env 项
  invalid: InvalidValue[]
  // locked 是由进程环境变量（env）或启动参数（flag）决定的配置项，键为 .env 名
  locked: Record<string, 'env' | 'flag'>
}

// InvalidValue 对应 internal/config 的 InvalidValue
export interface InvalidValue {
  key: string
  value: string
  reason: string
}

// ConfigPatch 是提交给 PUT /api/config 的可写字段
export type ConfigPatch = Partial<{
  proxy_api_key: string
  proxy: string
  save_history: boolean
  nonstream_keepalive: boolean
  fake_stream: boolean
  model_mapping: string
  account_cooldown: string
  session_ttl: string
  init_timeout: string
  per_account_concurrency: number
  routing_strategy: ServiceConfig['routing_strategy']
  log_level: ServiceConfig['log_level']
  request_body_log: boolean
  log_file: boolean
  log_max_total_mb: number
  admin_auth_enabled: boolean
  admin_username: string
  admin_password: string
  listen_addr: string
  auth_states: string
  log_format: ServiceConfig['log_format']
  api_key_accounts: string
  account_order: string
  model_accounts: string
  first_event_hedge: string
  first_event_hedge_models: string
}>

// ProxyStatus 对应 GET /api/proxies 的 proxy，checked_at 在首次检测前省略
export interface ProxyStatus {
  url: string
  healthy: boolean
  checked_at?: string
  latency_ms: number
  error?: string
  accounts: string[]
}
