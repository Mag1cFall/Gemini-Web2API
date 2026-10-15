import { useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  ChartColumn,
  CircleCheck,
  CircleDollarSign,
  Gauge,
  Layers,
  RefreshCw,
  Timer,
  X,
} from 'lucide-react'
import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router'

import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Card, PageHeader } from '@/components/ui/page'
import { Select } from '@/components/ui/select'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Tooltip } from '@/components/ui/tooltip'
import { useModels } from '@/features/models/queries'
import { FilterMenu } from '@/features/usage/FilterMenu'
import { type UsageFormat, useUsageFormat } from '@/features/usage/format'
import { type Kpi, KpiCard, type KpiDelta } from '@/features/usage/KpiCard'
import { usageMessages } from '@/features/usage/messages'
import {
  dimensions,
  liveRange,
  parseUsageView,
  rangeError,
  rateTone,
  relativeChange,
  successRate,
  type UsageView,
  usageViewParams,
} from '@/features/usage/model'
import { usePreference } from '@/features/usage/preference'
import { useUsageReport } from '@/features/usage/queries'
import { RangePicker } from '@/features/usage/RangePicker'
import { RankingTable } from '@/features/usage/RankingTable'
import { RecordsTable } from '@/features/usage/RecordsTable'
import type { UsageDimension, UsageReport, UsageStats } from '@/features/usage/types'
import { UsageCharts } from '@/features/usage/UsageCharts'
import { fill, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// refreshChoices 是自动刷新间隔的可选秒数，0 表示关闭
const refreshChoices = [0, 15, 30, 60]

// isRefreshChoice 判断本地保存的自动刷新间隔是否有效
function isRefreshChoice(value: unknown): value is number {
  return refreshChoices.some((choice) => choice === value)
}

// RefreshStatus 显示最新请求的新鲜度、本页更新时间与下次自动刷新的倒计时
function RefreshStatus({
  latest,
  updatedAt,
  interval,
  live,
}: {
  latest: string | undefined
  updatedAt: number
  interval: number
  live: boolean
}) {
  const m = useMessages(usageMessages)
  const f = useUsageFormat()
  const now = useNow(1000)
  const age = latest === undefined ? Number.POSITIVE_INFINITY : now.getTime() - Date.parse(latest)
  const tone =
    latest === undefined
      ? 'bg-foreground-subtle'
      : age < 300_000
        ? 'bg-success'
        : age < 3_600_000
          ? 'bg-warning'
          : 'bg-foreground-subtle'
  const countdown = !live
    ? m.fixedRange
    : interval > 0 && updatedAt > 0
      ? fill(m.nextRefresh, {
          seconds: Math.max(
            0,
            interval - Math.max(0, Math.floor((now.getTime() - updatedAt) / 1000)),
          ),
        })
      : ''
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-foreground-muted">
      <Tooltip label={m.latestHelp}>
        <span className="flex items-center gap-2">
          <span aria-hidden className={'size-2 rounded-full ' + tone} />
          {latest === undefined ? m.latestNone : fill(m.latest, { time: f.ago(latest, now) })}
        </span>
      </Tooltip>
      {updatedAt > 0 && (
        <span aria-live="polite" className="tabular-nums">
          {fill(m.updatedAt, { time: f.clock(new Date(updatedAt)) })}
          {countdown && <span className="ml-1 text-foreground-subtle">· {countdown}</span>}
        </span>
      )}
    </div>
  )
}

