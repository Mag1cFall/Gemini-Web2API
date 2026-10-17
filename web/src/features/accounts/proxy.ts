import type { Account } from '@/features/accounts/types'

// fixedProxy 返回账号的固定代理，direct 表示直连，从代理池或系统代理分配时为空
export function fixedProxy(account: Account) {
  if (account.proxy_source === 'direct') return 'direct'
  return account.proxy_source === 'fixed' ? (account.proxy ?? '') : ''
}
