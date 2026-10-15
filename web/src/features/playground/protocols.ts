// Mode 是 Playground 的输出类型
export type Mode = 'text' | 'image' | 'speech' | 'transcription' | 'music' | 'video'

// Protocol 是 Playground 发送请求所用的公开协议
export type Protocol =
  | 'openai-chat'
  | 'openai-responses'
  | 'anthropic'
  | 'gemini'
  | 'openai-images'
  | 'openai-speech'
  | 'openai-transcriptions'
  | 'openai-translations'
  | 'openai-videos'

// modes 是全部输出类型
export const modes: Mode[] = ['text', 'image', 'speech', 'transcription', 'music', 'video']

// modeProtocols 是各输出类型可用的公开协议，首项为默认值
export const modeProtocols: Record<Mode, [Protocol, ...Protocol[]]> = {
  text: ['openai-chat', 'openai-responses', 'anthropic', 'gemini'],
  image: ['gemini', 'openai-images'],
  speech: ['openai-speech'],
  transcription: ['openai-transcriptions', 'openai-translations'],
  music: ['gemini'],
  video: ['gemini', 'openai-videos'],
}

// protocolLabels 是公开协议的名称
export const protocolLabels: Record<Protocol, string> = {
  'openai-chat': 'OpenAI Chat',
  'openai-responses': 'OpenAI Responses',
  anthropic: 'Anthropic Messages',
  gemini: 'Gemini',
  'openai-images': 'OpenAI Images',
  'openai-speech': 'OpenAI Speech',
  'openai-transcriptions': 'OpenAI Transcriptions',
  'openai-translations': 'OpenAI Translations',
  'openai-videos': 'OpenAI Videos',
}

// mediaTools 是只用于对应输出类型的官网工具
const mediaTools: Partial<Record<Mode, string>> = {
  image: 'image',
  music: 'music',
  video: 'video',
}

// modeModels 返回可用于输出类型的模型：图像、音乐、视频只用对应工具的条目，其余输出类型排除这三种工具
export function modeModels<T extends { tool?: string | undefined }>(mode: Mode, models: T[]): T[] {
  const tool = mediaTools[mode]
  if (tool) return models.filter((model) => model.tool === tool)
  const tools = Object.values(mediaTools)
  return models.filter((model) => !model.tool || !tools.includes(model.tool))
}

// Setting 是随协议生效的一项运行设置
export type Setting =
  | 'system'
  | 'stream'
  | 'reasoning'
  | 'webSearch'
  | 'codeExecution'
  | 'schema'
  | 'functions'
  | 'attachments'
  | 'aspectRatio'
  | 'size'
  | 'transcriptFormat'

// textSettings 是四种文本协议共有的运行设置
const textSettings: Setting[] = [
  'system',
  'stream',
  'reasoning',
  'webSearch',
  'functions',
  'attachments',
]

// protocolSettings 返回输出类型与协议在本服务公开接口中生效的运行设置
export function protocolSettings(mode: Mode, protocol: Protocol): ReadonlySet<Setting> {
  switch (protocol) {
    case 'openai-chat':
      return new Set([...textSettings, 'schema'])
    case 'openai-responses':
      return new Set([...textSettings, 'schema', 'codeExecution'])
    case 'anthropic':
      return new Set(textSettings)
    case 'gemini':
      if (mode === 'text') return new Set([...textSettings, 'schema', 'codeExecution'])
      if (mode === 'music') return new Set()
      return new Set(['aspectRatio', 'attachments'])
    case 'openai-images':
      return new Set(['size', 'attachments'])
    case 'openai-speech':
      return new Set()
    case 'openai-transcriptions':
      return new Set(['transcriptFormat', 'stream', 'attachments'])
    case 'openai-translations':
      return new Set(['transcriptFormat', 'attachments'])
    case 'openai-videos':
      return new Set(['aspectRatio', 'attachments'])
  }
}

// imageAspectRatios 是 Gemini imageConfig.aspectRatio 的取值
export const imageAspectRatios = [
  '1:1',
  '2:3',
  '3:2',
  '3:4',
  '4:3',
  '4:5',
  '5:4',
  '9:16',
  '16:9',
  '21:9',
]

// videoAspectRatios 是视频的横屏与竖屏宽高比
export const videoAspectRatios = ['16:9', '9:16']

