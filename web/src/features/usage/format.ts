import { requestStateMessages } from '@/features/requests/messages'
import { usageMessages } from '@/features/usage/messages'
import { protocolLabels } from '@/features/usage/model'
import type { UsageGroupDimension } from '@/features/usage/types'
import {
  formatCompact,
  formatDuration,
  formatNumber,
  formatPercent,
  formatRelative,
  formatUsd,
} from '@/lib/format'
import { useI18n, useMessages } from '@/lib/i18n'

// useUsageFormat 返回随界面语言变化的数字、耗时、时间与维度取值格式
export function useUsageFormat() {
  const { locale } = useI18n()
  const m = useMessages(usageMessages)
  const states: Record<string, string> = useMessages(requestStateMessages)
  const dateTime = (value: string | Date, seconds = false) =>
    new Date(value).toLocaleString(locale, {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      ...(seconds ? { second: '2-digit' } : {}),
      hour12: false,
    })
  return {
    locale,
    compact: (value: number) => formatCompact(value, locale),
    integer: (value: number) => formatNumber(value, locale),
    usd: (value: number) => formatUsd(value, locale),
    percent: (value: number | null) => (value === null ? '—' : formatPercent(value, locale)),
    duration: (milliseconds: number) =>
      milliseconds > 0 ? formatDuration(milliseconds, locale) : '—',
    dateTime,
    clock: (value: Date) => value.toLocaleTimeString(locale, { hour12: false }),
    ago: (value: string, now: Date) => formatRelative(new Date(value), now, locale),
    // bucketLabel 按分桶粒度格式化横轴时间，跨天的分钟与小时分桶带日期
    bucketLabel: (at: string, bucketSeconds: number, multiDay: boolean) => {
      const date = new Date(at)
      const day = date.toLocaleDateString(locale, { month: '2-digit', day: '2-digit' })
      if (bucketSeconds >= 86_400) return day
      const time = date.toLocaleTimeString(locale, {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      })
      return multiDay ? `${day} ${time}` : time
    },
    // label 返回维度取值的显示名，空取值显示为未分配
    label: (dimension: UsageGroupDimension, key: string) => {
      if (key === '') return m.unassigned
      if (dimension === 'protocol') return protocolLabels[key] ?? key
      if (dimension === 'state') return states[key] ?? key
      return key
    },
  }
}

// UsageFormat 是 useUsageFormat 返回的格式函数
export type UsageFormat = ReturnType<typeof useUsageFormat>
