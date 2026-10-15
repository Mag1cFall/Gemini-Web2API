import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trash } from 'lucide-react'
import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'

import { logsKey } from '@/app/events'
import { Button } from '@/components/ui/button'
import { Card, CardEmpty, CardHeader, CountBadge, PageHeader } from '@/components/ui/page'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  groupLogs,
  type LogFilter,
  type LogRow,
  matchRow,
  minimumLevels,
  parseLogFilter,
} from '@/features/logs/filter'
import { LogMessage } from '@/features/logs/LogField'
import { logsMessages } from '@/features/logs/messages'
import { RequestLogCard } from '@/features/logs/RequestLogCard'
import type { LogEntry, LogLevel } from '@/features/logs/types'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// pageRows 是首次显示与每次加载更早日志的行数
const pageRows = 200

// levelClass 是各级别日志行的左侧色条与底色，警告与错误才有色条
const levelClass: Record<LogLevel, string> = {
  DEBUG: 'border-l-transparent text-foreground-muted',
  INFO: 'border-l-transparent',
  WARN: 'border-l-warning bg-warning-soft/60',
  ERROR: 'border-l-error bg-error-soft/60',
}

// levelText 是级别列的文字颜色
const levelText: Record<LogLevel, string> = {
  DEBUG: 'text-foreground-subtle',
  INFO: 'text-primary',
  WARN: 'text-warning-text',
  ERROR: 'text-error-text',
}

// timeFormat 是日志行的 24 小时制时分秒
const timeFormat = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

// LogsPage 显示实时日志：请求的生命周期合成卡片，按级别、来源与搜索词筛选，可跟随最新日志滚动
export function LogsPage() {
  const m = useMessages(logsMessages)
  const { locale } = useI18n()
  const client = useQueryClient()
  const [params, setParams] = useSearchParams()
  const filter = parseLogFilter(params)
  const logData = useQuery({
    queryKey: logsKey,
    queryFn: () => [] as LogEntry[],
    enabled: false,
  }).data
  const logs = useMemo(() => logData ?? [], [logData])
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
  const [limit, setLimit] = useState(pageRows)
  const [pinned, setPinned] = useState<string | undefined>()
  const output = useRef<HTMLDivElement>(null)
  const first =
    pinned === undefined
      ? Math.max(0, filtered.length - limit)
      : Math.max(
          0,
          filtered.findIndex((row) => row.key === pinned),
        )
  const visible = filtered.slice(first)

  useLayoutEffect(() => {
    const node = output.current
    if (autoScroll && node) node.scrollTop = node.scrollHeight
  }, [autoScroll, logs, filtered.length])

  const clear = useMutation({
    mutationFn: () => api('/api/logs', { method: 'DELETE' }),
    onSuccess: () => client.setQueryData(logsKey, []),
  })

  // windowStart 返回跟随末尾时窗口首行的标识，自动滚动开启时为空
  const windowStart = (list: LogRow[], size: number, follow: boolean) =>
    follow ? undefined : list[Math.max(0, list.length - size)]?.key

  // update 同步写入筛选条件，窗口回到最近的一页
  const update = (next: Partial<LogFilter>) => {
    const search = new URLSearchParams(params)
    for (const [key, value] of Object.entries(next)) {
      if (value && value !== 'all') search.set(key, value)
      else search.delete(key)
    }
    setParams(search, { replace: true, flushSync: true })
    const nextFilter = { ...filter, ...next }
    setLimit(pageRows)
    setPinned(
      windowStart(
        rows.filter((row) => matchRow(row, nextFilter)),
        pageRows,
        autoScroll,
      ),
    )
  }

  // toggleAutoScroll 开关跟随末尾，关闭时固定当前窗口首行
  const toggleAutoScroll = (on: boolean) => {
    setAutoScroll(on)
    setLimit(Math.max(pageRows, visible.length))
    setPinned(on ? undefined : visible[0]?.key)
  }

  // loadOlder 在窗口上方多显示一页更早的日志
  const loadOlder = () => {
    const size = visible.length + pageRows
    setLimit(size)
    setPinned(windowStart(filtered, size, autoScroll))
  }

  return (
    <>
      <PageHeader
        title={m.title}
        description={m.description}
        actions={
          <Button
            onClick={() => {
              clear.mutate()
            }}
            loading={clear.isPending}
          >
            <Trash size={16} aria-hidden />
            {m.clear}
          </Button>
        }
      />
      <Card className="flex grow basis-0 flex-col">
        <CardHeader
          title={m.live}
          actions={<CountBadge>{formatNumber(filtered.length, locale)}</CountBadge>}
        />
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
          <Segmented
            label={m.level}
            value={filter.level}
            onValueChange={(level) => {
              update({ level })
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
            className="w-[200px]"
          />
          <SearchInput
            aria-label={m.search}
            placeholder={m.search}
            value={filter.q}
            onChange={(event) => {
              update({ q: event.target.value })
            }}
            className="w-[280px]"
          />
          <label className="ml-auto flex items-center gap-2 text-sm whitespace-nowrap">
            <Switch checked={autoScroll} onCheckedChange={toggleAutoScroll} />
            {m.autoScroll}
          </label>
        </div>
        <div
          ref={output}
          role="log"
          aria-label={m.title}
          tabIndex={0}
          className="min-h-[320px] grow basis-0 [scrollbar-gutter:stable] overflow-y-auto"
        >
          {filtered.length === 0 ? (
            <CardEmpty>{logs.length === 0 ? m.waiting : m.empty}</CardEmpty>
          ) : (
            <>
              {first > 0 && (
                <div className="flex justify-center py-2">
                  <Button size="sm" onClick={loadOlder}>
                    {fill(m.loadOlder, { count: formatNumber(first, locale) })}
                  </Button>
                </div>
              )}
              {visible.map((row) => {
                const level = row.entry.level
                const time = row.events[0]?.time ?? row.entry.time
                return (
                  <div
                    key={row.key}
                    className={
                      'grid grid-cols-[auto_auto_minmax(0,1fr)] items-baseline gap-x-4 border-b border-l-2 border-b-border py-2 pr-5 pl-[18px] text-base select-text ' +
                      levelClass[level]
                    }
                  >
                    <time
                      dateTime={time}
                      className="font-mono text-sm text-foreground-subtle tabular-nums"
                    >
                      {timeFormat.format(new Date(time))}
                    </time>
                    <span
                      className={'w-[44px] font-mono text-sm font-semibold ' + levelText[level]}
                    >
                      {level}
                    </span>
                    {row.entry.request ? (
                      <RequestLogCard row={row} />
                    ) : (
                      <LogMessage source={row.entry.source} message={row.entry.message} />
                    )}
                  </div>
                )
              })}
            </>
          )}
        </div>
      </Card>
    </>
  )
}
