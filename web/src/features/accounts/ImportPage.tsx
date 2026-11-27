import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { useEffect, useState } from 'react'

import { appMessages } from '@/app/messages'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { CopyButton } from '@/components/ui/copy-button'
import { Field } from '@/components/ui/field'
import { Input, Textarea } from '@/components/ui/input'
import { PageHeader, Section } from '@/components/ui/page'
import { ErrorState, Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import { DisabledHint, Tooltip } from '@/components/ui/tooltip'
import { AccountTasks } from '@/features/accounts/AccountTasks'
import { importMessages } from '@/features/accounts/messages'
import { chromeProfilesKey, tasksKey } from '@/features/accounts/queries'
import { SelectAllCheckbox } from '@/features/accounts/SelectAllCheckbox'
import type { ChromeProfiles, CookieAccount, PairingToken } from '@/features/accounts/types'
import { api, cliCommand, serviceURL } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// ChromeImport 是从本机 Chrome Profile 导入，已导入的 Profile 可再次导入以更新登录
function ChromeImport() {
  const m = useMessages(importMessages)
  const app = useMessages(appMessages)
  const client = useQueryClient()
  const [selected, setSelected] = useState<string[]>([])
  const [proxy, setProxy] = useState('')
  const profiles = useQuery({
    queryKey: chromeProfilesKey,
    queryFn: () => api<ChromeProfiles>('/api/accounts/import/chrome'),
  })
  const run = useMutation({
    mutationFn: () =>
      api('/api/accounts/import/chrome', { method: 'POST', json: { emails: selected, proxy } }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: tasksKey })
      toast({ tone: 'success', title: m.importStarted })
      setSelected([])
    },
    onError: (error) => {
      toast({
        tone: 'error',
        title: fill(app.actionFailed, { action: m.chromeTitle }),
        detail: error.message,
      })
    },
  })
  if (profiles.isPending) return <Loading label={m.chromeTitle} />
  if (profiles.isError) {
    return (
      <ErrorState
        title={m.chromeTitle}
        error={profiles.error}
        retryLabel={app.retry}
        onRetry={() => void profiles.refetch()}
      />
    )
  }
  const items = profiles.data.profiles
  const importable = items.filter((profile) => profile.importable).map((profile) => profile.email)
  const selectedImportable = importable.filter((email) => selected.includes(email)).length
  const toggle = (email: string) => {
    setSelected((current) =>
      current.includes(email) ? current.filter((item) => item !== email) : [...current, email],
    )
  }
  return (
    <Section title={m.chromeTitle} description={m.chromeHint}>
      {items.length === 0 ? (
        <p className="text-sm text-foreground-muted">{m.noProfiles}</p>
      ) : (
        <div className="-mx-5 -mt-4 border-b border-border">
          <label className="flex cursor-pointer items-center gap-3 border-b border-border px-5 py-3 text-sm text-foreground-muted hover:bg-hover has-disabled:cursor-default has-disabled:opacity-70">
            <SelectAllCheckbox
              selected={selectedImportable}
              total={importable.length}
              onToggle={(checked) => {
                setSelected(checked ? importable : [])
              }}
            />
            {m.selectAllProfiles}
          </label>
          <ul className="flex max-h-[360px] flex-col divide-y divide-border overflow-y-auto">
            {items.map((profile) => (
              <li key={profile.profile + '/' + profile.email}>
                <label className="flex cursor-pointer items-center gap-3 px-5 py-3 hover:bg-hover has-disabled:cursor-default has-disabled:opacity-70">
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={selected.includes(profile.email)}
                    disabled={!profile.importable}
                    onChange={() => {
                      toggle(profile.email)
                    }}
                  />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <Tooltip
                      overflow
                      label={profile.email || profile.display_name || profile.profile}
                    >
                      <span className="truncate text-base font-medium">
                        {profile.email || profile.display_name || profile.profile}
                      </span>
                    </Tooltip>
                    <Tooltip
                      overflow
                      label={[profile.display_name, profile.profile].filter(Boolean).join(' · ')}
                    >
                      <span className="truncate text-xs text-foreground-muted">
                        {[profile.display_name, profile.profile].filter(Boolean).join(' · ')}
                      </span>
                    </Tooltip>
                  </span>
                  {!profile.importable ? (
                    <Badge tone="warning">
                      {profile.reason === 'read_failed'
                        ? m.readFailed
                        : profile.reason === 'needs_reauth'
                          ? m.needsReauth
                          : m.notSignedIn}
                    </Badge>
                  ) : profile.imported ? (
                    <Badge tone="neutral">{m.imported}</Badge>
                  ) : null}
                </label>
              </li>
            ))}
          </ul>
        </div>
      )}
      <Field id="chrome-proxy" label={m.proxy} description={m.proxyHint}>
        <Input
          id="chrome-proxy"
          value={proxy}
          placeholder="http://127.0.0.1:7890"
          onChange={(event) => {
            setProxy(event.target.value)
          }}
        />
      </Field>
      <div>
        <DisabledHint reason={selected.length === 0 ? m.selectFirst : ''}>
          <Button
            disabled={selected.length === 0}
            loading={run.isPending}
            onClick={() => {
              run.mutate()
            }}
          >
            {m.importSelected}
          </Button>
        </DisabledHint>
      </div>
    </Section>
  )
}

