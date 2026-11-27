import { ClampText } from '@/components/ui/clamp'
import type { LogFieldEntry } from '@/features/logs/types'
import { serverText } from '@/lib/i18n'

// LogField 是日志中的一个键值，键为浅色文字，值为等宽文字
export function LogField({ label, value }: { label?: string | undefined; value: string }) {
  return (
    <span className="min-w-0 wrap-anywhere whitespace-pre-wrap">
      {label && <span className="text-foreground-subtle">{label} </span>}
      <span className="font-mono text-sm text-foreground">{value}</span>
    </span>
  )
}

// LogMessage 把日志显示为来源、消息与一组键值，只在键值之间换行，超过三行时折叠
export function LogMessage({
  source,
  message,
  fields = [],
}: {
  source: string
  message: string
  fields?: LogFieldEntry[] | undefined
}) {
  return (
    <ClampText className="flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-1">
      <span className="text-foreground-muted">{source}</span>
      <span className="min-w-0 wrap-anywhere whitespace-pre-wrap text-foreground">
        {serverText(message)}
      </span>
      {fields.map((field, index) => (
        <LogField
          key={index}
          label={field.key ? serverText(field.key) : undefined}
          value={serverText(field.value)}
        />
      ))}
    </ClampText>
  )
}
