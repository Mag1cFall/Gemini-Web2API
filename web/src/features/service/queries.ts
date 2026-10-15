import { useQuery } from '@tanstack/react-query'

import type { ServiceStatus, Session } from '@/features/service/types'
import { api } from '@/lib/api'

// statusKey 是服务状态的查询键，事件流推送时直接写入
export const statusKey = ['status'] as const

// sessionKey 是管理会话的查询键
export const sessionKey = ['session'] as const

// useStatus 读取服务状态
export function useStatus() {
  return useQuery({ queryKey: statusKey, queryFn: () => api<ServiceStatus>('/api/status') })
}

// useSession 读取当前管理会话
export function useSession() {
  return useQuery({ queryKey: sessionKey, queryFn: () => api<Session>('/api/auth/session') })
}
