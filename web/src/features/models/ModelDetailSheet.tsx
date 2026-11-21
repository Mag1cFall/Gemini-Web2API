import { useMutation, useQueryClient } from '@tanstack/react-query'

import { Dialog, DialogContent } from '@/components/ui/dialog'
import { toast } from '@/components/ui/toast'
import { useAccountLabel } from '@/features/accounts/queries'
import { modelsMessages } from '@/features/models/messages'
import type { Model } from '@/features/models/types'
import { DetailList, DetailSection } from '@/features/requests/RequestParts'
import { formatAccountBindings, parseAccountBindings } from '@/features/settings/apiKeys'
import { configKey, useConfig } from '@/features/settings/queries'
import type { ServiceConfig } from '@/features/settings/types'
import { FilterMenu } from '@/features/usage/FilterMenu'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// answeredTools 是由账号默认模型回答、按实际回答的模型计费的官网工具
const answeredTools = new Set(['canvas', 'deep_research', 'guided_learning'])

// usd 返回美元单价文字
function usd(value: number) {
  return '$' + value.toFixed(2)
}

// priceItems 返回模型详情中的单价行：有单价时列出输入与输出、图片或视频输出与长上下文单价，音乐模型按首计价，Gem、默认模型与工具模型按实际回答的模型计费
function priceItems(model: Model, m: (typeof modelsMessages)['zh-CN']) {
  const price = model.price
  if (!price) {
    const answered =
      model.id.startsWith('gem-') ||
      model.id === 'gemini-default' ||
      answeredTools.has(model.tool ?? '')
    return [{ label: m.price, value: answered ? m.priceByAnswer : m.priceNone }]
  }
  if (price.song) return [{ label: m.priceSong, value: usd(price.song) }]
  const items = [
    {
      label: m.price,
      value: fill(m.priceTokens, { input: usd(price.input), output: usd(price.output) }),
    },
  ]
  if (price.media) {
    items.push({
      label: model.tool === 'video' ? m.priceVideo : m.priceImage,
      value: usd(price.media),
    })
  }
  if (price.long_input && price.long_output) {
    items.push({
      label: m.priceLong,
      value: fill(m.priceTokens, { input: usd(price.long_input), output: usd(price.long_output) }),
    })
  }
  return items
}

// ModelDetailSheet 是一个模型的详情对话框：概要与该模型可用的账号，勾选账号后立即写入 MODEL_ACCOUNTS
export function ModelDetailSheet({ model, onClose }: { model: Model | null; onClose: () => void }) {
  const m = useMessages(modelsMessages)
  const { language } = useI18n()
  const client = useQueryClient()
  const config = useConfig()
  const accountLabel = useAccountLabel()
  const save = useMutation({
    mutationFn: ({ id, accounts }: { id: string; accounts: string[] }) => {
      const bindings = parseAccountBindings(config.data?.model_accounts ?? '')
      bindings.set(id, accounts)
      return api<ServiceConfig>('/api/config', {
        method: 'PUT',
        json: { model_accounts: formatAccountBindings(bindings) },
      })
    },
    onSuccess: (data) => {
      client.setQueryData(configKey, data)
      toast({ tone: 'success', title: m.accountsSaved })
    },
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.saveFailed, { message: error.message }) })
    },
  })
  if (!model) return null
  const selected = parseAccountBindings(config.data?.model_accounts ?? '').get(model.id) ?? []
  const contextWindow =
    model.min_context_window === model.max_context_window
      ? formatNumber(model.max_context_window, language)
      : formatNumber(model.min_context_window, language) +
        '–' +
        formatNumber(model.max_context_window, language)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        wide
        focusContent
        closeLabel={m.close}
        title={model.display_name || model.id}
        description={<code className="font-mono">{model.id}</code>}
      >
        <div className="flex flex-col gap-6">
          <DetailSection title={m.summary}>
            {model.description && (
              <p className="text-sm text-foreground-muted">{model.description}</p>
            )}
            <DetailList
              items={[
                ...(model.alias_of ? [{ label: m.aliasOf, value: model.alias_of }] : []),
                { label: m.context, value: contextWindow },
                { label: m.thinking, value: model.thinking_levels.join(' · ') || m.none },
                { label: m.capabilities, value: model.capabilities.join(' · ') || m.none },
                ...priceItems(model, m),
              ]}
            />
          </DetailSection>
          {!model.alias_of && config.data && (
            <DetailSection title={m.boundAccounts}>
              <div className="flex min-w-0 flex-col items-start gap-2">
                <FilterMenu
                  label={
                    selected.length === 0 ? m.allAccounts : selected.map(accountLabel).join(', ')
                  }
                  options={model.accounts}
                  selected={selected}
                  format={accountLabel}
                  disabled={save.isPending}
                  onChange={(accounts) => {
                    save.mutate({ id: model.id, accounts })
                  }}
                />
                <p className="text-sm text-foreground-muted">{m.boundAccountsHint}</p>
              </div>
            </DetailSection>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
