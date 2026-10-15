// fieldPattern 匹配日志正文中“键=值”形式的一段，键不含空白、斜杠与冒号
const fieldPattern = /^([^\s=/:]+)=([\s\S]*)$/

// LogField 是日志中的一个键值，键为浅色文字，值为等宽文字
export function LogField({ label, value }: { label?: string; value: string }) {
  return (
    <span className="min-w-0 wrap-anywhere whitespace-pre-wrap">
      {label && <span className="text-foreground-subtle">{label} </span>}
      <span className="font-mono text-sm text-foreground">{value}</span>
    </span>
  )
}

// LogMessage 把“阶段 | 键=值”格式的日志正文显示为来源、阶段与一组键值，只在键值之间换行
export function LogMessage({ source, message }: { source: string; message: string }) {
  const [stage, ...fields] = message.split(' | ')
  return (
    <div className="flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-1">
      <span className="text-foreground-muted">{source}</span>
      <span className="min-w-0 wrap-anywhere whitespace-pre-wrap text-foreground">{stage}</span>
      {fields.map((field, index) => {
        const match = fieldPattern.exec(field)
        return match ? (
          <LogField key={index} label={match[1] ?? ''} value={match[2] ?? ''} />
        ) : (
          <LogField key={index} value={field} />
        )
      })}
    </div>
  )
}
