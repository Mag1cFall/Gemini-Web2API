import { ChevronLeft, ChevronRight, Columns3, Download, Inbox } from 'lucide-react'
import { useEffect, useId, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { SearchInput } from '@/components/ui/search-input'
import { Select } from '@/components/ui/select'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Table, Td, Th } from '@/components/ui/table'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import { useUsageFormat } from '@/features/usage/format'
import { usageMessages } from '@/features/usage/messages'
import { latencyTone } from '@/features/usage/model'
import { usePreference } from '@/features/usage/preference'
import { recordsParams, type RecordsScope, useUsageRecords } from '@/features/usage/queries'
import { RecordDrawer } from '@/features/usage/RecordDrawer'
import type { UsageRecord } from '@/features/usage/types'
import { fill, plural, useI18n, useMessages } from '@/lib/i18n'

// Column 是请求记录表可开关的列
type Column = 'model' | 'account' | 'protocol' | 'tokens' | 'latency' | 'queue' | 'attempts'

// allColumns 是可开关列的显示顺序
const allColumns: Column[] = [
  'model',
  'account',
  'protocol',
  'tokens',
  'latency',
  'queue',
  'attempts',
]

// pageSizes 是每页条数的可选值
const pageSizes = [20, 50, 100]

// isPageSize 判断本地保存的每页条数是否有效
function isPageSize(value: unknown): value is number {
  return pageSizes.some((size) => size === value)
}

// isColumns 判断本地保存的显示列是否有效
function isColumns(value: unknown): value is Column[] {
  return Array.isArray(value) && value.every((item) => allColumns.some((column) => column === item))
}

// latencyWidth 把耗时映射为 4 到 100 的条形宽度，3 分钟为满格
function latencyWidth(milliseconds: number) {
  return Math.min(100, Math.max(4, (milliseconds / 180_000) * 100))
}

