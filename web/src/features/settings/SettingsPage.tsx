import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FolderOpen, KeyRound } from 'lucide-react'
import { createContext, type ReactNode, use, useEffect, useState } from 'react'
import { useBlocker, useNavigate, useParams } from 'react-router'

import { Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { DurationInput } from '@/components/ui/duration-input'
import { HelpTip } from '@/components/ui/help-tip'
import { Input } from '@/components/ui/input'
import { NumberInput } from '@/components/ui/number-input'
import { Card, CardHeader, PageHeader } from '@/components/ui/page'
import { Select } from '@/components/ui/select'
import { ErrorState, PagePlaceholder } from '@/components/ui/states'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { useAccountLabel, useAccounts } from '@/features/accounts/queries'
import { useModels } from '@/features/models/queries'
import { formatAccountBindings, maskKey, parseAccountBindings } from '@/features/settings/apiKeys'
import { DirectoryDialog } from '@/features/settings/DirectoryDialog'
import { ListInput } from '@/features/settings/ListInput'
import { settingsMessages } from '@/features/settings/messages'
import { OrderList } from '@/features/settings/OrderList'
import { PairInput } from '@/features/settings/PairInput'
import { ProxyPoolCard } from '@/features/settings/ProxyPoolCard'
import { configKey, proxiesKey, useConfig } from '@/features/settings/queries'
import { type ConfigPatch, editableKeys, type ServiceConfig } from '@/features/settings/types'
import { type SettingErrors, validateSettings } from '@/features/settings/validate'
import { FilterMenu } from '@/features/usage/FilterMenu'
import { api } from '@/lib/api'
import { useKeepPage } from '@/lib/desktop'
import { formatDuration } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// tabs 是设置页的标签页，与 /settings/:section 对应
const tabs = ['service', 'upstream', 'security', 'logging'] as const
type Tab = (typeof tabs)[number]

// restartKeys 是重启服务后才生效的配置项
const restartKeys = new Set<string>(['listen_addr', 'auth_states', 'log_format'])

// envNames 是各配置项在 .env 中的名称
const envNames: Record<string, string> = {
  proxy_api_key: 'PROXY_API_KEY',
  proxy: 'PROXY',
  save_history: 'GEMINI_SAVE_HISTORY',
  continue_session: 'GEMINI_CONTINUE_SESSION',
  nonstream_keepalive: 'NONSTREAM_KEEPALIVE',
  fake_stream: 'FAKE_STREAM',
  model_mapping: 'MODEL_MAPPING',
  account_cooldown: 'ACCOUNT_COOLDOWN',
  session_ttl: 'SESSION_TTL',
  init_timeout: 'INIT_TIMEOUT',
  per_account_concurrency: 'PER_ACCOUNT_CONCURRENCY',
  routing_strategy: 'ROUTING_STRATEGY',
  log_level: 'LOG_LEVEL',
  request_body_log: 'REQUEST_BODY_LOG',
  log_file: 'LOG_FILE',
  log_max_total_mb: 'LOG_MAX_TOTAL_MB',
  admin_auth_enabled: 'ADMIN_AUTH_ENABLED',
  admin_username: 'ADMIN_USERNAME',
  admin_password: 'ADMIN_PASSWORD',
  listen_addr: 'LISTEN_ADDR',
  auth_states: 'GEMINI_AUTH_STATES',
  log_format: 'LOG_FORMAT',
  api_key_accounts: 'API_KEY_ACCOUNTS',
  account_order: 'ACCOUNT_ORDER',
  model_accounts: 'MODEL_ACCOUNTS',
  first_event_hedge: 'FIRST_EVENT_HEDGE',
  first_event_hedge_models: 'FIRST_EVENT_HEDGE_MODELS',
}

// LockedContext 提供由环境变量或启动参数决定的配置项，键为 .env 名
const LockedContext = createContext<ServiceConfig['locked']>({})

// SettingRow 是行卡片中的一行：左侧名称、说明提示与重启后生效标记，右侧控件，格式错误显示在控件下方
function SettingRow({
  id,
  label,
  hint,
  restart = false,
  align = 'center',
  wide = false,
  error,
  children,
}: {
  id: string
  label: string
  hint: ReactNode
  restart?: boolean
  align?: 'center' | 'start'
  wide?: boolean
  error?: string | undefined
  children: ReactNode
}) {
  const m = useMessages(settingsMessages)
  const key = envNames[id] ?? id
  const locked = use(LockedContext)[key]
  return (
    <div
      className={
        'flex flex-col gap-3 px-5 py-4 md:flex-row md:justify-between md:gap-6 ' +
        (align === 'start' ? 'md:items-start' : 'md:items-center')
      }
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <label htmlFor={id} className="text-base">
          {label}
        </label>
        <HelpTip label={fill(m.helpFor, { name: label })}>{hint}</HelpTip>
        {locked ? (
          <Tooltip label={locked === 'env' ? fill(m.lockedEnvHint, { key }) : m.lockedFlagHint}>
            <button type="button" className="inline-flex cursor-default align-middle">
              <Tag>{locked === 'env' ? m.lockedEnv : m.lockedFlag}</Tag>
            </button>
          </Tooltip>
        ) : (
          restart && (
            <span className="rounded-sm bg-muted px-[6px] text-xs whitespace-nowrap text-foreground-muted">
              {m.restart}
            </span>
          )
        )}
      </div>
      <div
        className={
          'flex w-full min-w-0 shrink-0 flex-col items-end gap-1 ' +
          (wide ? 'md:w-[520px]' : 'md:w-[260px]')
        }
      >
        <fieldset disabled={Boolean(locked)} className="flex w-full min-w-0 justify-end">
          {children}
        </fieldset>
        {error && (
          <p id={id + '-error'} role="alert" className="self-start text-xs text-error-text">
            {error}
          </p>
        )}
      </div>
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
        aria-describedby={id + '-error'}
      />
    </span>
  )
}

// KeyAccounts 为每个 API key 选择可用账号，读写 API_KEY_ACCOUNTS 的 key:账号|账号 写法，不选账号的 key 可用全部账号
function KeyAccounts({
  keys,
  value,
  onChange,
}: {
  keys: string[]
  value: string
  onChange: (value: string) => void
}) {
  const m = useMessages(settingsMessages)
  const accounts = useAccounts().data?.accounts ?? []
  const bindings = parseAccountBindings(value)
  if (keys.length === 0) {
    return <p className="text-sm text-foreground-muted">{m.keyAccountsNone}</p>
  }
  const label = (id: string) => accounts.find((item) => item.account_id === id)?.email ?? id
  return (
    <ul className="flex w-full flex-col gap-2">
      {keys.map((key) => {
        const selected = bindings.get(key) ?? []
        return (
          <li key={key} className="flex min-w-0 items-center justify-between gap-2">
            <Tooltip label={key}>
              <code className="min-w-0 truncate font-mono text-sm">{maskKey(key)}</code>
            </Tooltip>
            <FilterMenu
              label={selected.length === 0 ? m.keyAccountsAll : selected.map(label).join(', ')}
              options={accounts.map((item) => item.account_id)}
              selected={selected}
              format={label}
              onChange={(next) => {
                const updated = new Map([...bindings].filter(([item]) => keys.includes(item)))
                updated.set(key, next)
                onChange(formatAccountBindings(updated))
              }}
            />
          </li>
        )
      })}
    </ul>
  )
}

// randomKey 返回以 sk- 开头、带 192 位随机数的 API key
function randomKey() {
  const bytes = crypto.getRandomValues(new Uint8Array(24))
  return 'sk-' + [...bytes].map((byte) => byte.toString(16).padStart(2, '0')).join('')
}

// HedgeStat 对应 GET /api/hedge 的一项
interface HedgeStat {
  model: string
  p95_ms: number
  samples: number
  ready: boolean
}

// HedgeStats 列出各备用请求模型近 24 小时原请求首事件等待的当前 p95
function HedgeStats() {
  const m = useMessages(settingsMessages)
  const { language } = useI18n()
  const stats = useQuery({
    queryKey: ['hedge'],
    queryFn: async () => (await api<{ models: HedgeStat[] }>('/api/hedge')).models,
    refetchInterval: 30_000,
  })
  if (!stats.data || stats.data.length === 0) return null
  return (
    <ul className="flex w-full flex-col gap-[2px] text-xs text-foreground-muted">
      {stats.data.map((item) => (
        <li key={item.model} className="wrap-anywhere">
          {item.ready
            ? fill(m.hedgeP95, {
                model: item.model,
                p95: formatDuration(item.p95_ms, language),
                samples: item.samples,
              })
            : fill(m.hedgeCollecting, { model: item.model, samples: item.samples })}
        </li>
      ))}
    </ul>
  )
}

// LeaveGuard 在有未保存改动时拦截站内跳转并询问是否离开，关闭或刷新标签页时由浏览器询问
function LeaveGuard({ dirty }: { dirty: boolean }) {
  const m = useMessages(settingsMessages)
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  )
  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault()
    }
    window.addEventListener('beforeunload', warn)
    return () => {
      window.removeEventListener('beforeunload', warn)
    }
  }, [dirty])
  return (
    <Dialog
      open={blocker.state === 'blocked'}
      onOpenChange={(open) => {
        if (!open) blocker.reset?.()
      }}
    >
      <DialogContent
        title={m.leaveTitle}
        description={m.leaveHint}
        closeLabel={m.stay}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                blocker.reset?.()
              }}
            >
              {m.stay}
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                blocker.proceed?.()
              }}
            >
              {m.leave}
            </Button>
          </>
        }
      />
    </Dialog>
  )
}

