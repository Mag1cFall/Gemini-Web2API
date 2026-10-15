import { useQuery } from '@tanstack/react-query'

import type { Account } from '@/features/accounts/types'
import { api } from '@/lib/api'

// accountsKey 是账户列表的查询键，事件流推送时直接写入
export const accountsKey = ['accounts'] as const

// useAccounts 读取全部账户
export function useAccounts() {
  return useQuery({
    queryKey: accountsKey,
    queryFn: () => api<{ accounts: Account[] }>('/api/accounts'),
  })
}
