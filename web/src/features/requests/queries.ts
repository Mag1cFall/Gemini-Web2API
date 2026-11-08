import { skipToken, useMutation, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { recentKey, runningKey } from '@/app/events'
import type { RequestBody, RequestInfo } from '@/features/requests/types'
import type { UsageRecordPage } from '@/features/usage/types'
import { api } from '@/lib/api'

// dayMs 是一天的毫秒数
const dayMs = 86_400_000

// ledgerDays 是按 ID 查找请求记录的天数，取账本接口允许的最长范围
const ledgerDays = 93

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

// useLedgerRecord 按请求 ID 读取请求账本中的记录，查询上限比本机时间多留一天，浏览器时钟慢于服务时也能查到刚结束的请求；id 为空时不查询，账本中没有时为 null
export function useLedgerRecord(id: string | null) {
  return useQuery({
    queryKey: ['ledger-record', id],
    queryFn:
      id === null
        ? skipToken
        : async ({ signal }) => {
            const to = Date.now() + dayMs
            const query = new URLSearchParams({
              from: new Date(to - ledgerDays * dayMs).toISOString(),
              to: new Date(to).toISOString(),
              q: id,
            })
            const page = await api<UsageRecordPage>('/api/usage/records?' + query.toString(), {
              signal,
            })
            return page.items.find((item) => item.id === id) ?? null
          },
  })
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