// discoverDelay 是输入停止后开始识别 Cookie 账号的等待毫秒数
const discoverDelay = 600

// CookieImport 是从 Cookie 导入：粘贴后自动识别其中登录的账号，选中的账号在后台导入，输入变化后旧的识别结果立即失效
function CookieImport() {
  const m = useMessages(importMessages)
  const app = useMessages(appMessages)
  const client = useQueryClient()
  const [cookies, setCookies] = useState('')
  const [proxy, setProxy] = useState('')
  const [input, setInput] = useState({ cookies: '', proxy: '' })
  const [flipped, setFlipped] = useState<ReadonlySet<number>>(new Set())
  useEffect(() => {
    const timer = setTimeout(() => {
      setInput({ cookies: cookies.trim(), proxy })
    }, discoverDelay)
    return () => {
      clearTimeout(timer)
    }
  }, [cookies, proxy])
  const discover = useQuery({
    queryKey: ['cookie-accounts', input],
    queryFn: async ({ signal }) =>
      (
        await api<{ accounts: CookieAccount[] }>('/api/accounts/import/cookie/discover', {
          method: 'POST',
          json: input,
          signal,
        })
      ).accounts,
    enabled: input.cookies !== '',
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
  })
  const current = input.cookies !== '' && input.cookies === cookies.trim() && input.proxy === proxy
  const found = current ? (discover.data ?? []) : []
  const checked = (account: CookieAccount) => account.imported === flipped.has(account.auth_user)
  const selected = found.filter(checked)
  const run = useMutation({
    mutationFn: () =>
      api('/api/accounts/import/cookie', {
        method: 'POST',
        json: {
          ...input,
          accounts: selected.map(({ email, auth_user }) => ({ email, auth_user })),
        },
      }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: tasksKey })
      toast({ tone: 'success', title: m.importStarted })
      setCookies('')
      setInput({ cookies: '', proxy })
      setFlipped(new Set())
    },
    onError: (error) => {
      toast({
        tone: 'error',
        title: fill(app.actionFailed, { action: m.cookieTitle }),
        detail: error.message,
      })
    },
  })
  return (
    <Section title={m.cookieTitle} description={m.cookieHint}>
      <Callout tone="muted">{m.cookieRotationNotice}</Callout>
      <Field id="cookie-value" label={m.cookieValue}>
        <Textarea
          id="cookie-value"
          rows={5}
          value={cookies}
          onChange={(event) => {
            setCookies(event.target.value)
            setFlipped(new Set())
          }}
        />
      </Field>
      {discover.isFetching && (
        <p role="status" className="flex items-center gap-2 text-sm text-foreground-muted">
          <LoaderCircle size={14} aria-hidden className="animate-spin" />
          {m.discovering}
        </p>
      )}
      {discover.isError && current && (
        <Callout tone="error" role="alert">
          {discover.error.message}
        </Callout>
      )}
      {found.length > 0 && (
        <fieldset className="flex flex-col">
          <legend className="mb-2 text-sm font-medium">{m.accountsFound}</legend>
          <ul className="flex flex-col divide-y divide-border rounded-md border border-border">
            {found.map((account) => (
              <li key={account.auth_user}>
                <label className="flex cursor-pointer items-center gap-3 px-3 py-2 hover:bg-hover">
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={checked(account)}
                    onChange={() => {
                      const next = new Set(flipped)
                      if (!next.delete(account.auth_user)) next.add(account.auth_user)
                      setFlipped(next)
                    }}
                  />
                  <Tooltip overflow label={account.email}>
                    <span className="min-w-0 flex-1 truncate text-base">{account.email}</span>
                  </Tooltip>
                  {account.imported && <Badge tone="neutral">{m.imported}</Badge>}
                </label>
              </li>
            ))}
          </ul>
        </fieldset>
      )}
      <Field id="cookie-proxy" label={m.proxy} description={m.proxyHint}>
        <Input
          id="cookie-proxy"
          value={proxy}
          placeholder="http://127.0.0.1:7890"
          onChange={(event) => {
            setProxy(event.target.value)
          }}
        />
      </Field>
      <div>
        <DisabledHint reason={selected.length === 0 ? m.selectFirst : ''}>
          <Button
            disabled={selected.length === 0}
            loading={run.isPending}
            onClick={() => {
              run.mutate()
            }}
          >
            {m.importSelected}
          </Button>
        </DisabledHint>
      </div>
    </Section>
  )
}

