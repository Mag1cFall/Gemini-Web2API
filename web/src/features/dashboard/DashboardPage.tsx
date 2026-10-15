import { CircleCheck, Eye, EyeOff, MessageSquare, UserPlus } from 'lucide-react'
import { type ReactNode, useState } from 'react'
import { Link } from 'react-router'

import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { Card, CardHeader, PageHeader } from '@/components/ui/page'
import { Loading } from '@/components/ui/states'
import { Tooltip } from '@/components/ui/tooltip'
import { dashboardMessages } from '@/features/dashboard/messages'
import { useModels } from '@/features/models/queries'
import { useStatus } from '@/features/service/queries'
import { firstApiKey } from '@/features/settings/apiKeys'
import { useConfig } from '@/features/settings/queries'
import { useUsage } from '@/features/usage/queries'
import { formatCompact, formatDuration, formatNumber, formatPercent, formatUsd } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// copiedKey 是本地存储中记录已复制接入地址或 API Key 的键
const copiedKey = 'gemini-web2api.copied-endpoint'

// ledgerSpan 是判断是否已有请求记录时查询的时间范围
const ledgerSpan = 90 * 24 * 3_600_000

// readCopied 读取本浏览器是否复制过接入地址或 API Key
function readCopied() {
  try {
    return localStorage.getItem(copiedKey) === '1'
  } catch {
    return false
  }
}

// Field 是卡片内的一项：上方灰色小标签，下方数值
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="text-xs text-foreground-muted">{label}</dt>
      <dd className="min-w-0 text-base font-medium break-words tabular-nums">{children}</dd>
    </div>
  )
}

// FieldGrid 是卡片内按列排布的一组 Field，卡片够宽时固定五列，各卡片列线对齐
function FieldGrid({ children }: { children: ReactNode }) {
  return (
    <div className="@container">
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 px-5 py-5 @min-[560px]:grid-cols-5">
        {children}
      </dl>
    </div>
  )
}

// Step 是使用流程中的一步：序号或完成标记、标题与说明、右侧操作
function Step({
  index,
  done,
  doneLabel,
  title,
  hint,
  action,
  children,
}: {
  index: number
  done: boolean
  doneLabel: string
  title: string
  hint: string
  action: ReactNode
  children?: ReactNode
}) {
  return (
    <li className="flex flex-wrap items-center gap-4 px-5 py-4">
      {done ? (
        <CircleCheck size={24} aria-label={doneLabel} className="shrink-0 text-success-text" />
      ) : (
        <span
          aria-hidden
          className="flex size-6 shrink-0 items-center justify-center rounded-full border border-border text-xs font-medium text-foreground-muted tabular-nums"
        >
          {index}
        </span>
      )}
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="text-base font-medium">
          {title}
          {done && <span className="ml-2 text-sm font-normal text-success-text">{doneLabel}</span>}
        </span>
        <span className="text-sm text-foreground-muted">{hint}</span>
        {children}
      </div>
      <div className="shrink-0">{action}</div>
    </li>
  )
}

