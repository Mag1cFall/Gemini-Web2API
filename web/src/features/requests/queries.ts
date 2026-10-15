import { useMutation, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { recentKey, runningKey } from '@/app/events'
import type { RequestBody, RequestInfo } from '@/features/requests/types'
import { api } from '@/lib/api'

// useRunning 返回事件流推送的进行中请求，按开始时间排序
export function useRunning() {
  const data = useQuery({
    queryKey: runningKey,
    queryFn: () => [] as RequestInfo[],
    enabled: false,
  }).data
  return useMemo(
    () => [...(data ?? [])].sort((a, b) => a.started_at.localeCompare(b.started_at)),
    [data],
  )
}

// useFinished 返回事件流推送的最近结束的 limit 个请求，最新结束的在前
export function useFinished(limit: number) {
  const data = useQuery({
    queryKey: recentKey,
    queryFn: () => [] as RequestInfo[],
    enabled: false,
  }).data
  return useMemo(() => (data ?? []).slice(0, limit), [data, limit])
}

// useCancelRequest 取消一个进行中的请求，请求已结束时返回 404
export function useCancelRequest() {
  return useMutation({
    mutationFn: (id: string) =>
      api<undefined>('/api/requests/' + encodeURIComponent(id) + '/cancel', { method: 'POST' }),
  })
}

// useRequestBody 读取一条已结束请求保存的请求与响应正文，没有保存时返回 404
export function useRequestBody(id: string, enabled: boolean) {
  return useQuery({
    queryKey: ['request-body', id],
    queryFn: ({ signal }) =>
      api<RequestBody>('/api/requests/' + encodeURIComponent(id) + '/body', { signal }),
    enabled,
    staleTime: Number.POSITIVE_INFINITY,
  })
}
