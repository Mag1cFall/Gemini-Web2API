import { useMutation, useQueryClient } from '@tanstack/react-query'
import { type ReactNode, useState } from 'react'
import { useNavigate, useParams } from 'react-router'

import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Callout } from '@/components/ui/callout'
import { Input, Textarea } from '@/components/ui/input'
import { NumberInput } from '@/components/ui/number-input'
import { Card, CardHeader, PageHeader } from '@/components/ui/page'
import { Select } from '@/components/ui/select'
import { ErrorState, Loading } from '@/components/ui/states'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toast } from '@/components/ui/toast'
import { ListInput } from '@/features/settings/ListInput'
import { settingsMessages } from '@/features/settings/messages'
import { ProxyPoolCard } from '@/features/settings/ProxyPoolCard'
import { configKey, proxiesKey, useConfig } from '@/features/settings/queries'
import type { ConfigPatch, ServiceConfig } from '@/features/settings/types'
import { api } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// tabs 是设置页的标签页，与 /settings/:section 对应
const tabs = ['service', 'upstream', 'security', 'logging'] as const
type Tab = (typeof tabs)[number]

// editableKeys 是可写回 .env 的配置项
const editableKeys = [
  'proxy_api_key',
  'proxy',
  'save_history',
  'account_fallback',
  'model_mapping',
  'account_cooldown',
  'session_ttl',
  'init_timeout',
  'per_account_concurrency',
  'routing_strategy',
  'log_level',
  'request_body_log',
  'log_file',
  'log_max_total_mb',
  'admin_auth_enabled',
  'admin_username',
  'listen_addr',
  'auth_states',
  'log_format',
] as const

// restartKeys 是重启服务后才生效的配置项
const restartKeys = new Set<string>(['listen_addr', 'auth_states', 'log_format'])

// SettingRow 是行卡片中的一行：左侧名称、说明与生效方式，右侧控件
function SettingRow({
  id,
  label,
  hint,
  restart = false,
  align = 'center',
  children,
}: {
  id: string
  label: string
  hint: string
  restart?: boolean
  align?: 'center' | 'start'
  children: ReactNode
}) {
  const m = useMessages(settingsMessages)
  return (
    <div
      className={
        'flex flex-col gap-3 px-5 py-4 md:flex-row md:justify-between md:gap-6 ' +
        (align === 'start' ? 'md:items-start' : 'md:items-center')
      }
    >
      <div className="flex min-w-0 flex-col gap-1">
        <label htmlFor={id} className="text-base">
          {label}
        </label>
        <p id={id + '-hint'} className="text-xs text-foreground-muted">
          {hint}
          <span className="ml-2 rounded-sm bg-muted px-[6px] whitespace-nowrap text-foreground-muted">
            {restart ? m.restart : m.immediate}
          </span>
        </p>
      </div>
      <div className="flex w-full shrink-0 justify-end md:w-[260px]">{children}</div>
    </div>
  )
}

// SettingCard 是一组设置行共用的分组卡片：标题行在上，行之间以细线分隔
function SettingCard({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: ReactNode
}) {
  return (
    <Card>
      <CardHeader title={title} description={description} />
      <div className="flex flex-col divide-y divide-border">{children}</div>
    </Card>
  )
}

// SwitchControl 是左侧带开、关文字的开关
function SwitchControl({
  id,
  checked,
  onCheckedChange,
}: {
  id: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  const m = useMessages(settingsMessages)
  return (
    <span className="flex items-center gap-2 text-sm text-foreground-muted">
      <span aria-hidden>{checked ? m.on : m.off}</span>
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onCheckedChange}
        aria-describedby={id + '-hint'}
      />
    </span>
  )
}

