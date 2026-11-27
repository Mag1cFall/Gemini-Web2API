import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Boxes } from 'lucide-react'
import { useState } from 'react'
import { useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Badge } from '@/components/ui/badge'
import { Card, CardEmpty, CardHeader, CountBadge, PageHeader } from '@/components/ui/page'
import { SearchInput } from '@/components/ui/search-input'
import { ErrorState, PagePlaceholder } from '@/components/ui/states'
import { Switch } from '@/components/ui/switch'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { modelsMessages } from '@/features/models/messages'
import { ModelDetailSheet } from '@/features/models/ModelDetailSheet'
import { modelsKey, useModels } from '@/features/models/queries'
import type { Model } from '@/features/models/types'
import { toggleHiddenModels } from '@/features/models/visibility'
import { configKey, useConfig } from '@/features/settings/queries'
import type { ServiceConfig } from '@/features/settings/types'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { fill, plural, useI18n, useMessages } from '@/lib/i18n'

// ModelsPage 列出全部模型目录并逐个切换 API 开放状态，保存到模型目录刷新完成前禁用全部开关，搜索词写入地址的 q 参数，点模型名称打开详情
export function ModelsPage() {
  const m = useMessages(modelsMessages)
  const app = useMessages(appMessages)
  const { language } = useI18n()
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const client = useQueryClient()
  const models = useModels()
  const config = useConfig()
  const [selected, setSelected] = useState<string | null>(null)
  const toggle = useMutation({
    mutationFn: ({ target, open }: { target: Model; open: boolean }) => {
      const hidden_models = toggleHiddenModels(
        config.data?.hidden_models ?? '',
        target,
        models.data?.models ?? [],
        open,
      )
      return api<ServiceConfig>('/api/config', {
        method: 'PUT',
        json: { hidden_models },
      })
    },
    onSuccess: async (data, { target, open }) => {
      client.setQueryData(configKey, data)
      toast({
        tone: 'success',
        title: fill(open ? m.accessOpened : m.accessClosed, { model: target.id }),
      })
      await client.invalidateQueries({ queryKey: modelsKey })
    },
    onError: (error) => {
      toast({ tone: 'error', title: m.saveFailed, detail: error.message })
    },
  })
  if (models.isPending) {
    return <PagePlaceholder title={m.title} description={m.description} label={m.title} />
  }
  if (models.isError) {
    return (
      <ErrorState
        title={m.title}
        error={models.error}
        retryLabel={app.retry}
        onRetry={() => void models.refetch()}
      />
    )
  }
  const allModels = models.data.models
  const locked = Boolean(config.data?.locked.HIDDEN_MODELS)
  const needle = q.trim().toLowerCase()
  const rows = allModels.filter(
    (model) =>
      !needle ||
      model.id.toLowerCase().includes(needle) ||
      model.display_name.toLowerCase().includes(needle),
  )
  const openCount = rows.filter((model) => !model.hidden).length
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      <Card className="@container">
        <CardHeader
          title={m.catalog}
          actions={
            <>
              <CountBadge>
                {fill(m.openCount, {
                  open: formatNumber(openCount, language),
                  total: formatNumber(rows.length, language),
                })}
              </CountBadge>
              <SearchInput
                aria-label={m.search}
                placeholder={m.search}
                value={q}
                onChange={(event) => {
                  const search = new URLSearchParams(params)
                  if (event.target.value) search.set('q', event.target.value)
                  else search.delete('q')
                  setParams(search, { replace: true })
                }}
                className="w-[240px]"
              />
            </>
          }
        />
        {allModels.length === 0 ? (
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
                <Th className="text-right">{m.apiAccess}</Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((model) => {
                const capabilities = model.capabilities.join(' · ') || m.none
                const hidden = Boolean(model.hidden)
                const control = model.alias_of ? (
                  <Tooltip label={fill(m.accessFollowsTarget, { target: model.alias_of })}>
                    <span className="inline-flex">
                      <Badge tone={hidden ? 'neutral' : 'success'}>
                        {hidden ? m.hidden : m.open}
                      </Badge>
                    </span>
                  </Tooltip>
                ) : (
                  <Switch
                    aria-label={fill(m.toggleAccess, { model: model.id })}
                    checked={!hidden}
                    disabled={locked || !config.data || toggle.isPending}
                    onCheckedChange={(open) => {
                      toggle.mutate({ target: model, open })
                    }}
                  />
                )
                return (
                  <tr
                    key={model.id}
                    className={hidden ? 'opacity-70 hover:bg-hover' : 'hover:bg-hover'}
                  >
                    <Td className="whitespace-nowrap">
                      <div className="flex flex-col gap-1">
                        <div className="flex h-6 items-center gap-2">
                          <button
                            type="button"
                            aria-label={fill(m.openDetail, { model: model.id })}
                            onClick={() => {
                              setSelected(model.id)
                            }}
                            className="text-left font-medium text-primary hover:text-primary-hover hover:underline"
                          >
                            {model.display_name || model.id}
                          </button>
                          {model.default && <Badge tone="info">{m.default}</Badge>}
                          {hidden && <Badge tone="neutral">{m.hidden}</Badge>}
                        </div>
                        <span className="font-mono text-xs text-foreground-muted">
                          {model.id}
                          {model.alias_of ? ' · ' + fill(m.alias, { target: model.alias_of }) : ''}
                        </span>
                      </div>
                    </Td>
                    <Td className="max-w-0">
                      <Tooltip overflow label={capabilities}>
                        <div className="truncate font-mono text-foreground-muted">
                          {capabilities}
                        </div>
                      </Tooltip>
                    </Td>
                    <Td className="font-mono whitespace-nowrap text-foreground-muted @max-[860px]:hidden">
                      {model.thinking_levels.join(' · ') || m.none}
                    </Td>
                    <Td className="text-right whitespace-nowrap text-foreground-muted @max-[640px]:hidden">
                      {formatNumber(model.max_context_window, language)}
                    </Td>
                    <Td className="whitespace-nowrap">
                      <div className="flex flex-nowrap gap-1">
                        <Badge tone="success">
                          {fill(
                            plural(language, model.accounts.length, m.availableOne, m.available),
                            { count: model.accounts.length },
                          )}
                        </Badge>
                        {model.cooling_accounts.length > 0 && (
                          <Badge tone="warning">
                            {fill(m.cooling, { count: model.cooling_accounts.length })}
                          </Badge>
                        )}
                      </div>
                    </Td>
                    <Td className="text-right whitespace-nowrap">
                      <div className="flex justify-end">
                        {locked && !model.alias_of ? (
                          <Tooltip label={m.accessLockedEnv}>
                            <span className="inline-flex">{control}</span>
                          </Tooltip>
                        ) : (
                          control
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
      <ModelDetailSheet
        model={allModels.find((model) => model.id === selected) ?? null}
        onClose={() => {
          setSelected(null)
        }}
      />
    </>
  )
}
