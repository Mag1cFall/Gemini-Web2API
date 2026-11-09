// AccountState 对应 internal/admin 的账户状态
export type AccountState =
  'initializing' | 'ready' | 'cooldown' | 'auth_required' | 'unavailable' | 'disabled'

// AccountSource 是账户导入来源
export type AccountSource = 'chrome' | 'cookie' | 'guest'

// UsageWindow 是官网用量窗口，used_ratio 为已用比例
export interface UsageWindow {
  used_ratio: number
  reset_at: string
}

// AccountUsage 对应 internal/gemini 的 UsageInfo，current 为五小时窗口
export interface AccountUsage {
  tier: string
  tier_code: number
  current?: UsageWindow
  weekly?: UsageWindow
  remaining_credits?: number
  use_overage_ai_credits: boolean
  fetched_at: string
}

// ModelCooldown 是单个模型的冷却
export interface ModelCooldown {
  model: string
  until: string
  reason: string
}

// Account 对应 GET /api/accounts 的 account，可选字段为空时后端省略
export interface Account {
  account_id: string
  email?: string
  state: AccountState
  enabled: boolean
  source: AccountSource
  profile?: string
  proxy?: string
  proxy_source?: 'fixed' | 'direct' | 'pool' | 'system'
  // fixed_proxy 是账号固定代理的原文，direct 表示直连，从代理池或系统代理分配时没有
  fixed_proxy?: string
  tier?: string
  context_window?: number
  models: string[]
  pending: number
  last_used?: string
  ready_at?: string
  cooldown_until?: string
  cooldown_reason?: string
  model_cooldowns?: ModelCooldown[]
  consecutive_failures: number
  last_error?: string
  last_error_at?: string
  usage?: AccountUsage
}

// AccountUsageStatus 对应 GET /api/accounts/usage 的 accounts 项，error 为本次读取用量失败的原因
export interface AccountUsageStatus {
  account_id: string
  state: AccountState
  usage?: AccountUsage
  error?: string
}

// AccountsUsage 对应 GET /api/accounts/usage 的响应
export interface AccountsUsage {
  accounts: AccountUsageStatus[]
}

// ChromeProfile 对应 GET /api/accounts/import/chrome 的 profiles 项
export interface ChromeProfile {
  id: string
  profile: string
  display_name: string
  email: string
  importable: boolean
  imported: boolean
  // reason 为账号不可导入的原因，不可导入且缺省时为未在 Chrome 登录
  reason?: 'read_failed' | 'needs_reauth'
}

// ChromeProfiles 对应 GET /api/accounts/import/chrome 的响应
export interface ChromeProfiles {
  profiles: ChromeProfile[]
}

// PairingToken 对应 POST /api/pairing 的响应
export interface PairingToken {
  token: string
  expires_at: string
}

// AccountTaskItem 是导入任务中的一个账号，label 为邮箱
export interface AccountTaskItem {
  label: string
  state: 'pending' | 'running' | 'done' | 'failed'
  error?: string
}

// AccountTask 对应 GET /api/accounts/tasks 的 task
export interface AccountTask {
  id: string
  kind: 'chrome' | 'cookie'
  state: 'running' | 'done' | 'failed'
  items: AccountTaskItem[]
  started_at: string
}

// CookieAccount 对应 POST /api/accounts/import/cookie/discover 的 account，auth_user 为 Cookie 中的会话序号
export interface CookieAccount {
  auth_user: number
  email: string
  id: string
  imported: boolean
}

// WarmupResult 对应 POST /api/warmup 的返回，request 是请求记录 ID
export interface WarmupResult {
  account: string
  model: string
  ok: boolean
  duration_ms: number
  error?: string
  request: string
  usage?: AccountUsage
}
