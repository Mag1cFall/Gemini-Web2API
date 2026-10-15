import { useQuery } from '@tanstack/react-query'

import type { Cooldown, Model } from '@/features/models/types'
import { api } from '@/lib/api'

// modelsKey 是模型目录的查询键，事件流推送时直接写入
export const modelsKey = ['models'] as const

// cooldownsKey 是冷却列表的查询键，事件流推送时直接写入
export const cooldownsKey = ['cooldowns'] as const

// useModels 读取各就绪账户模型目录的合集
export function useModels() {
  return useQuery({ queryKey: modelsKey, queryFn: () => api<{ models: Model[] }>('/api/models') })
}

// useCooldowns 读取账户与模型冷却
export function useCooldowns() {
  return useQuery({
    queryKey: cooldownsKey,
    queryFn: () => api<{ cooldowns: Cooldown[] }>('/api/cooldowns'),
  })
}
