import cherryStudioIcon from '@lobehub/icons-static-svg/icons/cherrystudio-color.svg'
import claudeCodeIcon from '@lobehub/icons-static-svg/icons/claudecode-color.svg'
import codexIcon from '@lobehub/icons-static-svg/icons/codex-color.svg'
import magpieIcon from '@lobehub/icons-static-svg/icons/magpie.svg'
import openClawIcon from '@lobehub/icons-static-svg/icons/openclaw-color.svg'
import opencodeIcon from '@lobehub/icons-static-svg/icons/opencode.svg'
import sillyTavernIcon from '@lobehub/icons-static-svg/icons/sillytavern.svg'
import { CircleAlert, CircleCheck, Terminal } from 'lucide-react'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { CodeBlock } from '@/components/ui/code-block'
import { CopyButton } from '@/components/ui/copy-button'
import { Card, CardHeader, PageHeader } from '@/components/ui/page'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { ErrorState, Loading } from '@/components/ui/states'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { connectMessages } from '@/features/connect/messages'
import ompIcon from '@/features/connect/omp.svg'
import {
  claudeEnv,
  claudeSettings,
  codexConfig,
  codexEnv,
  curlAnthropic,
  curlGemini,
  curlOpenai,
  magpieImport,
  ompEnv,
  ompModels,
  openclawConfig,
  opencodeConfig,
  type Shell,
  type SnippetInput,
} from '@/features/connect/snippets'
import { useModels } from '@/features/models/queries'
import type { Model } from '@/features/models/types'
import { parseError } from '@/features/playground/protocols'
import { maskKey, modelsForKey, parseKeyAccounts } from '@/features/settings/apiKeys'
import { useConfig } from '@/features/settings/queries'
import { appURL, serviceURL } from '@/lib/api'
import { localizeError } from '@/lib/errorMessages'
import { formatDuration } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// clients 是提供配置的客户端
const clients = [
  'claude-code',
  'codex',
  'opencode',
  'openclaw',
  'omp',
  'magpie',
  'cherry-studio',
  'sillytavern',
  'curl',
] as const
type Client = (typeof clients)[number]

// clientNames 是客户端的显示名
const clientNames: Record<Client, string> = {
  'claude-code': 'Claude Code',
  codex: 'Codex',
  opencode: 'OpenCode',
  openclaw: 'OpenClaw',
  omp: 'omp',
  magpie: 'Magpie',
  'cherry-studio': 'Cherry Studio',
  sillytavern: 'SillyTavern',
  curl: 'curl',
}

// ClientIcon 是客户端标签前的图标，单色图标以遮罩跟随文字颜色
function ClientIcon({ client }: { client: Client }) {
  if (client === 'curl') return <Terminal size={16} aria-hidden />
  if (
    client === 'opencode' ||
    client === 'sillytavern' ||
    client === 'omp' ||
    client === 'magpie'
  ) {
    const mask = {
      opencode: opencodeIcon,
      sillytavern: sillyTavernIcon,
      omp: ompIcon,
      magpie: magpieIcon,
    }[client]
    return (
      <span
        aria-hidden
        className="size-4 bg-current"
        style={{ mask: `url("${mask}") center / contain no-repeat` }}
      />
    )
  }
  const src = {
    'claude-code': claudeCodeIcon,
    codex: codexIcon,
    openclaw: openClawIcon,
    'cherry-studio': cherryStudioIcon,
  }[client]
  return <img src={src} alt="" className="size-4" />
}

// endpoints 返回各协议 SDK 填写的 base URL 与主要接口路径
function endpoints(origin: string) {
  return [
    {
      protocol: 'openai',
      base: origin + '/v1',
      paths: ['/v1/chat/completions', '/v1/responses'],
    },
    { protocol: 'anthropic', base: origin, paths: ['/v1/messages'] },
    { protocol: 'gemini', base: origin, paths: ['/v1beta/models/{model}:generateContent'] },
  ] as const
}

