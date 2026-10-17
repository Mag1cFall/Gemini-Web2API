import { useQuery } from '@tanstack/react-query'

import type { ProxyStatus, ServiceConfig } from '@/features/settings/types'
import { api } from '@/lib/api'

// configKey 是服务配置的查询键
export const configKey = ['config'] as const

// useConfig 读取 .env 中的服务配置
export function useConfig() {
  return useQuery({ queryKey: configKey, queryFn: () => api<ServiceConfig>('/api/config') })
}

// proxiesKey 是代理池状态的查询键
export const proxiesKey = ['proxies'] as const

// useProxies 读取代理池中每个代理的检测结果与绑定的账号
export function useProxies() {
  return useQuery({
    queryKey: proxiesKey,
    queryFn: () => api<{ proxies: ProxyStatus[] }>('/api/proxies'),
    select: (data) => data.proxies,
    refetchInterval: 5000,
  })
}
