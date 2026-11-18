import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'

// ApiRoute 对应 GET /api/routes 的一项：方法与 gin 路径
export interface ApiRoute {
  method: string
  path: string
}

// ApiProtocol 是接入页按协议分组的公开接口
export type ApiProtocol = 'openai' | 'anthropic' | 'gemini' | 'mcp'

// apiProtocols 是接入页列出协议的顺序
export const apiProtocols: ApiProtocol[] = ['openai', 'anthropic', 'gemini', 'mcp']

// RouteLabel 是接口用途在 connect 文案中的键
export type RouteLabel =
  | 'routeChat'
  | 'routeResponses'
  | 'routeResponseGet'
  | 'routeResponseDelete'
  | 'routeResponseItems'
  | 'routeResponseCancel'
  | 'routeInputTokens'
  | 'routeCompact'
  | 'routeCompletions'
  | 'routeImages'
  | 'routeImageEdits'
  | 'routeModels'
  | 'routeModel'
  | 'routeGenerate'
  | 'routeMessages'
  | 'routeCountTokens'
  | 'routeFileUpload'
  | 'routeFileList'
  | 'routeFileGet'
  | 'routeFileDelete'
  | 'routeFileContent'
  | 'routeVideoCreate'
  | 'routeVideoList'
  | 'routeVideoGet'
  | 'routeVideoDelete'
  | 'routeVideoContent'
  | 'routeVideoRemix'
  | 'routeVideoExtend'
  | 'routeOperation'
  | 'routeInteractionCreate'
  | 'routeInteractionGet'
  | 'routeInteractionDelete'
  | 'routeInteractionCancel'
  | 'routeCacheCreate'
  | 'routeCacheList'
  | 'routeCacheGet'
  | 'routeCacheUpdate'
  | 'routeCacheDelete'
  | 'routeSpeech'
  | 'routeTranscription'
  | 'routeTranslation'
  | 'routeLive'
  | 'routeMcp'
  | 'routeUnavailable'

// unavailable 是本服务不提供、返回说明原因错误的 OpenAI 接口
const unavailable = ['/v1/embeddings', '/v1/moderations', '/v1/realtime']

// routeLabel 返回接口用途的文案键，没有对应用途时返回 undefined
export function routeLabel({ method, path }: ApiRoute): RouteLabel | undefined {
  if (unavailable.some((prefix) => path.startsWith(prefix))) return 'routeUnavailable'
  const tail = path.replace(/^\/(upload\/)?v1(beta)?/, '')
  const files: Record<string, RouteLabel> = {
    'POST /files': 'routeFileUpload',
    'PUT /files': 'routeFileUpload',
    'GET /files': 'routeFileList',
    'GET /files/:file': 'routeFileGet',
    'DELETE /files/:file': 'routeFileDelete',
    'GET /files/:file/content': 'routeFileContent',
  }
  const exact: Record<string, RouteLabel> = {
    'POST /chat/completions': 'routeChat',
    'POST /responses': 'routeResponses',
    'GET /responses/:response': 'routeResponseGet',
    'DELETE /responses/:response': 'routeResponseDelete',
    'GET /responses/:response/input_items': 'routeResponseItems',
    'POST /responses/:response/cancel': 'routeResponseCancel',
    'POST /responses/input_tokens': 'routeInputTokens',
    'POST /responses/compact': 'routeCompact',
    'POST /completions': 'routeCompletions',
    'POST /images/generations': 'routeImages',
    'POST /images/edits': 'routeImageEdits',
    'GET /models': 'routeModels',
    'GET /models/:model': 'routeModel',
    'POST /models/*action': 'routeGenerate',
    'POST /messages': 'routeMessages',
    'POST /messages/count_tokens': 'routeCountTokens',
    'POST /videos': 'routeVideoCreate',
    'GET /videos': 'routeVideoList',
    'GET /videos/:video': 'routeVideoGet',
    'DELETE /videos/:video': 'routeVideoDelete',
    'GET /videos/:video/content': 'routeVideoContent',
    'POST /videos/:video/remix': 'routeVideoRemix',
    'POST /videos/extensions': 'routeVideoExtend',
    'GET /models/:model/operations/:operation': 'routeOperation',
    'GET /operations/:operation': 'routeOperation',
    'POST /interactions': 'routeInteractionCreate',
    'GET /interactions/:interaction': 'routeInteractionGet',
    'DELETE /interactions/:interaction': 'routeInteractionDelete',
    'POST /interactions/:interaction/cancel': 'routeInteractionCancel',
    'POST /cachedContents': 'routeCacheCreate',
    'GET /cachedContents': 'routeCacheList',
    'GET /cachedContents/:cache': 'routeCacheGet',
    'PATCH /cachedContents/:cache': 'routeCacheUpdate',
    'DELETE /cachedContents/:cache': 'routeCacheDelete',
    'POST /audio/speech': 'routeSpeech',
    'POST /audio/transcriptions': 'routeTranscription',
    'POST /audio/translations': 'routeTranslation',
    ...files,
  }
  if (path.startsWith('/ws/')) return 'routeLive'
  if (path === '/mcp') return 'routeMcp'
  return exact[method + ' ' + tail]
}

// routeProtocols 返回接口所属的协议：模型目录三种协议共用，/v1/files 的元数据接口 OpenAI 与 Gemini 共用，Interactions 与 cachedContents 属于 Gemini
export function routeProtocols({ method, path }: ApiRoute): ApiProtocol[] {
  if (path === '/mcp') return ['mcp']
  if (path.startsWith('/v1/messages')) return ['anthropic']
  if (
    path.startsWith('/v1beta/') ||
    path.startsWith('/upload/') ||
    path.startsWith('/ws/') ||
    path.includes('operations') ||
    path.startsWith('/v1/interactions') ||
    path.startsWith('/v1/cachedContents') ||
    (method === 'POST' && path === '/v1/models/*action')
  ) {
    return ['gemini']
  }
  if (method === 'GET' && (path === '/v1/models' || path === '/v1/models/:model')) {
    return ['openai', 'anthropic', 'gemini']
  }
  if (path.startsWith('/v1/files') && method !== 'POST' && !path.endsWith('/content')) {
    return ['openai', 'gemini']
  }
  return ['openai']
}

// displayPath 把 gin 路径写成文档形式：参数为 {名称}，Gemini 的模型方法为 {model}:{method}
export function displayPath(path: string) {
  return path
    .replace(/\/models\/\*action$/, '/models/{model}:{method}')
    .replace(/[:*](\w+)/g, '{$1}')
}

// routesKey 是公开接口列表的查询键
export const routesKey = ['routes'] as const

// useRoutes 读取服务注册的全部公开接口
export function useRoutes() {
  return useQuery({
    queryKey: routesKey,
    queryFn: async () => (await api<{ routes: ApiRoute[] }>('/api/routes')).routes,
    staleTime: Infinity,
  })
}
