import { ChevronRight } from 'lucide-react'
import { type ReactNode, useId, useState } from 'react'

import { Collapse } from '@/components/ui/collapse'
import type { LogRow } from '@/features/logs/filter'
import { LogField } from '@/features/logs/LogField'
import { logsMessages } from '@/features/logs/messages'
import { bodyMessages, finishMessages } from '@/features/requests/messages'
import { RequestBodyView } from '@/features/requests/RequestParts'
import type { RequestState } from '@/features/requests/types'
import { protocolLabels } from '@/features/usage/model'
import { formatDuration, formatNumber } from '@/lib/format'
import { useI18n, useMessages } from '@/lib/i18n'

// stateClass 是请求状态标记的颜色
const stateClass: Record<RequestState, string> = {
  running: 'border-primary-border text-primary',
  completed: 'border-success text-success-text bg-success-soft',
  tool_calls: 'border-success text-success-text bg-success-soft',
  failed: 'border-error text-error-text bg-error-soft',
  cancelled: 'border-warning text-warning-text bg-warning-soft',
}

// timeFormat 是时间线的 24 小时制时分秒
const timeFormat = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

// Disclosure 是可展开的一节，首次展开后才渲染内容
function Disclosure({
  summary,
  nested = false,
  children,
}: {
  summary: ReactNode
  nested?: boolean
  children: () => ReactNode
}) {
  const [open, setOpen] = useState(false)
  const [rendered, setRendered] = useState(false)
  const id = useId()
  return (
    <div className={nested ? 'mt-1' : ''}>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => {
          setOpen(!open)
          setRendered(true)
        }}
        className="max-w-full text-left font-sans text-sm text-foreground-muted hover:text-foreground pointer-coarse:min-h-touch"
      >
        <ChevronRight
          size={12}
          aria-hidden
          className={
            'mr-1 inline-block align-[-1px] transition-transform duration-(--duration-base) ' +
            (open ? 'rotate-90' : '')
          }
        />
        {summary}
      </button>
      <Collapse open={open} id={id}>
        {rendered && <div className="flex flex-col gap-2 pt-2 pb-1 font-sans">{children()}</div>}
      </Collapse>
    </div>
  )
}

// RequestLogCard 把一个请求的生命周期日志显示为一组行：来源、状态、模型、耗时与用量，展开后为详情、时间线、原始 JSON 与正文
export function RequestLogCard({ row }: { row: LogRow }) {
  const m = useMessages(logsMessages)
  const body = useMessages(bodyMessages)
  const finish: Record<string, string> = useMessages(finishMessages)
  const { locale } = useI18n()
  const request = row.entry.request
  if (!request) return null
  const number = (value: number, digits = 0) =>
    formatNumber(Math.round(value * 10 ** digits) / 10 ** digits, locale)
  const usage = request.usage
  const seconds = request.duration_ms / 1000
  const finishLabel = request.finish_reason
    ? (finish[request.finish_reason] ?? request.finish_reason)
    : ''
  const details = [
    { label: m.requestId, value: request.id },
    { label: m.endpoint, value: `${request.method} ${request.path}` },
    ...(request.account ? [{ label: m.account, value: request.account }] : []),
    ...(request.protocol
      ? [{ label: m.protocol, value: protocolLabels[request.protocol] ?? request.protocol }]
      : []),
    ...(usage
      ? [
          { label: m.inputTokens, value: `${number(usage.input_tokens)} tokens` },
          { label: m.totalTokens, value: `${number(usage.total_tokens)} tokens` },
        ]
      : []),
    ...(request.messages ? [{ label: m.inputMessages, value: number(request.messages) }] : []),
    ...(request.attempts?.length
      ? [{ label: m.attempts, value: number(request.attempts.length) }]
      : []),
    ...(finishLabel ? [{ label: m.finishReason, value: finishLabel }] : []),
  ]
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-1">
        <span className="text-foreground-muted">{row.entry.source}</span>
        <span
          className={
            'rounded-sm border px-1 text-xs leading-[16px] tabular-nums ' +
            stateClass[request.state]
          }
        >
          {request.status ?? m.running}
        </span>
        <span className="min-w-0 font-medium wrap-anywhere text-foreground">
          {request.model ?? `${request.method} ${request.path}`}
        </span>
        <LogField value={formatDuration(request.duration_ms, locale)} />
        {request.tool_calls ? (
          <LogField label={m.tools} value={number(request.tool_calls)} />
        ) : null}
      </div>
      {usage && (
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 tabular-nums">
          <LogField label={m.reasoning} value={number(usage.reasoning_tokens)} />
          <LogField label={m.reply} value={number(usage.reply_tokens)} />
          <LogField label={m.outputTotal} value={`${number(usage.output_tokens)} tokens`} />
          {seconds > 0 && (
            <LogField
              label={m.averageSpeed}
              value={`${number(usage.output_tokens / seconds, 1)} tokens/s`}
            />
          )}
          {request.first_event_ms !== undefined && (
            <LogField label={m.firstToken} value={formatDuration(request.first_event_ms, locale)} />
          )}
        </div>
      )}
      {request.error && (
        <p className="font-sans text-sm break-words whitespace-pre-wrap text-error-text">
          {request.error}
        </p>
      )}
      <Disclosure
        summary={
          <>
            {m.details}{' '}
            <span className="wrap-anywhere text-foreground-subtle">
              {request.method} · HTTP {request.status ?? '…'}
              {request.account ? ' · ' + request.account : ''}
            </span>
          </>
        }
      >
        {() => (
          <>
            <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
              {details.map((item) => (
                <div key={item.label} className="contents">
                  <dt className="text-foreground-muted">{item.label}</dt>
                  <dd className="break-all">{item.value}</dd>
                </div>
              ))}
            </dl>
            <ol className="flex flex-col gap-1 border-l border-border pl-3 font-mono text-xs">
              {row.events.map((event, index) => (
                <li key={index} className="flex items-baseline gap-3">
                  <time dateTime={event.time} className="shrink-0 text-foreground-subtle">
                    {timeFormat.format(new Date(event.time))}
                  </time>
                  <span className="min-w-0 break-words whitespace-pre-wrap">{event.message}</span>
                </li>
              ))}
            </ol>
            <Disclosure nested summary="JSON">
              {() => (
                <pre className="max-h-[320px] overflow-auto rounded-sm border border-border bg-panel p-3 font-mono text-xs">
                  {JSON.stringify(row.entry, null, 2)}
                </pre>
              )}
            </Disclosure>
            {request.state !== 'running' && (
              <Disclosure nested summary={body.title}>
                {() => <RequestBodyView id={request.id} />}
              </Disclosure>
            )}
          </>
        )}
      </Disclosure>
    </div>
  )
}
