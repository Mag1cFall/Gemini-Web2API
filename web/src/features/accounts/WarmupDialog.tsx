import { useQueryClient } from '@tanstack/react-query'
import { Flame, LoaderCircle } from 'lucide-react'
import { type ReactNode, useState } from 'react'
import { Link } from 'react-router'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Table, Td, Th } from '@/components/ui/table'
import {
  accountsMessages,
  accountStateMessages,
  warmupMessages,
} from '@/features/accounts/messages'
import { accountsKey } from '@/features/accounts/queries'
import type { Account, WarmupResult } from '@/features/accounts/types'
import { stateTone } from '@/features/accounts/usage'
import { useModels } from '@/features/models/queries'
import { api } from '@/lib/api'
import { formatDuration, formatPercent } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// Run 是一个账号与模型组合的预热进度，result 在完成后出现
interface Run {
  account: string
  model: string
  running: boolean
  result?: WarmupResult
}

// Picker 是带全选的复选框列表
function Picker({
  legend,
  selectAll,
  empty,
  options,
  picked,
  onChange,
}: {
  legend: string
  selectAll: string
  empty: string
  options: { value: string; label: string; badge?: ReactNode }[]
  picked: ReadonlySet<string>
  onChange: (next: ReadonlySet<string>) => void
}) {
  const chosen = options.filter((option) => picked.has(option.value)).length
  return (
    <fieldset className="flex min-w-0 flex-col">
      <legend className="mb-2 text-sm font-medium">{legend}</legend>
      {options.length === 0 ? (
        <p className="text-sm text-foreground-muted">{empty}</p>
      ) : (
        <div className="max-h-72 flex flex-col overflow-y-auto rounded-md border border-border">
          <label className="flex items-center gap-2 border-b border-border bg-muted px-3 py-2 text-sm">
            <input
              type="checkbox"
              className="size-4 accent-primary"
              checked={chosen === options.length}
              ref={(node) => {
                if (node) node.indeterminate = chosen > 0 && chosen < options.length
              }}
              onChange={() => {
                onChange(
                  new Set(chosen === options.length ? [] : options.map((option) => option.value)),
                )
              }}
            />
            {selectAll}
          </label>
          {options.map((option) => (
            <label
              key={option.value}
              className="flex items-center gap-2 px-3 py-2 text-sm hover:bg-hover"
            >
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={picked.has(option.value)}
                onChange={() => {
                  const next = new Set(picked)
                  if (!next.delete(option.value)) next.add(option.value)
                  onChange(next)
                }}
              />
              <span className="min-w-0 flex-1 wrap-anywhere">{option.label}</span>
              {option.badge}
            </label>
          ))}
        </div>
      )}
    </fieldset>
  )
}