// imageSizes 是 OpenAI 图像接口的 size 取值
export const imageSizes = ['1024x1024', '1536x1024', '1024x1536']

// videoSizes 是 OpenAI 视频接口各宽高比对应的 size
const videoSizes: Record<string, string> = { '16:9': '1280x720', '9:16': '720x1280' }

// transcriptFormats 是 OpenAI 转写接口的 response_format 取值
export const transcriptFormats = ['json', 'text', 'srt', 'vtt', 'verbose_json']

// streamableTranscriptFormats 是可以流式输出的转写格式
export const streamableTranscriptFormats = ['json', 'text']

// Attachment 是随请求上传的一个文件，data 为 base64 内容
export interface Attachment {
  name: string
  type: string
  data: string
}

// FunctionDeclaration 是函数调用声明的函数，parameters 为 JSON Schema
export interface FunctionDeclaration {
  name: string
  description?: string
  parameters?: unknown
}

// PlaygroundInput 是一次 Playground 请求的输入，reasoning 为空表示不设置思考、off 表示关闭，aspectRatio 与 size 为空表示自动
export interface PlaygroundInput {
  mode: Mode
  protocol: Protocol
  model: string
  apiKey: string
  prompt: string
  system: string
  stream: boolean
  reasoning: string
  webSearch: boolean
  codeExecution: boolean
  schema: unknown
  functions: FunctionDeclaration[]
  attachments: Attachment[]
  aspectRatio: string
  size: string
  transcriptFormat: string
}

// ResponseKind 是读取响应的方式
export type ResponseKind = 'stream' | 'json' | 'audio' | 'video'

// Prepared 是发往公开接口的请求，multipart 请求的 body 为 FormData
export interface Prepared {
  url: string
  headers: Record<string, string>
  body: Record<string, unknown> | FormData
  response: ResponseKind
}

// anthropicMaxTokens 是 Anthropic 请求的 max_tokens
const anthropicMaxTokens = 16384

// authHeaders 返回协议使用的认证头，没有 API key 时为空
export function authHeaders(protocol: Protocol, apiKey: string): Record<string, string> {
  if (!apiKey) return {}
  if (protocol === 'anthropic') return { 'x-api-key': apiKey }
  if (protocol === 'gemini') return { 'x-goog-api-key': apiKey }
  return { Authorization: 'Bearer ' + apiKey }
}

// isImage 判断附件是否为图片
function isImage(attachment: Attachment) {
  return attachment.type.startsWith('image/')
}

// dataURL 返回附件的 data URL
function dataURL(attachment: Attachment) {
  return 'data:' + attachment.type + ';base64,' + attachment.data
}

// blob 把附件还原为 multipart 上传的文件内容
function blob(attachment: Attachment) {
  const bytes = Uint8Array.from(atob(attachment.data), (char) => char.charCodeAt(0))
  return new Blob([bytes], { type: attachment.type })
}

// audioFormats 是 Chat input_audio 接受的音频格式
const audioFormats: Record<string, string> = {
  'audio/wav': 'wav',
  'audio/x-wav': 'wav',
  'audio/mpeg': 'mp3',
  'audio/mp3': 'mp3',
}

// chatPart 把附件写成 Chat Completions 的内容段
function chatPart(attachment: Attachment) {
  if (isImage(attachment)) return { type: 'image_url', image_url: { url: dataURL(attachment) } }
  const format = audioFormats[attachment.type]
  if (format) return { type: 'input_audio', input_audio: { data: attachment.data, format } }
  return { type: 'file', file: { filename: attachment.name, file_data: dataURL(attachment) } }
}

// responsesPart 把附件写成 Responses 的输入内容
function responsesPart(attachment: Attachment) {
  if (isImage(attachment)) return { type: 'input_image', image_url: dataURL(attachment) }
  return { type: 'input_file', filename: attachment.name, file_data: dataURL(attachment) }
}

// anthropicBlock 把附件写成 Anthropic 的 image 或 document 内容块
function anthropicBlock(attachment: Attachment) {
  return {
    type: isImage(attachment) ? 'image' : 'document',
    source: { type: 'base64', media_type: attachment.type, data: attachment.data },
  }
}