// SettingsPage 按标签页编辑 .env 中的服务配置，格式错误在该行即时显示，有改动时底部浮出保存栏，离开前确认
export function SettingsPage() {
  const m = useMessages(settingsMessages)
  const client = useQueryClient()
  const navigate = useNavigate()
  const { section } = useParams()
  const tab: Tab = tabs.find((item) => item === section) ?? 'service'
  const config = useConfig()
  const accounts = useAccounts().data?.accounts ?? []
  const accountLabel = useAccountLabel()
  const catalog = (useModels().data?.models ?? []).map((model) => model.id)
  const [edited, setEdited] = useState<ServiceConfig | null>(null)
  const [password, setPassword] = useState('')
  const [browsing, setBrowsing] = useState<{ start: string; replace: (path: string) => void }>()
  useKeepPage(edited !== null || password !== '')
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
  if (config.isPending) {
    return <PagePlaceholder title={m.title} description={m.description} label={m.title} />
  }
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
  const problems: SettingErrors = validateSettings(value, password, original.admin_password_set)
  const errorText = {
    listen: m.errorListen,
    proxy: m.errorProxy,
    authStates: m.errorAuthStates,
    password: m.errorPassword,
    incomplete: m.errorPairIncomplete,
    duplicate: m.errorPairDuplicate,
  }
  const errorOf = (key: keyof SettingErrors) => {
    const problem = problems[key]
    return problem && errorText[problem]
  }
  const invalid = Object.keys(problems).length > 0
  const labels: Record<string, string> = {
    listen_addr: m.listenAddr,
    proxy: m.proxy,
    auth_states: m.authStates,
    save_history: m.saveHistory,
    continue_session: m.continueSession,
    nonstream_keepalive: m.nonstreamKeepalive,
    fake_stream: m.fakeStream,
    model_mapping: m.modelMapping,
    per_account_concurrency: m.perAccountConcurrency,
    routing_strategy: m.routingStrategy,
    account_cooldown: m.accountCooldown,
    session_ttl: m.sessionTtl,
    init_timeout: m.initTimeout,
    proxy_api_key: m.apiKey,
    admin_auth_enabled: m.adminAuthSwitch,
    admin_username: m.adminUsername,
    log_level: m.logLevel,
    log_format: m.logFormat,
    log_file: m.logFile,
    log_max_total_mb: m.logMaxTotal,
    request_body_log: m.requestBodyLog,
    api_key_accounts: m.keyAccounts,
    account_order: m.accountOrder,
    model_accounts: m.modelAccounts,
    first_event_hedge: m.firstEventHedge,
    first_event_hedge_models: m.hedgeModels,
  }
  const ordered = value.account_order.split(',').filter(Boolean)
  const accountOrder = [
    ...ordered.filter((id) => accounts.some((item) => item.account_id === id)),
    ...accounts.map((item) => item.account_id).filter((id) => !ordered.includes(id)),
  ]
  const hedgeMode = ['off', 'auto'].includes(value.first_event_hedge)
    ? value.first_event_hedge
    : 'fixed'
  const envLabel = (key: string) =>
    labels[Object.keys(envNames).find((field) => envNames[field] === key) ?? ''] ?? key
  const text = (key: 'listen_addr' | 'admin_username', placeholder = '') => (
    <Input
      id={key}
      value={value[key]}
      placeholder={placeholder}
      aria-invalid={problems[key] ? true : undefined}
      aria-describedby={key + '-error'}
      onChange={(event) => {
        set(key, event.target.value)
      }}
    />
  )
  const modelSelect = (current: string, onChange: (next: string) => void) => (
    <Select
      label={m.chooseModel}
      size="sm"
      value={current || '__none'}
      className="w-full font-mono"
      onValueChange={(next) => {
        onChange(next === '__none' ? '' : next)
      }}
      options={[
        ...(current ? [] : [{ value: '__none', label: m.chooseModel }]),
        ...[...new Set([...(current ? [current] : []), ...catalog])].map((id) => ({
          value: id,
          label: id,
        })),
      ]}
    />
  )
  const duration = (key: 'account_cooldown' | 'session_ttl' | 'init_timeout', label: string) => (
    <DurationInput
      id={key}
      value={value[key]}
      aria-describedby={key + '-error'}
      units={{ s: m.unitSeconds, m: m.unitMinutes, h: m.unitHours }}
      unitLabel={fill(m.unitOf, { name: label })}
      onChange={(next) => {
        set(key, next)
      }}
    />
  )
  return (
    <LockedContext value={original.locked}>
      <PageHeader title={m.title} description={m.description} />
      {original.invalid.length > 0 && (
        <Callout tone="warning" role="status">
          {m.invalidNotice}
          <ul className="mt-1 list-disc pl-5">
            {original.invalid.map((item) => (
              <li key={item.key}>
                <code className="font-mono">
                  {item.key}
                  {item.value && '=' + item.value}
                </code>
                {' · ' + envLabel(item.key)}
              </li>
            ))}
          </ul>
        </Callout>
      )}
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
          <SettingRow
            id="listen_addr"
            label={m.listenAddr}
            hint={m.listenAddrHint}
            restart
            error={errorOf('listen_addr')}
          >
            {text('listen_addr', '127.0.0.1:8007')}
          </SettingRow>
          <SettingRow
            id="proxy"
            label={m.proxy}
            hint={m.proxyHint}
            align="start"
            error={errorOf('proxy')}
          >
            <ListInput
              id="proxy"
              value={value.proxy}
              itemLabel={m.proxy}
              addLabel={m.proxyAdd}
              removeLabel={m.proxyRemove}
              placeholder="http://127.0.0.1:7890"
              invalid={Boolean(problems.proxy)}
              onChange={(next) => {
                set('proxy', next)
              }}
            />
          </SettingRow>
          <SettingRow
            id="auth_states"
            label={m.authStates}
            hint={m.authStatesHint}
            restart
            align="start"
            error={errorOf('auth_states')}
          >
            <ListInput
              id="auth_states"
              value={value.auth_states}
              itemLabel={m.authStates}
              addLabel={m.addRow}
              removeLabel={m.removeRow}
              placeholder="auth"
              invalid={Boolean(problems.auth_states)}
              rowAction={(index, replace) => (
                <Tooltip label={m.browse}>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={m.browse}
                    onClick={() => {
                      setBrowsing({
                        start: value.auth_states.split(',')[index]?.trim() ?? '',
                        replace,
                      })
                    }}
                  >
                    <FolderOpen size={16} aria-hidden />
                  </Button>
                </Tooltip>
              )}
              onChange={(next) => {
                set('auth_states', next)
              }}
            />
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
          <SettingRow id="continue_session" label={m.continueSession} hint={m.continueSessionHint}>
            <SwitchControl
              id="continue_session"
              checked={value.continue_session}
              onCheckedChange={(next) => {
                set('continue_session', next)
              }}
            />
          </SettingRow>
          <SettingRow
            id="nonstream_keepalive"
            label={m.nonstreamKeepalive}
            hint={m.nonstreamKeepaliveHint}
          >
            <SwitchControl
              id="nonstream_keepalive"
              checked={value.nonstream_keepalive}
              onCheckedChange={(next) => {
                set('nonstream_keepalive', next)
              }}
            />
          </SettingRow>
          <SettingRow id="fake_stream" label={m.fakeStream} hint={m.fakeStreamHint}>
            <SwitchControl
              id="fake_stream"
              checked={value.fake_stream}
              onCheckedChange={(next) => {
                set('fake_stream', next)
              }}
            />
          </SettingRow>
          <SettingRow
            id="model_mapping"
            label={m.modelMapping}
            hint={m.modelMappingHint}
            align="start"
            wide
            error={errorOf('model_mapping')}
          >
            <PairInput
              id="model_mapping"
              value={value.model_mapping}
              invalid={Boolean(problems.model_mapping)}
              addLabel={m.addRow}
              removeLabel={m.removeRow}
              left={(current, onChange) => (
                <Input
                  value={current}
                  placeholder="gpt-4o"
                  aria-label={m.mappingAlias}
                  className="font-mono"
                  onChange={(event) => {
                    onChange(event.target.value)
                  }}
                />
              )}
              right={(current, onChange) => modelSelect(current, onChange)}
              onChange={(next) => {
                set('model_mapping', next)
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
              aria-describedby="per_account_concurrency-error"
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
            <Select
              id="routing_strategy"
              label={m.routingStrategy}
              value={value.routing_strategy}
              className="min-w-[160px]"
              onValueChange={(next) => {
                set('routing_strategy', next)
              }}
              options={[
                { value: 'round-robin', label: m.routingRoundRobin },
                { value: 'fill-first', label: m.routingFillFirst },
              ]}
            />
          </SettingRow>
          <SettingRow
            id="account_order"
            label={m.accountOrder}
            hint={m.accountOrderHint}
            align="start"
          >
            {accountOrder.length === 0 ? (
              <p className="text-sm text-foreground-muted">{m.noAccounts}</p>
            ) : (
              <OrderList
                id="account_order"
                items={accountOrder}
                format={accountLabel}
                dragLabel={m.dragToReorder}
                moveUpLabel={m.moveUp}
                moveDownLabel={m.moveDown}
                onChange={(next) => {
                  set('account_order', next.join(','))
                }}
              />
            )}
          </SettingRow>
          <SettingRow
            id="model_accounts"
            label={m.modelAccounts}
            hint={m.modelAccountsHint}
            align="start"
            wide
            error={errorOf('model_accounts')}
          >
            <PairInput
              id="model_accounts"
              value={value.model_accounts}
              invalid={Boolean(problems.model_accounts)}
              addLabel={m.addRow}
              removeLabel={m.removeRow}
              left={(current, onChange) => modelSelect(current, onChange)}
              right={(current, onChange) => {
                const selected = current.split('|').filter(Boolean)
                return (
                  <FilterMenu
                    label={
                      selected.length === 0
                        ? m.chooseAccounts
                        : selected.map(accountLabel).join(', ')
                    }
                    options={accounts.map((item) => item.account_id)}
                    selected={selected}
                    format={accountLabel}
                    onChange={(next) => {
                      onChange(next.join('|'))
                    }}
                  />
                )
              }}
              onChange={(next) => {
                set('model_accounts', next)
              }}
            />
          </SettingRow>
          <SettingRow id="account_cooldown" label={m.accountCooldown} hint={m.accountCooldownHint}>
            {duration('account_cooldown', m.accountCooldown)}
          </SettingRow>
          <SettingRow id="session_ttl" label={m.sessionTtl} hint={m.sessionTtlHint}>
            {duration('session_ttl', m.sessionTtl)}
          </SettingRow>
          <SettingRow id="init_timeout" label={m.initTimeout} hint={m.initTimeoutHint}>
            {duration('init_timeout', m.initTimeout)}
          </SettingRow>
          <SettingRow
            id="first_event_hedge"
            label={m.firstEventHedge}
            hint={m.firstEventHedgeHint}
            align="start"
          >
            <span className="flex flex-col items-end gap-2">
              <span className="flex items-center gap-2">
                <Select
                  id="first_event_hedge"
                  label={m.firstEventHedge}
                  value={hedgeMode}
                  className="min-w-[128px]"
                  onValueChange={(next) => {
                    set('first_event_hedge', next === 'fixed' ? '30' : next)
                  }}
                  options={[
                    { value: 'off', label: m.hedgeOff },
                    { value: 'auto', label: m.hedgeAuto },
                    { value: 'fixed', label: m.hedgeFixed },
                  ]}
                />
                {hedgeMode === 'fixed' && (
                  <>
                    <NumberInput
                      aria-label={m.hedgeFixed}
                      value={Number(value.first_event_hedge)}
                      min={1}
                      max={600}
                      decreaseLabel={m.decrease}
                      increaseLabel={m.increase}
                      onChange={(next) => {
                        set('first_event_hedge', String(next))
                      }}
                    />
                    <span className="text-sm text-foreground-muted">{m.unitSeconds}</span>
                  </>
                )}
              </span>
              {hedgeMode === 'auto' && <HedgeStats />}
            </span>
          </SettingRow>
          <SettingRow id="first_event_hedge_models" label={m.hedgeModels} hint={m.hedgeModelsHint}>
            <Input
              id="first_event_hedge_models"
              value={value.first_event_hedge_models}
              aria-describedby="first_event_hedge_models-error"
              className="font-mono"
              onChange={(event) => {
                set('first_event_hedge_models', event.target.value)
              }}
            />
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
              actions={(append) => (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    append(randomKey())
                  }}
                >
                  <KeyRound size={16} aria-hidden />
                  {m.generateKey}
                </Button>
              )}
              onChange={(next) => {
                set('proxy_api_key', next)
              }}
            />
          </SettingRow>
          <SettingRow
            id="api_key_accounts"
            label={m.keyAccounts}
            hint={m.keyAccountsHint}
            align="start"
          >
            <KeyAccounts
              keys={value.proxy_api_key.split(',').filter(Boolean)}
              value={value.api_key_accounts}
              onChange={(next) => {
                set('api_key_accounts', next)
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
            error={errorOf('admin_password')}
          >
            <Input
              id="admin_password"
              type="password"
              autoComplete="new-password"
              aria-invalid={problems.admin_password ? true : undefined}
              aria-describedby="admin_password-error"
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
              className="min-w-[120px]"
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
              className="min-w-[120px]"
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
            <span className="flex items-center gap-2">
              <NumberInput
                id="log_max_total_mb"
                aria-describedby="log_max_total_mb-error"
                value={value.log_max_total_mb}
                min={20}
                max={100000}
                decreaseLabel={m.decrease}
                increaseLabel={m.increase}
                onChange={(next) => {
                  set('log_max_total_mb', next)
                }}
              />
              <span className="text-sm text-foreground-muted">{m.megabytes}</span>
            </span>
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
            <Tooltip label={invalid ? m.invalidSave : m.save}>
              <span>
                <Button
                  variant="primary"
                  loading={save.isPending}
                  disabled={invalid}
                  onClick={() => {
                    save.mutate(patch)
                  }}
                >
                  {m.save}
                </Button>
              </span>
            </Tooltip>
          </div>
        </div>
      )}
      <LeaveGuard dirty={dirty} />
      {browsing && (
        <DirectoryDialog
          start={browsing.start}
          onChoose={(path) => {
            browsing.replace(path)
            setBrowsing(undefined)
          }}
          onClose={() => {
            setBrowsing(undefined)
          }}
        />
      )}
    </LockedContext>
  )
}
