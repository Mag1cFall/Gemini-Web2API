import { memo } from 'react'

import type { LogRow } from '@/features/logs/filter'
import { timeFormat } from '@/features/logs/format'
import { LogMessage } from '@/features/logs/LogField'
import { RequestLogCard } from '@/features/logs/RequestLogCard'
import type { LogLevel } from '@/features/logs/types'
import { formatDateTime } from '@/lib/format'
import { useI18n } from '@/lib/i18n'

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

// LogTime 是日志行的时间，dated 为真时不是今天的日志带上月日
function LogTime({ time, dated }: { time: string; dated: boolean }) {
  const { language } = useI18n()
  const date = new Date(time)
  return (
    <time dateTime={time} className="font-mono text-sm text-foreground-subtle tabular-nums">
      {dated ? formatDateTime(date, new Date(), language) : timeFormat.format(date)}
    </time>
  )
}

// LogLine 渲染一行日志或请求卡片，行的最新事件、事件数与筛选的账号不变时不重新渲染；dated 为真时时间带上日期
export const LogLine = memo(
  function LogLine({
    row,
    account,
    dated = false,
  }: {
    row: LogRow
    account: string
    dated?: boolean
  }) {
    const level = row.entry.level
    return (
      <div
        className={
          'grid grid-cols-[auto_auto_minmax(0,1fr)] items-baseline gap-x-4 border-b border-l-2 border-b-border py-2 pr-5 pl-[18px] text-base select-text ' +
          levelClass[level]
        }
      >
        <LogTime time={row.events[0]?.time ?? row.entry.time} dated={dated} />
        <span className={'w-[44px] font-mono text-sm font-semibold ' + levelText[level]}>
          {level}
        </span>
        {row.entry.request ? (
          <RequestLogCard row={row} account={account} />
        ) : (
          <LogMessage
            source={row.entry.source}
            message={row.entry.message}
            fields={row.entry.fields}
          />
        )}
      </div>
    )
  },
  (prev, next) =>
    prev.row.entry === next.row.entry &&
    prev.row.events.length === next.row.events.length &&
    prev.account === next.account &&
    prev.dated === next.dated,
)
