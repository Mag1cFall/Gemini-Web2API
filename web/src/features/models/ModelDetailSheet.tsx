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

// ModelDetailSheet 是一个模型的右侧详情：概要与该模型可用的账号，勾选账号后立即写入 MODEL_ACCOUNTS
export function ModelDetailSheet({ model, onClose }: { model: Model | null; onClose: () => void }) {
  const m = useMessages(modelsMessages)
  const { locale } = useI18n()
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
      ? formatNumber(model.max_context_window, locale)
      : formatNumber(model.min_context_window, locale) +
        '–' +
        formatNumber(model.max_context_window, locale)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        layout="sheet"
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
