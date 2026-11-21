import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ArrowDown,
  ArrowUp,
  ChevronLeft,
  ChevronRight,
  Globe,
  Power,
  PowerOff,
  ShieldCheck,
  Trash2,
  Users,
} from 'lucide-react'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardEmpty, CountBadge, PageHeader } from '@/components/ui/page'
import { SearchInput } from '@/components/ui/search-input'
import { Select } from '@/components/ui/select'
import { EmptyState, ErrorState, PagePlaceholder } from '@/components/ui/states'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { AbuseBadge } from '@/features/accounts/AbuseBadge'
import { AccountTasks } from '@/features/accounts/AccountTasks'
import { runBatch } from '@/features/accounts/batch'
import {
  accountQueryParams,
  accountStates,
  filterAccounts,
  pageSize,
  parseAccountQuery,
  type SortKey,
  tierLabel,
} from '@/features/accounts/list'
import { accountsMessages, accountStateMessages } from '@/features/accounts/messages'
import { ProxyDialog } from '@/features/accounts/ProxyDialog'
import { ProxyValue } from '@/features/accounts/ProxyValue'
import {
  accountsKey,
  runAccountAction,
  useAccounts,
  useDeleteAccounts,
} from '@/features/accounts/queries'
import { SelectAllCheckbox } from '@/features/accounts/SelectAllCheckbox'
import type { Account, AccountState } from '@/features/accounts/types'
import { stateTone } from '@/features/accounts/usage'
import { WarmupDialog } from '@/features/accounts/WarmupDialog'
import { formatDateTime, formatPercent } from '@/lib/format'
import { fill, plural, serverText, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// batchLimit 是批量操作同时进行的请求数
const batchLimit = 3

// BatchAction 是可对多个账号执行的操作
type BatchAction = 'proxy' | 'enable' | 'disable' | 'verify'

// BatchRequest 是一次批量操作的目标账号与要设置的代理
interface BatchRequest {
  action: BatchAction
  accounts: Account[]
  proxy?: string
}

// errorStates 是在行内显示失败原因的账号状态
const errorStates: ReadonlySet<AccountState> = new Set(['auth_required', 'unavailable'])

// SortHeader 是可点击排序的表头，再次点击同一列时反向
function SortHeader({
  column,
  label,
  sort,
  desc,
  onSort,
  className,
}: {
  column: SortKey
  label: string
  sort: SortKey
  desc: boolean
  onSort: (column: SortKey) => void
  className?: string
}) {
  const m = useMessages(accountsMessages)
  const active = sort === column
  const Arrow = desc ? ArrowDown : ArrowUp
  return (
    <Th
      aria-sort={active ? (desc ? 'descending' : 'ascending') : 'none'}
      {...(className ? { className } : {})}
    >
      <button
        type="button"
        aria-label={fill(m.sortBy, { column: label })}
        className="inline-flex items-center gap-1 rounded-sm hover:text-foreground"
        onClick={() => {
          onSort(column)
        }}
      >
        {label}
        {active && <Arrow size={12} aria-hidden />}
      </button>
    </Th>
  )
}

// AccountsPage 列出全部账号：按邮箱搜索、按状态筛选、按列排序并分页，条件保存在地址栏；每行可启停、重新连接与删除，勾选后可批量操作
export function AccountsPage() {
  const m = useMessages(accountsMessages)
  const app = useMessages(appMessages)
  const sm = useMessages(accountStateMessages)
  const { language } = useI18n()
  const now = useNow(30_000)
  const client = useQueryClient()
  const accounts = useAccounts()
  const remove = useDeleteAccounts()
  const [params, setParams] = useSearchParams()
  const query = parseAccountQuery(params)
  const [selection, setSelection] = useState<ReadonlySet<string>>(new Set())
  const [proxyOpen, setProxyOpen] = useState(false)
  const batch = useMutation({
    mutationFn: ({ action, accounts: targets, proxy = '' }: BatchRequest) => {
      const proxies = new Map(targets.map((item) => [item.account_id, item.fixed_proxy ?? '']))
      return runBatch(
        targets.map((account) => account.account_id),
        batchLimit,
        (id) =>
          runAccountAction(
            id,
            action === 'verify'
              ? { kind: 'verify' }
              : action === 'proxy'
                ? { kind: 'proxy', proxy, current: proxies.get(id) ?? '' }
                : { kind: 'enabled', enabled: action === 'enable' },
          ),
      )
    },
    onSuccess: (failures, request) => {
      const labels = { proxy: m.setProxy, enable: m.enable, disable: m.disable, verify: m.verify }
      const result = fill(m.batchResult, {
        action: labels[request.action],
        succeeded: request.accounts.length - failures.length,
        failed: failures.length,
      })
      const items = failures
        .map((failure) =>
          fill(m.batchFailure, { id: failure.id, message: serverText(failure.message) }),
        )
        .join(m.listSeparator)
      toast({
        tone: failures.length === 0 ? 'success' : 'error',
        title: failures.length === 0 ? result : fill(m.batchFailures, { result, items }),
      })
    },
    onSettled: () => {
      setProxyOpen(false)
      void client.invalidateQueries({ queryKey: accountsKey })
    },
  })
  if (accounts.isPending) {
    return <PagePlaceholder title={m.title} description={m.description} label={m.title} />
  }
  if (accounts.isError) {
    return (
      <ErrorState
        title={m.title}
        error={accounts.error}
        retryLabel={app.retry}
        onRetry={() => void accounts.refetch()}
      />
    )
  }
  const rows = accounts.data.accounts
  const matched = filterAccounts(rows, query)
  const pages = Math.max(1, Math.ceil(matched.length / pageSize))
  const page = Math.min(query.page, pages)
  const visible = matched.slice((page - 1) * pageSize, page * pageSize)
  const selected = rows.filter((account) => selection.has(account.account_id))
  const visibleSelected = visible.filter((account) => selection.has(account.account_id))
  const running = batch.isPending ? batch.variables.action : undefined
  const update = (change: Partial<typeof query>) => {
    setParams(accountQueryParams({ ...query, page: 1, ...change }), { replace: true })
  }
  const sortBy = (column: SortKey) => {
    update({ sort: column, desc: query.sort === column && !query.desc })
  }
  const toggle = (id: string) => {
    const next = new Set(selection)
    if (!next.delete(id)) next.add(id)
    setSelection(next)
  }
  const deleteAccounts = (targets: Account[]) => {
    const ids = new Set(targets.map((account) => account.account_id))
    setSelection(new Set([...selection].filter((id) => !ids.has(id))))
    remove(targets)
  }
  const start = (action: BatchAction) => {
    batch.mutate({ action, accounts: selected })
  }
  const sortProps = { sort: query.sort, desc: query.desc, onSort: sortBy }
  return (
    <>
      <PageHeader
        title={m.title}
        description={m.description}
        actions={
          <div className="flex items-center gap-2">
            <WarmupDialog
              accounts={rows}
              selected={selected.map((account) => account.account_id)}
            />
            <Button asChild>
              <Link to="/accounts/import">{m.import}</Link>
            </Button>
          </div>
        }
      />
      <AccountTasks />
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
              <h2 className="text-md font-semibold">{m.title}</h2>
              <CountBadge>{rows.length}</CountBadge>
              <div className="ml-auto flex w-full flex-wrap items-center gap-2 sm:w-auto">
                <SearchInput
                  aria-label={m.search}
                  placeholder={m.search}
                  className="w-full sm:w-[240px]"
                  value={query.q}
                  onChange={(event) => {
                    update({ q: event.target.value })
                  }}
                />
                <Select<AccountState | 'all'>
                  label={m.state}
                  size="sm"
                  value={query.state || 'all'}
                  onValueChange={(value) => {
                    update({ state: value === 'all' ? '' : value })
                  }}
                  options={[
                    { value: 'all', label: m.allStates },
                    ...accountStates.map((state) => ({ value: state, label: sm[state] })),
                  ]}
                />
              </div>
            </div>
          ) : (
            <div
              role="toolbar"
              aria-label={m.batchActions}
              className="flex min-h-[60px] flex-wrap items-center gap-2 border-b border-border bg-primary-soft px-5 py-3"
            >
              <span className="mr-auto text-sm font-medium tabular-nums">
                {fill(plural(language, selected.length, m.selectedCountOne, m.selectedCount), {
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
                disabled={batch.isPending}
                onClick={() => {
                  deleteAccounts(selected)
                }}
              >
                <Trash2 size={16} aria-hidden />
                {m.delete}
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
          {matched.length === 0 ? (
            <CardEmpty icon={<Users size={20} aria-hidden />}>
              <span className="flex flex-col items-center gap-2">
                {m.noMatch}
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    update({ q: '', state: '' })
                  }}
                >
                  {m.clearFilters}
                </Button>
              </span>
            </CardEmpty>
          ) : (
            <Table label={m.title}>
              <thead>
                <tr>
                  <Th className="w-[40px] first:pr-0">
                    <SelectAllCheckbox
                      label={m.selectAll}
                      selected={visibleSelected.length}
                      total={visible.length}
                      onToggle={(checked) => {
                        const next = new Set(selection)
                        for (const account of visible) {
                          if (checked) next.add(account.account_id)
                          else next.delete(account.account_id)
                        }
                        setSelection(next)
                      }}
                    />
                  </Th>
                  <SortHeader column="account" label={m.account} {...sortProps} />
                  <SortHeader column="state" label={m.state} {...sortProps} />
                  <Th>{m.source}</Th>
                  <SortHeader column="tier" label={m.tier} {...sortProps} />
                  <SortHeader
                    column="models"
                    label={m.models}
                    className="text-right"
                    {...sortProps}
                  />
                  <SortHeader
                    column="window5h"
                    label={m.window5h}
                    className="text-right"
                    {...sortProps}
                  />
                  <SortHeader column="lastUsed" label={m.lastUsed} {...sortProps} />
                  <Th>{m.proxy}</Th>
                  <Th className="sticky right-0 bg-surface text-right shadow-[-6px_0_6px_-6px_rgb(0_0_0/0.25)]">
                    <span className="sr-only">{m.actions}</span>
                  </Th>
                </tr>
              </thead>
              <tbody>
                {visible.map((account) => {
                  const label = account.email ?? account.account_id
                  const failure =
                    errorStates.has(account.state) && account.last_error
                      ? serverText(account.last_error)
                      : undefined
                  return (
                    <tr
                      key={account.account_id}
                      className={
                        'bg-(--row) ' +
                        (selection.has(account.account_id)
                          ? '[--row:var(--color-primary-soft)]'
                          : 'hover:[--row:var(--color-hover)]')
                      }
                    >
                      <Td className="w-[40px] first:pr-0">
                        <input
                          type="checkbox"
                          className="size-4 align-middle accent-primary"
                          aria-label={fill(m.selectAccount, { id: label })}
                          checked={selection.has(account.account_id)}
                          onChange={() => {
                            toggle(account.account_id)
                          }}
                        />
                      </Td>
                      <Td className="max-w-cell">
                        <Tooltip overflow label={label}>
                          <Link
                            to={'/accounts/' + encodeURIComponent(account.account_id)}
                            className="block truncate font-medium text-primary"
                          >
                            {label}
                          </Link>
                        </Tooltip>
                      </Td>
                      <Td className="max-w-cell">
                        <span className="flex flex-wrap items-center gap-1">
                          <Badge tone={stateTone(account.state)}>{sm[account.state]}</Badge>
                          <AbuseBadge abuse={account.abuse} />
                        </span>
                        {failure && (
                          <p className="mt-1 line-clamp-2 text-xs break-words text-error-text">
                            {failure}
                          </p>
                        )}
                      </Td>
                      <Td>{account.source === 'chrome' ? m.sourceChrome : m.sourceCookie}</Td>
                      <Td>{account.tier ? tierLabel(account.tier, m.tierFree) : m.none}</Td>
                      <Td className="text-right">{account.models.length}</Td>
                      <Td className="text-right tabular-nums">
                        {account.usage?.current
                          ? formatPercent(account.usage.current.used_ratio, language)
                          : m.none}
                      </Td>
                      <Td className="whitespace-nowrap text-foreground-muted">
                        {account.last_used
                          ? formatDateTime(new Date(account.last_used), now, language)
                          : m.never}
                      </Td>
                      <Td className="max-w-[104px] font-mono text-foreground-muted">
                        <Tooltip overflow label={account.proxy}>
                          <span className="block truncate">
                            <ProxyValue account={account} />
                          </span>
                        </Tooltip>
                      </Td>
                      <Td className="sticky right-0 bg-surface bg-[linear-gradient(var(--row),var(--row))] text-right whitespace-nowrap shadow-[-6px_0_6px_-6px_rgb(0_0_0/0.25)]">
                        <RowActions account={account} onDelete={deleteAccounts} />
                      </Td>
                    </tr>
                  )
                })}
              </tbody>
            </Table>
          )}
          {pages > 1 && (
            <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3 text-sm text-foreground-muted">
              <span className="mr-2 tabular-nums">
                {fill(m.pageStatus, { page, pages, count: matched.length })}
              </span>
              <Button
                size="icon"
                variant="ghost"
                aria-label={m.previousPage}
                disabled={page <= 1}
                onClick={() => {
                  setParams(accountQueryParams({ ...query, page: page - 1 }), { replace: true })
                }}
              >
                <ChevronLeft size={16} aria-hidden />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                aria-label={m.nextPage}
                disabled={page >= pages}
                onClick={() => {
                  setParams(accountQueryParams({ ...query, page: page + 1 }), { replace: true })
                }}
              >
                <ChevronRight size={16} aria-hidden />
              </Button>
            </div>
          )}
        </Card>
      )}
      {proxyOpen && (
        <ProxyDialog
          title={fill(plural(language, selected.length, m.batchProxyTitleOne, m.batchProxyTitle), {
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
    </>
  )
}

// RowActions 是一行账号的启停、重新连接与删除按钮，不改变勾选
function RowActions({
  account,
  onDelete,
}: {
  account: Account
  onDelete: (accounts: Account[]) => void
}) {
  const m = useMessages(accountsMessages)
  const client = useQueryClient()
  const action = useMutation({
    mutationFn: (kind: 'toggle' | 'verify') =>
      runAccountAction(
        account.account_id,
        kind === 'verify' ? { kind: 'verify' } : { kind: 'enabled', enabled: !account.enabled },
      ),
    onSuccess: (_, kind) => {
      if (kind === 'verify') toast({ tone: 'success', title: m.verified })
    },
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
    onSettled: () => void client.invalidateQueries({ queryKey: accountsKey }),
  })
  const running = action.isPending ? action.variables : undefined
  return (
    <span className="inline-flex items-center gap-1">
      <Tooltip label={account.enabled ? m.disable : m.enable}>
        <Button
          size="icon"
          variant="ghost"
          aria-label={account.enabled ? m.disable : m.enable}
          loading={running === 'toggle'}
          disabled={action.isPending}
          onClick={() => {
            action.mutate('toggle')
          }}
        >
          {account.enabled ? <PowerOff size={16} aria-hidden /> : <Power size={16} aria-hidden />}
        </Button>
      </Tooltip>
      <Tooltip label={account.enabled ? m.verifyHint : m.verifyDisabled}>
        <span className="inline-flex">
          <Button
            size="icon"
            variant="ghost"
            aria-label={m.verify}
            loading={running === 'verify'}
            disabled={action.isPending || !account.enabled}
            onClick={() => {
              action.mutate('verify')
            }}
          >
            <ShieldCheck size={16} aria-hidden />
          </Button>
        </span>
      </Tooltip>
      <Tooltip label={m.delete}>
        <Button
          size="icon"
          variant="ghost"
          aria-label={m.delete}
          disabled={action.isPending}
          onClick={() => {
            onDelete([account])
          }}
        >
          <Trash2 size={16} aria-hidden />
        </Button>
      </Tooltip>
    </span>
  )
}