// buildKpis 生成用量页顶部的六张指标卡片
function buildKpis(report: UsageReport, m: (typeof usageMessages)['zh-CN'], f: UsageFormat): Kpi[] {
  const { totals, previous, buckets, recent } = report
  const span = Date.parse(report.to) - Date.parse(report.from)
  const previousRange = `${f.dateTime(new Date(Date.parse(report.from) - span))} – ${f.dateTime(report.from)}`
  // delta 生成相对上一周期的变化标注，上一周期没有请求时不显示
  const delta = (
    change: number | null,
    text: (value: number) => string,
    better: 'up' | 'down' | 'none',
  ): KpiDelta | undefined => {
    if (change === null || previous.requests === 0) return undefined
    const flat = Math.abs(change) < 0.0005
    return {
      text: (change >= 0 ? '+' : '−') + text(Math.abs(change)),
      tone:
        flat || better === 'none' ? 'neutral' : change > 0 === (better === 'up') ? 'good' : 'bad',
      direction: change >= 0 ? 'up' : 'down',
      help: fill(m.vsPrevious, { range: previousRange }),
    }
  }
  const series = (pick: (bucket: UsageStats) => number | null) =>
    buckets.map(pick).filter((value): value is number => value !== null)
  const rate = successRate(totals)
  const previousRate = successRate(previous)
  const otherErrors = totals.failed - totals.rate_limited
  const output = totals.reasoning_tokens + totals.reply_tokens
  return [
    {
      label: m.requests,
      icon: Activity,
      value: f.integer(totals.requests),
      help: m.requestsHelp,
      details: [
        {
          label: m.failed,
          value: f.integer(totals.failed),
          ...(totals.failed > 0 ? { tone: 'text-error-text' } : {}),
        },
        { label: m.canceled, value: f.integer(totals.canceled) },
      ],
      delta: delta(relativeChange(totals.requests, previous.requests), f.percent, 'none'),
      spark: buckets.map((bucket) => bucket.requests),
      color: '#3a7bfc',
    },
    {
      label: m.successRate,
      icon: CircleCheck,
      value: f.percent(rate),
      help: m.successRateHelp,
      details: [
        {
          label: m.rateLimitedShort,
          value: f.integer(totals.rate_limited),
          ...(totals.rate_limited > 0 ? { tone: 'text-warning-text' } : {}),
        },
        {
          label: m.otherErrors,
          value: f.integer(otherErrors),
          ...(otherErrors > 0 ? { tone: 'text-error-text' } : {}),
        },
      ],
      delta: delta(
        rate === null || previousRate === null ? null : rate - previousRate,
        (value) => fill(m.points, { value: (value * 100).toFixed(1) }),
        'up',
      ),
      spark: series((bucket) => {
        const value = successRate(bucket)
        return value === null ? null : value * 100
      }),
      color: '#1adb87',
      tone: rateTone(rate),
    },
    {
      label: m.tokens,
      icon: Layers,
      value: f.compact(totals.total_tokens),
      help: m.tokensHelp,
      details: [
        { label: m.input, value: f.compact(totals.input_tokens) },
        { label: m.output, value: f.compact(output) },
      ],
      delta: delta(relativeChange(totals.total_tokens, previous.total_tokens), f.percent, 'none'),
      spark: buckets.map((bucket) => bucket.total_tokens),
      color: '#22b8cf',
    },
    {
      label: m.throughput,
      icon: Gauge,
      value: `${f.compact(recent.requests / recent.minutes)} RPM`,
      help: fill(m.throughputHelp, { minutes: recent.minutes }),
      details: [
        { label: fill(m.recentMinutes, { minutes: recent.minutes }), value: '' },
        { label: 'TPM', value: f.compact(recent.tokens / recent.minutes) },
      ],
      spark: buckets.map((bucket) => bucket.requests),
      color: '#f58600',
    },
    {
      label: m.cost,
      icon: CircleDollarSign,
      value: f.usd(report.cost),
      help: m.costHelp,
      details: [
        {
          label: m.pricedRequests,
          value: `${f.integer(report.priced_requests)}/${f.integer(totals.requests)}`,
        },
        { label: m.output, value: f.compact(output) },
      ],
      delta: delta(relativeChange(report.cost, report.previous_cost), f.percent, 'none'),
      spark: buckets.map((bucket) => bucket.cost),
      color: '#1d7a4d',
    },
    {
      label: `${m.firstEvent} P50`,
      icon: Timer,
      value: f.duration(totals.first_event.p50_ms),
      help: m.firstEventHelp,
      details: [{ label: 'P95', value: f.duration(totals.first_event.p95_ms) }],
      delta: delta(
        relativeChange(totals.first_event.p50_ms, previous.first_event.p50_ms),
        f.percent,
        'down',
      ),
      spark: series((bucket) => (bucket.first_event.p50_ms > 0 ? bucket.first_event.p50_ms : null)),
      color: '#8e6cf0',
    },
  ]
}

