import { useQuery } from '@tanstack/react-query'

import type { ServiceConfig } from '@/features/settings/types'
import { api } from '@/lib/api'

// configKey 是服务配置的查询键
export const configKey = ['config'] as const

// useConfig 读取 .env 中的服务配置
export function useConfig() {
  return useQuery({ queryKey: configKey, queryFn: () => api<ServiceConfig>('/api/config') })
}