// WarmupDialog 对选定的账号与模型的每个组合发一条最小生成请求，各账号并行、同一账号内依次进行，逐行显示结果、耗时、失败原因与用量；selected 非空时默认选中这些账号，否则默认选中就绪账号
export function WarmupDialog({ accounts, selected }: { accounts: Account[]; selected: string[] }) {
  const m = useMessages(warmupMessages)
  const am = useMessages(accountsMessages)
  const sm = useMessages(accountStateMessages)
  const { locale } = useI18n()
  const client = useQueryClient()
  const models = (useModels().data?.models ?? []).filter((model) => !model.tool && !model.alias_of)
  const [open, setOpen] = useState(false)
  const [pickedAccounts, setPickedAccounts] = useState<ReadonlySet<string>>(new Set())
  const [pickedModels, setPickedModels] = useState<ReadonlySet<string>>(new Set())
  const [runs, setRuns] = useState<Run[]>()
  const running = runs?.some((run) => !run.result) ?? false

  const onOpenChange = (next: boolean) => {
    if (running) return
    if (next) {
      setPickedAccounts(
        new Set(
          selected.length > 0
            ? selected
            : accounts
                .filter((account) => account.state === 'ready')
                .map((account) => account.account_id),
        ),
      )
      setPickedModels(new Set(models.filter((model) => model.default).map((model) => model.id)))
      setRuns(undefined)
    }
    setOpen(next)
  }

  const update = (account: string, model: string, change: Partial<Run>) => {
    setRuns((current) =>
      current?.map((run) =>
        run.account === account && run.model === model ? { ...run, ...change } : run,
      ),
    )
  }

  const targets = accounts
    .filter((account) => pickedAccounts.has(account.account_id))
    .map((account) => account.account_id)
  const ids = models.filter((model) => pickedModels.has(model.id)).map((model) => model.id)
  const total = targets.length * ids.length

  const start = async () => {
    setRuns(targets.flatMap((account) => ids.map((model) => ({ account, model, running: false }))))
    await Promise.all(
      targets.map(async (account) => {
        for (const model of ids) {
          update(account, model, { running: true })
          const result = await api<WarmupResult>('/api/warmup', {
            method: 'POST',
            json: { account, model },
          }).catch((error: unknown): WarmupResult => ({
            account,
            model,
            ok: false,
            duration_ms: 0,
            error: error instanceof Error ? error.message : String(error),
            request: '',
          }))
          update(account, model, { running: false, result })
        }
      }),
    )
    void client.invalidateQueries({ queryKey: accountsKey })
  }

  const done = runs?.filter((run) => run.result).length ?? 0
  const succeeded = runs?.filter((run) => run.result?.ok).length ?? 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="secondary" disabled={accounts.length === 0}>
          <Flame size={16} aria-hidden />
          {m.open}
        </Button>
      </DialogTrigger>
      <DialogContent
        title={m.open}
        description={m.description}
        wide
        closeLabel={m.cancel}
        footer={
          runs ? (
            <>
              <Button
                variant="secondary"
                disabled={running}
                onClick={() => {
                  setRuns(undefined)
                }}
              >
                {m.back}
              </Button>
              <Button
                loading={running}
                onClick={() => {
                  void start()
                }}
              >
                {m.rerun}
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="secondary"
                onClick={() => {
                  onOpenChange(false)
                }}
              >
                {m.cancel}
              </Button>
              <Button
                disabled={total === 0}
                onClick={() => {
                  void start()
                }}
              >
                {fill(m.start, { count: total })}
              </Button>
            </>
          )
        }
      >
        {runs ? (
          <div className="flex flex-col gap-3">
            <p role="status" className="text-sm text-foreground-muted">
              {fill(m.progress, { done, total: runs.length, ok: succeeded })}
            </p>
            <div className="rounded-md border border-border">
              <Table label={m.results}>
                <thead>
                  <tr>
                    <Th>{m.target}</Th>
                    <Th>{m.result}</Th>
                  </tr>
                </thead>
                <tbody>
                  {runs.map((run) => (
                    <tr key={run.account + '\n' + run.model}>
                      <Td>
                        <div className="wrap-anywhere">{run.account}</div>
                        <div className="mt-1 font-mono text-xs wrap-anywhere text-foreground-muted">
                          {run.model}
                        </div>
                      </Td>
                      <Td>
                        {run.result ? (
                          <div className="flex flex-col items-start gap-1">
                            <span className="flex items-center gap-2 whitespace-nowrap">
                              <Badge tone={run.result.ok ? 'success' : 'error'}>
                                {run.result.ok ? m.succeeded : m.failed}
                              </Badge>
                              <span className="text-xs text-foreground-muted">
                                {formatDuration(run.result.duration_ms, locale)}
                              </span>
                            </span>
                            {run.result.error && (
                              <p className="max-w-80 text-xs break-words text-error-text">
                                {run.result.error}
                              </p>
                            )}
                            {run.result.usage?.current && (
                              <span className="text-xs text-foreground-muted">
                                {fill(m.usage, {
                                  used: formatPercent(run.result.usage.current.used_ratio, locale),
                                })}
                              </span>
                            )}
                            {run.result.request && (
                              <Link
                                to={'/requests?id=' + encodeURIComponent(run.result.request)}
                                className="text-xs text-primary hover:underline"
                              >
                                {m.record}
                              </Link>
                            )}
                          </div>
                        ) : (
                          <span className="flex items-center gap-2 text-foreground-muted">
                            {run.running && (
                              <LoaderCircle size={14} aria-hidden className="animate-spin" />
                            )}
                            {run.running ? m.running : m.waiting}
                          </span>
                        )}
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </div>
          </div>
        ) : (
          <div className="grid gap-6 sm:grid-cols-2">
            <Picker
              legend={m.models}
              selectAll={m.selectAll}
              empty={m.noModels}
              options={models.map((model) => ({ value: model.id, label: model.id }))}
              picked={pickedModels}
              onChange={setPickedModels}
            />
            <Picker
              legend={m.accounts}
              selectAll={m.selectAll}
              empty={am.empty}
              options={accounts.map((account) => ({
                value: account.account_id,
                label: account.email ?? account.account_id,
                badge: <Badge tone={stateTone(account.state)}>{sm[account.state]}</Badge>,
              }))}
              picked={pickedAccounts}
              onChange={setPickedAccounts}
            />
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
