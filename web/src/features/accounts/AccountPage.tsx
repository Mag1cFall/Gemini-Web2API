import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Globe, MoreHorizontal, Power, RefreshCw, ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'

import { Badge, Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogClose, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Meter } from '@/components/ui/meter'
import { Card, CardHeader, PageHeader, Section } from '@/components/ui/page'
import { EmptyState, Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import {
  accountsMessages,
  accountStateMessages,
  cooldownReason,
  cooldownReasonMessages,
} from '@/features/accounts/messages'
import { fixedProxy } from '@/features/accounts/proxy'
import { ProxyDialog } from '@/features/accounts/ProxyDialog'
import { ProxyValue } from '@/features/accounts/ProxyValue'
import { accountsKey, useAccounts, useClearCooldown } from '@/features/accounts/queries'
import type { Account, AccountsUsage, UsageWindow } from '@/features/accounts/types'
import { stateTone, windowTone } from '@/features/accounts/usage'
import { api } from '@/lib/api'
import { formatDateTime, formatNumber, formatPercent } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// DeleteDialog 是删除账号的确认对话框
function DeleteDialog({ account }: { account: Account }) {
  const m = useMessages(accountsMessages)
  const client = useQueryClient()
  const navigate = useNavigate()
  const remove = useMutation({
    mutationFn: () =>
      api('/api/accounts/' + encodeURIComponent(account.account_id), { method: 'DELETE' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accountsKey })
      void navigate('/accounts')
    },
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
  return (
    <Dialog>
      <DialogTrigger asChild>
        <DropdownMenuItem
          destructive
          onSelect={(event) => {
            event.preventDefault()
          }}
        >
          {m.delete}
        </DropdownMenuItem>
      </DialogTrigger>
      <DialogContent
        title={m.deleteTitle}
        description={fill(m.deleteHint, { id: account.account_id })}
        closeLabel={m.cancel}
        footer={
          <>
            <DialogClose asChild>
              <Button variant="secondary">{m.cancel}</Button>
            </DialogClose>
            <Button
              variant="destructive"
              loading={remove.isPending}
              onClick={() => {
                remove.mutate()
              }}
            >
              {m.confirmDelete}
            </Button>
          </>
        }
      >
        <span className="sr-only">{m.deleteTitle}</span>
      </DialogContent>
    </Dialog>
  )
}

// AccountPage 是账号详情：状态胶囊、用量窗口与模型冷却
export function AccountPage() {
  const m = useMessages(accountsMessages)
  const sm = useMessages(accountStateMessages)
  const rm = useMessages(cooldownReasonMessages)
  const { locale } = useI18n()
  const now = useNow(30_000)
  const { id = '' } = useParams()
  const client = useQueryClient()
  const accounts = useAccounts()
  const [proxyOpen, setProxyOpen] = useState(false)
  const clearCooldown = useClearCooldown()
  const verify = useMutation({
    mutationFn: () =>
      api('/api/accounts/' + encodeURIComponent(id) + '/verify', { method: 'POST' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accountsKey })
      toast({ tone: 'success', title: m.verified })
    },
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
  const toggle = useMutation({
    mutationFn: (enabled: boolean) =>
      api('/api/accounts/' + encodeURIComponent(id), { method: 'PUT', json: { enabled } }),
    onSuccess: () => void client.invalidateQueries({ queryKey: accountsKey }),
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
  const saveProxy = useMutation({
    mutationFn: ({ proxy, current }: { proxy: string; current: string }) =>
      proxy === current
        ? Promise.resolve()
        : api('/api/accounts/' + encodeURIComponent(id), { method: 'PUT', json: { proxy } }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accountsKey })
      setProxyOpen(false)
    },
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
  const refreshUsage = useMutation({
    mutationFn: () => api<AccountsUsage>('/api/accounts/usage?refresh=true'),
    onSuccess: (usage) => {
      void client.invalidateQueries({ queryKey: accountsKey })
      const failure = usage.accounts.find((item) => item.account_id === id)?.error
      if (failure) toast({ tone: 'error', title: failure })
    },
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
  if (accounts.isPending) return <Loading label={m.title} />
  const account = accounts.data?.accounts.find((item) => item.account_id === id)
  if (!account) {
    return (
      <EmptyState
        icon={<ShieldCheck size={20} aria-hidden />}
        title={m.notFound}
        action={
          <Button asChild>
            <Link to="/accounts">{m.back}</Link>
          </Button>
        }
      />
    )
  }
  const usage = account.usage
  const windows = [
    { key: 'current', label: m.window5h, window: usage?.current },
    { key: 'weekly', label: m.windowWeekly, window: usage?.weekly },
  ].filter((item): item is { key: string; label: string; window: UsageWindow } => !!item.window)
  const cooldownFor = (model: string) =>
    account.model_cooldowns?.find((item) => item.model === model && new Date(item.until) > now)
  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-3">
            <Badge tone={stateTone(account.state)}>{sm[account.state]}</Badge>
            <span className="break-all">{account.email ?? account.account_id}</span>
          </span>
        }
        description={[
          account.source === 'chrome'
            ? m.sourceChrome
            : account.source === 'brave'
              ? m.sourceBrave
              : account.source === 'edge'
                ? m.sourceEdge
                : account.source === 'firefox'
                  ? m.sourceFirefox
                  : m.sourceCookie,
          account.profile,
          account.tier,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <>
            <Tooltip label={m.verifyHint}>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  verify.mutate()
                }}
                loading={verify.isPending}
              >
                <ShieldCheck size={16} aria-hidden />
                {m.verify}
              </Button>
            </Tooltip>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                refreshUsage.mutate()
              }}
              loading={refreshUsage.isPending}
            >
              <RefreshCw size={16} aria-hidden />
              {m.refreshUsage}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setProxyOpen(true)
              }}
            >
              <Globe size={16} aria-hidden />
              {m.editProxy}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              loading={toggle.isPending}
              onClick={() => {
                toggle.mutate(!account.enabled)
              }}
            >
              <Power size={16} aria-hidden />
              {account.enabled ? m.disable : m.enable}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="secondary" size="icon" aria-label={m.more}>
                  <MoreHorizontal size={18} aria-hidden />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DeleteDialog account={account} />
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      {account.cooldown_until && new Date(account.cooldown_until) > now && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-md bg-warning-soft px-4 py-3 text-sm text-warning-text">
          <p>
            {fill(m.accountCooldown, {
              time: formatDateTime(new Date(account.cooldown_until), now, locale),
              reason: cooldownReason(rm, account.cooldown_reason ?? ''),
            })}
          </p>
          <Button
            size="sm"
            loading={clearCooldown.isPending && clearCooldown.variables.scope === '*'}
            onClick={() => {
              clearCooldown.mutate({ account: account.account_id, scope: '*' })
            }}
          >
            {m.clearCooldown}
          </Button>
        </div>
      )}

      <Card>
        <CardHeader
          title={m.usageWindows}
          actions={
            usage && (
              <span className="text-xs text-foreground-muted">
                {fill(m.fetchedAt, {
                  time: formatDateTime(new Date(usage.fetched_at), now, locale),
                })}
              </span>
            )
          }
        />
        <div className="flex flex-col gap-4 px-5 py-5">
          {windows.length === 0 ? (
            <p className="text-sm text-foreground-muted">{m.noUsage}</p>
          ) : (
            <ul className="grid gap-6 sm:grid-cols-2">
              {windows.map(({ key, label, window }) => (
                <li key={key} className="flex flex-col gap-2">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-base font-medium">{label}</span>
                    <span className="text-xs text-foreground-muted">
                      {fill(m.usedPercent, { percent: formatPercent(window.used_ratio, locale) })}
                    </span>
                  </div>
                  <Meter
                    value={window.used_ratio}
                    tone={windowTone(window.used_ratio)}
                    label={label}
                  />
                  <span className="text-xs text-foreground-muted">
                    {fill(m.resetAt, {
                      time: formatDateTime(new Date(window.reset_at), now, locale),
                    })}
                  </span>
                </li>
              ))}
            </ul>
          )}
          {usage && (usage.remaining_credits !== undefined || usage.use_overage_ai_credits) && (
            <div className="flex flex-wrap gap-2">
              {usage.remaining_credits !== undefined && (
                <Tag>{m.credits + ' ' + formatNumber(usage.remaining_credits, locale)}</Tag>
              )}
              {usage.use_overage_ai_credits && <Tag>{m.overage}</Tag>}
            </div>
          )}
        </div>
      </Card>

      <Section title={m.modelList}>
        {account.models.length === 0 ? (
          <p className="text-sm text-foreground-muted">{m.none}</p>
        ) : (
          <ul className="flex flex-wrap gap-x-6 gap-y-3">
            {account.models.map((model) => {
              const cooldown = cooldownFor(model)
              return (
                <li key={model} className="flex items-center gap-2 text-sm">
                  <Tag>{model}</Tag>
                  {cooldown ? (
                    <>
                      <Badge tone="warning">
                        {fill(m.cooling, {
                          time: formatDateTime(new Date(cooldown.until), now, locale),
                        })}
                      </Badge>
                      <Button
                        size="sm"
                        variant="ghost"
                        loading={clearCooldown.isPending && clearCooldown.variables.scope === model}
                        onClick={() => {
                          clearCooldown.mutate({ account: account.account_id, scope: model })
                        }}
                      >
                        {m.clearCooldown}
                      </Button>
                    </>
                  ) : (
                    <Badge tone="success">{m.available}</Badge>
                  )}
                </li>
              )
            })}
          </ul>
        )}
      </Section>

      <Card>
        <CardHeader title={m.runtimeInfo} />
        <dl className="grid gap-4 px-5 py-5 text-sm sm:grid-cols-3">
          <div className="flex flex-col gap-1">
            <dt className="text-foreground-muted">{m.contextWindow}</dt>
            <dd className="font-medium">
              {account.context_window ? formatNumber(account.context_window, locale) : m.none}
            </dd>
          </div>
          <div className="flex flex-col gap-1">
            <dt className="text-foreground-muted">{m.pending}</dt>
            <dd className="font-medium tabular-nums">{account.pending}</dd>
          </div>
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-foreground-muted">{m.proxy}</dt>
            <dd className="truncate font-mono" title={account.proxy}>
              <ProxyValue account={account} source />
            </dd>
          </div>
          <div className="flex flex-col gap-1">
            <dt className="text-foreground-muted">{m.readyAt}</dt>
            <dd className="font-medium">
              {account.ready_at ? formatDateTime(new Date(account.ready_at), now, locale) : m.none}
            </dd>
          </div>
          <div className="flex flex-col gap-1">
            <dt className="text-foreground-muted">{m.lastUsed}</dt>
            <dd className="font-medium">
              {account.last_used
                ? formatDateTime(new Date(account.last_used), now, locale)
                : m.never}
            </dd>
          </div>
          <div className="flex flex-col gap-1">
            <dt className="text-foreground-muted">{m.consecutiveFailures}</dt>
            <dd className="font-medium tabular-nums">{account.consecutive_failures}</dd>
          </div>
          {account.last_error && (
            <div className="flex min-w-0 flex-col gap-1 sm:col-span-3">
              <dt className="text-foreground-muted">
                {m.lastError}
                {account.last_error_at &&
                  ' · ' + formatDateTime(new Date(account.last_error_at), now, locale)}
              </dt>
              <dd className="font-medium break-words">{account.last_error}</dd>
            </div>
          )}
        </dl>
      </Card>
      {proxyOpen && (
        <ProxyDialog
          title={m.proxyTitle}
          initial={fixedProxy(account)}
          pending={saveProxy.isPending}
          onSave={(proxy) => {
            saveProxy.mutate({ proxy, current: fixedProxy(account) })
          }}
          onClose={() => {
            setProxyOpen(false)
          }}
        />
      )}
    </>
  )
}
