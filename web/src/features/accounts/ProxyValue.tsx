import { accountsMessages } from '@/features/accounts/messages'
import type { Account } from '@/features/accounts/types'
import { useMessages } from '@/lib/i18n'

// ProxyValue 显示账号实际使用的代理，source 为真时在前面标出代理来源
export function ProxyValue({ account, source = false }: { account: Account; source?: boolean }) {
  const m = useMessages(accountsMessages)
  if (account.proxy_source === 'direct') return <>{m.direct}</>
  const sources = {
    fixed: m.proxySourceFixed,
    pool: m.proxySourcePool,
    system: m.proxySourceSystem,
  }
  return (
    <>
      {source && account.proxy_source && account.proxy && (
        <span className="mr-2 font-sans text-xs">{sources[account.proxy_source]}</span>
      )}
      {account.proxy ?? m.none}
    </>
  )
}
