import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { ServiceStatus } from '@/features/service/types'
import { api } from '@/lib/api'

// UpdateStatus 对应 GET /api/update
export interface UpdateStatus {
  current: string
  latest?: string
  available: boolean
  // mode 为 binary（可原地更新）、container（随镜像更新）或 development（源码构建）
  mode: 'binary' | 'container' | 'development'
  notes?: string
  url?: string
  published_at?: string
  checked_at?: string
  error?: string
  updating: boolean
}

// updateKey 是更新状态的查询键
export const updateKey = ['update'] as const

// useUpdateStatus 读取当前版本与最新发行版本
export function useUpdateStatus() {
  return useQuery({ queryKey: updateKey, queryFn: () => api<UpdateStatus>('/api/update') })
}

// useCheckUpdate 重新读取 GitHub 最新发行版本
export function useCheckUpdate() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<UpdateStatus>('/api/update?refresh=1'),
    onSuccess: (status) => {
      client.setQueryData(updateKey, status)
    },
  })
}

// useApplyUpdate 下载、校验并安装最新发行版本，成功后服务重启
export function useApplyUpdate() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<UpdateStatus>('/api/update', { method: 'POST' }),
    onSuccess: (status) => {
      client.setQueryData(updateKey, status)
    },
  })
}

// useRestartedVersion 在 enabled 时轮询服务状态，返回重启后服务报告的版本
export function useRestartedVersion(enabled: boolean) {
  return useQuery({
    queryKey: ['status', 'restart'],
    queryFn: () => api<ServiceStatus>('/api/status'),
    enabled,
    refetchInterval: 1500,
    retry: false,
    select: (status) => status.version,
  })
}
