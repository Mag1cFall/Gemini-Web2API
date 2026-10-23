import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { useAccountLabel } from '@/features/accounts/queries'
import { bodyMessages, finishMessages, requestsMessages } from '@/features/requests/messages'
import {
  AttemptList,
  DetailList,
  DetailSection,
  RequestBodyView,
} from '@/features/requests/RequestParts'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import type { RequestInfo } from '@/features/requests/types'
import { protocolLabels } from '@/features/usage/model'
import { formatBytes, formatDuration, formatFullDateTime, formatNumber } from '@/lib/format'
import { useI18n, useMessages } from '@/lib/i18n'

// RequestDetailSheet 是一个请求的右侧详情：概要、用量、错误、上游尝试与正文，进行中的请求可在此取消
export function RequestDetailSheet({
  request,
  durationMs,
  cancelling,
  onCancel,
  onClose,
}: {
  request: RequestInfo | null
  durationMs: number
  cancelling: boolean
  onCancel: (id: string) => void
  onClose: () => void
}) {
  const m = useMessages(requestsMessages)
  const body = useMessages(bodyMessages)
  const finish: Record<string, string> = useMessages(finishMessages)
  const { locale } = useI18n()
  const accountLabel = useAccountLabel()
  if (!request) return null
  const running = request.state === 'running'
  const duration = (value: number | undefined) => (value ? formatDuration(value, locale) : '—')
  const summary = [
    {
      label: m.protocol,
      value: request.protocol ? (protocolLabels[request.protocol] ?? request.protocol) : '—',
    },
    { label: m.endpoint, value: `${request.method} ${request.path}` },
    { label: m.account, value: request.account ? accountLabel(request.account) : m.unassigned },
    ...(request.provider_model && request.provider_model !== request.model
      ? [{ label: m.providerModel, value: request.provider_model }]
      : []),
    { label: m.stream, value: request.stream ? m.yes : m.no },
    { label: m.status, value: request.status ? String(request.status) : '—' },
    { label: m.duration, value: duration(durationMs) },
    { label: m.queue, value: duration(request.queue_ms) },
    { label: m.firstEvent, value: duration(request.first_event_ms) },
    ...(request.stage ? [{ label: m.stage, value: request.stage }] : []),
    { label: m.messages, value: formatNumber(request.messages ?? 0, locale) },
    ...(request.media ? [{ label: m.media, value: formatNumber(request.media, locale) }] : []),
    ...(request.tools ? [{ label: m.tools, value: formatNumber(request.tools, locale) }] : []),
    ...(request.tool_calls
      ? [{ label: m.toolCalls, value: formatNumber(request.tool_calls, locale) }]
      : []),
    ...(request.finish_reason
      ? [{ label: m.finishReason, value: finish[request.finish_reason] ?? request.finish_reason }]
      : []),
    { label: m.upstreamBytes, value: formatBytes(request.upstream_bytes ?? 0, locale) },
    ...(request.heartbeats
      ? [{ label: m.heartbeats, value: formatNumber(request.heartbeats, locale) }]
      : []),
    ...(request.client_ip ? [{ label: m.clientIp, value: request.client_ip }] : []),
    ...(request.response_id ? [{ label: m.responseId, value: request.response_id }] : []),
    ...(request.conversation_id
      ? [{ label: m.conversationId, value: request.conversation_id }]
      : []),
  ]
  const usage = request.usage
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        layout="sheet"
        closeLabel={m.close}
        title={
          <span className="flex flex-wrap items-center gap-2">
            <RequestStateBadge state={request.state} />
            <span className="font-mono">
              {request.model ?? `${request.method} ${request.path}`}
            </span>
          </span>
        }
        description={
          <span className="flex flex-wrap items-center gap-2">
            <code className="font-mono text-foreground">{request.id}</code>
            <CopyButton value={request.id} label={m.copyId} copiedLabel={m.copied} />
            <span className="tabular-nums">
              {formatFullDateTime(new Date(request.started_at), locale)}
            </span>
          </span>
        }
        footer={
          running && (
            <Button
              variant="destructive"
              loading={cancelling}
              onClick={() => {
                onCancel(request.id)
              }}
            >
              {m.cancel}
            </Button>
          )
        }
      >
        <div className="flex flex-col gap-6">
          <DetailSection title={m.summary}>
            <DetailList items={summary} />
          </DetailSection>
          {usage && (
            <DetailSection title={m.tokens}>
              <DetailList
                items={[
                  { label: m.input, value: formatNumber(usage.input_tokens, locale) },
                  { label: m.reasoning, value: formatNumber(usage.reasoning_tokens, locale) },
                  { label: m.reply, value: formatNumber(usage.reply_tokens, locale) },
                  { label: m.total, value: formatNumber(usage.total_tokens, locale) },
                ]}
              />
            </DetailSection>
          )}
          {request.error && (
            <DetailSection title={m.error}>
              <pre className="rounded-sm bg-error-soft p-3 font-mono text-xs break-words whitespace-pre-wrap text-error-text">
                {request.error}
              </pre>
            </DetailSection>
          )}
          <DetailSection title={m.attemptsTitle}>
            <AttemptList attempts={request.attempts ?? []} empty={m.attemptsNone} />
          </DetailSection>
          {!running && (
            <DetailSection title={body.title}>
              <RequestBodyView id={request.id} />
            </DetailSection>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