// SettingsPage 按标签页编辑 .env 中的服务配置，有改动时底部浮出保存栏
export function SettingsPage() {
  const m = useMessages(settingsMessages)
  const client = useQueryClient()
  const navigate = useNavigate()
  const { section } = useParams()
  const tab: Tab = tabs.find((item) => item === section) ?? 'service'
  const config = useConfig()
  const [edited, setEdited] = useState<ServiceConfig | null>(null)
  const [password, setPassword] = useState('')
  const save = useMutation({
    mutationFn: (patch: ConfigPatch) =>
      api<ServiceConfig>('/api/config', { method: 'PUT', json: patch }),
    onSuccess: (data) => {
      client.setQueryData(configKey, data)
      void client.invalidateQueries({ queryKey: proxiesKey })
      setEdited(null)
      setPassword('')
      toast({ tone: 'success', title: m.saved })
    },
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.saveFailed, { message: error.message }) })
    },
  })
  if (config.isPending) return <Loading label={m.title} />
  if (config.isError) {
    return (
      <ErrorState
        title={m.title}
        error={config.error}
        retryLabel={m.retry}
        onRetry={() => void config.refetch()}
      />
    )
  }
  const original = config.data
  const value = edited ?? original
  const set = <K extends keyof ServiceConfig>(key: K, next: ServiceConfig[K]) => {
    setEdited({ ...value, [key]: next })
  }
  const patch: Record<string, unknown> = {}
  for (const key of editableKeys) {
    if (value[key] !== original[key]) patch[key] = value[key]
  }
  if (password) patch.admin_password = password
  const dirty = Object.keys(patch).length > 0
  const labels: Record<string, string> = {
    listen_addr: m.listenAddr,
    auth_states: m.authStates,
    log_format: m.logFormat,
  }
  const text = (key: 'listen_addr' | 'auth_states' | 'admin_username', placeholder = '') => (
    <Input
      id={key}
      value={value[key]}
      placeholder={placeholder}
      aria-describedby={key + '-hint'}
      onChange={(event) => {
        set(key, event.target.value)
      }}
    />
  )
  const duration = (
    key: 'account_cooldown' | 'session_ttl' | 'init_timeout',
    placeholder: string,
  ) => (
    <Input
      id={key}
      value={value[key]}
      placeholder={placeholder}
      aria-describedby={key + '-hint'}
      className="w-[120px]"
      onChange={(event) => {
        set(key, event.target.value)
      }}
    />
  )
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      {original.restart_required && (
        <Callout tone="warning" role="status">
          {fill(m.pendingRestart, {
            fields: original.restart_fields.map((field) => labels[field] ?? field).join('、'),
          })}
        </Callout>
      )}
      <Tabs
        value={tab}
        onValueChange={(next) => {
          void navigate('/settings/' + next, { replace: true })
        }}
      >
        <TabsList aria-label={m.title}>
          <TabsTrigger value="service">{m.tabService}</TabsTrigger>
          <TabsTrigger value="upstream">{m.tabUpstream}</TabsTrigger>
          <TabsTrigger value="security">{m.tabSecurity}</TabsTrigger>
          <TabsTrigger value="logging">{m.tabLogging}</TabsTrigger>
        </TabsList>
      </Tabs>

      {tab === 'service' && (
        <SettingCard title={m.tabService}>
          <SettingRow id="listen_addr" label={m.listenAddr} hint={m.listenAddrHint} restart>
            {text('listen_addr', '127.0.0.1:8007')}
          </SettingRow>
          <SettingRow id="proxy" label={m.proxy} hint={m.proxyHint} align="start">
            <ListInput
              id="proxy"
              value={value.proxy}
              itemLabel={m.proxy}
              addLabel={m.proxyAdd}
              removeLabel={m.proxyRemove}
              placeholder="http://127.0.0.1:7890"
              onChange={(next) => {
                set('proxy', next)
              }}
            />
          </SettingRow>
          <SettingRow id="auth_states" label={m.authStates} hint={m.authStatesHint} restart>
            {text('auth_states', 'auth')}
          </SettingRow>
        </SettingCard>
      )}
      {tab === 'service' && <ProxyPoolCard />}

      {tab === 'upstream' && (
        <SettingCard title={m.tabUpstream}>
          <SettingRow id="save_history" label={m.saveHistory} hint={m.saveHistoryHint}>
            <SwitchControl
              id="save_history"
              checked={value.save_history}
              onCheckedChange={(next) => {
                set('save_history', next)
              }}
            />
          </SettingRow>
          <SettingRow id="account_fallback" label={m.accountFallback} hint={m.accountFallbackHint}>
            <SwitchControl
              id="account_fallback"
              checked={value.account_fallback}
              onCheckedChange={(next) => {
                set('account_fallback', next)
              }}
            />
          </SettingRow>
          <SettingRow
            id="model_mapping"
            label={m.modelMapping}
            hint={m.modelMappingHint}
            align="start"
          >
            <Textarea
              id="model_mapping"
              rows={3}
              placeholder="gpt-4o:gemini-3.8-flash"
              aria-describedby="model_mapping-hint"
              value={value.model_mapping.split(',').filter(Boolean).join('\n')}
              onChange={(event) => {
                set(
                  'model_mapping',
                  event.target.value
                    .split('\n')
                    .map((line) => line.trim())
                    .filter(Boolean)
                    .join(','),
                )
              }}
            />
          </SettingRow>
          <SettingRow
            id="per_account_concurrency"
            label={m.perAccountConcurrency}
            hint={m.perAccountConcurrencyHint}
          >
            <NumberInput
              id="per_account_concurrency"
              aria-describedby="per_account_concurrency-hint"
              value={value.per_account_concurrency}
              min={1}
              max={64}
              decreaseLabel={m.decrease}
              increaseLabel={m.increase}
              onChange={(next) => {
                set('per_account_concurrency', next)
              }}
            />
          </SettingRow>
          <SettingRow id="routing_strategy" label={m.routingStrategy} hint={m.routingStrategyHint}>
            <Badge tone="info">{m.routingFillFirst}</Badge>
          </SettingRow>
          <SettingRow id="account_cooldown" label={m.accountCooldown} hint={m.accountCooldownHint}>
            {duration('account_cooldown', '2m')}
          </SettingRow>
          <SettingRow id="session_ttl" label={m.sessionTtl} hint={m.sessionTtlHint}>
            {duration('session_ttl', '30m')}
          </SettingRow>
          <SettingRow id="init_timeout" label={m.initTimeout} hint={m.initTimeoutHint}>
            {duration('init_timeout', '20s')}
          </SettingRow>
        </SettingCard>
      )}

      {tab === 'security' && (
        <SettingCard title={m.publicApi}>
          <SettingRow id="proxy_api_key" label={m.apiKey} hint={m.apiKeyHint} align="start">
            <ListInput
              id="proxy_api_key"
              value={value.proxy_api_key}
              itemLabel={m.apiKey}
              addLabel={m.apiKeyAdd}
              removeLabel={m.apiKeyRemove}
              onChange={(next) => {
                set('proxy_api_key', next)
              }}
            />
          </SettingRow>
        </SettingCard>
      )}
      {tab === 'security' && (
        <SettingCard title={m.adminAuth} description={m.adminLoginPurpose}>
          <SettingRow id="admin_auth_enabled" label={m.adminAuthSwitch} hint={m.adminAuthHint}>
            <SwitchControl
              id="admin_auth_enabled"
              checked={value.admin_auth_enabled}
              onCheckedChange={(next) => {
                set('admin_auth_enabled', next)
              }}
            />
          </SettingRow>
          <SettingRow id="admin_username" label={m.adminUsername} hint={m.adminUsernameHint}>
            {text('admin_username', 'admin')}
          </SettingRow>
          <SettingRow
            id="admin_password"
            label={m.adminPassword}
            hint={original.admin_password_set ? m.adminPasswordKeep : m.adminPasswordHint}
          >
            <Input
              id="admin_password"
              type="password"
              autoComplete="new-password"
              aria-describedby="admin_password-hint"
              placeholder={original.admin_password_set ? '••••••••' : ''}
              value={password}
              onChange={(event) => {
                setPassword(event.target.value)
              }}
            />
          </SettingRow>
        </SettingCard>
      )}

      {tab === 'logging' && (
        <SettingCard title={m.tabLogging}>
          <SettingRow id="log_level" label={m.logLevel} hint={m.logLevelHint}>
            <Select
              id="log_level"
              label={m.logLevel}
              value={value.log_level}
              className="w-[120px]"
              onValueChange={(next) => {
                set('log_level', next)
              }}
              options={(['debug', 'info', 'warn', 'error'] as const).map((level) => ({
                value: level,
                label: level,
              }))}
            />
          </SettingRow>
          <SettingRow id="log_format" label={m.logFormat} hint={m.logFormatHint} restart>
            <Select
              id="log_format"
              label={m.logFormat}
              value={value.log_format}
              className="w-[120px]"
              onValueChange={(next) => {
                set('log_format', next)
              }}
              options={[
                { value: 'text', label: 'text' },
                { value: 'json', label: 'json' },
              ]}
            />
          </SettingRow>
          <SettingRow id="log_file" label={m.logFile} hint={m.logFileHint}>
            <SwitchControl
              id="log_file"
              checked={value.log_file}
              onCheckedChange={(next) => {
                set('log_file', next)
              }}
            />
          </SettingRow>
          <SettingRow id="log_max_total_mb" label={m.logMaxTotal} hint={m.logMaxTotalHint}>
            <NumberInput
              id="log_max_total_mb"
              aria-describedby="log_max_total_mb-hint"
              value={value.log_max_total_mb}
              min={20}
              max={100000}
              decreaseLabel={m.decrease}
              increaseLabel={m.increase}
              onChange={(next) => {
                set('log_max_total_mb', next)
              }}
            />
          </SettingRow>
          <SettingRow id="request_body_log" label={m.requestBodyLog} hint={m.requestBodyLogHint}>
            <SwitchControl
              id="request_body_log"
              checked={value.request_body_log}
              onCheckedChange={(next) => {
                set('request_body_log', next)
              }}
            />
          </SettingRow>
        </SettingCard>
      )}

      {dirty && (
        <div
          role="region"
          aria-label={m.unsaved}
          className="sticky bottom-4 z-sticky flex w-full flex-wrap items-center justify-between gap-3 rounded-md border border-border bg-surface px-4 py-3 shadow-popover"
        >
          <span className="text-sm">
            {m.unsaved}
            {Object.keys(patch).some((key) => restartKeys.has(key)) && (
              <span className="ml-2 text-foreground-muted">· {m.restart}</span>
            )}
          </span>
          <div className="flex items-center gap-2">
            <Button
              onClick={() => {
                setEdited(null)
                setPassword('')
              }}
            >
              {m.discard}
            </Button>
            <Button
              variant="primary"
              loading={save.isPending}
              onClick={() => {
                save.mutate(patch)
              }}
            >
              {m.save}
            </Button>
          </div>
        </div>
      )}
    </>
  )
}
