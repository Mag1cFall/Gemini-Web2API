import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { CopyButton } from '@/components/ui/copy-button'
import { Field } from '@/components/ui/field'
import { Input, Textarea } from '@/components/ui/input'
import { PageHeader, Section } from '@/components/ui/page'
import { ErrorState, Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import { AccountTasks } from '@/features/accounts/AccountTasks'
import { importMessages } from '@/features/accounts/messages'
import { chromeProfilesKey, tasksKey } from '@/features/accounts/queries'
import type { ChromeProfiles, CookieAccount, PairingToken } from '@/features/accounts/types'
import { api, serviceURL } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// ChromeImport 是从本机 Chrome Profile 导入
function ChromeImport() {
  const m = useMessages(importMessages)
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
      toast({ tone: 'error', title: error.message })
    },
  })
  if (profiles.isPending) return <Loading label={m.chromeTitle} />
  if (profiles.isError) {
    return (
      <ErrorState
        title={m.chromeTitle}
        error={profiles.error}
        retryLabel={m.chromeTitle}
        onRetry={() => void profiles.refetch()}
      />
    )
  }
  const items = profiles.data.profiles
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
        <ul className="-mx-5 -mt-4 flex flex-col divide-y divide-border border-b border-border">
          {items.map((profile) => (
            <li key={profile.profile + '/' + profile.email}>
              <label className="flex cursor-pointer items-center gap-3 px-5 py-3 hover:bg-hover has-disabled:cursor-default has-disabled:opacity-70">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={selected.includes(profile.email)}
                  disabled={profile.imported || !profile.importable}
                  onChange={() => {
                    toggle(profile.email)
                  }}
                />
                <span className="flex min-w-0 flex-1 flex-col">
                  <span
                    className="truncate text-base font-medium"
                    title={profile.email || profile.display_name || profile.profile}
                  >
                    {profile.email || profile.display_name || profile.profile}
                  </span>
                  <span
                    className="truncate text-xs text-foreground-muted"
                    title={[profile.display_name, profile.profile].filter(Boolean).join(' · ')}
                  >
                    {[profile.display_name, profile.profile].filter(Boolean).join(' · ')}
                  </span>
                </span>
                {profile.imported ? (
                  <Badge tone="neutral">{m.imported}</Badge>
                ) : !profile.importable ? (
                  <Badge tone="warning">
                    {profile.reason === 'read_failed'
                      ? m.readFailed
                      : profile.reason === 'needs_reauth'
                        ? m.needsReauth
                        : m.notSignedIn}
                  </Badge>
                ) : null}
              </label>
            </li>
          ))}
        </ul>
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
        <Button
          disabled={selected.length === 0}
          loading={run.isPending}
          onClick={() => {
            run.mutate()
          }}
        >
          {m.importSelected}
        </Button>
      </div>
    </Section>
  )
}

// discoverDelay 是输入停止后开始识别 Cookie 账号的等待毫秒数
const discoverDelay = 600

// CookieImport 是从 Cookie 导入：粘贴后自动识别其中登录的账号，选中的账号在后台导入，输入变化后旧的识别结果立即失效
function CookieImport() {
  const m = useMessages(importMessages)
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
      toast({ tone: 'error', title: error.message })
    },
  })
  return (
    <Section title={m.cookieTitle} description={m.cookieHint}>
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
                  <span className="min-w-0 flex-1 truncate text-base" title={account.email}>
                    {account.email}
                  </span>
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
        <Button
          disabled={selected.length === 0}
          loading={run.isPending}
          onClick={() => {
            run.mutate()
          }}
        >
          {m.importSelected}
        </Button>
      </div>
    </Section>
  )
}

// pairingCommand 是在登录了 Chrome 的电脑上上传账号的命令
const pairingCommand =
  (navigator.userAgent.includes('Windows') ? '.\\gemini-web2api.exe' : './gemini-web2api') +
  ' setup --remote ' +
  serviceURL

// pairingInsecure 表示当前页面经 HTTP 访问非本机地址，setup --remote 不接受这种地址
const pairingInsecure =
  window.location.protocol !== 'https:' &&
  !['localhost', '[::1]'].includes(window.location.hostname) &&
  !/^127\.\d+\.\d+\.\d+$/.test(window.location.hostname)

// RemotePairing 生成配对令牌并给出 setup --remote 命令
function RemotePairing() {
  const m = useMessages(importMessages)
  const { locale } = useI18n()
  const create = useMutation({
    mutationFn: () => api<PairingToken>('/api/pairing', { method: 'POST' }),
    onError: (error) => {
      toast({ tone: 'error', title: error.message })
    },
  })
  const pairing = create.data
  return (
    <Section title={m.pairingTitle} description={m.pairingHint}>
      {pairingInsecure && <Callout tone="warning">{m.pairingInsecure}</Callout>}
      {pairing && (
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
              time: formatDateTime(new Date(pairing.expires_at), new Date(), locale),
            })}
          </p>
        </div>
      )}
      <div>
        <Button
          variant={pairing ? 'secondary' : 'primary'}
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