// EndpointList 列出 OpenAI、Anthropic 与 Gemini 三种协议的 base URL 与主要接口
function EndpointList() {
  const m = useMessages(connectMessages)
  const hints = { openai: m.openaiHint, anthropic: m.anthropicHint, gemini: m.geminiHint }
  return (
    <ul className="flex flex-col divide-y divide-border">
      {endpoints(serviceURL).map((item) => (
        <li
          key={item.protocol}
          className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-5 py-3 sm:grid-cols-[160px_minmax(0,1fr)_auto]"
        >
          <div className="min-w-0 max-sm:col-span-2">
            <span className="block text-sm font-medium">{m[item.protocol]}</span>
            <span className="block text-xs text-foreground-muted">{hints[item.protocol]}</span>
          </div>
          <div className="min-w-0">
            <code className="block truncate font-mono text-sm select-all" title={item.base}>
              {item.base}
            </code>
            <span className="block truncate font-mono text-xs text-foreground-subtle">
              {item.paths.join('  ·  ')}
            </span>
          </div>
          <CopyButton
            value={item.base}
            label={fill(m.copyUrl, { name: m[item.protocol] })}
            copiedLabel={m.copied}
          />
        </li>
      ))}
    </ul>
  )
}

// ModelSelect 是带标题的模型下拉框
function ModelSelect({
  id,
  label,
  value,
  models,
  onChange,
}: {
  id: string
  label: string
  value: string
  models: Model[]
  onChange: (value: string) => void
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-xs text-foreground-muted">
        {label}
      </label>
      <Select
        id={id}
        label={label}
        value={value}
        onValueChange={onChange}
        options={models.map((model) => ({ value: model.id, label: model.id }))}
        className="w-full font-mono sm:w-[256px]"
      />
    </div>
  )
}

// TestResult 是连通测试中一个请求的结果
interface TestResult {
  request: string
  ok: boolean
  detail: string
  elapsed: number
}

