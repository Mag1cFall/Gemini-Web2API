import type { Language } from '@/lib/i18n'

// formatters 缓存按语言与选项创建的 Intl 格式化器
const formatters = new Map<
  string,
  Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat
>()

// numberFormat 返回缓存的 Intl.NumberFormat
function numberFormat(language: Language, options: Intl.NumberFormatOptions = {}) {
  const key = 'n' + language + JSON.stringify(options)
  let format = formatters.get(key) as Intl.NumberFormat | undefined
  if (!format) {
    format = new Intl.NumberFormat(language, options)
    formatters.set(key, format)
  }
  return format
}

// dateFormat 返回缓存的 Intl.DateTimeFormat
function dateFormat(language: Language, options: Intl.DateTimeFormatOptions) {
  const key = 'd' + language + JSON.stringify(options)
  let format = formatters.get(key) as Intl.DateTimeFormat | undefined
  if (!format) {
    format = new Intl.DateTimeFormat(language, options)
    formatters.set(key, format)
  }
  return format
}

// formatNumber 按语言输出带千分位的整数
export function formatNumber(value: number, language: Language) {
  return numberFormat(language).format(value)
}

// formatCompact 以紧凑写法输出 token 等大数
export function formatCompact(value: number, language: Language) {
  return numberFormat(language, { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

// formatPercent 把 0 到 1 的比例输出为百分比，非整数保留一位小数
export function formatPercent(fraction: number, language: Language) {
  return numberFormat(language, { style: 'percent', maximumFractionDigits: 1 }).format(fraction)
}

// formatDuration 把毫秒输出为 ms、s、min、h 中最合适的单位
export function formatDuration(ms: number, language: Language) {
  const unit = (value: number, name: string, digits: number) =>
    numberFormat(language, {
      style: 'unit',
      unit: name,
      unitDisplay: 'narrow',
      maximumFractionDigits: digits,
    }).format(value)
  if (ms < 1000) return unit(Math.round(ms), 'millisecond', 0)
  if (ms < 60_000) return unit(ms / 1000, 'second', ms < 10_000 ? 2 : 1)
  if (ms < 3_600_000) return unit(ms / 60_000, 'minute', 1)
  return unit(ms / 3_600_000, 'hour', 1)
}

// formatBytes 以 1024 进制输出字节数
export function formatBytes(bytes: number, language: Language) {
  const units = ['byte', 'kilobyte', 'megabyte', 'gigabyte'] as const
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return numberFormat(language, {
    style: 'unit',
    unit: units[index],
    unitDisplay: 'short',
    maximumFractionDigits: index === 0 ? 0 : 1,
  }).format(value)
}

// relativeSteps 是相对时间从秒到天的进位
const relativeSteps: [Intl.RelativeTimeFormatUnit, number][] = [
  ['second', 60],
  ['minute', 60],
  ['hour', 24],
  ['day', Number.POSITIVE_INFINITY],
]

// formatRelative 输出目标时间相对 now 的描述，如“3 分钟前”“2 小时后”
export function formatRelative(target: Date, now: Date, language: Language) {
  let value = (target.getTime() - now.getTime()) / 1000
  const key = 'r' + language
  let format = formatters.get(key) as Intl.RelativeTimeFormat | undefined
  if (!format) {
    format = new Intl.RelativeTimeFormat(language, { numeric: 'auto' })
    formatters.set(key, format)
  }
  for (const [unit, size] of relativeSteps) {
    if (Math.abs(value) < size) return format.format(Math.round(value), unit)
    value /= size
  }
  return format.format(Math.round(value), 'day')
}

// formatDateTime 输出到秒的本地日期时间，同一天只输出时间
export function formatDateTime(target: Date, now: Date, language: Language) {
  const sameDay = target.toDateString() === now.toDateString()
  return dateFormat(language, {
    ...(sameDay ? {} : { month: '2-digit', day: '2-digit' }),
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(target)
}

// formatFullDateTime 输出完整的本地日期时间
export function formatFullDateTime(target: Date, language: Language) {
  return dateFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'medium',
    hour12: false,
  }).format(target)
}

// formatUsd 输出美元金额，小于 1 美元时保留到分以下两位
export function formatUsd(value: number, language: Language) {
  return numberFormat(language, {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: value < 1 ? 4 : 2,
  }).format(value)
}
