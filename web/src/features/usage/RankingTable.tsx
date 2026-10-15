import { ChevronDown, ChevronRight, Funnel } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Meter } from '@/components/ui/meter'
import { Table, Td, Th } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Tooltip } from '@/components/ui/tooltip'
import { useUsageFormat } from '@/features/usage/format'
import { usageMessages } from '@/features/usage/messages'
import { rateTone, successRate } from '@/features/usage/model'
import { usePreference } from '@/features/usage/preference'
import type {
  UsageDimension,
  UsageGroupDimension,
  UsageReport,
  UsageStats,
} from '@/features/usage/types'
import { useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// tabs 是分项排行的维度
const tabs: UsageGroupDimension[] = ['account', 'model', 'protocol', 'state', 'status']

// isTab 判断本地保存的排行维度是否有效
function isTab(value: unknown): value is UsageGroupDimension {
  return tabs.some((tab) => tab === value)
}

// SortKey 是排行表可排序的列
type SortKey = 'key' | 'requests' | 'rate' | 'tokens' | 'p50' | 'p95' | 'first' | 'last'

// sortValue 返回一行在排序列上的比较值
function sortValue(label: string, stats: UsageStats, column: SortKey): number | string {
  switch (column) {
    case 'key':
      return label.toLowerCase()
    case 'requests':
      return stats.requests
    case 'rate':
      return successRate(stats) ?? -1
    case 'tokens':
      return stats.total_tokens
    case 'p50':
      return stats.duration.p50_ms
    case 'p95':
      return stats.duration.p95_ms
    case 'first':
      return stats.first_event.p95_ms
    case 'last':
      return stats.last_at ? new Date(stats.last_at).getTime() : 0
  }
}

// RankingTable 是按维度分项的用量排行，账号与模型行可展开组合明细，行尾按钮把取值加入筛选
export function RankingTable({
  report,
  onFilter,
  onStatus,
}: {
  report: UsageReport
  onFilter: (dimension: UsageDimension, value: string) => void
  onStatus: (status: string) => void
}) {
  const m = useMessages(usageMessages)
  const f = useUsageFormat()
  const now = useNow(30_000)
  const [tab, setTab] = usePreference<UsageGroupDimension>('rankingTab', 'account', isTab)
  const [sort, setSort] = useState<{ key: SortKey; descending: boolean }>({
    key: 'requests',
    descending: true,
  })
  const [expanded, setExpanded] = useState<string[]>([])
  const tabLabels: Record<UsageGroupDimension, string> = {
    model: m.model,
    account: m.account,
    protocol: m.protocol,
    state: m.state,
    status: m.status,
  }
  const columns: { key: SortKey; label: string }[] = [
    { key: 'key', label: tabLabels[tab] },
    { key: 'requests', label: m.requests },
    { key: 'rate', label: m.successRate },
    { key: 'tokens', label: m.tokens },
    { key: 'p50', label: `${m.duration} P50` },
    { key: 'p95', label: `${m.duration} P95` },
    { key: 'first', label: `${m.firstEvent} P95` },
    { key: 'last', label: m.lastRequest },
  ]
  const total = Math.max(report.totals.requests, 1)
  const rows = [...report.groups[tab]].sort((a, b) => {
    const left = sortValue(f.label(tab, a.key), a, sort.key)
    const right = sortValue(f.label(tab, b.key), b, sort.key)
    const order = left < right ? -1 : left > right ? 1 : 0
    return sort.descending ? -order : order
  })

  // children 返回账号或模型行展开后的组合明细
  const children = (key: string) => {
    if (tab === 'account')
      return report.pairs
        .filter((pair) => pair.account === key)
        .map((pair) => ({ key: pair.model, label: f.label('model', pair.model), stats: pair }))
    if (tab === 'model')
      return report.pairs
        .filter((pair) => pair.model === key)
        .map((pair) => ({
          key: pair.account,
          label: f.label('account', pair.account),
          stats: pair,
        }))
    return []
  }

  // statCells 是一行的数值单元格
  const statCells = (stats: UsageStats, share: boolean) => (
    <>
      <Td className="text-right whitespace-nowrap">
        {f.integer(stats.requests)}
        {share && (
          <span className="ml-1 text-foreground-muted">
            {f.percent(Math.round((stats.requests / total) * 100) / 100)}
          </span>
        )}
      </Td>
      <Td className={'text-right ' + rateTone(successRate(stats))}>
        {f.percent(successRate(stats))}
      </Td>
      <Td className="text-right">{f.compact(stats.total_tokens)}</Td>
      <Td className="text-right whitespace-nowrap">{f.duration(stats.duration.p50_ms)}</Td>
      <Td className="text-right whitespace-nowrap">{f.duration(stats.duration.p95_ms)}</Td>
      <Td className="text-right whitespace-nowrap">{f.duration(stats.first_event.p95_ms)}</Td>
      <Td className="text-right whitespace-nowrap text-foreground-muted">
        {stats.last_at ? f.ago(stats.last_at, now) : '—'}
      </Td>
    </>
  )

  return (
    <section className="flex min-w-0 flex-col overflow-hidden rounded-md border border-border bg-surface">
      <Tabs
        value={tab}
        onValueChange={(value) => {
          if (isTab(value)) setTab(value)
          setExpanded([])
        }}
        className="flex min-w-0 flex-col"
      >
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
          <h2 className="mr-auto text-md font-semibold">{m.ranking}</h2>
          <TabsList aria-label={m.ranking}>
            {tabs.map((item) => (
              <TabsTrigger key={item} value={item} className="h-control-sm text-sm">
                {tabLabels[item]}
                <span className="font-normal text-foreground-muted tabular-nums">
                  {report.groups[item].length}
                </span>
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        <TabsContent value={tab}>
          <Table label={m.ranking}>
            <thead>
              <tr>
                {columns.map((column) => (
                  <Th
                    key={column.key}
                    aria-sort={
                      sort.key === column.key
                        ? sort.descending
                          ? 'descending'
                          : 'ascending'
                        : 'none'
                    }
                    className={column.key === 'key' ? '' : 'text-right'}
                  >
                    <button
                      type="button"
                      onClick={() => {
                        setSort(
                          sort.key === column.key
                            ? { key: column.key, descending: !sort.descending }
                            : { key: column.key, descending: column.key !== 'key' },
                        )
                      }}
                      className={
                        'inline-flex items-center gap-1 hover:text-foreground ' +
                        (sort.key === column.key ? 'text-foreground' : '')
                      }
                    >
                      {column.label}
                      {sort.key === column.key && (
                        <ChevronDown
                          size={12}
                          aria-hidden
                          className={sort.descending ? '' : 'rotate-180'}
                        />
                      )}
                    </button>
                  </Th>
                ))}
                <Th className="w-control">
                  <span className="sr-only">{m.onlyThis}</span>
                </Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const label = f.label(tab, row.key)
                const breakdown = children(row.key)
                const open = expanded.includes(row.key)
                return [
                  <tr key={row.key} className="hover:bg-hover">
                    <Td className="max-w-[288px]">
                      <div className="flex items-center gap-1">
                        {breakdown.length > 0 ? (
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-expanded={open}
                            aria-label={open ? m.hideBreakdown : m.showBreakdown}
                            className="size-6 shrink-0"
                            onClick={() => {
                              setExpanded(
                                open
                                  ? expanded.filter((item) => item !== row.key)
                                  : [...expanded, row.key],
                              )
                            }}
                          >
                            <ChevronRight
                              size={14}
                              aria-hidden
                              className={open ? 'rotate-90' : ''}
                            />
                          </Button>
                        ) : (
                          <span aria-hidden className="w-6 shrink-0" />
                        )}
                        <span title={label} className="truncate">
                          {label}
                        </span>
                      </div>
                      <div className="mt-1 ml-[28px]">
                        <Meter value={row.requests / total} tone="info" label={label} />
                      </div>
                    </Td>
                    {statCells(row, true)}
                    <Td className="text-right">
                      {row.key !== '' && (
                        <Tooltip label={m.onlyThis}>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={`${m.onlyThis}: ${label}`}
                            className="size-control-sm"
                            onClick={() => {
                              if (tab === 'status') onStatus(row.key)
                              else onFilter(tab, row.key)
                            }}
                          >
                            <Funnel size={14} aria-hidden />
                          </Button>
                        </Tooltip>
                      )}
                    </Td>
                  </tr>,
                  ...(open
                    ? breakdown.map((child) => (
                        <tr
                          key={row.key + '\n' + child.key}
                          className="bg-panel text-xs text-foreground-muted"
                        >
                          <Td className="max-w-[288px] first:pl-[44px]">
                            <span title={child.label} className="block truncate">
                              {child.label}
                            </span>
                          </Td>
                          {statCells(child.stats, false)}
                          <Td />
                        </tr>
                      ))
                    : []),
                ]
              })}
            </tbody>
          </Table>
        </TabsContent>
      </Tabs>
    </section>
  )
}
