import { Activity, History, Snowflake } from 'lucide-react'
import { useState } from 'react'
import { useSearchParams } from 'react-router'

import { Button } from '@/components/ui/button'
import { Card, CardEmpty, CardHeader, CountBadge, PageHeader } from '@/components/ui/page'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import {
  accountsMessages,
  cooldownReason,
  cooldownReasonMessages,
} from '@/features/accounts/messages'
import { useAccountLabel, useClearCooldown } from '@/features/accounts/queries'
import { useCooldowns } from '@/features/models/queries'
import { DateTimeText, Elapsed, RelativeText } from '@/features/requests/Elapsed'
import { requestsMessages } from '@/features/requests/messages'
import {
  useCancelRequest,
  useFinished,
  useLedgerRecord,
  useRunning,
} from '@/features/requests/queries'
import { RequestDetailSheet } from '@/features/requests/RequestDetailSheet'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import type { RequestInfo } from '@/features/requests/types'
import { protocolLabels } from '@/features/usage/model'
import { RecordDrawer } from '@/features/usage/RecordDrawer'
import { ApiError } from '@/lib/api'
import { formatBytes, formatDuration, formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// Truncated 是单行截断的表格文字，被截断时悬停显示全文
function Truncated({ text }: { text: string }) {
  return (
    <Tooltip overflow label={text}>
      <span className="block truncate">{text}</span>
    </Tooltip>
  )
}

// recentLimit 是最近结束列表显示的请求数
const recentLimit = 20

// RequestsPage 显示进行中的请求与取消、最近结束的请求与账号冷却及其清除，数据来自事件流，地址带 id 时打开请求账本中该请求的记录；每秒只重新渲染计时文字
export function RequestsPage() {
  const m = useMessages(requestsMessages)
  const rm = useMessages(cooldownReasonMessages)
  const am = useMessages(accountsMessages)
  const { language } = useI18n()
  const clearCooldown = useClearCooldown()
  const accountLabel = useAccountLabel()
  const running = useRunning()
  const finished = useFinished(recentLimit)
  const cooldowns = useCooldowns().data?.cooldowns ?? []
  const cancel = useCancelRequest()
  const [params, setParams] = useSearchParams()
  const linked = useLedgerRecord(params.get('id'))
  const [selected, setSelected] = useState<RequestInfo | null>(null)
  const live = selected
    ? (running.find((item) => item.id === selected.id) ??
      finished.find((item) => item.id === selected.id) ??
      selected)
    : null
  const protocol = (request: RequestInfo) =>
    request.protocol ? (protocolLabels[request.protocol] ?? request.protocol) : '—'

  // cancelRequest 取消一个进行中的请求，结果以轻提示告知
  const cancelRequest = (id: string) => {
    cancel.mutate(id, {
      onSuccess: () => {
        toast({ tone: 'success', title: fill(m.cancelSent, { id }) })
      },
      onError: (error) => {
        toast(
          error instanceof ApiError && error.status === 404
            ? { tone: 'error', title: m.cancelGone }
            : { tone: 'error', title: m.cancelFailed, detail: error.message },
        )
      },
    })
  }

  // openButton 是打开请求详情的模型名按钮
  const openButton = (request: RequestInfo) => {
    const label = request.model ?? `${request.method} ${request.path}`
    return (
      <Tooltip overflow label={label}>
        <button
          type="button"
          aria-label={fill(m.openDetail, { id: request.id })}
          onClick={() => {
            setSelected(request)
          }}
          className="max-w-full truncate text-left font-mono text-primary hover:text-primary-hover hover:underline"
        >
          {label}
        </button>
      </Tooltip>
    )
  }

  // accountText 是账号列的文字：首页登录邮箱，未分配账号时为提示
  const accountText = (id: string | undefined) => (id ? accountLabel(id) : m.unassigned)

  return (
    <>
      <PageHeader title={m.title} description={m.description} />

      <Card className="@container">
        <CardHeader
          title={m.active}
          actions={<CountBadge>{formatNumber(running.length, language)}</CountBadge>}
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
                  <Td className="max-w-cell @max-[480px]:hidden">
                    <Truncated text={accountText(request.account)} />
                  </Td>
                  <Td className="whitespace-nowrap @max-[480px]:hidden">{protocol(request)}</Td>
                  <Td className="max-w-cell text-foreground-muted @max-[760px]:hidden">
                    <Truncated text={request.stage ?? '—'} />
                  </Td>
                  <Td className="whitespace-nowrap text-foreground-muted @max-[480px]:hidden">
                    <DateTimeText date={new Date(request.started_at)} />
                  </Td>
                  <Td className="text-right whitespace-nowrap">
                    <Elapsed request={request} />
                  </Td>
                  <Td className="text-right whitespace-nowrap @max-[480px]:hidden">
                    {request.first_event_ms
                      ? formatDuration(request.first_event_ms, language)
                      : '—'}
                  </Td>
                  <Td className="text-right whitespace-nowrap @max-[960px]:hidden">
                    {formatBytes(request.upstream_bytes ?? 0, language)}
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
          actions={<CountBadge>{formatNumber(finished.length, language)}</CountBadge>}
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
                    <DateTimeText
                      date={new Date(Date.parse(request.started_at) + request.duration_ms)}
                    />
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
                  <Td className="max-w-cell">
                    <Truncated text={accountText(request.account)} />
                  </Td>
                  <Td className="whitespace-nowrap">{protocol(request)}</Td>
                  <Td className="text-right whitespace-nowrap">
                    {formatDuration(request.duration_ms, language)}
                  </Td>
                  <Td className="text-right">
                    {formatNumber(request.usage?.total_tokens ?? 0, language)}
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
          actions={<CountBadge>{formatNumber(cooldowns.length, language)}</CountBadge>}
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
                <Th>
                  <span className="sr-only">{am.clearCooldown}</span>
                </Th>
              </tr>
            </thead>
            <tbody>
              {cooldowns.map((cooldown) => (
                <tr key={cooldown.account_id + '\n' + cooldown.model}>
                  <Td className="max-w-cell">
                    <Truncated text={accountLabel(cooldown.account_id)} />
                  </Td>
                  <Td className="font-mono whitespace-nowrap">
                    {cooldown.model || m.wholeAccount}
                  </Td>
                  <Td className="whitespace-nowrap text-foreground-muted">
                    {cooldown.reason ? cooldownReason(rm, cooldown.reason) : '—'}
                  </Td>
                  <Td className="whitespace-nowrap">
                    <DateTimeText date={new Date(cooldown.until)} />
                    <span className="ml-2 text-foreground-muted">
                      <RelativeText date={new Date(cooldown.until)} />
                    </span>
                  </Td>
                  <Td className="text-right whitespace-nowrap">
                    <Button
                      size="sm"
                      loading={
                        clearCooldown.isPending &&
                        clearCooldown.variables.account === cooldown.account_id &&
                        clearCooldown.variables.scope === (cooldown.model || '*')
                      }
                      onClick={() => {
                        clearCooldown.mutate({
                          account: cooldown.account_id,
                          scope: cooldown.model || '*',
                        })
                      }}
                    >
                      {am.clearCooldown}
                    </Button>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <RequestDetailSheet
        request={live}
        cancelling={cancel.isPending && cancel.variables === live?.id}
        onCancel={cancelRequest}
        onClose={() => {
          setSelected(null)
        }}
      />
      <RecordDrawer
        record={linked.data ?? null}
        onClose={() => {
          setParams({}, { replace: true })
        }}
      />
    </>
  )
}
