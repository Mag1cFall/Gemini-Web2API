import { ChevronRight } from 'lucide-react'
import { type ReactNode, useId, useState } from 'react'

import { ClampText } from '@/components/ui/clamp'
import { Collapse } from '@/components/ui/collapse'
import { type LogRow, logText } from '@/features/logs/filter'
import { timeFormat } from '@/features/logs/format'
import { LogField } from '@/features/logs/LogField'
import { logsMessages } from '@/features/logs/messages'
import { runningElapsed } from '@/features/requests/Elapsed'
import { bodyMessages, finishMessages } from '@/features/requests/messages'
import { RequestBodyView } from '@/features/requests/RequestParts'
import type { Attempt, RequestInfo, RequestState } from '@/features/requests/types'
import { protocolLabels } from '@/features/usage/model'
import { formatDuration, formatNumber } from '@/lib/format'
import { fill, serverText, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// stateClass 是请求状态标记的颜色
const stateClass: Record<RequestState, string> = {
  running: 'border-primary-border text-primary',
  completed: 'border-success text-success-text bg-success-soft',
  tool_calls: 'border-success text-success-text bg-success-soft',
  failed: 'border-error text-error-text bg-error-soft',
  cancelled: 'border-warning text-warning-text bg-warning-soft',
}

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

// RunningElapsed 是进行中请求从开始到现在的耗时，每秒更新
function RunningElapsed({ request }: { request: RequestInfo }) {
  const { language } = useI18n()
  const now = useNow(1000)
  const elapsed = runningElapsed(request, now)
  return <LogField value={formatDuration(Math.floor(elapsed / 1000) * 1000, language)} />
}

// AttemptList 按顺序列出被换下的账号与原因，备用请求带标记
function AttemptList({ title, attempts }: { title: string; attempts: Attempt[] }) {
  const m = useMessages(logsMessages)
  return (
    <div className="flex flex-col gap-[2px] font-sans text-sm">
      <span className="text-xs text-foreground-muted">{title}</span>
      <ol className="flex flex-col gap-[2px]">
        {attempts.map((attempt, index) => (
          <li key={index}>
            <ClampText className="wrap-anywhere text-foreground">
              {fill(m.attemptLine, {
                account: attempt.account,
                reason: attempt.superseded
                  ? m.attemptSuperseded
                  : attempt.error
                    ? serverText(attempt.error)
                    : attempt.status
                      ? 'HTTP ' + String(attempt.status)
                      : m.attemptNoResponse,
              })}
              {attempt.hedge && (
                <span className="ml-2 rounded-sm bg-muted px-1 text-xs text-foreground-muted">
                  {m.hedge}
                </span>
              )}
            </ClampText>
          </li>
        ))}
      </ol>
    </div>
  )
}

// RequestLogCard 把一个请求的生命周期日志显示为一组行：来源、状态、模型、耗时与用量，换号时按顺序列出账号与原因，展开后为详情、时间线、原始 JSON 与正文；account 为日志页筛选的账号，卡片显示该账号参与的尝试
export function RequestLogCard({ row, account }: { row: LogRow; account: string }) {
  const m = useMessages(logsMessages)
  const body = useMessages(bodyMessages)
  const finish: Record<string, string> = useMessages(finishMessages)
  const { language } = useI18n()
  const request = row.entry.request
  if (!request) return null
  const number = (value: number, digits = 0) =>
    formatNumber(Math.round(value * 10 ** digits) / 10 ** digits, language)
  const usage = request.usage
  const seconds =
    request.first_event_ms === undefined ? 0 : (request.duration_ms - request.first_event_ms) / 1000
  const attempts = request.attempts ?? []
  const filtered =
    account && request.account !== account
      ? attempts.filter((attempt) => attempt.account === account)
      : undefined
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
        <span className="text-foreground-muted">{account || row.entry.source}</span>
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
        {request.state === 'running' ? (
          <RunningElapsed request={request} />
        ) : (
          <LogField value={formatDuration(request.duration_ms, language)} />
        )}
        {request.state === 'running' && request.stage && (
          <LogField label={m.stage} value={serverText(request.stage)} />
        )}
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
            <LogField
              label={m.firstToken}
              value={formatDuration(request.first_event_ms, language)}
            />
          )}
        </div>
      )}
      {filtered ? (
        <AttemptList title={fill(m.accountAttempts, { account })} attempts={filtered} />
      ) : (
        attempts.length > 0 && <AttemptList title={m.switches} attempts={attempts} />
      )}
      {request.error && (
        <ClampText className="font-sans text-sm break-words whitespace-pre-wrap text-error-text">
          {serverText(request.error)}
        </ClampText>
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
                  <span className="min-w-0 break-words whitespace-pre-wrap">
                    {logText(event.message, event.fields)}
                  </span>
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
