import { Boxes } from 'lucide-react'
import { useState } from 'react'

import { Badge } from '@/components/ui/badge'
import { Card, CardEmpty, CardHeader, CountBadge, PageHeader } from '@/components/ui/page'
import { SearchInput } from '@/components/ui/search-input'
import { ErrorState, Loading } from '@/components/ui/states'
import { Table, Td, Th } from '@/components/ui/table'
import { modelsMessages } from '@/features/models/messages'
import { useModels } from '@/features/models/queries'
import { formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// ModelsPage 列出公开模型目录
export function ModelsPage() {
  const m = useMessages(modelsMessages)
  const { locale } = useI18n()
  const [q, setQ] = useState('')
  const models = useModels()
  if (models.isPending) return <Loading label={m.title} />
  if (models.isError) {
    return (
      <ErrorState
        title={m.title}
        error={models.error}
        retryLabel={m.title}
        onRetry={() => void models.refetch()}
      />
    )
  }
  const needle = q.trim().toLowerCase()
  const rows = models.data.models.filter(
    (model) =>
      !needle ||
      model.id.toLowerCase().includes(needle) ||
      model.display_name.toLowerCase().includes(needle),
  )
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      <Card className="@container">
        <CardHeader
          title={m.catalog}
          actions={
            <>
              <CountBadge>{formatNumber(rows.length, locale)}</CountBadge>
              <SearchInput
                aria-label={m.search}
                placeholder={m.search}
                value={q}
                onChange={(event) => {
                  setQ(event.target.value)
                }}
                className="w-[240px]"
              />
            </>
          }
        />
        {models.data.models.length === 0 ? (
          <CardEmpty icon={<Boxes size={20} aria-hidden />}>
            <span className="flex flex-col gap-1">
              <span>{m.empty}</span>
              <span className="text-xs">{m.emptyHint}</span>
            </span>
          </CardEmpty>
        ) : (
          <Table label={m.title}>
            <thead>
              <tr>
                <Th>{m.model}</Th>
                <Th className="w-full">{m.capabilities}</Th>
                <Th className="@max-[860px]:hidden">{m.thinking}</Th>
                <Th className="text-right @max-[640px]:hidden">{m.context}</Th>
                <Th>{m.accounts}</Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((model) => {
                const capabilities = model.capabilities.join(' · ') || m.none
                return (
                  <tr key={model.id} className="hover:bg-hover">
                    <Td className="whitespace-nowrap">
                      <div className="flex flex-col gap-1">
                        <div className="flex h-6 items-center gap-2">
                          <span className="font-medium">{model.display_name || model.id}</span>
                          {model.default && <Badge tone="info">{m.default}</Badge>}
                        </div>
                        <span className="font-mono text-xs text-foreground-muted">
                          {model.id}
                          {model.alias_of ? ' · ' + fill(m.alias, { target: model.alias_of }) : ''}
                        </span>
                      </div>
                    </Td>
                    <Td className="max-w-0">
                      <div
                        title={capabilities}
                        className="truncate font-mono text-foreground-muted"
                      >
                        {capabilities}
                      </div>
                    </Td>
                    <Td className="font-mono whitespace-nowrap text-foreground-muted @max-[860px]:hidden">
                      {model.thinking_levels.join(' · ') || m.none}
                    </Td>
                    <Td className="text-right whitespace-nowrap text-foreground-muted @max-[640px]:hidden">
                      {formatNumber(model.max_context_window, locale)}
                    </Td>
                    <Td className="whitespace-nowrap">
                      <div className="flex flex-nowrap gap-1">
                        <Badge tone="success">
                          {fill(m.available, { count: model.accounts.length })}
                        </Badge>
                        {model.cooling_accounts.length > 0 && (
                          <Badge tone="warning">
                            {fill(m.cooling, { count: model.cooling_accounts.length })}
                          </Badge>
                        )}
                      </div>
                    </Td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        )}
      </Card>
    </>
  )
}