// ConnectionTest 用当前 API key 读取 /v1/models，成功后向所选模型发一次最短的 Chat Completions 请求，逐条显示请求、结果与耗时，models 是该 key 能请求的模型
function ConnectionTest({ apiKey, models }: { apiKey: string; models: Model[] }) {
  const m = useMessages(connectMessages)
  const { locale } = useI18n()
  const [chosen, setChosen] = useState<string>()
  const [testing, setTesting] = useState(false)
  const [results, setResults] = useState<TestResult[]>([])
  const model =
    models.find((item) => item.id === chosen) ?? models.find((item) => item.default) ?? models[0]

  // probe 发送一个测试请求并记录结果，read 读取成功响应的说明；连接失败或返回错误时记录原因并返回 false
  const probe = async (
    request: string,
    path: string,
    init: RequestInit,
    read: (response: Response) => Promise<string>,
  ) => {
    const started = performance.now()
    const finish = (ok: boolean, detail: string) => {
      const elapsed = Math.round(performance.now() - started)
      setResults((current) => [...current, { request, ok, detail, elapsed }])
      return ok
    }
    try {
      const response = await fetch(appURL(path), init)
      if (!response.ok) {
        const reason = parseError(await response.text()).slice(0, 200)
        return finish(false, `${String(response.status)} ${reason}`.trim())
      }
      return finish(true, await read(response))
    } catch (error) {
      return finish(
        false,
        error instanceof TypeError ? localizeError('network', error.message) : String(error),
      )
    }
  }

  // run 依次发送两个测试请求，模型列表读取失败时不再发送对话请求
  const run = async () => {
    setTesting(true)
    setResults([])
    const headers = { Authorization: 'Bearer ' + apiKey, 'Content-Type': 'application/json' }
    const listed = await probe('GET /v1/models', '/v1/models', { headers }, async (response) => {
      const body = (await response.json()) as { data: unknown[] }
      return fill(m.testModels, { count: body.data.length })
    })
    if (listed && model) {
      await probe(
        'POST /v1/chat/completions',
        '/v1/chat/completions',
        {
          method: 'POST',
          headers,
          body: JSON.stringify({
            model: model.id,
            messages: [{ role: 'user', content: 'Reply with OK' }],
          }),
        },
        async (response) => {
          const body = (await response.json()) as {
            choices: { message: { content: string | null } }[]
          }
          const text = body.choices[0]?.message.content?.trim() ?? ''
          return text ? fill(m.testReply, { text: text.slice(0, 200) }) : m.testEmptyReply
        },
      )
    }
    setTesting(false)
  }

  return (
    <section
      aria-labelledby="connection-test"
      className="flex flex-col gap-3 border-t border-border px-5 py-4"
    >
      <div>
        <h3 id="connection-test" className="text-base font-medium">
          {m.test}
        </h3>
        <p className="mt-1 text-xs text-foreground-muted">{m.testHelp}</p>
      </div>
      <div className="flex flex-wrap items-end gap-3">
        {model && (
          <ModelSelect
            id="connection-test-model"
            label={m.testModel}
            value={model.id}
            models={models}
            onChange={setChosen}
          />
        )}
        <Button
          variant="primary"
          loading={testing}
          onClick={() => {
            void run()
          }}
        >
          {m.testRun}
        </Button>
      </div>
      {results.length > 0 && (
        <ul
          aria-live="polite"
          className="flex flex-col divide-y divide-border rounded-md border border-border"
        >
          {results.map((item) => (
            <li
              key={item.request}
              className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-2 gap-y-1 px-3 py-2 sm:grid-cols-[auto_auto_minmax(0,1fr)_auto]"
            >
              {item.ok ? (
                <CircleCheck size={16} aria-hidden className="mt-[2px] text-success" />
              ) : (
                <CircleAlert size={16} aria-hidden className="mt-[2px] text-error" />
              )}
              <code className="font-mono text-xs leading-5 text-foreground-muted">
                {item.request}
              </code>
              <span
                className={
                  'text-sm wrap-anywhere max-sm:col-span-3 max-sm:row-start-2 ' +
                  (item.ok ? 'text-foreground' : 'text-error-text')
                }
              >
                {item.detail}
              </span>
              <span className="text-xs leading-5 text-foreground-muted tabular-nums max-sm:col-start-3 max-sm:row-start-1">
                {formatDuration(item.elapsed, locale)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

// ClientConfigs 按客户端给出可直接复制的配置，模型取自实时目录中的对话模型，key 取自已保存的 API key，绑定了账号的 key 只列这些账号的模型
function ClientConfigs() {
  const m = useMessages(connectMessages)
  const app = useMessages(appMessages)
  const [params, setParams] = useSearchParams()
  const client = clients.find((item) => item === params.get('client')) ?? 'claude-code'
  const catalog = useModels()
  const config = useConfig()
  const keys = (config.data?.proxy_api_key ?? '').split(',').filter(Boolean)
  const [choice, setChoice] = useState<{ model?: string; small?: string; key?: string }>({})
  const key = choice.key ?? keys[0] ?? 'gemini-web2api'
  const models = modelsForKey(
    (catalog.data?.models ?? []).filter((model) => !model.tool),
    parseKeyAccounts(config.data?.api_key_accounts ?? '').get(key),
  )
  const [shell, setShell] = useState<Shell>(
    navigator.userAgent.includes('Windows') ? 'windows' : 'unix',
  )
  const origin = serviceURL
  const fallback = m.modelPlaceholder
  const defaultModel = (models.find((model) => model.default) ?? models[0])?.id ?? ''
  const smallDefault = (models.find((model) => model.id.includes('flash')) ?? models[0])?.id ?? ''
  const pick = (chosen: string | undefined, preset: string) =>
    (chosen && models.some((item) => item.id === chosen) ? chosen : preset) || fallback
  const model = pick(choice.model, defaultModel)
  const input: SnippetInput = {
    origin,
    key,
    model,
    smallModel: pick(choice.small, smallDefault),
    models: models.map((item) => ({ id: item.id, name: item.display_name || item.id })),
  }
  const block = (title: string, code: string) => (
    <CodeBlock title={title} code={code} copyLabel={m.copy + ' ' + title} copiedLabel={m.copied} />
  )
  const step = (text: string) => <p className="text-sm text-foreground-muted">{text}</p>
  const modelSelect = models.length > 0 && (
    <ModelSelect
      id={'client-model-' + client}
      label={m.model}
      value={model}
      models={models}
      onChange={(next) => {
        setChoice((current) => ({ ...current, model: next }))
      }}
    />
  )
  const shellSelect = (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-xs text-foreground-muted">{m.shell}</span>
      <Segmented
        label={m.shell}
        value={shell}
        onValueChange={setShell}
        options={[
          { value: 'unix', label: m.shellUnix },
          { value: 'windows', label: m.shellWindows },
        ]}
      />
    </div>
  )
  const keySelect = keys.length > 1 && (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor="client-key" className="text-xs text-foreground-muted">
        {m.key}
      </label>
      <Select
        id="client-key"
        label={m.key}
        value={key}
        onValueChange={(next) => {
          setChoice((current) => ({ ...current, key: next }))
        }}
        options={keys.map((item) => ({ value: item, label: maskKey(item) }))}
        className="w-full font-mono sm:w-[192px]"
      />
    </div>
  )
  const notes = (
    <>
      {catalog.isError ? (
        <Callout tone="error" role="alert">
          {fill(m.modelsFailed, { message: catalog.error.message })}
        </Callout>
      ) : (
        models.length === 0 && <Callout tone="muted">{m.noModels}</Callout>
      )}
      {keys.length === 0 && (
        <p className="text-xs text-foreground-muted">
          {m.keyNone} ·{' '}
          <Link to="/settings/security" className="text-primary hover:underline">
            {m.keyManage}
          </Link>
        </p>
      )}
    </>
  )
  const fields = (rows: (readonly [string, string])[]) => (
    <dl className="overflow-hidden rounded-md border border-border">
      {rows.map(([label, value]) => (
        <div
          key={label}
          className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-t border-border py-2 pr-2 pl-4 first:border-t-0 sm:grid-cols-[200px_minmax(0,1fr)_auto]"
        >
          <dt className="text-sm text-foreground-muted max-sm:col-span-2">{label}</dt>
          <dd className="min-w-0 font-mono text-sm break-all">{value}</dd>
          <CopyButton value={value} label={m.copy + ' ' + label} copiedLabel={m.copied} />
        </div>
      ))}
    </dl>
  )
  if (config.isPending || catalog.isPending) return <Loading label={app.loading} />
  if (config.isError) {
    return (
      <ErrorState
        title={m.configsFailed}
        error={config.error}
        retryLabel={app.retry}
        onRetry={() => void config.refetch()}
      />
    )
  }
  const terminal = shell === 'windows' ? 'PowerShell' : 'bash / zsh'
  return (
    <>
      <Tabs
        value={client}
        onValueChange={(value) => {
          setParams(
            (current) => {
              const next = new URLSearchParams(current)
              next.set('client', value)
              return next
            },
            { replace: true },
          )
        }}
        className="flex flex-col gap-4 px-5 py-4"
      >
        <TabsList
          aria-label={m.clientLabel}
          className="flex-wrap gap-y-1 max-sm:grid max-sm:w-full max-sm:grid-cols-2"
        >
          {clients.map((item) => (
            <TabsTrigger key={item} value={item} className="gap-2 max-sm:justify-start">
              <ClientIcon client={item} />
              {clientNames[item]}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="claude-code" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-start gap-4">
            {modelSelect}
            {models.length > 0 && (
              <ModelSelect
                id="client-small-model"
                label={m.smallModel}
                value={input.smallModel}
                models={models}
                onChange={(next) => {
                  setChoice((current) => ({ ...current, small: next }))
                }}
              />
            )}
            {keySelect}
            {shellSelect}
          </div>
          {notes}
          {step(m.claudeEnv)}
          {block(terminal, claudeEnv(input, shell))}
          {step(m.claudeSettings)}
          {block('~/.claude/settings.json', claudeSettings(input))}
        </TabsContent>
        <TabsContent value="codex" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-start gap-4">
            {modelSelect}
            {keySelect}
            {shellSelect}
          </div>
          {notes}
          {step(m.codexConfig)}
          {block('~/.codex/config.toml', codexConfig(input))}
          {step(m.codexEnv)}
          {block(terminal, codexEnv(input, shell))}
        </TabsContent>
        <TabsContent value="opencode" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-start gap-4">
            {modelSelect}
            {keySelect}
          </div>
          {notes}
          {step(m.opencodeConfig)}
          {block('~/.config/opencode/opencode.json', opencodeConfig(input))}
        </TabsContent>
        <TabsContent value="openclaw" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-start gap-4">
            {modelSelect}
            {keySelect}
          </div>
          {notes}
          {step(m.openclawConfig)}
          {block('~/.openclaw/openclaw.json', openclawConfig(input))}
        </TabsContent>
        <TabsContent value="omp" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-start gap-4">
            {modelSelect}
            {keySelect}
            {shellSelect}
          </div>
          {notes}
          {step(m.ompModels)}
          {block('~/.omp/agent/models.yml', ompModels(input))}
          {step(m.ompRun)}
          {block(terminal, ompEnv(input, shell))}
        </TabsContent>
        <TabsContent value="magpie" className="flex flex-col gap-4">
          {keySelect && <div className="flex flex-wrap items-start gap-4">{keySelect}</div>}
          {notes}
          {step(m.magpieImport)}
          {block(terminal, magpieImport(input))}
          {step(m.magpieManual)}
          {fields([
            ['Name', 'Gemini-Web2API'],
            [m.openai + ' base URL', origin + '/v1'],
            [m.anthropic + ' base URL', origin],
            ['API key', key],
          ])}
        </TabsContent>
        <TabsContent value="cherry-studio" className="flex flex-col gap-4">
          {keySelect && <div className="flex flex-wrap items-start gap-4">{keySelect}</div>}
          {notes}
          {step(m.cherrySteps)}
          {fields([
            [m.cherryType, m.cherryTypeValue],
            [m.cherryHost, origin],
            [m.cherryKey, key],
            [m.cherryModels, models.map((item) => item.id).join(', ') || fallback],
          ])}
        </TabsContent>
        <TabsContent value="sillytavern" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-start gap-4">
            {modelSelect}
            {keySelect}
          </div>
          {notes}
          {step(m.tavernSteps)}
          {fields([
            [m.tavernApi, m.tavernApiValue],
            [m.tavernSource, m.tavernSourceValue],
            [m.tavernUrl, origin + '/v1'],
            [m.tavernKey, key],
            [m.tavernModel, model],
          ])}
        </TabsContent>
        <TabsContent value="curl" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-start gap-4">
            {modelSelect}
            {keySelect}
          </div>
          {notes}
          {step(m.curlHint)}
          {block(m.curlOpenai, curlOpenai(input))}
          {block(m.curlAnthropic, curlAnthropic(input))}
          {block(m.curlGemini, curlGemini(input))}
        </TabsContent>
      </Tabs>
      <ConnectionTest apiKey={key} models={models} />
    </>
  )
}

// ConnectPage 是接入页：三种协议的接口地址与各客户端的现成配置
export function ConnectPage() {
  const m = useMessages(connectMessages)
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      <Card>
        <CardHeader title={m.endpoints} description={m.endpointsHint} />
        <EndpointList />
      </Card>
      <Card>
        <CardHeader title={m.clients} description={m.clientsHint} />
        <ClientConfigs />
      </Card>
    </>
  )
}
