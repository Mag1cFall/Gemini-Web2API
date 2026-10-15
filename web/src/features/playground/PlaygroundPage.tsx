import { ChevronRight, MessageSquare, Paperclip, Send, Square, X } from 'lucide-react'
import { type ReactNode, useEffect, useId, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Collapse } from '@/components/ui/collapse'
import { CopyButton } from '@/components/ui/copy-button'
import { Field } from '@/components/ui/field'
import { Textarea } from '@/components/ui/input'
import { Card, CardEmpty, CardHeader, PageHeader } from '@/components/ui/page'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useModels } from '@/features/models/queries'
import { renderMarkdown } from '@/features/playground/markdown'
import { playgroundMessages } from '@/features/playground/messages'
import {
  type Attachment,
  type FunctionDeclaration,
  imageAspectRatios,
  imageSizes,
  type Mode,
  modeModels,
  modeProtocols,
  modes,
  prepare,
  type Prepared,
  type Protocol,
  protocolLabels,
  protocolSettings,
  streamableTranscriptFormats,
  transcriptFormats,
  videoAspectRatios,
} from '@/features/playground/protocols'
import { execute, type Run } from '@/features/playground/run'
import { firstApiKey } from '@/features/settings/apiKeys'
import { useConfig } from '@/features/settings/queries'
import { formatBytes, formatDuration, formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// Text 是 Playground 当前语言的文案
type Text = (typeof playgroundMessages)['zh-CN']

// Form 是表单中的运行设置，reasoning、aspectRatio 与 size 以 off、auto 表示不设置
interface Form {
  system: string
  prompt: string
  stream: boolean
  reasoning: string
  webSearch: boolean
  codeExecution: boolean
  structured: boolean
  schema: string
  calling: boolean
  functions: string
  aspectRatio: string
  size: string
  transcriptFormat: string
}

// initialForm 是表单的初始设置
const initialForm: Form = {
  system: '',
  prompt: '',
  stream: true,
  reasoning: 'off',
  webSearch: false,
  codeExecution: false,
  structured: false,
  schema: '',
  calling: false,
  functions: '',
  aspectRatio: 'auto',
  size: 'auto',
  transcriptFormat: 'json',
}

// schemaPlaceholder 是结构化输出 JSON Schema 的示例
const schemaPlaceholder = '{"type": "object", "properties": {"answer": {"type": "string"}}}'

// functionsPlaceholder 是函数声明的示例
const functionsPlaceholder =
  '[{"name": "get_weather", "description": "…", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}]'

// markdownClass 是 Markdown 回复的排版
const markdownClass =
  'text-base break-words select-text [&_:is(h1,h2,h3,h4)]:mt-4 [&_:is(h1,h2,h3,h4)]:font-semibold [&_:is(th,td)]:border [&_:is(th,td)]:border-border [&_:is(th,td)]:px-3 [&_:is(th,td)]:py-1 [&_a]:text-primary [&_a]:underline [&_blockquote]:border-l-2 [&_blockquote]:border-border-strong [&_blockquote]:pl-4 [&_blockquote]:text-foreground-muted [&_code]:rounded-sm [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_code]:text-sm [&_h1]:text-xl [&_h2]:text-lg [&_h3]:text-md [&_hr]:border-border [&_img]:max-w-full [&_li+li]:mt-1 [&_ol]:list-decimal [&_ol]:pl-6 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-4 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_strong]:font-semibold [&_table]:block [&_table]:overflow-x-auto [&_table]:border-collapse [&_th]:bg-muted [&_ul]:list-disc [&_ul]:pl-6 [&>*+*]:mt-3'

// AttachedFile 是表单中已选择的附件
interface AttachedFile extends Attachment {
  id: number
  size: number
}

// modeLabels 是输出类型对应的文案键
const modeLabels: Record<Mode, keyof Text> = {
  text: 'modeText',
  image: 'modeImage',
  speech: 'modeSpeech',
  transcription: 'modeTranscription',
  music: 'modeMusic',
  video: 'modeVideo',
}

// promptLabels 是各输出类型输入框标签的文案键
const promptLabels: Record<Mode, keyof Text> = {
  text: 'promptText',
  image: 'promptImage',
  speech: 'promptSpeech',
  transcription: 'promptTranscription',
  music: 'promptMusic',
  video: 'promptVideo',
}

// attachRules 是各输出类型的附件标签、说明、可选类型与是否可选多个
const attachRules: Partial<
  Record<Mode, { label: keyof Text; hint?: keyof Text; accept?: string; multiple: boolean }>
> = {
  text: { label: 'attachText', hint: 'attachTextHint', multiple: true },
  image: { label: 'attachImage', hint: 'attachImageHint', accept: 'image/*', multiple: true },
  transcription: { label: 'attachAudio', accept: 'audio/*,video/*', multiple: false },
  video: { label: 'attachVideo', hint: 'attachVideoHint', accept: 'image/*', multiple: false },
}

// levelLabels 是思考强度对应的文案键
const levelLabels: Partial<Record<string, keyof Text>> = {
  low: 'levelLow',
  medium: 'levelMedium',
  high: 'levelHigh',
}

// readAttachment 把文件读成 base64 附件
function readAttachment(file: File) {
  return new Promise<Attachment>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const url = reader.result as string
      resolve({
        name: file.name,
        type: file.type || 'application/octet-stream',
        data: url.slice(url.indexOf(',') + 1),
      })
    }
    reader.onerror = () => {
      reject(reader.error ?? new Error(file.name))
    }
    reader.readAsDataURL(file)
  })
}