// geminiParts 返回附件在前、文本在后的 Gemini 内容段
function geminiParts(input: PlaygroundInput) {
  return [
    ...input.attachments.map((attachment) => ({
      inlineData: { mimeType: attachment.type, data: attachment.data },
    })),
    { text: input.prompt },
  ]
}

// level 返回选中的思考强度，未设置或关闭时为空
function level(input: PlaygroundInput) {
  return input.reasoning === 'off' ? '' : input.reasoning
}

// form 把字段写入 FormData，跳过空值
function form(fields: Record<string, string | Blob | undefined>, files: [string, Attachment][]) {
  const data = new FormData()
  for (const [key, value] of Object.entries(fields)) {
    if (value !== undefined && value !== '') data.append(key, value)
  }
  for (const [key, attachment] of files) data.append(key, blob(attachment), attachment.name)
  return data
}

// prepareChat 构造 Chat Completions 请求
function prepareChat(input: PlaygroundInput, headers: Record<string, string>): Prepared {
  const system = input.system.trim()
  const effort = input.reasoning === 'off' ? 'none' : input.reasoning
  return {
    url: '/v1/chat/completions',
    headers: { ...headers, 'Content-Type': 'application/json' },
    response: input.stream ? 'stream' : 'json',
    body: {
      model: input.model,
      messages: [
        ...(system ? [{ role: 'system', content: system }] : []),
        {
          role: 'user',
          content:
            input.attachments.length > 0
              ? [...input.attachments.map(chatPart), { type: 'text', text: input.prompt }]
              : input.prompt,
        },
      ],
      stream: input.stream,
      ...(input.stream ? { stream_options: { include_usage: true } } : {}),
      ...(effort ? { reasoning_effort: effort } : {}),
      ...(input.webSearch ? { web_search_options: {} } : {}),
      ...(input.schema === undefined
        ? {}
        : {
            response_format: {
              type: 'json_schema',
              json_schema: { name: 'response', schema: input.schema },
            },
          }),
      ...(input.functions.length > 0
        ? { tools: input.functions.map((item) => ({ type: 'function', function: item })) }
        : {}),
    },
  }
}

// prepareResponses 构造 Responses 请求
function prepareResponses(input: PlaygroundInput, headers: Record<string, string>): Prepared {
  const system = input.system.trim()
  const effort = level(input)
  const tools = [
    ...(input.webSearch ? [{ type: 'web_search' }] : []),
    ...(input.codeExecution ? [{ type: 'code_interpreter', container: { type: 'auto' } }] : []),
    ...input.functions.map((item) => ({ type: 'function', ...item })),
  ]
  return {
    url: '/v1/responses',
    headers: { ...headers, 'Content-Type': 'application/json' },
    response: input.stream ? 'stream' : 'json',
    body: {
      model: input.model,
      ...(system ? { instructions: system } : {}),
      input:
        input.attachments.length > 0
          ? [
              {
                role: 'user',
                content: [
                  ...input.attachments.map(responsesPart),
                  { type: 'input_text', text: input.prompt },
                ],
              },
            ]
          : input.prompt,
      stream: input.stream,
      ...(effort ? { reasoning: { effort, summary: 'auto' } } : {}),
      ...(tools.length > 0 ? { tools } : {}),
      ...(input.schema === undefined
        ? {}
        : { text: { format: { type: 'json_schema', name: 'response', schema: input.schema } } }),
    },
  }
}

// prepareAnthropic 构造 Anthropic Messages 请求
function prepareAnthropic(input: PlaygroundInput, headers: Record<string, string>): Prepared {
  const system = input.system.trim()
  const effort = level(input)
  const tools = [
    ...(input.webSearch ? [{ type: 'web_search_20250305', name: 'web_search' }] : []),
    ...input.functions.map((item) => ({
      name: item.name,
      ...(item.description ? { description: item.description } : {}),
      input_schema: item.parameters ?? { type: 'object', properties: {} },
    })),
  ]
  return {
    url: '/v1/messages',
    headers: { ...headers, 'anthropic-version': '2023-06-01', 'Content-Type': 'application/json' },
    response: input.stream ? 'stream' : 'json',
    body: {
      model: input.model,
      max_tokens: anthropicMaxTokens,
      ...(system ? { system } : {}),
      messages: [
        {
          role: 'user',
          content:
            input.attachments.length > 0
              ? [...input.attachments.map(anthropicBlock), { type: 'text', text: input.prompt }]
              : input.prompt,
        },
      ],
      stream: input.stream,
      ...(effort ? { thinking: { type: 'adaptive' }, output_config: { effort } } : {}),
      ...(tools.length > 0 ? { tools } : {}),
    },
  }
}

