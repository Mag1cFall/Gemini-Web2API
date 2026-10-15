import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { bodyMessages } from '@/features/requests/messages'
import {
  AttemptList,
  DetailList,
  DetailSection,
  RequestBodyView,
} from '@/features/requests/RequestParts'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import { useUsageFormat } from '@/features/usage/format'
import { usageMessages } from '@/features/usage/messages'
import { latencyTone } from '@/features/usage/model'
import type { UsageRecord } from '@/features/usage/types'
import { useMessages } from '@/lib/i18n'

// RecordDrawer 是一条请求记录的右侧详情：概要、错误、上游尝试与正文
export function RecordDrawer({
  record,
  onClose,
}: {
  record: UsageRecord | null
  onClose: () => void
}) {
  const m = useMessages(usageMessages)
  const body = useMessages(bodyMessages)
  const f = useUsageFormat()
  if (!record) return null
  const summary = [
    { label: m.model, value: record.model || '—' },
    ...(record.provider_model && record.provider_model !== record.model
      ? [{ label: m.providerModel, value: record.provider_model }]
      : []),
    { label: m.account, value: f.label('account', record.account) },
    { label: m.protocol, value: f.label('protocol', record.protocol) },
    { label: m.path, value: record.path },
    { label: m.status, value: String(record.status) },
    { label: m.duration, value: f.duration(record.duration_ms) },
    { label: m.firstEvent, value: f.duration(record.first_event_ms) },
    { label: m.queue, value: f.duration(record.queue_ms) },
    { label: m.toolCalls, value: f.integer(record.tool_calls) },
    { label: m.input, value: f.integer(record.input_tokens) },
    { label: m.reasoning, value: f.integer(record.reasoning_tokens) },
    { label: m.reply, value: f.integer(record.reply_tokens) },
    { label: m.tokens, value: f.integer(record.total_tokens) },
    ...(record.response_id ? [{ label: m.responseId, value: record.response_id }] : []),
  ]
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
            <RequestStateBadge state={record.state} />
            {m.detailTitle}
          </span>
        }
        description={
          <span className="flex flex-wrap items-center gap-2">
            <code className="font-mono text-foreground">{record.id}</code>
            <CopyButton value={record.id} label={m.copyId} copiedLabel={m.copied} />
            <span className="tabular-nums">{f.dateTime(record.time, true)}</span>
            <span className="flex items-center gap-1">
              <span
                aria-hidden
                className={'size-2 rounded-full ' + latencyTone(record.duration_ms, 'duration')}
              />
              {f.duration(record.duration_ms)}
            </span>
          </span>
        }
      >
        <div className="flex flex-col gap-6">
          <DetailSection title={m.detailSummary}>
            <DetailList items={summary} />
          </DetailSection>
          {record.error && (
            <DetailSection title={m.detailError}>
              <pre className="rounded-sm bg-error-soft p-3 font-mono text-xs break-words whitespace-pre-wrap text-error-text">
                {record.error}
              </pre>
            </DetailSection>
          )}
          <DetailSection title={m.detailAttempts}>
            <AttemptList attempts={record.attempts} empty={m.detailAttemptsNone} />
          </DetailSection>
          <DetailSection title={body.title}>
            {record.has_body ? (
              <RequestBodyView id={record.id} />
            ) : (
              <p className="text-sm text-foreground-muted">{body.notCaptured}</p>
            )}
          </DetailSection>
        </div>
      </DialogContent>
    </Dialog>
  )
}
