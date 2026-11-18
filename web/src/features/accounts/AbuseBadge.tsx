import { Badge } from '@/components/ui/badge'
import { Tooltip } from '@/components/ui/tooltip'
import { accountsMessages } from '@/features/accounts/messages'
import type { AccountAbuse } from '@/features/accounts/types'
import { fill, useMessages } from '@/lib/i18n'

// abuseText 是账号异常标记的说明，带状态码时附在句末
export function abuseText(
  m: { abuseDetail: string; abuseCode: string },
  abuse: AccountAbuse,
): string {
  return abuse.code
    ? m.abuseDetail + ' · ' + fill(m.abuseCode, { code: abuse.code })
    : m.abuseDetail
}

// AbuseBadge 是 Google 给出异常标记的账号的警告徽标，悬停或聚焦时显示说明与状态码
export function AbuseBadge({ abuse }: { abuse: AccountAbuse | undefined }) {
  const m = useMessages(accountsMessages)
  if (!abuse?.restricted) return null
  return (
    <Tooltip label={abuseText(m, abuse)}>
      <button type="button" className="inline-flex cursor-default align-middle">
        <Badge tone="warning">{m.abuseFlag}</Badge>
      </button>
    </Tooltip>
  )
}