// prepareGemini 构造 Gemini 原生请求：文本按流式选择方法，图像与音乐用 generateContent，视频用 predictLongRunning
function prepareGemini(input: PlaygroundInput, headers: Record<string, string>): Prepared {
  const base = '/v1beta/models/' + encodeURIComponent(input.model)
  const json = { ...headers, 'Content-Type': 'application/json' }
  if (input.mode === 'video') {
    const image = input.attachments[0]
    return {
      url: base + ':predictLongRunning',
      headers: json,
      response: 'video',
      body: {
        instances: [
          {
            prompt: input.prompt,
            ...(image ? { image: { bytesBase64Encoded: image.data, mimeType: image.type } } : {}),
          },
        ],
        ...(input.aspectRatio ? { parameters: { aspectRatio: input.aspectRatio } } : {}),
      },
    }
  }
  const contents = [{ role: 'user', parts: geminiParts(input) }]
  if (input.mode !== 'text') {
    return {
      url: base + ':generateContent',
      headers: json,
      response: 'json',
      body: {
        contents,
        ...(input.aspectRatio
          ? { generationConfig: { imageConfig: { aspectRatio: input.aspectRatio } } }
          : {}),
      },
    }
  }
  const system = input.system.trim()
  const effort = level(input)
  const generationConfig = {
    ...(effort ? { thinkingConfig: { thinkingLevel: effort, includeThoughts: true } } : {}),
    ...(input.schema === undefined
      ? {}
      : { responseMimeType: 'application/json', responseJsonSchema: input.schema }),
  }
  const tools = [
    ...(input.webSearch ? [{ googleSearch: {} }] : []),
    ...(input.codeExecution ? [{ codeExecution: {} }] : []),
    ...(input.functions.length > 0 ? [{ functionDeclarations: input.functions }] : []),
  ]
  return {
    url: base + (input.stream ? ':streamGenerateContent?alt=sse' : ':generateContent'),
    headers: json,
    response: input.stream ? 'stream' : 'json',
    body: {
      ...(system ? { systemInstruction: { parts: [{ text: system }] } } : {}),
      contents,
      ...(Object.keys(generationConfig).length > 0 ? { generationConfig } : {}),
      ...(tools.length > 0 ? { tools } : {}),
    },
  }
}

// prepare 按协议构造请求路径、请求头与请求体
export function prepare(input: PlaygroundInput): Prepared {
  const headers = authHeaders(input.protocol, input.apiKey)
  const json = { ...headers, 'Content-Type': 'application/json' }
  switch (input.protocol) {
    case 'openai-chat':
      return prepareChat(input, headers)
    case 'openai-responses':
      return prepareResponses(input, headers)
    case 'anthropic':
      return prepareAnthropic(input, headers)
    case 'gemini':
      return prepareGemini(input, headers)
    case 'openai-images':
      if (input.attachments.length > 0) {
        return {
          url: '/v1/images/edits',
          headers,
          response: 'json',
          body: form(
            { model: input.model, prompt: input.prompt, n: '1', size: input.size },
            input.attachments.map((attachment) => ['image[]', attachment]),
          ),
        }
      }
      return {
        url: '/v1/images/generations',
        headers: json,
        response: 'json',
        body: {
          model: input.model,
          prompt: input.prompt,
          n: 1,
          ...(input.size ? { size: input.size } : {}),
        },
      }
    case 'openai-speech':
      return {
        url: '/v1/audio/speech',
        headers: json,
        response: 'audio',
        body: { model: input.model, input: input.prompt },
      }
    case 'openai-transcriptions':
    case 'openai-translations': {
      const transcribe = input.protocol === 'openai-transcriptions'
      const stream =
        transcribe && input.stream && streamableTranscriptFormats.includes(input.transcriptFormat)
      const file = input.attachments[0]
      return {
        url: transcribe ? '/v1/audio/transcriptions' : '/v1/audio/translations',
        headers,
        response: stream ? 'stream' : 'json',
        body: form(
          {
            model: input.model,
            response_format: input.transcriptFormat,
            prompt: input.prompt.trim(),
            stream: stream ? 'true' : undefined,
          },
          file ? [['file', file]] : [],
        ),
      }
    }
    case 'openai-videos': {
      const size = videoSizes[input.aspectRatio]
      const image = input.attachments[0]
      if (image) {
        return {
          url: '/v1/videos',
          headers,
          response: 'video',
          body: form({ model: input.model, prompt: input.prompt, size }, [
            ['input_reference', image],
          ]),
        }
      }
      return {
        url: '/v1/videos',
        headers: json,
        response: 'video',
        body: { model: input.model, prompt: input.prompt, ...(size ? { size } : {}) },
      }
    }
  }
}

