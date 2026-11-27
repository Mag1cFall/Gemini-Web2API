import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { Trash } from 'lucide-react'
import { type ReactNode, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'

import { logsKey } from '@/app/events'
import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Card, CardEmpty, CardHeader, CountBadge, PageHeader } from '@/components/ui/page'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'
import { DisabledHint } from '@/components/ui/tooltip'
import { groupLogs, matchRow, minimumLevels, parseLogFilter } from '@/features/logs/filter'
import { LogLine } from '@/features/logs/LogLine'
import { logsMessages } from '@/features/logs/messages'
import { SavedLogs } from '@/features/logs/SavedLogs'
import type { LogEntry } from '@/features/logs/types'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { patchSearchParams } from '@/lib/searchParams'

// LiveLogs 显示实时日志：请求的生命周期合成卡片，按级别、来源与搜索词筛选，只渲染可见的行，可跟随最新日志滚动；清空后在提示关闭前可撤销；toolbar 是视图切换
function LiveLogs({ toolbar }: { toolbar: ReactNode }) {
  const m = useMessages(logsMessages)
  const app = useMessages(appMessages)
  const { language } = useI18n()
  const client = useQueryClient()
  const [params, setParams] = useSearchParams()
  const filter = parseLogFilter(params)
  const logData = useQuery({
    queryKey: logsKey,
    queryFn: () => [] as LogEntry[],
    enabled: false,
  }).data
  const [hiddenThrough, setHiddenThrough] = useState(0)
  const logs = useMemo(
    () => (logData ?? []).filter((entry) => entry.seq > hiddenThrough),
    [logData, hiddenThrough],
  )
  const rows = useMemo(() => groupLogs(logs), [logs])
  const sources = useMemo(
    () => [...new Set(logs.map((entry) => entry.source))].sort((a, b) => a.localeCompare(b)),
    [logs],
  )
  const filtered = useMemo(
    () =>
      rows.filter((row) =>
        matchRow(row, { level: filter.level, source: filter.source, q: filter.q }),
      ),
    [rows, filter.level, filter.source, filter.q],
  )
  const [autoScroll, setAutoScroll] = useState(true)
  const output = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => output.current,
    estimateSize: () => 41,
    getItemKey: (index) => filtered[index]?.key ?? index,
    overscan: 12,
  })
  const totalSize = virtualizer.getTotalSize()

  useEffect(() => {
    const node = output.current
    if (autoScroll && node) node.scrollTop = node.scrollHeight
  }, [autoScroll, filtered.length, totalSize])

  // clear 立即隐藏当前全部日志并提示可撤销，提示关闭前没有撤销时清空服务端中点击时最新一条及以前的日志
  const clear = () => {
    const through = logData?.at(-1)?.seq ?? 0
    setHiddenThrough(through)
    toast({
      tone: 'success',
      title: m.cleared,
      action: {
        label: m.undo,
        onClick: () => {
          setHiddenThrough(0)
        },
      },
      onClose: () => {
        void api(`/api/logs?through=${String(through)}`, { method: 'DELETE' }).then(
          () => {
            client.setQueryData<LogEntry[]>(logsKey, (current = []) =>
              current.filter((entry) => entry.seq > through),
            )
            setHiddenThrough(0)
          },
          (error: unknown) => {
            setHiddenThrough(0)
            toast({
              tone: 'error',
              title: fill(app.actionFailed, { action: m.clear }),
              detail: error instanceof Error ? error.message : String(error),
            })
          },
        )
      },
    })
  }

  // update 同步写入筛选条件
  const update = (next: Record<string, string>) => {
    patchSearchParams(setParams, next)
  }

  // followScroll 在日志区离开末尾时停止跟随，回到末尾时恢复跟随
  const followScroll = () => {
    const node = output.current
    if (!node) return
    const distance = node.scrollHeight - node.scrollTop - node.clientHeight
    if (autoScroll && distance > 40) setAutoScroll(false)
    else if (!autoScroll && distance <= 10) setAutoScroll(true)
  }

  return (
    <>
      <PageHeader
        title={m.title}
        description={m.description}
        actions={
          <DisabledHint reason={logs.length === 0 ? m.nothingToClear : ''}>
            <Button onClick={clear} disabled={logs.length === 0}>
              <Trash size={16} aria-hidden />
              {m.clear}
            </Button>
          </DisabledHint>
        }
      />
      <Card className="flex grow basis-0 flex-col max-lg:grow-0 max-lg:basis-auto">
        <CardHeader
          title={m.live}
          actions={<CountBadge>{formatNumber(filtered.length, language)}</CountBadge>}
        />
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
          {toolbar}
          <Segmented
            label={m.level}
            value={filter.level}
            onValueChange={(level) => {
              update({ level: level === 'all' ? '' : level })
            }}
            options={minimumLevels.map((level) => ({
              value: level,
              label: level === 'all' ? m.levelAll : level,
            }))}
          />
          <Select
            label={m.source}
            value={filter.source || 'all'}
            onValueChange={(value) => {
              update({ source: value === 'all' ? '' : value })
            }}
            options={[
              { value: 'all', label: m.sourceAll },
              ...[...new Set([...(filter.source ? [filter.source] : []), ...sources])].map(
                (source) => ({ value: source, label: source }),
              ),
            ]}
            className="min-w-[200px]"
          />
          <div className="flex w-full flex-wrap items-center gap-3">
            <SearchInput
              aria-label={m.search}
              placeholder={m.search}
              value={filter.q}
              onChange={(event) => {
                update({ q: event.target.value })
              }}
              className="max-w-[520px] min-w-[200px] flex-1"
            />
            <label className="ml-auto flex items-center gap-2 text-sm whitespace-nowrap">
              <Switch checked={autoScroll} onCheckedChange={setAutoScroll} />
              {m.autoScroll}
            </label>
          </div>
        </div>
        <div
          ref={output}
          role="log"
          aria-label={m.title}
          tabIndex={0}
          onScroll={followScroll}
          className="min-h-[320px] grow basis-0 [scrollbar-gutter:stable] overflow-y-auto max-lg:h-[70dvh] max-lg:grow-0 max-lg:basis-auto"
        >
          {filtered.length === 0 ? (
            <CardEmpty>{logs.length === 0 ? m.waiting : m.empty}</CardEmpty>
          ) : (
            <div className="relative w-full" style={{ height: totalSize }}>
              {virtualizer.getVirtualItems().map((item) => {
                const row = filtered[item.index]
                return (
                  row && (
                    <div
                      key={item.key}
                      data-index={item.index}
                      ref={virtualizer.measureElement}
                      className="absolute top-0 left-0 w-full"
                      style={{ transform: `translateY(${String(item.start)}px)` }}
                    >
                      <LogLine row={row} account={filter.source} />
                    </div>
                  )
                )
              })}
            </div>
          )}
        </div>
      </Card>
    </>
  )
}

// LogsPage 是日志页：实时日志与已保存日志检索两个视图，视图保存在 URL
export function LogsPage() {
  const m = useMessages(logsMessages)
  const [params, setParams] = useSearchParams()
  const view = params.get('view') === 'saved' ? 'saved' : 'live'
  const toolbar = (
    <Segmented
      label={m.view}
      value={view}
      onValueChange={(value) => {
        setParams(value === 'saved' ? { view: 'saved' } : {}, { replace: true })
      }}
      options={[
        { value: 'live', label: m.live },
        { value: 'saved', label: m.saved },
      ]}
    />
  )
  return view === 'saved' ? <SavedLogs toolbar={toolbar} /> : <LiveLogs toolbar={toolbar} />
}
