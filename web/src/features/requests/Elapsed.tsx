import type { RequestInfo } from '@/features/requests/types'
import { formatDateTime, formatDuration, formatRelative } from '@/lib/format'
import { useI18n } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// runningElapsed 是进行中请求从开始到 now 的耗时，不小于服务端已记录的耗时
export function runningElapsed(request: RequestInfo, now: Date) {
  return Math.max(request.duration_ms, now.getTime() - Date.parse(request.started_at))
}

// RunningElapsed 是进行中请求的耗时文字，每秒更新
function RunningElapsed({ request }: { request: RequestInfo }) {
  const { locale } = useI18n()
  const now = useNow(1000)
  return formatDuration(runningElapsed(request, now), locale)
}

// Elapsed 是请求的耗时文字，进行中的请求每秒更新，只有这段文字重新渲染
export function Elapsed({ request }: { request: RequestInfo }) {
  const { locale } = useI18n()
  return request.state === 'running' ? (
    <RunningElapsed request={request} />
  ) : (
    formatDuration(request.duration_ms, locale)
  )
}

// DateTimeText 是到秒的本地日期时间，今天的只显示时间，每分钟核对一次是否跨天
export function DateTimeText({ date }: { date: Date }) {
  const { locale } = useI18n()
  const now = useNow(60_000)
  return formatDateTime(date, now, locale)
}

// RelativeText 是距离现在的相对时间，每秒更新
export function RelativeText({ date }: { date: Date }) {
  const { locale } = useI18n()
  const now = useNow(1000)
  return formatRelative(date, now, locale)
}