// Usage 是统一后的用量，input 含缓存命中部分，output 含思考部分
export interface Usage {
  input?: number | undefined
  cached?: number | undefined
  output?: number | undefined
}

// Media 是响应中的一个生成媒体，url 为 data URL 或 blob URL
export interface Media {
  type: string
  url: string
}

// Delta 是从一个流事件或一个完整响应中解析出的内容，tools 为工具调用与代码执行的文本
export interface Delta {
  text?: string | undefined
  reasoning?: string | undefined
  tools?: string | undefined
  media?: Media[] | undefined
  usage?: Usage | undefined
  error?: string | undefined
}

interface ErrorBody {
  error?: { message?: string } | null
}

interface ChatToolCall {
  function?: { name?: string; arguments?: string }
}

interface ChatPart {
  content?: string | null
  reasoning_content?: string | null
  tool_calls?: ChatToolCall[] | null
}

interface ChatBody extends ErrorBody {
  choices?: { delta?: ChatPart; message?: ChatPart }[]
  usage?: {
    prompt_tokens?: number
    completion_tokens?: number
    prompt_tokens_details?: { cached_tokens?: number }
  } | null
}

interface ResponsesUsage {
  input_tokens?: number
  output_tokens?: number
  input_tokens_details?: { cached_tokens?: number }
}

interface ResponsesItem {
  type?: string
  name?: string
  arguments?: string
  code?: string | null
  outputs?: { type?: string; logs?: string }[] | null
  result?: string | null
  content?: { type?: string; text?: string }[]
  summary?: { text?: string }[]
}

interface ResponsesObject extends ErrorBody {
  output?: ResponsesItem[]
  output_format?: string
  usage?: ResponsesUsage | null
}

interface ResponsesEvent extends ErrorBody {
  type?: string
  delta?: string
  message?: string
  response?: ResponsesObject
}

interface AnthropicUsage {
  input_tokens?: number
  output_tokens?: number
  cache_read_input_tokens?: number | null
  cache_creation_input_tokens?: number | null
}

interface AnthropicBlock {
  type?: string
  text?: string
  thinking?: string
  partial_json?: string
  name?: string
  input?: unknown
}

interface AnthropicBody extends ErrorBody {
  type?: string
  delta?: AnthropicBlock
  content_block?: AnthropicBlock
  message?: { usage?: AnthropicUsage }
  content?: AnthropicBlock[]
  usage?: AnthropicUsage
}

interface GeminiPart {
  text?: string
  thought?: boolean
  inlineData?: { mimeType?: string; data?: string }
  functionCall?: { name?: string; args?: unknown }
  executableCode?: { code?: string }
  codeExecutionResult?: { output?: string }
}

interface GeminiBody extends ErrorBody {
  candidates?: { content?: { parts?: GeminiPart[] } }[]
  usageMetadata?: {
    promptTokenCount?: number
    candidatesTokenCount?: number
    thoughtsTokenCount?: number
    cachedContentTokenCount?: number
  }
}

interface ImagesBody extends ErrorBody {
  output_format?: string
  data?: { b64_json?: string; url?: string; revised_prompt?: string }[]
}

interface TranscriptionBody extends ErrorBody {
  type?: string
  text?: string
  delta?: string
  usage?: { type?: string; input_tokens?: number; output_tokens?: number }
}

// joined 拼接非空文本，没有内容时返回 undefined
function joined(parts: (string | null | undefined)[]) {
  const text = parts.filter(Boolean).join('')
  return text || undefined
}

// compact 去掉值为 undefined 的字段，合并时只覆盖有值的字段
function compact<T extends object>(value: T): T {
  return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined)) as T
}

