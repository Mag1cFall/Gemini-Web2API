import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback } from 'react'

import { toast } from '@/components/ui/toast'
import { accountsMessages } from '@/features/accounts/messages'
import type { Account } from '@/features/accounts/types'
import { cooldownsKey } from '@/features/models/queries'
import { api } from '@/lib/api'
import { useMessages } from '@/lib/i18n'

// accountsKey 是账户列表的查询键，事件流推送时直接写入
export const accountsKey = ['accounts'] as const

// useAccounts 读取全部账户
export function useAccounts() {
  return useQuery({
    queryKey: accountsKey,
    queryFn: () => api<{ accounts: Account[] }>('/api/accounts'),
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
