import type { UsageDimension, UsageStats } from '@/features/usage/types'

// relativePresets 是相对当前时间的快捷范围
export const relativePresets = ['1h', '6h', '24h', '7d', '30d', '90d'] as const

// calendarPresets 是按本地日历对齐的快捷范围
export const calendarPresets = ['today', 'yesterday', 'week', 'month', 'lastMonth'] as const

// RangePreset 是用量范围，custom 使用视图中的 from 与 to
export type RangePreset =
  (typeof relativePresets)[number] | (typeof calendarPresets)[number] | 'custom'

// dimensions 是可筛选与堆叠的维度
export const dimensions: UsageDimension[] = ['model', 'account', 'protocol', 'state']

// maxSpan 是服务端允许的最长查询范围
export const maxSpan = 93 * 86_400_000

// retention 是请求账本的保留时长
export const retention = 90 * 86_400_000

// relativeSpans 是相对范围的毫秒数
const relativeSpans: Record<(typeof relativePresets)[number], number> = {
  '1h': 3_600_000,
  '6h': 6 * 3_600_000,
  '24h': 86_400_000,
  '7d': 7 * 86_400_000,
  '30d': 30 * 86_400_000,
  '90d': 90 * 86_400_000,
}

// UsageFilters 是各维度选中的取值，同一维度内为或关系
export type UsageFilters = Partial<Record<UsageDimension, string[]>>

// UsageView 是用量页写入地址栏的视图状态，status 与 q 只作用于请求记录
export interface UsageView {
  range: RangePreset
  from: string
  to: string
  stack: UsageDimension
  filters: UsageFilters
  status: string
  q: string
}

// defaultView 是地址栏没有参数时的视图
export const defaultView: UsageView = {
  range: '24h',
  from: '',
  to: '',
  stack: 'model',
  filters: {},
  status: '',
  q: '',
}

// presets 是全部范围取值
const presets: readonly string[] = [...relativePresets, ...calendarPresets, 'custom']

// parseUsageView 从地址栏参数读取视图状态，非法取值使用默认值
export function parseUsageView(params: URLSearchParams): UsageView {
  const range = params.get('range') ?? ''
  const stack = params.get('stack') ?? ''
  const status = params.get('status') ?? ''
  const filters: UsageFilters = {}
  for (const dimension of dimensions) {
    const values = (params.get(dimension) ?? '').split(',').filter((value) => value !== '')
    if (values.length > 0) filters[dimension] = values
  }
  return {
    range: presets.includes(range) ? (range as RangePreset) : defaultView.range,
    from: params.get('from') ?? '',
    to: params.get('to') ?? '',
    stack: dimensions.find((item) => item === stack) ?? defaultView.stack,
    filters,
    status: /^\d{3}$/.test(status) ? status : '',
    q: params.get('q') ?? '',
  }
}

// usageViewParams 返回只包含非默认取值的地址栏参数
export function usageViewParams(view: UsageView): URLSearchParams {
  const params = new URLSearchParams()
  if (view.range !== defaultView.range) params.set('range', view.range)
  if (view.range === 'custom') {
    params.set('from', view.from)
    params.set('to', view.to)
  }
  if (view.stack !== defaultView.stack) params.set('stack', view.stack)
  for (const dimension of dimensions) {
    const values = view.filters[dimension] ?? []
    if (values.length > 0) params.set(dimension, values.join(','))
  }
  if (view.status) params.set('status', view.status)
  if (view.q) params.set('q', view.q)
  return params
}

// rangeError 校验自定义范围，返回错误类别，有效时返回空字符串
export function rangeError(from: Date, to: Date): '' | 'invalid' | 'tooLong' {
  if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || from >= to) return 'invalid'
  if (to.getTime() - from.getTime() > maxSpan) return 'tooLong'
  return ''
}