// toolLine 把一次工具调用写成“名称 参数”一行
function toolLine(name: string | undefined, args: unknown) {
  return (name ?? '') + ' ' + (typeof args === 'string' ? args : JSON.stringify(args ?? {})) + '\n'
}

// chatDelta 解析 Chat Completions 的流事件或完整响应，流式工具调用的名称与参数分段到达
function chatDelta(body: ChatBody): Delta {
  const choice = body.choices?.[0]
  const part = choice?.delta ?? choice?.message
  const usage = body.usage
  return compact({
    text: joined([part?.content]),
    reasoning: joined([part?.reasoning_content]),
    tools: joined(
      (part?.tool_calls ?? []).map((call) =>
        choice?.delta
          ? (call.function?.name ? '\n' + call.function.name + ' ' : '') +
            (call.function?.arguments ?? '')
          : toolLine(call.function?.name, call.function?.arguments),
      ),
    ),
    usage: usage
      ? compact({
          input: usage.prompt_tokens,
          cached: usage.prompt_tokens_details?.cached_tokens ?? 0,
          output: usage.completion_tokens,
        })
      : undefined,
    error: body.error?.message,
  })
}

// responsesUsage 统一 Responses 的用量
function responsesUsage(usage: ResponsesUsage | null | undefined): Usage | undefined {
  return usage
    ? compact({
        input: usage.input_tokens,
        cached: usage.input_tokens_details?.cached_tokens ?? 0,
        output: usage.output_tokens,
      })
    : undefined
}

// responsesExtras 读取 Responses 输出中的工具调用、代码执行与生成图片
function responsesExtras(response: ResponsesObject): Delta {
  const output = response.output ?? []
  const media = output
    .filter((item) => item.type === 'image_generation_call' && item.result)
    .map((item) => ({
      type: 'image/' + (response.output_format ?? 'png'),
      url: 'data:image/' + (response.output_format ?? 'png') + ';base64,' + (item.result ?? ''),
    }))
  return compact({
    tools: joined(
      output.map((item) => {
        if (item.type === 'function_call') return toolLine(item.name, item.arguments)
        if (item.type === 'code_interpreter_call') {
          return [item.code, ...(item.outputs ?? []).map((out) => out.logs)]
            .filter(Boolean)
            .map((text) => (text ?? '') + '\n')
            .join('')
        }
        return undefined
      }),
    ),
    media: media.length > 0 ? media : undefined,
    usage: responsesUsage(response.usage),
  })
}

// responsesObject 解析 Responses 的完整 response 对象
function responsesObject(response: ResponsesObject): Delta {
  const output = response.output ?? []
  return compact({
    text: joined(
      output
        .filter((item) => item.type === 'message')
        .flatMap((item) => item.content ?? [])
        .filter((content) => content.type === 'output_text')
        .map((content) => content.text),
    ),
    reasoning: joined(
      output
        .filter((item) => item.type === 'reasoning')
        .flatMap((item) => item.summary ?? [])
        .map((summary) => summary.text),
    ),
    ...responsesExtras(response),
    error: response.error?.message,
  })
}

// responsesEvent 解析 Responses 的流事件，工具调用、图片与用量取自完成事件中的 response
function responsesEvent(body: ResponsesEvent): Delta {
  switch (body.type) {
    case 'response.output_text.delta':
      return compact({ text: body.delta })
    case 'response.reasoning_summary_text.delta':
    case 'response.reasoning_text.delta':
      return compact({ reasoning: body.delta })
    case 'response.completed':
    case 'response.incomplete':
      return body.response ? responsesExtras(body.response) : {}
    case 'response.failed':
      return { error: body.response?.error?.message ?? body.type }
    case 'error':
      return { error: body.message ?? body.error?.message ?? body.type }
    default:
      return {}
  }
}

// anthropicUsage 统一 Anthropic 的用量，input 加回缓存读写部分
function anthropicUsage(usage: AnthropicUsage): Usage {
  const cached = usage.cache_read_input_tokens ?? 0
  if (usage.input_tokens === undefined) return compact({ output: usage.output_tokens })
  return compact({
    input: usage.input_tokens + cached + (usage.cache_creation_input_tokens ?? 0),
    cached,
    output: usage.output_tokens,
  })
}

