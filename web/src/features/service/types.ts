import type { Account } from '@/features/accounts/types'

// AccountsSummary 是服务状态里的账户分布
export interface AccountsSummary {
  total: number
  available: number
  cooling_down: number
  initializing: number
  auth_required: number
  unavailable: number
  disabled: number
}

// ServiceStatus 对应 GET /api/status
export interface ServiceStatus {
  version: string
  ready: boolean
  started_at: string
  listen_addr: string
  // reserved_listen 是被 Windows 保留而改用 listen_addr 的配置地址
  reserved_listen?: string
  base_url: string
  api_key_required: boolean
  active_requests: number
  accounts: AccountsSummary
  // guest 是访客会话的状态
  guest?: Account
  desktop: boolean
  data_dir: string
}

// Session 对应 GET /api/auth/session
export interface Session {
  enabled: boolean
  authenticated: boolean
  username: string
}