// parseJSON 解析 JSON 文本，无效时返回 undefined
function parseJSON(text: string): { value: unknown } | undefined {
  try {
    return { value: JSON.parse(text) as unknown }
  } catch {
    return undefined
  }
}

// requestText 把请求写成可读的 JSON，multipart 字段中的文件只列出名称、类型与大小
function requestText(request: Prepared) {
  let body: unknown = request.body
  if (request.body instanceof FormData) {
    const fields: Record<string, unknown> = {}
    for (const [key, value] of request.body.entries()) {
      const item =
        typeof value === 'string' ? value : { name: value.name, type: value.type, size: value.size }
      const existing = fields[key]
      fields[key] =
        existing === undefined
          ? item
          : Array.isArray(existing)
            ? [...(existing as unknown[]), item]
            : [existing, item]
    }
    body = fields
  }
  return JSON.stringify({ method: 'POST', url: request.url, body }, null, 2)
}

// Disclosure 是可展开的思考与工具输出
function Disclosure({
  label,
  open,
  children,
}: {
  label: string
  open: boolean
  children: ReactNode
}) {
  const [expanded, setExpanded] = useState(open)
  const id = useId()
  return (
    <div className="flex flex-col">
      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={id}
        onClick={() => {
          setExpanded(!expanded)
        }}
        className="inline-flex h-control items-center gap-1 self-start rounded-md px-2 text-sm font-medium text-foreground-muted hover:bg-muted"
      >
        <ChevronRight
          size={16}
          aria-hidden
          className={
            'transition-transform duration-(--duration-base) ' + (expanded ? 'rotate-90' : '')
          }
        />
        {label}
      </button>
      <Collapse open={expanded} id={id}>
        <div className="mt-2 rounded-md bg-muted p-4 text-sm break-words whitespace-pre-wrap text-foreground-muted select-text">
          {children}
        </div>
      </Collapse>
    </div>
  )
}

// MediaList 渲染生成的图像、音频与视频
function MediaList({ run }: { run: Run }) {
  const m = useMessages(playgroundMessages)
  const media = run.result.media ?? []
  if (media.length === 0) return null
  return (
    <div className="flex flex-col gap-3">
      {media.map((item, position) => {
        const index = position + 1
        if (item.type.startsWith('image/')) {
          return (
            <img
              key={item.url}
              src={item.url}
              alt={fill(m.generatedImage, { index })}
              className="max-h-[560px] max-w-full self-start rounded-md border border-border object-contain"
            />
          )
        }
        if (item.type.startsWith('audio/')) {
          return (
            // eslint-disable-next-line jsx-a11y/media-has-caption
            <audio
              key={item.url}
              src={item.url}
              controls
              aria-label={fill(m.generatedAudio, { index })}
              className="w-full"
            />
          )
        }
        return (
          // eslint-disable-next-line jsx-a11y/media-has-caption
          <video
            key={item.url}
            src={item.url}
            controls
            aria-label={fill(m.generatedVideo, { index })}
            className="max-h-[560px] w-full rounded-md border border-border bg-muted"
          />
        )
      })}
    </div>
  )
}