// anthropicTool 判断内容块是否为客户端或服务端工具调用
function anthropicTool(block: AnthropicBlock | undefined) {
  return block?.type === 'tool_use' || block?.type === 'server_tool_use'
}

// anthropicEvent 解析 Anthropic Messages 的流事件
function anthropicEvent(body: AnthropicBody): Delta {
  switch (body.type) {
    case 'message_start':
      return body.message?.usage ? { usage: anthropicUsage(body.message.usage) } : {}
    case 'content_block_start':
      return anthropicTool(body.content_block)
        ? { tools: '\n' + (body.content_block?.name ?? '') + ' ' }
        : {}
    case 'content_block_delta':
      return compact({
        text: body.delta?.type === 'text_delta' ? body.delta.text : undefined,
        reasoning: body.delta?.type === 'thinking_delta' ? body.delta.thinking : undefined,
        tools: body.delta?.type === 'input_json_delta' ? body.delta.partial_json : undefined,
      })
    case 'message_delta':
      return body.usage ? { usage: anthropicUsage(body.usage) } : {}
    case 'error':
      return { error: body.error?.message ?? body.type }
    default:
      return {}
  }
}

// anthropicMessage 解析 Anthropic Messages 的完整响应
function anthropicMessage(body: AnthropicBody): Delta {
  const content = body.content ?? []
  return compact({
    text: joined(content.filter((block) => block.type === 'text').map((block) => block.text)),
    reasoning: joined(
      content.filter((block) => block.type === 'thinking').map((block) => block.thinking),
    ),
    tools: joined(content.filter(anthropicTool).map((block) => toolLine(block.name, block.input))),
    usage: body.usage ? anthropicUsage(body.usage) : undefined,
    error: body.error?.message,
  })
}

// geminiDelta 解析 Gemini 的流事件或完整响应，thought 为真的部分是思考
function geminiDelta(body: GeminiBody): Delta {
  const parts = body.candidates?.[0]?.content?.parts ?? []
  const usage = body.usageMetadata
  const media = parts.flatMap((part) =>
    part.inlineData?.mimeType && part.inlineData.data
      ? [
          {
            type: part.inlineData.mimeType,
            url: 'data:' + part.inlineData.mimeType + ';base64,' + part.inlineData.data,
          },
        ]
      : [],
  )
  return compact({
    text: joined(parts.filter((part) => !part.thought).map((part) => part.text)),
    reasoning: joined(parts.filter((part) => part.thought).map((part) => part.text)),
    tools: joined(
      parts.map((part) => {
        if (part.functionCall) return toolLine(part.functionCall.name, part.functionCall.args)
        if (part.executableCode) return (part.executableCode.code ?? '') + '\n'
        if (part.codeExecutionResult) return (part.codeExecutionResult.output ?? '') + '\n'
        return undefined
      }),
    ),
    media: media.length > 0 ? media : undefined,
    usage: usage
      ? compact({
          input: usage.promptTokenCount,
          cached: usage.cachedContentTokenCount ?? 0,
          output: (usage.candidatesTokenCount ?? 0) + (usage.thoughtsTokenCount ?? 0),
        })
      : undefined,
    error: body.error?.message,
  })
}

// imagesDelta 解析 OpenAI 图像接口的响应
function imagesDelta(body: ImagesBody): Delta {
  const type = 'image/' + (body.output_format ?? 'png')
  const data = body.data ?? []
  const media = data.flatMap((item) => {
    if (item.b64_json) return [{ type, url: 'data:' + type + ';base64,' + item.b64_json }]
    if (item.url) return [{ type: /^data:([^;,]+)/.exec(item.url)?.[1] ?? type, url: item.url }]
    return []
  })
  return compact({
    text: joined(data.map((item) => item.revised_prompt)),
    media: media.length > 0 ? media : undefined,
    error: body.error?.message,
  })
}

// transcriptionUsage 统一转写的 tokens 用量，按时长计费的用量不计入
function transcriptionUsage(usage: TranscriptionBody['usage']): Usage | undefined {
  return usage?.type === 'tokens'
    ? compact({ input: usage.input_tokens, output: usage.output_tokens })
    : undefined
}

