import { useMutation, useQueryClient } from '@tanstack/react-query'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/page'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { settingsMessages } from '@/features/settings/messages'
import { proxiesKey, useProxies } from '@/features/settings/queries'
import type { ProxyStatus } from '@/features/settings/types'
import { api } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// ProxyPoolCard 列出代理池中每个代理的检测结果、延迟与绑定的账号，可立即检测，代理池为空时不显示
export function ProxyPoolCard() {
  const m = useMessages(settingsMessages)
  const client = useQueryClient()
  const proxies = useProxies()
  const check = useMutation({
    mutationFn: () => api<{ proxies: ProxyStatus[] }>('/api/proxies/check', { method: 'POST' }),
    onSuccess: (data) => {
      client.setQueryData(proxiesKey, data)
    },
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.poolCheckFailed, { message: error.message }) })
    },
  })
  if (!proxies.data?.length) return null
  return (
    <Card>
      <CardHeader
        title={m.poolTitle}
        description={m.poolDescription}
        actions={
          <Button
            loading={check.isPending}
            onClick={() => {
              check.mutate()
            }}
          >
            {m.poolCheck}
          </Button>
        }
      />
      <Table label={m.poolTitle}>
        <thead>
          <tr>
            <Th>{m.poolProxy}</Th>
            <Th>{m.poolState}</Th>
            <Th>{m.poolLatency}</Th>
            <Th>{m.poolAccounts}</Th>
          </tr>
        </thead>
        <tbody>
          {proxies.data.map((proxy) => (
            <tr key={proxy.url}>
              <Td>
                <div className="font-mono whitespace-nowrap">{proxy.url}</div>
                {proxy.error && (
                  <div className="max-w-cell truncate text-xs text-error-text" title={proxy.error}>
                    {proxy.error}
                  </div>
                )}
              </Td>
              <Td>
                {proxy.cooldown_until && new Date(proxy.cooldown_until) > new Date() ? (
                  <Badge tone="warning">{m.poolCooldown}</Badge>
                ) : !proxy.checked_at ? (
                  <Badge tone="neutral">{m.poolUnchecked}</Badge>
                ) : proxy.healthy ? (
                  <Badge tone="success">{m.poolHealthy}</Badge>
                ) : (
                  <Badge tone="error">{m.poolUnhealthy}</Badge>
                )}
              </Td>
              <Td className="whitespace-nowrap text-foreground-muted">
                {proxy.checked_at && proxy.healthy ? String(proxy.latency_ms) + ' ms' : '—'}
              </Td>
              <Td className="text-foreground-muted">
                {proxy.accounts.length ? proxy.accounts.join(', ') : '—'}
              </Td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  )
}