// RecordsTable 是按完成时间倒序分页的请求记录，scope 为空时等待用量报告，pagingKey 变化时回到第一页
export function RecordsTable({
  scope,
  pagingKey,
  total,
  status,
  q,
  statusOptions,
  onStatus,
  onSearch,
}: {
  scope: RecordsScope | null
  pagingKey: string
  total: number
  status: string
  q: string
  statusOptions: string[]
  onStatus: (status: string) => void
  onSearch: (q: string) => void
}) {
  const m = useMessages(usageMessages)
  const { locale } = useI18n()
  const f = useUsageFormat()
  const id = useId()
  const [pageSize, setPageSize] = usePreference('pageSize', 20, isPageSize)
  const [visible, setVisible] = usePreference<Column[]>(
    'columns',
    ['model', 'account', 'protocol', 'tokens', 'latency', 'attempts'],
    isColumns,
  )
  const key = pagingKey + '\n' + String(pageSize)
  const [paging, setPaging] = useState<{
    key: string
    cursors: (string | undefined)[]
    page: number
  }>({ key, cursors: [undefined], page: 0 })
  const current = paging.key === key ? paging : { key, cursors: [undefined], page: 0 }
  const records = useUsageRecords(scope, current.cursors[current.page], pageSize)
  const [selected, setSelected] = useState<UsageRecord | null>(null)
  const [searchText, setSearchText] = useState(q)
  const [syncedQ, setSyncedQ] = useState(q)
  if (q !== syncedQ) {
    setSyncedQ(q)
    setSearchText(q)
  }
  useEffect(() => {
    const value = searchText.trim()
    if (value === q) return
    const timer = setTimeout(() => {
      onSearch(value)
    }, 300)
    return () => {
      clearTimeout(timer)
    }
  }, [searchText, q, onSearch])

  const columns = allColumns.filter((column) => visible.includes(column))
  const columnLabels: Record<Column, string> = {
    model: m.model,
    account: m.account,
    protocol: m.protocol,
    tokens: m.tokens,
    latency: m.duration,
    queue: m.queue,
    attempts: m.attempts,
  }
  const nextCursor = records.data?.next_cursor
  const codes =
    status && !statusOptions.includes(status) ? [status, ...statusOptions] : statusOptions

  // move 前后翻页，向后翻页时记住下一页的位置
  const move = (step: 1 | -1) => {
    if (step === 1 && nextCursor === undefined) return
    setPaging({
      key,
      cursors:
        step === 1 ? [...current.cursors.slice(0, current.page + 1), nextCursor] : current.cursors,
      page: Math.max(0, current.page + step),
    })
  }

  // cell 渲染一条记录在可开关列上的单元格
  const cell = (record: UsageRecord, column: Column) => {
    switch (column) {
      case 'model':
        return (
          <Td key={column} className="max-w-cell">
            <span title={record.model} className="block truncate font-mono">
              {record.model || '—'}
            </span>
          </Td>
        )
      case 'account':
        return (
          <Td key={column} className="max-w-cell">
            <span title={record.account} className="block truncate">
              {f.label('account', record.account)}
            </span>
          </Td>
        )
      case 'protocol':
        return (
          <Td key={column} className="whitespace-nowrap">
            {f.label('protocol', record.protocol)}
          </Td>
        )
      case 'tokens':
        return (
          <Td key={column} className="text-right whitespace-nowrap">
            <div>{f.compact(record.total_tokens)}</div>
            <div className="text-xs text-foreground-muted">
              {f.compact(record.input_tokens)} / {f.compact(record.reasoning_tokens)} /{' '}
              {f.compact(record.reply_tokens)}
            </div>
          </Td>
        )
      case 'latency':
        return (
          <Td key={column} className="w-[152px] whitespace-nowrap">
            <div className="flex items-center justify-between gap-2">
              <span>{f.duration(record.duration_ms)}</span>
              <span className="text-xs text-foreground-muted">
                {f.duration(record.first_event_ms)}
              </span>
            </div>
            <div className="mt-1 h-1 rounded-full bg-hover">
              <div
                className={'h-1 rounded-full ' + latencyTone(record.duration_ms, 'duration')}
                style={{ width: `${String(latencyWidth(record.duration_ms))}%` }}
              />
            </div>
          </Td>
        )
      case 'queue':
        return (
          <Td key={column} className="text-right whitespace-nowrap @max-[1000px]:hidden">
            {f.duration(record.queue_ms)}
          </Td>
        )
      case 'attempts':
        return (
          <Td
            key={column}
            className={
              'text-right @max-[1000px]:hidden ' +
              (record.attempts.length > 0 ? 'text-warning-text' : 'text-foreground-subtle')
            }
          >
            {record.attempts.length}
          </Td>
        )
    }
  }

  return (
    <section className="@container flex min-w-0 flex-col overflow-hidden rounded-md border border-border bg-surface">
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-5 py-3">
        <div className="mr-auto min-w-0">
          <h2 id={id + '-title'} className="text-md font-semibold">
            {m.records}
          </h2>
          {!status && !q && (
            <p className="text-xs text-foreground-muted">
              {fill(plural(locale, total, m.recordsCountOne, m.recordsCount), {
                count: f.integer(total),
              })}
            </p>
          )}
        </div>
        <SearchInput
          aria-label={m.recordsSearch}
          placeholder={m.recordsSearch}
          value={searchText}
          onChange={(event) => {
            setSearchText(event.target.value)
          }}
          className="w-[256px]"
        />
        <Select
          label={m.status}
          value={status || 'all'}
          onValueChange={(value) => {
            onStatus(value === 'all' ? '' : value)
          }}
          options={[
            { value: 'all', label: m.recordsAllStatus },
            ...codes.map((code) => ({ value: code, label: code })),
          ]}
          size="sm"
          className="w-[144px]"
        />
        <Popover>
          <PopoverTrigger asChild>
            <Button size="sm">
              <Columns3 size={16} aria-hidden />
              {m.columns}
            </Button>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-menu p-2">
            <fieldset>
              <legend className="sr-only">{m.columns}</legend>
              {allColumns.map((column) => (
                <label
                  key={column}
                  htmlFor={`${id}-column-${column}`}
                  className="flex h-control-sm cursor-pointer items-center gap-2 rounded-sm px-2 text-sm hover:bg-hover"
                >
                  <input
                    id={`${id}-column-${column}`}
                    type="checkbox"
                    checked={visible.includes(column)}
                    onChange={(event) => {
                      setVisible(
                        event.target.checked
                          ? allColumns.filter((item) => item === column || visible.includes(item))
                          : visible.filter((item) => item !== column),
                      )
                    }}
                    className="size-4 accent-primary"
                  />
                  {columnLabels[column]}
                </label>
              ))}
            </fieldset>
          </PopoverContent>
        </Popover>
        {scope ? (
          <Button size="sm" asChild>
            <a href={'/api/usage/records.csv?' + recordsParams(scope).toString()} download>
              <Download size={16} aria-hidden />
              {m.export}
            </a>
          </Button>
        ) : (
          <Button size="sm" disabled>
            <Download size={16} aria-hidden />
            {m.export}
          </Button>
        )}
      </div>

      {records.isError ? (
        <ErrorState
          title={m.records}
          error={records.error}
          retryLabel={m.retry}
          onRetry={() => void records.refetch()}
        />
      ) : !records.data ? (
        <Loading label={m.loading} />
      ) : records.data.items.length === 0 ? (
        <EmptyState icon={<Inbox size={20} aria-hidden />} title={m.recordsEmpty} />
      ) : (
        <div aria-busy={records.isFetching}>
          <Table label={m.records}>
            <thead>
              <tr>
                <Th>{m.time}</Th>
                <Th>{m.result}</Th>
                {columns.map((column) => (
                  <Th
                    key={column}
                    className={
                      column === 'tokens'
                        ? 'text-right'
                        : column === 'queue' || column === 'attempts'
                          ? 'text-right @max-[1000px]:hidden'
                          : ''
                    }
                  >
                    {columnLabels[column]}
                  </Th>
                ))}
              </tr>
            </thead>
            <tbody>
              {records.data.items.map((record) => (
                <tr
                  key={record.id}
                  className={record.state === 'failed' ? 'bg-error-soft/40' : 'hover:bg-hover'}
                >
                  <Td className="whitespace-nowrap">
                    <button
                      type="button"
                      aria-label={fill(m.openDetail, { id: record.id })}
                      onClick={() => {
                        setSelected(record)
                      }}
                      className="text-left text-primary tabular-nums hover:text-primary-hover hover:underline"
                    >
                      {f.dateTime(record.time, true)}
                    </button>
                  </Td>
                  <Td className="whitespace-nowrap">
                    <span className="flex items-center gap-2">
                      <RequestStateBadge state={record.state} />
                      <span className="text-foreground-muted tabular-nums">{record.status}</span>
                    </span>
                  </Td>
                  {columns.map((column) => cell(record, column))}
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 border-t border-border px-5 py-3 text-sm text-foreground-muted">
        <Select
          label={fill(m.pageSize, { count: pageSize })}
          value={String(pageSize)}
          onValueChange={(value) => {
            setPageSize(Number(value))
          }}
          options={pageSizes.map((size) => ({
            value: String(size),
            label: fill(m.pageSize, { count: size }),
          }))}
          size="sm"
          className="w-[128px]"
        />
        <span className="ml-auto tabular-nums">{fill(m.page, { page: current.page + 1 })}</span>
        <Button
          size="sm"
          disabled={current.page === 0}
          onClick={() => {
            move(-1)
          }}
        >
          <ChevronLeft size={16} aria-hidden />
          {m.previousPage}
        </Button>
        <Button
          size="sm"
          disabled={nextCursor === undefined || records.isPlaceholderData}
          onClick={() => {
            move(1)
          }}
        >
          {m.nextPage}
          <ChevronRight size={16} aria-hidden />
        </Button>
      </div>
      <RecordDrawer
        record={selected}
        onClose={() => {
          setSelected(null)
        }}
      />
    </section>
  )
}
