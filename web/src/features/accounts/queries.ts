import { type QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useSyncExternalStore } from 'react'

import { toast, toastDuration } from '@/components/ui/toast'
import { runBatch } from '@/features/accounts/batch'
import { accountsMessages } from '@/features/accounts/messages'
import type { Account, AccountTask } from '@/features/accounts/types'
import { cooldownsKey } from '@/features/models/queries'
import { api } from '@/lib/api'
import { fill, serverText, useMessages } from '@/lib/i18n'

// accountsKey 是账户列表的查询键，事件流推送时直接写入
export const accountsKey = ['accounts'] as const

// tasksKey 是导入账号任务的查询键
export const tasksKey = ['account-tasks'] as const

// chromeProfilesKey 是本机 Chrome 账号列表的查询键
export const chromeProfilesKey = ['chrome-profiles'] as const

// useAccountTasks 读取导入账号任务，有任务进行中时每秒刷新
export function useAccountTasks() {
  return useQuery({
    queryKey: tasksKey,
    queryFn: async () => (await api<{ tasks: AccountTask[] }>('/api/accounts/tasks')).tasks,
    refetchInterval: (query) =>
      query.state.data?.some((task) => task.state === 'running') ? 1000 : false,
  })
}

// deleting 是等待撤销期结束、尚未向服务端发送删除的账号
let deleting: ReadonlySet<string> = new Set()
const deletingListeners = new Set<() => void>()

// setDeleting 替换等待删除的账号并通知订阅者
function setDeleting(next: ReadonlySet<string>) {
  deleting = next
  for (const listener of deletingListeners) listener()
}

// subscribeDeleting 订阅等待删除的账号变化
function subscribeDeleting(listener: () => void) {
  deletingListeners.add(listener)
  return () => {
    deletingListeners.delete(listener)
  }
}

// useAccounts 读取全部账户，等待撤销期结束的账号不在其中
export function useAccounts() {
  const hidden = useSyncExternalStore(subscribeDeleting, () => deleting)
  return useQuery({
    queryKey: accountsKey,
    queryFn: () => api<{ accounts: Account[] }>('/api/accounts'),
    select: useCallback(
      (data: { accounts: Account[] }) =>
        hidden.size === 0
          ? data
          : { accounts: data.accounts.filter((account) => !hidden.has(account.account_id)) },
      [hidden],
    ),
  })
}

// deleteBatchLimit 是撤销期结束后同时发送的删除请求数
const deleteBatchLimit = 3

// useDeleteAccounts 返回删除账号的函数：账号立即从列表隐藏并提示可撤销，5 秒内撤销时不向服务端发送删除
export function useDeleteAccounts() {
  const m = useMessages(accountsMessages)
  const client = useQueryClient()
  return useCallback(
    (accounts: Account[]) => {
      const ids = accounts.map((account) => account.account_id)
      setDeleting(new Set([...deleting, ...ids]))
      const restore = () => {
        setDeleting(new Set([...deleting].filter((id) => !ids.includes(id))))
      }
      const timer = setTimeout(() => {
        void removeAccounts(client, ids).then((failures) => {
          restore()
          void client.invalidateQueries({ queryKey: accountsKey })
          if (failures.length === 0) return
          toast({
            tone: 'error',
            title: fill(m.batchFailures, {
              result: fill(m.batchResult, {
                action: m.delete,
                succeeded: ids.length - failures.length,
                failed: failures.length,
              }),
              items: failures
                .map((failure) =>
                  fill(m.batchFailure, { id: failure.id, message: serverText(failure.message) }),
                )
                .join(m.listSeparator),
            }),
          })
        })
      }, toastDuration)
      const first = accounts[0]
      toast({
        tone: 'success',
        title:
          accounts.length === 1 && first
            ? fill(m.deletedOne, { id: first.email ?? first.account_id })
            : fill(m.deleted, { count: accounts.length }),
        action: {
          label: m.undo,
          onClick: () => {
            clearTimeout(timer)
            restore()
          },
        },
      })
    },
    [client, m],
  )
}

// removeAccounts 向服务端删除账号，返回失败项
function removeAccounts(client: QueryClient, ids: string[]) {
  return runBatch(ids, deleteBatchLimit, async (id) => {
    await api('/api/accounts/' + encodeURIComponent(id), { method: 'DELETE' })
    client.setQueryData<{ accounts: Account[] }>(
      accountsKey,
      (data) => data && { accounts: data.accounts.filter((account) => account.account_id !== id) },
    )
  })
}

// useAccountLabel 返回按账号 ID 取首页登录邮箱的函数，邮箱未知时返回账号 ID
export function useAccountLabel() {
  const accounts = useAccounts().data?.accounts
  return useCallback(
    (id: string) => accounts?.find((account) => account.account_id === id)?.email ?? id,
    [accounts],
  )
}

// useClearCooldown 清除账号的冷却，scope 为模型 ID 或表示账号级冷却的 *
export function useClearCooldown() {
  const m = useMessages(accountsMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ account, scope }: { account: string; scope: string }) =>
      api(
        '/api/accounts/' + encodeURIComponent(account) + '/cooldowns/' + encodeURIComponent(scope),
        { method: 'DELETE' },
      ),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accountsKey })
      void client.invalidateQueries({ queryKey: cooldownsKey })
      toast({ tone: 'success', title: m.cooldownCleared })
    },
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
}
