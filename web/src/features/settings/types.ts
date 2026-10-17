// ServiceConfig 对应 GET /api/config 返回与 PUT /api/config 接受的字段
export interface ServiceConfig {
  proxy_api_key: string
  proxy: string
  save_history: boolean
  model_mapping: string
  account_cooldown: string
  session_ttl: string
  init_timeout: string
  per_account_concurrency: number
  routing_strategy: 'round-robin' | 'fill-first'
  log_level: 'debug' | 'info' | 'warn' | 'error'
  request_body_log: boolean
  admin_auth_enabled: boolean
  admin_username: string
  admin_password_set: boolean
  listen_addr: string
  auth_states: string
  log_format: 'json' | 'text'
  active_listen_addr: string
  restart_required: boolean
  restart_fields: string[]
}

// ConfigPatch 是提交给 PUT /api/config 的可写字段
export type ConfigPatch = Partial<{
  proxy_api_key: string
  proxy: string
  save_history: boolean
  model_mapping: string
  account_cooldown: string
  session_ttl: string
  init_timeout: string
  per_account_concurrency: number
  routing_strategy: ServiceConfig['routing_strategy']
  log_level: ServiceConfig['log_level']
  request_body_log: boolean
  admin_auth_enabled: boolean
  admin_username: string
  admin_password: string
  listen_addr: string
  auth_states: string
  log_format: ServiceConfig['log_format']
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