// transcriptionEvent 解析转写的流事件，完整文本已由增量给出，done 事件只取用量
function transcriptionEvent(body: TranscriptionBody): Delta {
  switch (body.type) {
    case 'transcript.text.delta':
      return compact({ text: body.delta })
    case 'transcript.text.done':
      return compact({ usage: transcriptionUsage(body.usage) })
    default:
      return compact({ error: body.error?.message })
  }
}

// parseObject 解析 JSON 对象，其他值返回 undefined
function parseObject(text: string): object | undefined {
  try {
    const value: unknown = JSON.parse(text)
    return typeof value === 'object' && value !== null ? value : undefined
  } catch {
    return undefined
  }
}

// parseEvent 解析一个 SSE 事件的 data，[DONE] 与非 JSON 数据不产生内容
export function parseEvent(protocol: Protocol, data: string): Delta {
  const body = parseObject(data)
  if (!body) return {}
  switch (protocol) {
    case 'openai-chat':
      return chatDelta(body)
    case 'openai-responses':
      return responsesEvent(body)
    case 'anthropic':
      return anthropicEvent(body)
    case 'openai-transcriptions':
      return transcriptionEvent(body)
    default:
      return geminiDelta(body)
  }
}

// parseResponse 解析成功响应的正文，转写的 text、srt、vtt 格式原样作为正文
export function parseResponse(protocol: Protocol, text: string): Delta {
  const body = parseObject(text)
  switch (protocol) {
    case 'openai-chat':
      return chatDelta(body ?? {})
    case 'openai-responses':
      return responsesObject(body ?? {})
    case 'anthropic':
      return anthropicMessage(body ?? {})
    case 'openai-images':
      return imagesDelta(body ?? {})
    case 'openai-transcriptions':
    case 'openai-translations': {
      if (!body) return { text }
      const transcript = body as TranscriptionBody
      return compact({ text: transcript.text, usage: transcriptionUsage(transcript.usage) })
    }
    default:
      return geminiDelta(body ?? {})
  }
}

// parseError 取出错误正文中的 message，非 JSON 正文原样返回
export function parseError(text: string): string {
  const body = parseObject(text) as ErrorBody | undefined
  return body?.error?.message ?? text
}

// merge 把一段 Delta 追加到已有结果，媒体依次追加，用量按字段覆盖
export function merge(current: Delta, delta: Delta): Delta {
  const append = (left: string | undefined, right: string | undefined) =>
    right === undefined ? left : (left ?? '') + right
  return compact({
    text: append(current.text, delta.text),
    reasoning: append(current.reasoning, delta.reasoning),
    tools: append(current.tools, delta.tools),
    media: delta.media ? [...(current.media ?? []), ...delta.media] : current.media,
    usage: delta.usage ? { ...current.usage, ...delta.usage } : current.usage,
    error: delta.error ?? current.error,
  })
}

// VideoState 是视频任务的当前状态，poll 为下一次查询的路径，content 为成品的下载地址
export interface VideoState {
  done: boolean
  status: string
  poll: string
  content?: string | undefined
  error?: string | undefined
}

interface OpenAIVideo extends ErrorBody {
  id?: string
  status?: string
  progress?: number
}

interface GeminiOperation extends ErrorBody {
  name?: string
  done?: boolean
  response?: {
    generateVideoResponse?: { generatedSamples?: { video?: { uri?: string } }[] }
  }
}

// videoState 读取 OpenAI 视频对象或 Gemini 长时操作的状态
export function videoState(protocol: Protocol, text: string): VideoState {
  if (protocol === 'openai-videos') {
    const video = (parseObject(text) ?? {}) as OpenAIVideo
    const path = '/v1/videos/' + encodeURIComponent(video.id ?? '')
    const status = video.status ?? ''
    return compact({
      done: status === 'completed' || status === 'failed',
      status: video.progress ? status + ' ' + String(video.progress) + '%' : status,
      poll: path,
      content: status === 'completed' ? path + '/content' : undefined,
      error: status === 'failed' ? (video.error?.message ?? status) : undefined,
    })
  }
  const operation = (parseObject(text) ?? {}) as GeminiOperation
  const done = operation.done === true
  return compact({
    done,
    status: done ? 'done' : 'running',
    poll: '/v1beta/' + (operation.name ?? ''),
    content: operation.response?.generateVideoResponse?.generatedSamples?.[0]?.video?.uri,
    error: operation.error?.message,
  })
}
