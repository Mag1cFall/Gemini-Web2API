import { Calendar, ChevronDown } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useUsageFormat } from '@/features/usage/format'
import { usageMessages } from '@/features/usage/messages'
import {
  calendarPresets,
  localInput,
  rangeError,
  type RangePreset,
  relativePresets,
  resolveRange,
  retention,
} from '@/features/usage/model'
import { useMessages } from '@/lib/i18n'

// useRangeLabels 返回各范围取值的文案
export function useRangeLabels(): Record<RangePreset, string> {
  const m = useMessages(usageMessages)
  return {
    '1h': m.range1h,
    '6h': m.range6h,
    '24h': m.range24h,
    '7d': m.range7d,
    '30d': m.range30d,
    '90d': m.range90d,
    today: m.rangeToday,
    yesterday: m.rangeYesterday,
    week: m.rangeWeek,
    month: m.rangeMonth,
    lastMonth: m.rangeLastMonth,
    custom: m.rangeCustom,
  }
}

// RangePicker 是时间范围选择：快捷范围与自定义起止时间，summary 为当前数据的实际范围
export function RangePicker({
  range,
  from,
  to,
  summary,
  onApply,
}: {
  range: RangePreset
  from: string
  to: string
  summary: { from: string; to: string } | null
  onApply: (range: RangePreset, from: string, to: string) => void
}) {
  const m = useMessages(usageMessages)
  const labels = useRangeLabels()
  const { dateTime } = useUsageFormat()
  const [open, setOpen] = useState(false)
  const [customFrom, setCustomFrom] = useState('')
  const [customTo, setCustomTo] = useState('')
  const [now, setNow] = useState(() => new Date())
  const error = rangeError(new Date(customFrom), new Date(customTo))
  const errorText = error === 'invalid' ? m.rangeInvalid : error === 'tooLong' ? m.rangeTooLong : ''
  const beforeRetention = new Date(customFrom).getTime() < now.getTime() - retention
  const summaryText = summary ? `${dateTime(summary.from)} – ${dateTime(summary.to)}` : ''

  // prepare 打开浮层时用当前范围填充自定义输入框
  const prepare = (value: boolean) => {
    setOpen(value)
    if (!value) return
    const current = new Date()
    setNow(current)
    const resolved = resolveRange({ range, from, to }, current) ?? {
      from: new Date(current.getTime() - 86_400_000),
      to: current,
    }
    setCustomFrom(localInput(resolved.from))
    setCustomTo(localInput(resolved.to))
  }

  // choose 应用快捷范围并关闭浮层
  const choose = (preset: RangePreset) => {
    onApply(preset, '', '')
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={prepare}>
      <PopoverTrigger asChild>
        <Button
          size="sm"
          aria-label={`${m.range}: ${labels[range]} ${summaryText}`}
          className="min-w-0 gap-2"
        >
          <Calendar size={16} aria-hidden className="shrink-0 text-foreground-muted" />
          <span className="font-medium">{labels[range]}</span>
          {summaryText && (
            <span className="hidden truncate text-xs text-foreground-muted tabular-nums sm:inline">
              {summaryText}
            </span>
          )}
          <ChevronDown size={16} aria-hidden className="shrink-0 text-foreground-muted" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-dialog-wide">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-3">
            {[
              { title: m.rangeRecent, items: relativePresets },
              { title: m.rangeCalendar, items: calendarPresets },
            ].map((group) => (
              <section key={group.title} className="flex flex-col gap-1">
                <h3 className="text-xs font-medium text-foreground-muted">{group.title}</h3>
                <div className="grid grid-cols-2 gap-1">
                  {group.items.map((item) => (
                    <button
                      key={item}
                      type="button"
                      aria-pressed={range === item}
                      onClick={() => {
                        choose(item)
                      }}
                      className="h-control-sm rounded-sm px-2 text-left text-sm text-foreground hover:bg-hover aria-pressed:bg-primary-soft aria-pressed:font-semibold aria-pressed:text-primary"
                    >
                      {labels[item]}
                    </button>
                  ))}
                </div>
              </section>
            ))}
          </div>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (error !== '') return
              onApply('custom', customFrom, customTo)
              setOpen(false)
            }}
            className="flex flex-col gap-2 border-border sm:border-l sm:pl-4"
          >
            <h3 className="text-xs font-medium text-foreground-muted">{m.rangeCustomTitle}</h3>
            <label className="flex flex-col gap-1 text-xs text-foreground-muted">
              {m.rangeStart}
              <Input
                type="datetime-local"
                value={customFrom}
                max={localInput(now)}
                onChange={(event) => {
                  setCustomFrom(event.target.value)
                }}
              />
            </label>
            <label className="flex flex-col gap-1 text-xs text-foreground-muted">
              {m.rangeEnd}
              <Input
                type="datetime-local"
                value={customTo}
                onChange={(event) => {
                  setCustomTo(event.target.value)
                }}
              />
            </label>
            {errorText ? (
              <p role="alert" className="text-xs text-error-text">
                {errorText}
              </p>
            ) : (
              beforeRetention && <p className="text-xs text-warning-text">{m.rangeRetention}</p>
            )}
            <Button type="submit" variant="primary" size="sm" disabled={error !== ''}>
              {m.rangeApply}
            </Button>
          </form>
        </div>
      </PopoverContent>
    </Popover>
  )
}