// Output 是右侧的输出卡片：标题行显示状态、耗时与 token，内容为回复、原始响应与请求
function Output({ run }: { run: Run | undefined }) {
  const m = useMessages(playgroundMessages)
  const { locale } = useI18n()
  if (!run) {
    return (
      <Card className="flex flex-col xl:min-h-0">
        <CardHeader title={m.output} />
        <div className="flex flex-1 flex-col justify-center">
          <CardEmpty icon={<MessageSquare size={20} aria-hidden />}>{m.idle}</CardEmpty>
        </div>
      </Card>
    )
  }
  const { result, phase } = run
  const usage = result.usage
  const facts = [
    run.firstMs === undefined
      ? undefined
      : fill(m.firstEvent, { time: formatDuration(run.firstMs, locale) }),
    run.durationMs === undefined
      ? undefined
      : fill(m.duration, { time: formatDuration(run.durationMs, locale) }),
    usage
      ? fill(usage.cached === undefined ? m.usage : m.usageCached, {
          input: formatNumber(usage.input ?? 0, locale),
          cached: formatNumber(usage.cached ?? 0, locale),
          output: formatNumber(usage.output ?? 0, locale),
        })
      : undefined,
  ].filter(Boolean)
  const request = requestText(run.request)
  const tools = result.tools?.trim()
  const plain = run.protocol === 'openai-transcriptions' || run.protocol === 'openai-translations'
  const empty = !result.text && !tools && !result.media?.length
  return (
    <Card className="flex flex-col xl:min-h-0">
      <CardHeader
        title={m.output}
        actions={
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm text-foreground-muted">
            {phase === 'cancelled' ? (
              <Badge tone="neutral">{m.cancelled}</Badge>
            ) : run.status === undefined ? (
              <Badge tone="info">{m.waiting}</Badge>
            ) : (
              <Badge tone={run.status < 400 && !result.error ? 'success' : 'error'}>
                HTTP {run.status}
              </Badge>
            )}
            {facts.map((fact) => (
              <span key={fact} className="tabular-nums">
                {fact}
              </span>
            ))}
            {run.requestId && phase !== 'running' && (
              <Button variant="link" asChild>
                <Link to={'/requests?id=' + encodeURIComponent(run.requestId)}>{m.record}</Link>
              </Button>
            )}
          </div>
        }
      />
      <div className="relative flex min-w-0 flex-col gap-4 px-5 py-5 xl:min-h-0 xl:grow xl:basis-0 xl:overflow-y-auto">
        {result.error && (
          <Callout tone="error" role="alert">
            <p className="font-mono text-xs break-words whitespace-pre-wrap select-text">
              {result.error}
            </p>
          </Callout>
        )}
        <Tabs defaultValue="reply">
          <TabsList aria-label={m.output}>
            <TabsTrigger value="reply">{m.reply}</TabsTrigger>
            <TabsTrigger value="raw">{m.raw}</TabsTrigger>
            <TabsTrigger value="request">{m.request}</TabsTrigger>
          </TabsList>
          <TabsContent value="reply" className="mt-4 flex flex-col gap-4">
            {run.progress && phase === 'running' && (
              <p role="status" className="text-sm text-foreground-muted">
                {fill(m.videoProgress, { status: run.progress })}
              </p>
            )}
            {result.reasoning && (
              <Disclosure label={m.thoughts} open={false}>
                {result.reasoning}
              </Disclosure>
            )}
            {result.text && (
              <div className="flex flex-col gap-2">
                {plain ? (
                  <div
                    aria-live={phase === 'running' ? 'off' : 'polite'}
                    className="text-base break-words whitespace-pre-wrap select-text"
                  >
                    {result.text}
                  </div>
                ) : (
                  <div
                    aria-live={phase === 'running' ? 'off' : 'polite'}
                    className={markdownClass}
                    dangerouslySetInnerHTML={{ __html: renderMarkdown(result.text) }}
                  />
                )}
                {phase !== 'running' && (
                  <div className="flex justify-end">
                    <CopyButton value={result.text} label={m.copy} copiedLabel={m.copied} />
                  </div>
                )}
              </div>
            )}
            {tools && (
              <Disclosure label={m.tools} open>
                <span className="font-mono text-xs">{tools}</span>
              </Disclosure>
            )}
            <MediaList run={run} />
            {empty && phase !== 'running' && !result.error && (
              <p className="text-sm text-foreground-muted">{m.noReply}</p>
            )}
          </TabsContent>
          <TabsContent value="raw" className="mt-4 flex flex-col gap-2">
            <div className="flex justify-end">
              <CopyButton value={run.raw} label={m.copy} copiedLabel={m.copied} />
            </div>
            <pre className="max-h-dialog overflow-auto rounded-md bg-muted p-4 font-mono text-xs break-all whitespace-pre-wrap select-text">
              {run.raw}
            </pre>
          </TabsContent>
          <TabsContent value="request" className="mt-4 flex flex-col gap-2">
            <div className="flex justify-end">
              <CopyButton value={request} label={m.copy} copiedLabel={m.copied} />
            </div>
            <pre className="max-h-dialog overflow-auto rounded-md bg-muted p-4 font-mono text-xs break-all whitespace-pre-wrap select-text">
              {request}
            </pre>
          </TabsContent>
        </Tabs>
      </div>
    </Card>
  )
}