// pairingCommand 是在登录了 Chrome 的电脑上上传账号的命令
const pairingCommand = cliCommand + ' setup --remote ' + serviceURL

// pairingInsecure 表示当前页面经 HTTP 访问非本机地址，setup --remote 不接受这种地址
const pairingInsecure =
  window.location.protocol !== 'https:' &&
  !['localhost', '[::1]'].includes(window.location.hostname) &&
  !/^127\.\d+\.\d+\.\d+$/.test(window.location.hostname)

// countdown 把剩余毫秒显示为 分:秒
function countdown(ms: number) {
  const seconds = Math.ceil(ms / 1000)
  return String(Math.floor(seconds / 60)) + ':' + String(seconds % 60).padStart(2, '0')
}

// pairingKey 是本标签页保存最近生成的配对令牌的键，刷新页面后令牌与剩余有效时间仍显示
const pairingKey = 'gemini-web2api.pairing'

// savedPairing 读取本标签页保存的配对令牌
function savedPairing(): PairingToken | undefined {
  try {
    const saved = sessionStorage.getItem(pairingKey)
    return saved ? (JSON.parse(saved) as PairingToken) : undefined
  } catch {
    return undefined
  }
}

// RemotePairing 生成配对令牌并给出 setup --remote 命令，令牌保存在本标签页直到过期
function RemotePairing() {
  const m = useMessages(importMessages)
  const app = useMessages(appMessages)
  const { language } = useI18n()
  const now = useNow(1000)
  const [pairing, setPairing] = useState(savedPairing)
  const create = useMutation({
    mutationFn: () => api<PairingToken>('/api/pairing', { method: 'POST' }),
    onSuccess: (token) => {
      setPairing(token)
      try {
        sessionStorage.setItem(pairingKey, JSON.stringify(token))
      } catch {
        // 会话存储不可用时只在本次页面显示
      }
    },
    onError: (error) => {
      toast({
        tone: 'error',
        title: fill(app.actionFailed, { action: m.createPairing }),
        detail: error.message,
      })
    },
  })
  const remaining = pairing ? new Date(pairing.expires_at).getTime() - now.getTime() : 0
  return (
    <Section title={m.pairingTitle} description={m.pairingHint}>
      {pairingInsecure && <Callout tone="warning">{m.pairingInsecure}</Callout>}
      {pairing && remaining <= 0 && <Callout tone="warning">{m.pairingExpired}</Callout>}
      {pairing && remaining > 0 && (
        <div className="flex flex-col gap-3">
          {[
            { label: m.pairingCommand, value: pairingCommand },
            { label: m.pairingToken, value: pairing.token },
          ].map((item) => (
            <div key={item.label} className="flex flex-col gap-1">
              <span className="text-xs font-medium text-foreground-muted">{item.label}</span>
              <div className="flex items-center gap-2 rounded-md border border-border bg-surface py-[6px] pr-[6px] pl-3">
                <code className="min-w-0 flex-1 overflow-x-auto font-mono whitespace-nowrap select-all">
                  {item.value}
                </code>
                <CopyButton value={item.value} label={m.copy} copiedLabel={m.copied} />
              </div>
            </div>
          ))}
          <p className="text-xs text-foreground-muted">
            {fill(m.pairingExpires, {
              time: formatDateTime(new Date(pairing.expires_at), now, language),
              remaining: countdown(remaining),
            })}
          </p>
        </div>
      )}
      <div>
        <Button
          variant={pairing && remaining > 0 ? 'secondary' : 'primary'}
          loading={create.isPending}
          onClick={() => {
            create.mutate()
          }}
        >
          {m.createPairing}
        </Button>
      </div>
    </Section>
  )
}

// ImportPage 是导入账号页：Chrome Profile、Cookie 与远程配对三种来源
export function ImportPage() {
  const m = useMessages(importMessages)
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      <AccountTasks />
      <div className="flex w-full flex-col gap-3">
        <ChromeImport />
        <CookieImport />
        <RemotePairing />
      </div>
    </>
  )
}
