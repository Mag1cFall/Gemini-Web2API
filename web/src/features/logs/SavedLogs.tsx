import { useInfiniteQuery } from '@tanstack/react-query'
import { ScrollText } from 'lucide-react'
import type { ReactNode } from 'react'
import { useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, PageHeader } from '@/components/ui/page'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { LogLine } from '@/features/logs/LogLine'
import { logsMessages } from '@/features/logs/messages'
import type { LogPage } from '@/features/logs/types'
import { api } from '@/lib/api'
import { useMessages } from '@/lib/i18n'
import { patchSearchParams } from '@/lib/searchParams'

// savedRanges 是已保存日志可选的时间范围，截止到当前时间
const savedRanges = {
  '24h': 86_400_000,
  '7d': 604_800_000,
  '30d': 2_592_000_000,
  '90d': 7_776_000_000,
} as const

// SavedRange 是已保存日志的时间范围
type SavedRange = keyof typeof savedRanges

// SavedLogs 检索服务保存的警告与错误日志，按时间范围、级别、请求 ID 与关键词筛选，条件保存在 URL；toolbar 是视图切换
export function SavedLogs({ toolbar }: { toolbar: ReactNode }) {
  const m = useMessages(logsMessages)
  const app = useMessages(appMessages)
  const [params, setParams] = useSearchParams()
  const range =
    (Object.keys(savedRanges) as SavedRange[]).find((key) => key === params.get('range')) ?? '24h'
  const errorsOnly = params.get('level') === 'ERROR'
  const q = params.get('q') ?? ''
  const requestId = params.get('request_id') ?? ''
  const logs = useInfiniteQuery({
    queryKey: ['saved-logs', range, errorsOnly, q.trim(), requestId.trim()],
    queryFn: ({ pageParam, signal }) => {
      const now = Date.now()
      const query = new URLSearchParams({
        from: new Date(now - savedRanges[range]).toISOString(),
        to: new Date(now).toISOString(),
      })
      if (errorsOnly) query.set('level', 'ERROR')
      if (q.trim()) query.set('q', q.trim())
      if (requestId.trim()) query.set('request_id', requestId.trim())
      if (pageParam) query.set('before', String(pageParam))
      return api<LogPage>('/api/logs?' + query.toString(), { signal })
    },
    initialPageParam: 0,
    getNextPageParam: (last) => last.next_before,
  })

  // update 把筛选条件写入 URL，取默认值的条件从 URL 删除
  const update = (next: Record<string, string>) => {
    patchSearchParams(setParams, next)
  }

  const entries = logs.data?.pages.flatMap((page) => page.items) ?? []
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      <Card>
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
          {toolbar}
          <Segmented
            label={m.range}
            value={range}
            onValueChange={(value) => {
              update({ range: value === '24h' ? '' : value })
            }}
            options={[
              { value: '24h', label: m.range24h },
              { value: '7d', label: m.range7d },
              { value: '30d', label: m.range30d },
              { value: '90d', label: m.range90d },
            ]}
          />
          <Segmented
            label={m.level}
            value={errorsOnly ? 'ERROR' : 'WARN'}
            onValueChange={(value) => {
              update({ level: value === 'ERROR' ? 'ERROR' : '' })
            }}
            options={[
              { value: 'WARN', label: m.savedWarn },
              { value: 'ERROR', label: m.savedError },
            ]}
          />
          <div className="flex w-full flex-wrap items-center gap-3">
            <Input
              aria-label={m.requestIdFilter}
              placeholder={m.requestIdFilter}
              value={requestId}
              onChange={(event) => {
                update({ request_id: event.target.value })
              }}
              className="w-[200px] font-mono"
            />
            <SearchInput
              aria-label={m.searchSaved}
              placeholder={m.searchSaved}
              value={q}
              onChange={(event) => {
                update({ q: event.target.value })
              }}
              className="max-w-[520px] min-w-[240px] flex-1"
            />
          </div>
        </div>
        {logs.isPending ? (
          <Loading label={m.loading} />
        ) : logs.isError ? (
          <ErrorState
            title={m.loadFailed}
            error={logs.error}
            retryLabel={app.retry}
            onRetry={() => void logs.refetch()}
          />
        ) : entries.length === 0 ? (
          <EmptyState
            icon={<ScrollText size={20} aria-hidden />}
            title={m.savedEmptyTitle}
            description={m.savedEmptyDescription}
          />
        ) : (
          <div className="flex flex-col">
            {entries.map((entry) => (
              <LogLine
                key={entry.seq}
                row={{ key: String(entry.seq), entry, events: [entry] }}
                account=""
                dated
              />
            ))}
            {logs.hasNextPage && (
              <div className="px-5 py-3">
                <Button
                  loading={logs.isFetchingNextPage}
                  onClick={() => {
                    void logs.fetchNextPage()
                  }}
                >
                  {m.older}
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>
    </>
  )
}