// DashboardPage 是概览：未走完时的使用流程，服务、接入与近 1 小时三张卡片
export function DashboardPage() {
  const m = useMessages(dashboardMessages)
  const { locale } = useI18n()
  const status = useStatus()
  const models = useModels()
  const config = useConfig()
  const usage = useUsage(3_600_000)
  const ledger = useUsage(ledgerSpan)
  const [copied, setCopied] = useState(readCopied)
  const [revealed, setRevealed] = useState(false)
  const now = useNow(30_000)
  if (status.isPending) return <Loading label={m.title} />
  const base = window.location.origin
  const apiKey = firstApiKey(config.data?.proxy_api_key ?? '')
  const totals = usage.data?.totals
  const accounts = status.data?.accounts
  const markCopied = () => {
    setCopied(true)
    try {
      localStorage.setItem(copiedKey, '1')
    } catch {
      // 本地存储不可用时只在本次页面内记住
    }
  }
  const imported = (accounts?.total ?? 0) > 0
  const tried = (ledger.data?.totals.requests ?? 0) > 0
  const showSteps = !imported || ((accounts?.available ?? 0) > 0 && !tried)
  const command =
    (navigator.userAgent.includes('Windows') ? '.\\gemini-web2api.exe' : './gemini-web2api') +
    ' setup'
  const clients = [
    { name: 'OpenAI', url: base + '/v1' },
    { name: 'Anthropic', url: base },
    { name: 'Gemini', url: base },
  ]
  const ready = status.data?.ready ?? false
  const started = status.data ? Date.parse(status.data.started_at) : Number.NaN
  const requests = totals?.requests ?? 0
  return (
    <>
      <PageHeader title={m.title} description={m.description} />

      {showSteps && (
        <Card>
          <CardHeader title={m.steps} />
          <ol className="divide-y divide-border">
            <Step
              index={1}
              done={imported}
              doneLabel={m.done}
              title={m.importAccount}
              hint={
                imported
                  ? m.stepImportHint
                  : status.data?.guest?.state === 'ready'
                    ? m.setupHintGuest
                    : m.setupHint
              }
              action={
                <Button variant={imported ? 'secondary' : 'primary'} size="sm" asChild>
                  <Link to="/accounts/import">
                    <UserPlus size={16} aria-hidden />
                    {m.importAccount}
                  </Link>
                </Button>
              }
            >
              {!imported && !status.data?.desktop && (
                <div className="mt-1 flex max-w-prose items-center gap-2 rounded-sm border border-border bg-panel py-1 pr-1 pl-3">
                  <span className="text-xs text-foreground-muted">{m.setupCommand}</span>
                  <code className="min-w-0 flex-1 overflow-x-auto font-mono whitespace-nowrap select-all">
                    {command}
                  </code>
                  <CopyButton value={command} label={m.copy} copiedLabel={m.copied} />
                </div>
              )}
            </Step>
            <Step
              index={2}
              done={copied}
              doneLabel={m.done}
              title={m.stepCopy}
              hint={m.stepCopyHint}
              action={
                <Button variant="secondary" size="sm" asChild>
                  <a href="#endpoints">{m.quickStart}</a>
                </Button>
              }
            />
            <Step
              index={3}
              done={tried}
              doneLabel={m.done}
              title={m.stepTry}
              hint={m.stepTryHint}
              action={
                <Button variant={imported && !tried ? 'primary' : 'secondary'} size="sm" asChild>
                  <Link to="/playground">
                    <MessageSquare size={16} aria-hidden />
                    {m.openPlayground}
                  </Link>
                </Button>
              }
            />
          </ol>
        </Card>
      )}

      <Card>
        <CardHeader title={m.service} />
        <FieldGrid>
          <Field label={m.status}>
            <span className="inline-flex items-center gap-2">
              <span
                aria-hidden
                className={'size-2 rounded-full ' + (ready ? 'bg-success' : 'bg-warning')}
              />
              {ready ? m.ready : m.notReady}
            </span>
          </Field>
          <Field label={m.listen}>
            <span className="font-mono">{status.data?.listen_addr}</span>
          </Field>
          <Field label={m.uptime}>
            {Number.isNaN(started) ? '—' : formatDuration(now.getTime() - started, locale)}
          </Field>
          <Field label={m.accounts}>
            <Link to="/accounts" className="text-primary hover:underline">
              {fill(m.accountsReady, {
                ready: formatNumber(accounts?.available ?? 0, locale),
                total: formatNumber(accounts?.total ?? 0, locale),
              })}
            </Link>
          </Field>
          <Field label={m.availableModels}>
            {formatNumber(models.data?.models.length ?? 0, locale)}
          </Field>
        </FieldGrid>
      </Card>

      <div id="endpoints">
        <Card>
          <CardHeader title={m.quickStart} description={m.quickStartHint} />
          <div className="grid gap-x-10 gap-y-5 px-5 py-5 md:grid-cols-2">
            <div className="flex min-w-0 flex-col gap-2">
              <span className="text-xs text-foreground-muted">{m.baseUrl}</span>
              <ul className="flex flex-col gap-1">
                {clients.map((client) => (
                  <li key={client.name} className="flex min-w-0 items-center gap-3">
                    <span className="w-[88px] shrink-0 text-sm text-foreground-muted">
                      {client.name}
                    </span>
                    <code className="min-w-0 flex-1 font-mono break-all">{client.url}</code>
                    <CopyButton
                      value={client.url}
                      label={m.copy}
                      copiedLabel={m.copied}
                      onCopied={markCopied}
                    />
                  </li>
                ))}
              </ul>
            </div>
            <div className="flex min-w-0 flex-col gap-2">
              <span className="text-xs text-foreground-muted">{m.apiKey}</span>
              {apiKey ? (
                <div className="flex min-w-0 items-center gap-1">
                  <code className="min-w-0 flex-1 font-mono break-all">
                    {revealed ? apiKey : '••••••••••••'}
                  </code>
                  <Tooltip label={revealed ? m.hideKey : m.showKey}>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={revealed ? m.hideKey : m.showKey}
                      aria-pressed={revealed}
                      className="size-control-sm"
                      onClick={() => {
                        setRevealed(!revealed)
                      }}
                    >
                      {revealed ? <EyeOff size={16} aria-hidden /> : <Eye size={16} aria-hidden />}
                    </Button>
                  </Tooltip>
                  <CopyButton
                    value={apiKey}
                    label={m.copy}
                    copiedLabel={m.copied}
                    onCopied={markCopied}
                  />
                </div>
              ) : (
                <span className="text-sm text-foreground-muted">{m.apiKeyNone}</span>
              )}
            </div>
          </div>
        </Card>
      </div>

      <Card>
        <CardHeader
          title={m.lastHour}
          actions={
            <Button variant="ghost" size="sm" asChild>
              <Link to="/usage">{m.viewUsage}</Link>
            </Button>
          }
        />
        <FieldGrid>
          <Field label={m.requests}>{formatNumber(requests, locale)}</Field>
          <Field label={m.successRate}>
            {requests > 0 ? formatPercent((totals?.succeeded ?? 0) / requests, locale) : '—'}
          </Field>
          <Field label={m.tokens}>{formatCompact(totals?.total_tokens ?? 0, locale)}</Field>
          <Field label={m.cost}>{formatUsd(usage.data?.cost ?? 0, locale)}</Field>
        </FieldGrid>
      </Card>
    </>
  )
}
