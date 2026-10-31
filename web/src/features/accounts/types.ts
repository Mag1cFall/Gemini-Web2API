// AccountState 对应 internal/admin 的账户状态
export type AccountState =
  'initializing' | 'ready' | 'cooldown' | 'auth_required' | 'unavailable' | 'disabled'

// AccountSource 是账户导入来源
export type AccountSource = 'chrome' | 'brave' | 'edge' | 'firefox' | 'cookie' | 'guest'

// BrowserType 是支持导入的浏览器类型
export type BrowserType = 'chrome' | 'brave' | 'edge' | 'firefox'

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

// ChromeProfile 对应 GET /api/accounts/import/chrome 或 /api/accounts/import/browser 的 profiles 项
export interface ChromeProfile {
  id: string
  browser?: BrowserType
  profile: string
  display_name: string
  email: string
  auth_user?: number
  importable: boolean
  imported: boolean
  // reason 为账号不可导入的原因，不可导入且缺省时为未在浏览器登录
  reason?: 'read_failed' | 'needs_reauth' | 'browser_running'
}

export type BrowserProfile = ChromeProfile

// ChromeProfiles 对应 GET /api/accounts/import/chrome 或 /api/accounts/import/browser 的响应
export interface ChromeProfiles {
  browser?: BrowserType
  profiles: ChromeProfile[]
}

export type BrowserProfiles = ChromeProfiles

// ImportFailure 对应批量导入时单个账号的失败项
export interface ImportFailure {
  target: string
  error: string
}

// ImportBrowserResponse 对应 POST /api/accounts/import/browser 的响应
export interface ImportBrowserResponse {
  accounts: Account[]
  failures?: ImportFailure[]
}

// PairingToken 对应 POST /api/pairing 的响应
export interface PairingToken {
  token: string
  expires_at: string
}
