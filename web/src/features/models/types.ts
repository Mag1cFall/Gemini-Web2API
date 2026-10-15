// Model 对应 GET /api/models 的 model
export interface Model {
  id: string
  display_name: string
  description?: string
  default: boolean
  alias_of?: string
  capabilities: string[]
  // tool 为该条目选中的官网工具，普通对话模型没有该字段
  tool?: 'image' | 'video' | 'music' | 'canvas' | 'deep_research' | 'guided_learning' | 'task'
  thinking_levels: string[]
  min_context_window: number
  max_context_window: number
  accounts: string[]
  cooling_accounts: string[]
}

// Cooldown 对应 GET /api/cooldowns 的 cooldown，model 为空表示整个账户
export interface Cooldown {
  account_id: string
  model: string
  until: string
  reason: string
}
