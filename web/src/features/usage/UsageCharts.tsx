import type { ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

import { ChartLegend, type ChartSeries, TimeChart, useLegend } from '@/components/ui/chart'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { Tooltip } from '@/components/ui/tooltip'
import { useUsageFormat } from '@/features/usage/format'
import { usageMessages } from '@/features/usage/messages'
import {
  assignColors,
  dimensions,
  durationSteps,
  otherColor,
  successRate,
} from '@/features/usage/model'
import { usePreference } from '@/features/usage/preference'
import type { UsageDimension, UsageReport } from '@/features/usage/types'
import { useMessages } from '@/lib/i18n'

// Metric 是流量图的纵轴指标
type Metric = 'requests' | 'tokens'

// isMetric 判断本地保存的流量图指标是否有效
function isMetric(value: unknown): value is Metric {
  return value === 'requests' || value === 'tokens'
}

// colors 是结果、耗时与 token 构成图的固定序列颜色
const colors = {
  primary: '#3a7bfc',
  success: '#1adb87',
  warning: '#f58600',
  error: '#f04438',
  violet: '#8e6cf0',
}

// ChartCard 是一张图表卡片：标题、说明、右侧控件与图
function ChartCard({
  title,
  help,
  actions,
  className,
  children,
}: {
  title: string
  help?: string
  actions?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <figure
      className={twMerge(
        'flex min-w-0 flex-col overflow-hidden rounded-md border border-border bg-surface',
        className,
      )}
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-5 py-3">
        <figcaption className="mr-auto min-w-0">
          <span className="text-md font-semibold">{title}</span>
          {help && <span className="ml-2 text-xs text-foreground-muted">{help}</span>}
        </figcaption>
        {actions}
      </div>
      <div className="flex min-w-0 flex-col gap-3 px-5 py-4">{children}</div>
    </figure>
  )
}

// UsageCharts 是用量页的流量、结果、耗时与 token 构成四张图
export function UsageCharts({
  report,
  stack,
  onStack,
}: {
  report: UsageReport
  stack: UsageDimension
  onStack: (stack: UsageDimension) => void
}) {
  const m = useMessages(usageMessages)
  const f = useUsageFormat()
  const [metric, setMetric] = usePreference<Metric>('trafficMetric', 'requests', isMetric)
  const trafficLegend = useLegend()
  const outcomesLegend = useLegend()
  const latencyLegend = useLegend()
  const tokensLegend = useLegend()
  const { buckets, totals } = report
  const multiDay = new Date(report.to).getTime() - new Date(report.from).getTime() > 86_400_000
  const labels = buckets.map((bucket) => f.bucketLabel(bucket.at, report.bucket_seconds, multiDay))
  const dimensionLabels: Record<UsageDimension, string> = {
    model: m.model,
    account: m.account,
    protocol: m.protocol,
    state: m.state,
  }
  const palette = assignColors(report.series.filter((item) => !item.other).map((item) => item.key))
  const traffic: ChartSeries[] = report.series.map((item) => ({
    name: item.other === true ? m.other : f.label(report.stack_by, item.key),
    color: item.other === true ? otherColor : (palette.get(item.key) ?? otherColor),
    kind: 'bar',
    values: metric === 'tokens' ? item.tokens : item.requests,
  }))
  const outcomes: ChartSeries[] = [
    {
      name: m.succeeded,
      color: colors.success,
      kind: 'bar',
      values: buckets.map((item) => item.succeeded),
    },
    {
      name: m.rateLimited,
      color: colors.warning,
      kind: 'bar',
      values: buckets.map((item) => item.rate_limited),
    },
    {
      name: m.otherErrors,
      color: colors.error,
      kind: 'bar',
      values: buckets.map((item) => item.failed - item.rate_limited),
    },
    {
      name: m.canceled,
      color: otherColor,
      kind: 'bar',
      values: buckets.map((item) => item.canceled),
    },
    {
      name: m.successRate,
      color: colors.primary,
      kind: 'line',
      right: true,
      values: buckets.map((item) => {
        const rate = successRate(item)
        return rate === null ? null : Math.round(rate * 1000) / 10
      }),
    },
  ]
  const positive = (value: number) => (value > 0 ? Math.round(value) : null)
  const latency: ChartSeries[] = [
    {
      name: `${m.duration} P50`,
      color: colors.primary,
      kind: 'line',
      values: buckets.map((item) => positive(item.duration.p50_ms)),
    },
    {
      name: `${m.duration} P95`,
      color: colors.violet,
      kind: 'line',
      values: buckets.map((item) => positive(item.duration.p95_ms)),
    },
    {
      name: `${m.firstEvent} P95`,
      color: colors.success,
      kind: 'line',
      values: buckets.map((item) => positive(item.first_event.p95_ms)),
    },
  ]
  const tokens: ChartSeries[] = [
    {
      name: m.input,
      color: colors.primary,
      kind: 'area',
      values: buckets.map((item) => item.input_tokens),
    },
    {
      name: m.reasoning,
      color: colors.violet,
      kind: 'area',
      values: buckets.map((item) => item.reasoning_tokens),
    },
    {
      name: m.reply,
      color: colors.success,
      kind: 'area',
      values: buckets.map((item) => item.reply_tokens),
    },
  ]
  const latencyRows = [
    {
      label: m.duration,
      help: m.durationHelp,
      values: [
        totals.duration.p50_ms,
        totals.duration.p95_ms,
        totals.duration.p99_ms,
        totals.duration.avg_ms,
      ],
    },
    {
      label: m.firstEvent,
      help: m.firstEventHelp,
      values: [
        totals.first_event.p50_ms,
        totals.first_event.p95_ms,
        totals.first_event.p99_ms,
        totals.first_event.avg_ms,
      ],
    },
    { label: m.queue, help: m.queueHelp, values: [null, null, null, totals.queue_avg_ms] },
  ]
  const legendItems = (series: ChartSeries[]) =>
    series.map((item) => ({ name: item.name, color: item.color }))
  const axisDuration = (value: number) => (value === 0 ? '0' : f.duration(value))
  return (
    <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
      <ChartCard
        title={m.chartTraffic}
        help={m.chartTrafficHelp}
        className="xl:col-span-2"
        actions={
          <>
            <Select
              label={m.stackBy}
              value={stack}
              onValueChange={onStack}
              options={dimensions.map((item) => ({ value: item, label: dimensionLabels[item] }))}
              size="sm"
              className="w-[128px]"
            />
            <Segmented
              label={m.chartTraffic}
              value={metric}
              onValueChange={setMetric}
              options={[
                { value: 'requests', label: m.metricRequests },
                { value: 'tokens', label: m.metricTokens },
              ]}
            />
          </>
        }
      >
        <ChartLegend items={legendItems(traffic)} legend={trafficLegend} />
        <TimeChart
          label={m.chartTraffic}
          labels={labels}
          series={traffic}
          legend={trafficLegend}
          height={260}
          format={f.compact}
          integer
          total={m.total}
          sorted
        />
      </ChartCard>
      <ChartCard title={m.chartOutcomes}>
        <ChartLegend items={legendItems(outcomes)} legend={outcomesLegend} />
        <TimeChart
          label={m.chartOutcomes}
          labels={labels}
          series={outcomes}
          legend={outcomesLegend}
          height={240}
          format={f.compact}
          rightFormat={(value) => f.percent(value / 100)}
          integer
        />
      </ChartCard>
      <ChartCard title={m.chartLatency}>
        <table className="w-full text-xs tabular-nums">
          <thead className="text-foreground-muted">
            <tr>
              <th scope="col" className="py-1 pr-2 text-left font-normal">
                <span className="sr-only">{m.metric}</span>
              </th>
              {['P50', 'P95', 'P99', m.average].map((column) => (
                <th key={column} scope="col" className="py-1 pl-2 text-right font-normal">
                  {column}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {latencyRows.map((row) => (
              <tr key={row.label} className="border-t border-border">
                <th scope="row" className="py-1 pr-2 text-left font-normal text-foreground-muted">
                  <Tooltip label={row.help}>
                    <button
                      type="button"
                      className="cursor-help underline decoration-border-strong decoration-dotted underline-offset-4"
                    >
                      {row.label}
                    </button>
                  </Tooltip>
                </th>
                {row.values.map((value, index) => (
                  <td key={index} className="py-1 pl-2 text-right whitespace-nowrap">
                    {value === null ? '' : f.duration(value)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        <ChartLegend items={legendItems(latency)} legend={latencyLegend} />
        <TimeChart
          label={m.chartLatency}
          labels={labels}
          series={latency}
          legend={latencyLegend}
          height={200}
          format={axisDuration}
          steps={durationSteps}
          reference={
            totals.duration.p95_ms > 0
              ? {
                  value: totals.duration.p95_ms,
                  label: `P95 ${f.duration(totals.duration.p95_ms)}`,
                  color: colors.violet,
                }
              : undefined
          }
        />
      </ChartCard>
      <ChartCard title={m.chartTokens} className="xl:col-span-2">
        <ChartLegend items={legendItems(tokens)} legend={tokensLegend} />
        <TimeChart
          label={m.chartTokens}
          labels={labels}
          series={tokens}
          legend={tokensLegend}
          height={220}
          format={f.compact}
          integer
          total={m.total}
        />
      </ChartCard>
    </div>
  )
}
