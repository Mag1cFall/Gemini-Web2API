import { Activity, History, Snowflake } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardEmpty, CardHeader, CountBadge, PageHeader } from '@/components/ui/page'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { cooldownReason, cooldownReasonMessages } from '@/features/accounts/messages'
import { useCooldowns } from '@/features/models/queries'
import { requestsMessages } from '@/features/requests/messages'
import { useCancelRequest, useFinished, useRunning } from '@/features/requests/queries'
import { RequestDetailSheet } from '@/features/requests/RequestDetailSheet'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import type { RequestInfo } from '@/features/requests/types'
import { protocolLabels } from '@/features/usage/model'
import { ApiError } from '@/lib/api'
import {
  formatBytes,
  formatDateTime,
  formatDuration,
  formatNumber,
  formatRelative,
} from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// recentLimit 是最近结束列表显示的请求数
const recentLimit = 20

// RequestsPage 显示进行中的请求与取消、最近结束的请求与账号冷却，数据来自事件流
export function RequestsPage() {
  const m = useMessages(requestsMessages)
  const rm = useMessages(cooldownReasonMessages)
  const { locale } = useI18n()
  const now = useNow(1000)
  const running = useRunning()
  const finished = useFinished(recentLimit)
  const cooldowns = useCooldowns().data?.cooldowns ?? []
  const cancel = useCancelRequest()
  const [selected, setSelected] = useState<RequestInfo | null>(null)
  const live = selected
    ? (running.find((item) => item.id === selected.id) ??
      finished.find((item) => item.id === selected.id) ??
      selected)
    : null
  const elapsed = (request: RequestInfo) =>
    request.state === 'running'
      ? Math.max(request.duration_ms, now.getTime() - Date.parse(request.started_at))
      : request.duration_ms
  const protocol = (request: RequestInfo) =>
    request.protocol ? (protocolLabels[request.protocol] ?? request.protocol) : '—'

  // cancelRequest 取消一个进行中的请求，结果以轻提示告知
  const cancelRequest = (id: string) => {
    cancel.mutate(id, {
      onSuccess: () => {
        toast({ tone: 'success', title: fill(m.cancelSent, { id }) })
      },
      onError: (error) => {
        toast({
          tone: 'error',
          title:
            error instanceof ApiError && error.status === 404
              ? m.cancelGone
              : fill(m.cancelFailed, { message: error.message }),
        })
      },
    })
  }

  // openButton 是打开请求详情的模型名按钮
  const openButton = (request: RequestInfo) => (
    <button
      type="button"
      aria-label={fill(m.openDetail, { id: request.id })}
      onClick={() => {
        setSelected(request)
      }}
      className="max-w-full truncate text-left font-mono text-primary hover:text-primary-hover hover:underline"
    >
      {request.model ?? `${request.method} ${request.path}`}
    </button>
  )

  return (
    <>
      <PageHeader title={m.title} description={m.description} />

      <Card className="@container">
        <CardHeader
          title={m.active}
          actions={<CountBadge>{formatNumber(running.length, locale)}</CountBadge>}
        />
        {running.length === 0 ? (
          <CardEmpty icon={<Activity size={20} aria-hidden />}>{m.noActive}</CardEmpty>
        ) : (
          <Table label={m.active}>
            <thead>
              <tr>
                <Th>{m.model}</Th>
                <Th className="@max-[480px]:hidden">{m.account}</Th>
                <Th className="@max-[480px]:hidden">{m.protocol}</Th>
                <Th className="@max-[760px]:hidden">{m.stage}</Th>
                <Th className="@max-[480px]:hidden">{m.started}</Th>
                <Th className="text-right">{m.duration}</Th>
                <Th className="text-right @max-[480px]:hidden">{m.firstEvent}</Th>
                <Th className="text-right @max-[960px]:hidden">{m.upstreamBytes}</Th>
                <Th className="text-right @max-[960px]:hidden">{m.attempts}</Th>
                <Th className="sticky right-0 w-action">
                  <span className="sr-only">{m.cancel}</span>
                </Th>
              </tr>
            </thead>
            <tbody>
              {running.map((request) => (
                <tr key={request.id} className="hover:bg-hover">
                  <Td className="max-w-cell">{openButton(request)}</Td>
                  <Td className="max-w-cell truncate @max-[480px]:hidden">
                    {request.account ?? m.unassigned}
                  </Td>
                  <Td className="whitespace-nowrap @max-[480px]:hidden">{protocol(request)}</Td>
                  <Td className="max-w-cell truncate text-foreground-muted @max-[760px]:hidden">
                    {request.stage ?? '—'}
                  </Td>
                  <Td className="whitespace-nowrap text-foreground-muted @max-[480px]:hidden">
                    {formatDateTime(new Date(request.started_at), now, locale)}
                  </Td>
                  <Td className="text-right whitespace-nowrap">
                    {formatDuration(elapsed(request), locale)}
                  </Td>
                  <Td className="text-right whitespace-nowrap @max-[480px]:hidden">
                    {request.first_event_ms ? formatDuration(request.first_event_ms, locale) : '—'}
                  </Td>
                  <Td className="text-right whitespace-nowrap @max-[960px]:hidden">
                    {formatBytes(request.upstream_bytes ?? 0, locale)}
                  </Td>
                  <Td className="text-right @max-[960px]:hidden">
                    {request.attempts?.length ?? 0}
                  </Td>
                  <Td className="sticky right-0 w-action bg-surface text-right">
                    <Button
                      variant="outline"
                      size="sm"
                      aria-label={fill(m.cancelNamed, { id: request.id })}
                      loading={cancel.isPending && cancel.variables === request.id}
                      onClick={() => {
                        cancelRequest(request.id)
                      }}
                    >
                      {m.cancel}
                    </Button>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <Card>
        <CardHeader
          title={m.recent}
          description={fill(m.recentHelp, { count: recentLimit })}
          actions={<CountBadge>{formatNumber(finished.length, locale)}</CountBadge>}
        />
        {finished.length === 0 ? (
          <CardEmpty icon={<History size={20} aria-hidden />}>{m.noRecent}</CardEmpty>
        ) : (
          <Table label={m.recent}>
            <thead>
              <tr>
                <Th>{m.ended}</Th>
                <Th>{m.state}</Th>
                <Th>{m.model}</Th>
                <Th>{m.account}</Th>
                <Th>{m.protocol}</Th>
                <Th className="text-right">{m.duration}</Th>
                <Th className="text-right">{m.tokens}</Th>
              </tr>
            </thead>
            <tbody>
              {finished.map((request) => (
                <tr
                  key={request.id}
                  className={request.state === 'failed' ? 'bg-error-soft/40' : 'hover:bg-hover'}
                >
                  <Td className="whitespace-nowrap text-foreground-muted">
                    {formatDateTime(
                      new Date(Date.parse(request.started_at) + request.duration_ms),
                      now,
                      locale,
                    )}
                  </Td>
                  <Td className="whitespace-nowrap">
                    <span className="flex items-center gap-2">
                      <RequestStateBadge state={request.state} />
                      <span className="text-foreground-muted tabular-nums">
                        {request.status ?? ''}
                      </span>
                    </span>
                  </Td>
                  <Td className="max-w-cell">{openButton(request)}</Td>
                  <Td className="max-w-cell truncate">{request.account ?? m.unassigned}</Td>
                  <Td className="whitespace-nowrap">{protocol(request)}</Td>
                  <Td className="text-right whitespace-nowrap">
                    {formatDuration(request.duration_ms, locale)}
                  </Td>
                  <Td className="text-right">
                    {formatNumber(request.usage?.total_tokens ?? 0, locale)}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <Card>
        <CardHeader
          title={m.cooldowns}
          actions={<CountBadge>{formatNumber(cooldowns.length, locale)}</CountBadge>}
        />
        {cooldowns.length === 0 ? (
          <CardEmpty icon={<Snowflake size={20} aria-hidden />}>{m.noCooldowns}</CardEmpty>
        ) : (
          <Table label={m.cooldowns}>
            <thead>
              <tr>
                <Th>{m.account}</Th>
                <Th>{m.model}</Th>
                <Th>{m.reason}</Th>
                <Th>{m.until}</Th>
              </tr>
            </thead>
            <tbody>
              {cooldowns.map((cooldown) => (
                <tr key={cooldown.account_id + '\n' + cooldown.model}>
                  <Td className="max-w-cell truncate">{cooldown.account_id}</Td>
                  <Td className="font-mono whitespace-nowrap">
                    {cooldown.model || m.wholeAccount}
                  </Td>
                  <Td className="whitespace-nowrap text-foreground-muted">
                    {cooldown.reason ? cooldownReason(rm, cooldown.reason) : '—'}
                  </Td>
                  <Td className="whitespace-nowrap">
                    {formatDateTime(new Date(cooldown.until), now, locale)}
                    <span className="ml-2 text-foreground-muted">
                      {formatRelative(new Date(cooldown.until), now, locale)}
                    </span>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <RequestDetailSheet
        request={live}
        durationMs={live ? elapsed(live) : 0}
        cancelling={cancel.isPending && cancel.variables === live?.id}
        onCancel={cancelRequest}
        onClose={() => {
          setSelected(null)
        }}
      />
    </>
  )
}
