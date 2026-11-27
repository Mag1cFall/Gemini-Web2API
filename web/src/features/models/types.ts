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
  // price 为估算费用所用的 Gemini API 单价（美元每百万 token），没有对应单价的模型没有该字段
  price?: ModelPrice
  // hidden 表示该模型当前在设置中关闭、不在公开 API 中开放
  hidden?: boolean
  // predefined_gem 表示该条目是官网 CNgdBe 类型 3 的 Google 预置 Gem
  predefined_gem?: boolean
}

// ModelPrice 对应 /api/models 的 price：long_ 前缀为输入超过 20 万 token 时的单价，media 为生成图片或视频的模型回复 token 的单价，song 为每首歌的单价
export interface ModelPrice {
  input: number
  output: number
  long_input?: number
  long_output?: number
  media?: number
  song?: number
}

// Cooldown 对应 GET /api/cooldowns 的 cooldown，model 为空表示整个账户
export interface Cooldown {
  account_id: string
  model: string
  until: string
  reason: string
}