// SwitchRow 是卡片中的一行开关：左侧名称，右侧开关
function SwitchRow({
  label,
  checked,
  onChange,
}: {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="flex min-h-row items-center justify-between gap-4 px-5 py-2 text-base">
      {label}
      <Switch checked={checked} onCheckedChange={onChange} />
    </label>
  )
}

// PlaygroundPage 向公开接口发送单轮请求，输出类型、协议与模型保存在 URL
export function PlaygroundPage() {
  const m = useMessages(playgroundMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const models = useModels()
  const config = useConfig()
  const [params, setParams] = useSearchParams()
  const [form, setForm] = useState(initialForm)
  const [attachments, setAttachments] = useState<AttachedFile[]>([])
  const [errors, setErrors] = useState<Partial<Record<'prompt' | 'schema' | 'functions', string>>>(
    {},
  )
  const [run, setRun] = useState<Run>()
  const controller = useRef<AbortController>(undefined)
  const blobs = useRef<string[]>([])
  const fileInput = useRef<HTMLInputElement>(null)
  const nextId = useRef(1)
  useEffect(
    () => () => {
      controller.current?.abort()
      for (const url of blobs.current) URL.revokeObjectURL(url)
    },
    [],
  )

  const catalog = models.data?.models ?? []
  const mode = modes.find((item) => item === params.get('mode')) ?? 'text'
  const protocols = modeProtocols[mode]
  const protocol = protocols.find((item) => item === params.get('protocol')) ?? protocols[0]
  const candidates = modeModels(mode, catalog)
  const selected = candidates.find((item) => item.id === params.get('model')) ?? candidates[0]
  const model = selected?.id ?? ''
  const available = protocolSettings(mode, protocol)
  const apiKey = firstApiKey(config.data?.proxy_api_key ?? '')
  const running = run?.phase === 'running'
  const levels = available.has('reasoning') ? (selected?.thinking_levels ?? []) : []
  const reasoning = levels.includes(form.reasoning) ? form.reasoning : 'off'
  const ratios = mode === 'video' ? videoAspectRatios : imageAspectRatios
  const aspectRatio = ratios.includes(form.aspectRatio) ? form.aspectRatio : 'auto'
  const transcribing = protocol === 'openai-transcriptions' || protocol === 'openai-translations'
  const showStream =
    available.has('stream') &&
    (!transcribing || streamableTranscriptFormats.includes(form.transcriptFormat))
  const rule = available.has('attachments') ? attachRules[mode] : undefined

  const set = <K extends keyof Form>(key: K, value: Form[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }

  const setParam = (changes: Record<string, string | undefined>) => {
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        for (const [key, value] of Object.entries(changes)) {
          if (value === undefined) next.delete(key)
          else next.set(key, value)
        }
        return next
      },
      { replace: true },
    )
  }

  const input = (files: Attachment[], schema: unknown, functions: FunctionDeclaration[]) => ({
    mode,
    protocol,
    model,
    apiKey,
    prompt: form.prompt,
    system: available.has('system') ? form.system : '',
    stream: showStream && form.stream,
    reasoning: levels.length > 0 ? reasoning : '',
    webSearch: available.has('webSearch') && form.webSearch,
    codeExecution: available.has('codeExecution') && form.codeExecution,
    schema,
    functions,
    attachments: rule ? files : [],
    aspectRatio: available.has('aspectRatio') && aspectRatio !== 'auto' ? aspectRatio : '',
    size: available.has('size') && form.size !== 'auto' ? form.size : '',
    transcriptFormat: form.transcriptFormat,
  })
  const endpoint = model
    ? prepare(
        input(
          attachments.map((item) => ({ ...item, data: '' })),
          undefined,
          [],
        ),
      ).url
    : ''

  const attach = async (list: FileList | null) => {
    if (!list || !rule) return
    const files = await Promise.all([...list].map(readAttachment))
    const added = files.map((file, index) => ({
      ...file,
      id: nextId.current++,
      size: list[index]?.size ?? 0,
    }))
    setAttachments((current) => (rule.multiple ? [...current, ...added] : added.slice(0, 1)))
  }

  const send = () => {
    const next: typeof errors = {}
    if (!transcribing && !form.prompt.trim()) next.prompt = m.promptRequired
    if (transcribing && attachments.length === 0) next.prompt = m.fileRequired
    const useSchema = available.has('schema') && form.structured && form.schema.trim() !== ''
    const schema = useSchema ? parseJSON(form.schema) : undefined
    if (useSchema && !schema) next.schema = m.invalidJson
    const useFunctions = available.has('functions') && form.calling && form.functions.trim() !== ''
    const declared = useFunctions ? parseJSON(form.functions) : undefined
    if (useFunctions && !declared) next.functions = m.invalidJson
    setErrors(next)
    if (Object.keys(next).length > 0 || !model) return
    const functions = (
      declared === undefined
        ? []
        : Array.isArray(declared.value)
          ? declared.value
          : [declared.value]
    ) as FunctionDeclaration[]
    controller.current?.abort()
    for (const url of blobs.current) URL.revokeObjectURL(url)
    blobs.current = []
    const abort = new AbortController()
    controller.current = abort
    const request = prepare(input(attachments, schema?.value, functions))
    setRun({ phase: 'running', protocol, request, result: {}, raw: '' })
    const update = (change: Partial<Run>) => {
      for (const item of change.result?.media ?? []) {
        if (item.url.startsWith('blob:')) blobs.current.push(item.url)
      }
      if (!abort.signal.aborted) setRun((current) => current && { ...current, ...change })
    }
    void execute(request, protocol, apiKey, abort.signal, update, (message) =>
      fill(m.networkError, { message }),
    )
  }

  const stop = () => {
    controller.current?.abort()
    setRun((current) => current && { ...current, phase: 'cancelled' })
  }

  if (models.isPending) return <Loading label={app.loading} />
  if (models.isError) {
    return (
      <ErrorState
        title={m.loadFailed}
        error={models.error}
        retryLabel={app.retry}
        onRetry={() => void models.refetch()}
      />
    )
  }

  const promptError = errors.prompt
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      {catalog.length === 0 ? (
        <EmptyState
          icon={<MessageSquare size={20} aria-hidden />}
          title={m.noModelsTitle}
          description={m.noModelsDescription}
          action={
            <Button asChild>
              <Link to="/accounts">{m.addAccount}</Link>
            </Button>
          }
        />
      ) : (
        <>
          <Segmented
            label={m.mode}
            value={mode}
            onValueChange={(value) => {
              setAttachments([])
              setErrors({})
              setParam({ mode: value, protocol: undefined, model: undefined })
            }}
            options={modes.map((item) => ({ value: item, label: m[modeLabels[item]] }))}
          />
          <div className="grid grid-cols-1 gap-3 xl:min-h-[360px] xl:grow xl:basis-0 xl:grid-cols-2 xl:grid-rows-[minmax(0,1fr)]">
            <Card className="flex flex-col xl:min-h-0">
              <CardHeader title={m.request} />
              <form
                noValidate
                className="relative flex min-w-0 flex-col gap-4 px-5 py-5 xl:min-h-0 xl:grow xl:basis-0 xl:overflow-y-auto"
                onSubmit={(event) => {
                  event.preventDefault()
                  send()
                }}
              >
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field id="playground-protocol" label={m.protocol}>
                    <Select
                      id="playground-protocol"
                      label={m.protocol}
                      value={protocol}
                      onValueChange={(value: Protocol) => {
                        setParam({ protocol: value })
                      }}
                      options={protocols.map((item) => ({
                        value: item,
                        label: protocolLabels[item],
                      }))}
                    />
                  </Field>
                  {candidates.length > 0 && (
                    <Field id="playground-model" label={m.model}>
                      <Select
                        id="playground-model"
                        label={m.model}
                        value={model}
                        onValueChange={(value) => {
                          setParam({ model: value })
                        }}
                        options={candidates.map((item) => ({ value: item.id, label: item.id }))}
                      />
                    </Field>
                  )}
                  {levels.length > 0 && (
                    <Field id="playground-reasoning" label={m.reasoning}>
                      <Select
                        id="playground-reasoning"
                        label={m.reasoning}
                        value={reasoning}
                        onValueChange={(value) => {
                          set('reasoning', value)
                        }}
                        options={[
                          { value: 'off', label: m.reasoningOff },
                          ...levels.map((item) => {
                            const key = levelLabels[item]
                            return { value: item, label: key ? m[key] : item }
                          }),
                        ]}
                      />
                    </Field>
                  )}
                  {available.has('aspectRatio') && (
                    <Field id="playground-aspect" label={m.aspectRatio}>
                      <Select
                        id="playground-aspect"
                        label={m.aspectRatio}
                        value={aspectRatio}
                        onValueChange={(value) => {
                          set('aspectRatio', value)
                        }}
                        options={[
                          { value: 'auto', label: m.auto },
                          ...ratios.map((item) => ({ value: item, label: item })),
                        ]}
                      />
                    </Field>
                  )}
                  {available.has('size') && (
                    <Field id="playground-size" label={m.size}>
                      <Select
                        id="playground-size"
                        label={m.size}
                        value={form.size}
                        onValueChange={(value) => {
                          set('size', value)
                        }}
                        options={[
                          { value: 'auto', label: m.auto },
                          ...imageSizes.map((item) => ({ value: item, label: item })),
                        ]}
                      />
                    </Field>
                  )}
                  {available.has('transcriptFormat') && (
                    <Field id="playground-format" label={m.transcriptFormat}>
                      <Select
                        id="playground-format"
                        label={m.transcriptFormat}
                        value={form.transcriptFormat}
                        onValueChange={(value) => {
                          set('transcriptFormat', value)
                        }}
                        options={transcriptFormats.map((item) => ({ value: item, label: item }))}
                      />
                    </Field>
                  )}
                </div>
                {candidates.length === 0 ? (
                  <Callout tone="muted" role="status">
                    <p>{m.noModeModels}</p>
                  </Callout>
                ) : (
                  <p className="font-mono text-xs break-all text-foreground-muted">
                    <span className="text-primary">POST</span> {endpoint}
                  </p>
                )}
                {!apiKey && config.isSuccess && (
                  <p className="text-sm text-foreground-muted">{m.noKey}</p>
                )}
                {(showStream ||
                  available.has('webSearch') ||
                  available.has('codeExecution') ||
                  available.has('schema') ||
                  available.has('functions')) && (
                  <div className="-mx-5 flex flex-col divide-y divide-border border-y border-border">
                    {showStream && (
                      <SwitchRow
                        label={m.stream}
                        checked={form.stream}
                        onChange={(value) => {
                          set('stream', value)
                        }}
                      />
                    )}
                    {available.has('webSearch') && (
                      <SwitchRow
                        label={m.webSearch}
                        checked={form.webSearch}
                        onChange={(value) => {
                          set('webSearch', value)
                        }}
                      />
                    )}
                    {available.has('codeExecution') && (
                      <SwitchRow
                        label={m.codeExecution}
                        checked={form.codeExecution}
                        onChange={(value) => {
                          set('codeExecution', value)
                        }}
                      />
                    )}
                    {available.has('schema') && (
                      <SwitchRow
                        label={m.schema}
                        checked={form.structured}
                        onChange={(value) => {
                          set('structured', value)
                        }}
                      />
                    )}
                    {available.has('functions') && (
                      <SwitchRow
                        label={m.functions}
                        checked={form.calling}
                        onChange={(value) => {
                          set('calling', value)
                        }}
                      />
                    )}
                  </div>
                )}
                {available.has('schema') && form.structured && (
                  <Field id="playground-schema" label="JSON Schema" error={errors.schema}>
                    <Textarea
                      id="playground-schema"
                      rows={4}
                      spellCheck={false}
                      value={form.schema}
                      placeholder={schemaPlaceholder}
                      aria-invalid={errors.schema ? true : undefined}
                      className="font-mono text-xs"
                      onChange={(event) => {
                        set('schema', event.target.value)
                      }}
                    />
                  </Field>
                )}
                {available.has('functions') && form.calling && (
                  <Field id="playground-functions" label={m.functions} error={errors.functions}>
                    <Textarea
                      id="playground-functions"
                      rows={4}
                      spellCheck={false}
                      value={form.functions}
                      placeholder={functionsPlaceholder}
                      aria-invalid={errors.functions ? true : undefined}
                      className="font-mono text-xs"
                      onChange={(event) => {
                        set('functions', event.target.value)
                      }}
                    />
                  </Field>
                )}
                {available.has('system') && (
                  <Field id="playground-system" label={m.system}>
                    <Textarea
                      id="playground-system"
                      rows={3}
                      value={form.system}
                      placeholder={m.systemPlaceholder}
                      onChange={(event) => {
                        set('system', event.target.value)
                      }}
                    />
                  </Field>
                )}
                {rule && (
                  <div className="flex flex-col gap-2">
                    <span className="text-base font-medium">{m[rule.label]}</span>
                    {attachments.length > 0 && (
                      <ul className="flex flex-wrap gap-2">
                        {attachments.map((item) => (
                          <li
                            key={item.id}
                            className="inline-flex h-control-sm max-w-full items-center gap-2 rounded-md bg-muted pr-1 pl-3 text-sm"
                          >
                            <span className="truncate">{item.name}</span>
                            <span className="shrink-0 text-xs text-foreground-muted tabular-nums">
                              {formatBytes(item.size, locale)}
                            </span>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="size-6 pointer-coarse:size-touch"
                              aria-label={fill(m.remove, { name: item.name })}
                              onClick={() => {
                                setAttachments((current) =>
                                  current.filter((other) => other.id !== item.id),
                                )
                              }}
                            >
                              <X size={14} aria-hidden />
                            </Button>
                          </li>
                        ))}
                      </ul>
                    )}
                    <div className="flex flex-wrap items-center gap-3">
                      <Button
                        onClick={() => {
                          fileInput.current?.click()
                        }}
                      >
                        <Paperclip size={16} aria-hidden />
                        {m.attach}
                      </Button>
                      {rule.hint && (
                        <span className="text-sm text-foreground-muted">{m[rule.hint]}</span>
                      )}
                    </div>
                    <input
                      ref={fileInput}
                      type="file"
                      tabIndex={-1}
                      aria-hidden
                      className="sr-only"
                      multiple={rule.multiple}
                      {...(rule.accept ? { accept: rule.accept } : {})}
                      onChange={(event) => {
                        const target = event.currentTarget
                        void attach(target.files).finally(() => {
                          target.value = ''
                        })
                      }}
                    />
                  </div>
                )}
                <Field
                  id="playground-prompt"
                  label={m[promptLabels[mode]]}
                  description={transcribing ? m.promptOptional : m.sendHint}
                  error={promptError}
                >
                  <Textarea
                    id="playground-prompt"
                    rows={6}
                    value={form.prompt}
                    placeholder={m.promptPlaceholder}
                    aria-invalid={promptError ? true : undefined}
                    aria-describedby={
                      promptError ? 'playground-prompt-error' : 'playground-prompt-description'
                    }
                    onChange={(event) => {
                      set('prompt', event.target.value)
                    }}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
                        event.preventDefault()
                        send()
                      }
                    }}
                  />
                </Field>
                <div className="flex flex-wrap gap-3">
                  {running ? (
                    <Button type="button" variant="destructive" size="lg" onClick={stop}>
                      <Square size={16} aria-hidden />
                      {m.stop}
                    </Button>
                  ) : (
                    <Button type="submit" variant="primary" size="lg" disabled={!model}>
                      <Send size={16} aria-hidden />
                      {m.send}
                    </Button>
                  )}
                </div>
              </form>
            </Card>
            <Output run={run} />
          </div>
        </>
      )}
    </>
  )
}
