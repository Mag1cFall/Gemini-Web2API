import { ArrowDown, ArrowUp, type LucideIcon } from 'lucide-react'

import { Tooltip } from '@/components/ui/tooltip'

// KpiDelta 是相对上一周期的变化标注，tone 表示变化是好是坏
export interface KpiDelta {
  text: string
  tone: 'good' | 'bad' | 'neutral'
  direction: 'up' | 'down'
  help: string
}

// Kpi 是一张指标卡片的内容，spark 为逐桶的趋势
export interface Kpi {
  label: string
  icon: LucideIcon
  value: string
  help: string
  details: { label: string; value: string; tone?: string }[]
  delta?: KpiDelta | undefined
  spark: number[]
  color: string
  tone?: string
}

// deltaTone 是变化标注的文字色调
const deltaTone: Record<KpiDelta['tone'], string> = {
  good: 'text-success-text',
  bad: 'text-error-text',
  neutral: 'text-foreground-muted',
}

// sparkPath 把序列归一化为 100×28 的折线路径，少于两个点时为空
function sparkPath(values: number[]) {
  if (values.length < 2) return ''
  const high = Math.max(...values)
  const low = Math.min(...values)
  const span = high - low || 1
  return values
    .map((value, index) => {
      const x = (index / (values.length - 1)) * 100
      const y = 26 - ((value - low) / span) * 24
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
    })
    .join(' ')
}

// KpiCard 是用量页顶部的一张指标卡片：名称与说明、变化、主值、补充行与趋势线
export function KpiCard({ kpi }: { kpi: Kpi }) {
  const path = sparkPath(kpi.spark)
  const note = kpi.details.map((detail) => `${detail.label} ${detail.value}`.trim()).join(' · ')
  const Icon = kpi.icon
  return (
    <article className="flex min-w-0 flex-col overflow-hidden rounded-md border border-border bg-surface">
      <div className="flex items-center gap-2 border-b border-border px-5 py-3 text-xs text-foreground-muted">
        <Icon size={16} aria-hidden className="shrink-0" />
        <Tooltip label={kpi.help}>
          <button
            type="button"
            className="cursor-help truncate underline decoration-border-strong decoration-dotted underline-offset-4"
          >
            {kpi.label}
          </button>
        </Tooltip>
        {kpi.delta && (
          <Tooltip label={kpi.delta.help}>
            <button
              type="button"
              className={
                'ml-auto flex shrink-0 cursor-help items-center tabular-nums ' +
                deltaTone[kpi.delta.tone]
              }
            >
              {kpi.delta.direction === 'up' ? (
                <ArrowUp size={12} aria-hidden />
              ) : (
                <ArrowDown size={12} aria-hidden />
              )}
              {kpi.delta.text}
              <span className="sr-only">{kpi.delta.help}</span>
            </button>
          </Tooltip>
        )}
      </div>
      <div className="flex min-w-0 flex-1 flex-col px-5 pt-3 pb-4">
        <p
          title={kpi.value}
          className={
            'text-2xl font-semibold break-words tabular-nums ' + (kpi.tone ?? 'text-foreground')
          }
        >
          {kpi.value}
        </p>
        <p title={note} className="mt-1 text-xs break-words text-foreground-muted">
          {kpi.details.map((detail, index) => (
            <span key={detail.label}>
              {index > 0 && <span aria-hidden> · </span>}
              {detail.label}{' '}
              {detail.value && (
                <span className={'tabular-nums ' + (detail.tone ?? 'text-foreground')}>
                  {detail.value}
                </span>
              )}
            </span>
          ))}
        </p>
        {path && (
          <svg
            aria-hidden
            viewBox="0 0 100 28"
            preserveAspectRatio="none"
            className="mt-auto h-[36px] w-full pt-2"
          >
            <path
              d={path}
              fill="none"
              stroke={kpi.color}
              strokeWidth={1.5}
              vectorEffect="non-scaling-stroke"
            />
          </svg>
        )}
      </div>
    </article>
  )
}