// resolveRange 把范围解析为起止时间，日历范围按本地时区对齐，自定义范围无效时返回 null
export function resolveRange(
  view: Pick<UsageView, 'range' | 'from' | 'to'>,
  now: Date,
): { from: Date; to: Date } | null {
  const month = now.getMonth()
  const date = now.getDate()
  const midnight = (monthIndex: number, day: number) => new Date(now.getFullYear(), monthIndex, day)
  switch (view.range) {
    case 'today':
      return { from: midnight(month, date), to: now }
    case 'yesterday':
      return { from: midnight(month, date - 1), to: midnight(month, date) }
    case 'week':
      return { from: midnight(month, date - ((now.getDay() + 6) % 7)), to: now }
    case 'month':
      return { from: midnight(month, 1), to: now }
    case 'lastMonth':
      return { from: midnight(month - 1, 1), to: midnight(month, 1) }
    case 'custom': {
      const from = new Date(view.from)
      const to = new Date(view.to)
      return rangeError(from, to) === '' ? { from, to } : null
    }
    default:
      return { from: new Date(now.getTime() - relativeSpans[view.range]), to: now }
  }
}

// liveRange 判断范围终点是否跟随当前时间
export function liveRange(range: RangePreset) {
  return range !== 'yesterday' && range !== 'lastMonth' && range !== 'custom'
}

// filterParams 把各维度筛选以逗号分隔写入查询参数
export function filterParams(query: URLSearchParams, filters: UsageFilters) {
  for (const dimension of dimensions) {
    const values = filters[dimension] ?? []
    if (values.length > 0) query.set(dimension, values.join(','))
  }
  return query
}

// localInput 把时间格式化为 datetime-local 输入框的本地时间文本
export function localInput(date: Date) {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${String(date.getFullYear())}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// successRate 返回成功数除以排除客户端取消后的请求数，没有可计的请求时为 null
export function successRate(stats: UsageStats): number | null {
  const counted = stats.requests - stats.canceled
  return counted > 0 ? stats.succeeded / counted : null
}

// relativeChange 返回相对上一周期的变化比例，上一周期为 0 时为 null
export function relativeChange(current: number, previous: number): number | null {
  return previous > 0 ? (current - previous) / previous : null
}

// palette 是分类序列的颜色，按取值哈希分配，同一取值在各图中颜色一致
export const palette = [
  '#3a7bfc',
  '#1adb87',
  '#f58600',
  '#8e6cf0',
  '#22b8cf',
  '#e8618c',
  '#7cb342',
  '#c9a227',
  '#5c6bc0',
  '#26a69a',
]

// otherColor 是“其他”与已关闭序列的颜色
export const otherColor = '#a6a6a6'

// assignColors 为一组取值分配颜色，哈希冲突时顺延到未使用的颜色
export function assignColors(keys: string[]) {
  const result = new Map<string, string>()
  const used = new Set<number>()
  for (const key of keys) {
    let hash = 0
    for (const char of key) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
    let index = hash % palette.length
    for (let probe = 0; probe < palette.length && used.has(index); probe++)
      index = (index + 1) % palette.length
    used.add(index)
    result.set(key, palette[index] ?? otherColor)
  }
  return result
}

// protocolLabels 是公开协议的显示名
export const protocolLabels: Record<string, string> = {
  chat: 'OpenAI Chat',
  responses: 'OpenAI Responses',
  compact: 'Responses Compact',
  messages: 'Anthropic Messages',
  gemini: 'Gemini',
  completions: 'OpenAI Completions',
  images: 'Images',
  videos: 'Videos',
  speech: 'Speech',
  transcriptions: 'Transcriptions',
  translations: 'Translations',
  embeddings: 'Embeddings',
  moderations: 'Moderations',
  realtime: 'Realtime',
  live: 'Gemini Live',
}

// latencyTone 按耗时阈值返回色调类，首个输出与总耗时使用不同阈值
export function latencyTone(milliseconds: number, kind: 'duration' | 'first') {
  const [warn, slow] = kind === 'first' ? [10_000, 30_000] : [60_000, 180_000]
  if (milliseconds >= slow) return 'bg-error'
  if (milliseconds >= warn) return 'bg-warning'
  return 'bg-success'
}

// rateTone 按成功率阈值返回文字色调类
export function rateTone(rate: number | null) {
  if (rate === null) return 'text-foreground-subtle'
  if (rate >= 0.95) return 'text-success-text'
  if (rate >= 0.8) return 'text-warning-text'
  return 'text-error-text'
}

// durationSteps 是耗时坐标轴可选的刻度间隔，单位毫秒
export const durationSteps = [
  100, 200, 500, 1_000, 2_000, 5_000, 10_000, 15_000, 30_000, 60_000, 120_000, 300_000, 600_000,
  900_000, 1_800_000, 3_600_000,
]
