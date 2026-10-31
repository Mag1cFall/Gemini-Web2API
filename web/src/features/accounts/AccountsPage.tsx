import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Globe, Power, PowerOff, ShieldCheck, Trash2, Users } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogClose, DialogContent } from '@/components/ui/dialog'
import { Card, CountBadge, PageHeader } from '@/components/ui/page'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { runBatch } from '@/features/accounts/batch'
import { accountsMessages, accountStateMessages } from '@/features/accounts/messages'
import { fixedProxy } from '@/features/accounts/proxy'
import { ProxyDialog } from '@/features/accounts/ProxyDialog'
import { ProxyValue } from '@/features/accounts/ProxyValue'
import { accountsKey, useAccounts } from '@/features/accounts/queries'
import type { Account } from '@/features/accounts/types'
import { stateTone } from '@/features/accounts/usage'
import { api } from '@/lib/api'
import { formatDateTime, formatPercent } from '@/lib/format'
import { fill, plural, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// batchLimit 是批量操作同时进行的请求数
const batchLimit = 3

// BatchAction 是可对多个账号执行的操作
type BatchAction = 'proxy' | 'enable' | 'disable' | 'verify' | 'delete'

// BatchRequest 是一次批量操作的目标账号与要设置的代理
interface BatchRequest {
  action: BatchAction
  accounts: Account[]
  proxy?: string
}

// applyBatch 对一个账号执行批量操作，代理未变化时不发送请求
function applyBatch({ action, proxy = '' }: BatchRequest, account: Account) {
  const path = '/api/accounts/' + encodeURIComponent(account.account_id)
  switch (action) {
    case 'delete':
      return api(path, { method: 'DELETE' })
    case 'verify':
      return api(path + '/verify', { method: 'POST' })
    case 'proxy':
      return proxy === fixedProxy(account)
        ? Promise.resolve()
        : api(path, { method: 'PUT', json: { proxy } })
    default:
      return api(path, { method: 'PUT', json: { enabled: action === 'enable' } })
  }
}

// AccountsPage 列出全部账号，行链接到账号详情，勾选后可批量设置代理、启用、停用与验证
export function AccountsPage() {
  const m = useMessages(accountsMessages)
  const sm = useMessages(accountStateMessages)
  const { locale } = useI18n()
  const now = useNow(30_000)
  const client = useQueryClient()
  const accounts = useAccounts()
  const [selection, setSelection] = useState<ReadonlySet<string>>(new Set())
  const [proxyOpen, setProxyOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const batch = useMutation({
    mutationFn: (request: BatchRequest) =>
      runBatch(
        request.accounts.map((account) => account.account_id),
        batchLimit,
        (id) => {
          const account = request.accounts.find((item) => item.account_id === id)
          return account ? applyBatch(request, account) : Promise.resolve()
        },
      ),
    onSuccess: (failures, request) => {
      const labels = {
        proxy: m.setProxy,
        enable: m.enable,
        disable: m.disable,
        verify: m.verify,
        delete: m.batchDelete ?? m.deleteTitle,
      }
      const result = fill(m.batchResult, {
        action: labels[request.action],
        succeeded: request.accounts.length - failures.length,
        failed: failures.length,
      })
      const items = failures
        .map((failure) => fill(m.batchFailure, { id: failure.id, message: failure.message }))
        .join(m.listSeparator)
      toast({
        tone: failures.length === 0 ? 'success' : 'error',
        title: failures.length === 0 ? result : fill(m.batchFailures, { result, items }),
      })
      if (request.action === 'delete') {
        const failedIds = new Set(failures.map((f) => f.id))
        setSelection((current) => new Set([...current].filter((id) => failedIds.has(id))))
      }
    },
    onSettled: () => {
      setProxyOpen(false)
      setDeleteOpen(false)
      void client.invalidateQueries({ queryKey: accountsKey })
    },
  })
  if (accounts.isPending) return <Loading label={m.title} />
  if (accounts.isError) {
    return (
      <ErrorState
        title={m.title}
        error={accounts.error}
        retryLabel={m.title}
        onRetry={() => void accounts.refetch()}
      />
    )
  }
  const rows = accounts.data.accounts
  const selected = rows.filter((account) => selection.has(account.account_id))
  const allSelected = rows.length > 0 && selected.length === rows.length
  const running = batch.isPending ? batch.variables.action : undefined
  const toggle = (id: string) => {
    const next = new Set(selection)
    if (!next.delete(id)) next.add(id)
    setSelection(next)
  }
  const start = (action: BatchAction) => {
    batch.mutate({ action, accounts: selected })
  }
  return (
    <>
      <PageHeader
        title={m.title}
        description={m.description}
        actions={
          <Button asChild>
            <Link to="/accounts/import">{m.import}</Link>
          </Button>
        }
      />
      {rows.length === 0 ? (
        <EmptyState
          icon={<Users size={20} aria-hidden />}
          title={m.empty}
          description={m.emptyHint}
          action={
            <Button asChild>
              <Link to="/accounts/import">{m.import}</Link>
            </Button>
          }
        />
      ) : (
        <Card>
          {selected.length === 0 ? (
            <div className="flex min-h-[60px] flex-wrap items-center gap-2 border-b border-border px-5 py-3">
              <h2 className="mr-auto text-md font-semibold">{m.title}</h2>
              <CountBadge>{rows.length}</CountBadge>
            </div>
          ) : (
            <div
              role="toolbar"
              aria-label={m.batchActions}
              className="flex min-h-[60px] flex-wrap items-center gap-2 border-b border-border bg-primary-soft px-5 py-3"
            >
              <span className="mr-auto text-sm font-medium tabular-nums">
                {fill(plural(locale, selected.length, m.selectedCountOne, m.selectedCount), {
                  count: selected.length,
                })}
              </span>
              <Button
                size="sm"
                loading={running === 'proxy'}
                disabled={batch.isPending}
                onClick={() => {
                  setProxyOpen(true)
                }}
              >
                <Globe size={16} aria-hidden />
                {m.setProxy}
              </Button>
              <Button
                size="sm"
                loading={running === 'enable'}
                disabled={batch.isPending}
                onClick={() => {
                  start('enable')
                }}
              >
                <Power size={16} aria-hidden />
                {m.enable}
              </Button>
              <Button
                size="sm"
                loading={running === 'disable'}
                disabled={batch.isPending}
                onClick={() => {
                  start('disable')
                }}
              >
                <PowerOff size={16} aria-hidden />
                {m.disable}
              </Button>
              <Tooltip label={m.verifyHint}>
                <Button
                  size="sm"
                  loading={running === 'verify'}
                  disabled={batch.isPending}
                  onClick={() => {
                    start('verify')
                  }}
                >
                  <ShieldCheck size={16} aria-hidden />
                  {m.verify}
                </Button>
              </Tooltip>
              <Button
                size="sm"
                variant="destructive"
                loading={running === 'delete'}
                disabled={batch.isPending}
                onClick={() => {
                  setDeleteOpen(true)
                }}
              >
                <Trash2 size={16} aria-hidden />
                {m.batchDelete ?? m.deleteTitle}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={batch.isPending}
                onClick={() => {
                  setSelection(new Set())
                }}
              >
                {m.clearSelection}
              </Button>
            </div>
          )}
          <Table label={m.title}>
            <thead>
              <tr>
                <Th className="w-[40px] first:pr-0">
                  <input
                    type="checkbox"
                    className="size-4 align-middle accent-primary"
                    aria-label={m.selectAll}
                    checked={allSelected}
                    ref={(node) => {
                      if (node) node.indeterminate = selected.length > 0 && !allSelected
                    }}
                    onChange={() => {
                      setSelection(
                        allSelected
                          ? new Set()
                          : new Set(rows.map((account) => account.account_id)),
                      )
                    }}
                  />
                </Th>
                <Th>{m.account}</Th>
                <Th>{m.state}</Th>
                <Th>{m.source}</Th>
                <Th>{m.tier}</Th>
                <Th className="text-right">{m.models}</Th>
                <Th className="text-right">{m.window5h}</Th>
                <Th>{m.lastUsed}</Th>
                <Th>{m.proxy}</Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((account) => (
                <tr
                  key={account.account_id}
                  className={
                    selection.has(account.account_id) ? 'bg-primary-soft' : 'hover:bg-hover'
                  }
                >
                  <Td className="w-[40px] first:pr-0">
                    <input
                      type="checkbox"
                      className="size-4 align-middle accent-primary"
                      aria-label={fill(m.selectAccount, {
                        id: account.email ?? account.account_id,
                      })}
                      checked={selection.has(account.account_id)}
                      onChange={() => {
                        toggle(account.account_id)
                      }}
                    />
                  </Td>
                  <Td className="max-w-cell">
                    <Link
                      to={'/accounts/' + encodeURIComponent(account.account_id)}
                      title={account.email ?? account.account_id}
                      className="block truncate font-medium text-primary"
                    >
                      {account.email ?? account.account_id}
                    </Link>
                  </Td>
                  <Td>
                    <Badge tone={stateTone(account.state)}>{sm[account.state]}</Badge>
                  </Td>
                  <Td>
                    {account.source === 'chrome'
                      ? m.sourceChrome
                      : account.source === 'brave'
                        ? m.sourceBrave
                        : account.source === 'edge'
                          ? m.sourceEdge
                          : account.source === 'firefox'
                            ? m.sourceFirefox
                            : m.sourceCookie}
                  </Td>
                  <Td>{account.tier ?? m.none}</Td>
                  <Td className="text-right">{account.models.length}</Td>
                  <Td className="text-right tabular-nums">
                    {account.usage?.current
                      ? formatPercent(account.usage.current.used_ratio, locale)
                      : m.none}
                  </Td>
                  <Td className="whitespace-nowrap text-foreground-muted">
                    {account.last_used
                      ? formatDateTime(new Date(account.last_used), now, locale)
                      : m.never}
                  </Td>
                  <Td
                    className="max-w-cell truncate font-mono text-foreground-muted"
                    title={account.proxy}
                  >
                    <ProxyValue account={account} />
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      {proxyOpen && (
        <ProxyDialog
          title={fill(plural(locale, selected.length, m.batchProxyTitleOne, m.batchProxyTitle), {
            count: selected.length,
          })}
          initial=""
          pending={running === 'proxy'}
          onSave={(proxy) => {
            batch.mutate({ action: 'proxy', accounts: selected, proxy })
          }}
          onClose={() => {
            setProxyOpen(false)
          }}
        />
      )}
      {deleteOpen && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setDeleteOpen(false)
          }}
        >
          <DialogContent
            title={fill(plural(locale, selected.length, m.batchDeleteTitleOne, m.batchDeleteTitle), {
              count: selected.length,
            })}
            description={fill(plural(locale, selected.length, m.batchDeleteHintOne, m.batchDeleteHint), {
              count: selected.length,
            })}
            closeLabel={m.cancel}
            footer={
              <>
                <DialogClose asChild>
                  <Button variant="secondary">{m.cancel}</Button>
                </DialogClose>
                <Button
                  variant="destructive"
                  loading={running === 'delete'}
                  onClick={() => {
                    start('delete')
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
      )}
    </>
  )
}