// UsagePage 是用量看板：范围与筛选、指标卡片、趋势图、分项排行与请求记录，视图状态保存在地址栏
export function UsagePage() {
  const m = useMessages(usageMessages)
  const f = useUsageFormat()
  const client = useQueryClient()
  const [params, setParams] = useSearchParams()
  const view = useMemo(() => parseUsageView(params), [params])
  const [autoRefresh, setAutoRefresh] = usePreference('autoRefresh', 30, isRefreshChoice)
  const live = liveRange(view.range)
  const report = useUsageReport(view, live && autoRefresh > 0 ? autoRefresh * 1000 : false)
  const models = useModels().data?.models
  const invalid = view.range === 'custom' ? rangeError(new Date(view.from), new Date(view.to)) : ''

  // update 把视图变化同步写入地址栏，替换当前历史记录
  const update = useCallback(
    (next: Partial<UsageView>) => {
      setParams((current) => usageViewParams({ ...parseUsageView(current), ...next }), {
        replace: true,
        flushSync: true,
      })
    },
    [setParams],
  )
  const onSearch = useCallback(
    (q: string) => {
      update({ q })
    },
    [update],
  )
  const onStatus = useCallback(
    (status: string) => {
      update({ status })
    },
    [update],
  )

  // setFilter 替换一个维度的筛选取值，空数组表示不筛选该维度
  const setFilter = (dimension: UsageDimension, values: string[]) => {
    update({ filters: { ...view.filters, [dimension]: values } })
  }

  // filterOptions 返回筛选候选，服务端忽略空取值，模型按模型目录顺序排列，目录外的取值排在最后
  const filterOptions = (dimension: UsageDimension) => {
    const options = (report.data?.options[dimension] ?? []).filter((value) => value !== '')
    if (dimension !== 'model' || !models) return options
    const order = new Map(models.map((model, index) => [model.id, index]))
    const rank = (value: string) => order.get(value) ?? order.size
    return [...options].sort((left, right) => rank(left) - rank(right))
  }

  const dimensionLabels: Record<UsageDimension, string> = {
    model: m.model,
    account: m.account,
    protocol: m.protocol,
    state: m.state,
  }
  const chips = dimensions.flatMap((dimension) =>
    (view.filters[dimension] ?? []).map((value) => ({ dimension, value })),
  )
  const hasFilters = chips.length > 0
  const data = report.data
  const scope =
    data && !report.isPlaceholderData
      ? { from: data.from, to: data.to, filters: view.filters, status: view.status, q: view.q }
      : null
  const pagingKey = JSON.stringify([
    view.range,
    view.from,
    view.to,
    view.filters,
    view.status,
    view.q,
  ])

  // refresh 立即刷新报告与请求记录
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['usage-report'] })
    void client.invalidateQueries({ queryKey: ['usage-records'] })
  }

  return (
    <>
      <PageHeader
        title={m.title}
        description={m.description}
        actions={
          <>
            <RefreshStatus
              latest={data?.latest_at}
              updatedAt={report.dataUpdatedAt}
              interval={autoRefresh}
              live={live}
            />
            {live && (
              <Select
                label={m.autoRefresh}
                value={String(autoRefresh)}
                onValueChange={(value) => {
                  setAutoRefresh(Number(value))
                }}
                options={refreshChoices.map((choice) => ({
                  value: String(choice),
                  label:
                    choice === 0 ? m.autoRefreshOff : fill(m.autoRefreshEvery, { seconds: choice }),
                }))}
                size="sm"
                className="w-[128px]"
              />
            )}
            <Tooltip label={m.refresh}>
              <Button size="icon" aria-label={m.refresh} onClick={refresh}>
                <RefreshCw
                  size={16}
                  aria-hidden
                  className={report.isFetching ? 'animate-spin' : ''}
                />
              </Button>
            </Tooltip>
          </>
        }
      />

      <div
        role="toolbar"
        aria-label={m.filters}
        className="sticky top-0 z-sticky -mx-10 -my-3 flex flex-wrap items-center gap-2 bg-panel px-10 py-3 max-lg:-mx-4 max-lg:px-4"
      >
        <RangePicker
          range={view.range}
          from={view.from}
          to={view.to}
          summary={data ? { from: data.from, to: data.to } : null}
          onApply={(range, from, to) => {
            update({ range, from, to })
          }}
        />
        <span aria-hidden className="mx-1 hidden h-6 w-px bg-border sm:block" />
        {dimensions.map((dimension) => (
          <FilterMenu
            key={dimension}
            label={dimensionLabels[dimension]}
            options={filterOptions(dimension)}
            selected={view.filters[dimension] ?? []}
            format={(value) => f.label(dimension, value)}
            onChange={(values) => {
              setFilter(dimension, values)
            }}
          />
        ))}
        {hasFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              update({ filters: {} })
            }}
          >
            {m.filtersClear}
          </Button>
        )}
      </div>
      {hasFilters && (
        <ul aria-label={m.filters} className="-mt-3 flex flex-wrap gap-2">
          {chips.map((chip) => {
            const label = f.label(chip.dimension, chip.value)
            return (
              <li
                key={chip.dimension + '\n' + chip.value}
                className="flex h-6 max-w-full items-center gap-1 rounded-sm bg-primary-soft pr-1 pl-2 text-xs text-primary"
              >
                <span className="shrink-0 opacity-70">{dimensionLabels[chip.dimension]}:</span>
                <span title={label} className="min-w-0 truncate">
                  {label}
                </span>
                <button
                  type="button"
                  aria-label={fill(m.removeFilter, { value: label })}
                  onClick={() => {
                    setFilter(
                      chip.dimension,
                      (view.filters[chip.dimension] ?? []).filter((item) => item !== chip.value),
                    )
                  }}
                  className="flex size-4 items-center justify-center rounded-sm hover:bg-primary-soft-strong"
                >
                  <X size={12} aria-hidden />
                </button>
              </li>
            )
          })}
        </ul>
      )}

      {invalid ? (
        <Card>
          <ErrorState
            title={m.title}
            error={invalid === 'tooLong' ? m.rangeTooLong : m.rangeInvalid}
            retryLabel={m.retry}
          />
        </Card>
      ) : !data ? (
        report.isError ? (
          <Card>
            <ErrorState
              title={m.title}
              error={report.error}
              retryLabel={m.retry}
              onRetry={() => void report.refetch()}
            />
          </Card>
        ) : (
          <Loading label={m.loading} />
        )
      ) : (
        <>
          {report.isError && (
            <Callout tone="error" role="alert">
              <span>{report.error.message}</span>
              <Button size="sm" onClick={() => void report.refetch()}>
                {m.retry}
              </Button>
            </Callout>
          )}
          {data.totals.requests === 0 ? (
            <EmptyState
              icon={<ChartColumn size={20} aria-hidden />}
              title={hasFilters ? m.emptyFiltered : m.empty}
              description={hasFilters ? m.emptyFilteredHelp : m.emptyHelp}
              action={
                hasFilters && (
                  <Button
                    onClick={() => {
                      update({ filters: {} })
                    }}
                  >
                    {m.filtersClear}
                  </Button>
                )
              }
            />
          ) : (
            <>
              <div className="@container">
                <div className="grid grid-cols-1 gap-3 @min-[560px]:grid-cols-2 @min-[860px]:grid-cols-3 @min-[1440px]:grid-cols-6">
                  {buildKpis(data, m, f).map((kpi) => (
                    <KpiCard key={kpi.label} kpi={kpi} />
                  ))}
                </div>
              </div>
              <UsageCharts
                report={data}
                stack={view.stack}
                onStack={(stack) => {
                  update({ stack })
                }}
              />
              <RankingTable
                report={data}
                onFilter={(dimension, value) => {
                  const values = view.filters[dimension] ?? []
                  if (!values.includes(value)) setFilter(dimension, [...values, value])
                }}
                onStatus={onStatus}
              />
              <RecordsTable
                scope={scope}
                pagingKey={pagingKey}
                total={data.totals.requests}
                status={view.status}
                q={view.q}
                statusOptions={data.groups.status.map((group) => group.key).sort()}
                onStatus={onStatus}
                onSearch={onSearch}
              />
            </>
          )}
        </>
      )}
    </>
  )
}
