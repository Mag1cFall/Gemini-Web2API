import { keepPreviousData, skipToken, useQuery } from '@tanstack/react-query'

import {
  filterParams,
  rangeError,
  resolveRange,
  retention,
  type UsageFilters,
  type UsageView,
} from '@/features/usage/model'
import type { UsageRecordPage, UsageReport } from '@/features/usage/types'
import { api } from '@/lib/api'

// timeZone 是本机 IANA 时区，用量分桶按它对齐
const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone

// recentUsage 读取截止到当前时间 span 毫秒内的用量报告
function recentUsage(span: number, signal: AbortSignal) {
  const now = Date.now()
  const query = new URLSearchParams({
    from: new Date(now - span).toISOString(),
    to: new Date(now).toISOString(),
    tz: timeZone,
  })
  return api<UsageReport>('/api/usage?' + query.toString(), { signal })
}

// useUsage 读取截止到当前时间 rangeMs 毫秒内的用量报告，请求结束时由事件流刷新
export function useUsage(rangeMs: number) {
  return useQuery({
    queryKey: ['usage', rangeMs],
    queryFn: ({ signal }) => recentUsage(rangeMs, signal),
    placeholderData: keepPreviousData,
  })
}

// useHadRequests 读取近 90 天是否有过请求，打开页面时读取一次，不随请求结束重读
export function useHadRequests() {
  return useQuery({
    queryKey: ['had-requests'],
    queryFn: ({ signal }) => recentUsage(retention, signal),
    select: (report) => report.totals.requests > 0,
    staleTime: Number.POSITIVE_INFINITY,
  })
}

// useUsageReport 读取用量页视图的报告，跟随当前时间的范围在每次读取时重新解析，interval 为自动刷新间隔
export function useUsageReport(view: UsageView, interval: number | false) {
  const { range, from, to, stack, filters } = view
  const valid = range !== 'custom' || rangeError(new Date(from), new Date(to)) === ''
  return useQuery({
    queryKey: ['usage-report', { range, from, to, stack, filters }],
    queryFn: valid
      ? ({ signal }) => {
          const resolved = resolveRange(view, new Date())
          if (resolved === null) throw new Error('invalid range')
          const query = filterParams(
            new URLSearchParams({
              from: resolved.from.toISOString(),
              to: resolved.to.toISOString(),
              tz: timeZone,
              stack,
            }),
            filters,
          )
          return api<UsageReport>('/api/usage?' + query.toString(), { signal })
        }
      : skipToken,
    placeholderData: keepPreviousData,
    refetchInterval: interval,
  })
}

// RecordsScope 是请求记录的范围、维度筛选、状态码与关键字
export interface RecordsScope {
  from: string
  to: string
  filters: UsageFilters
  status: string
  q: string
}

// recordsParams 返回请求记录与 CSV 导出共用的查询参数
export function recordsParams(scope: RecordsScope) {
  const query = filterParams(new URLSearchParams({ from: scope.from, to: scope.to }), scope.filters)
  if (scope.status) query.set('status', scope.status)
  if (scope.q) query.set('q', scope.q)
  return query
}

// useUsageRecords 读取一页按完成时间倒序的请求记录，cursor 为空时读取第一页
export function useUsageRecords(
  scope: RecordsScope | null,
  cursor: string | undefined,
  limit: number,
) {
  return useQuery({
    queryKey: ['usage-records', scope, cursor, limit],
    queryFn:
      scope === null
        ? skipToken
        : ({ signal }) => {
            const query = recordsParams(scope)
            query.set('limit', String(limit))
            if (cursor !== undefined) query.set('cursor', cursor)
            return api<UsageRecordPage>('/api/usage/records?' + query.toString(), { signal })
          },
    placeholderData: keepPreviousData,
  })
}
