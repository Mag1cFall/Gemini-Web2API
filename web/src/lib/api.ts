import { localizeError } from '@/lib/errorMessages'

// ApiError 是管理 API 返回的错误，code 取自响应体，message 为该 code 在当前界面语言的文案，未知 code 为响应体原文
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
  }
}

// basePath 是管理端所在的路径，以 / 结尾；经反向代理挂在子路径下时为该子路径，取自服务写入入口页的 <base>
export const basePath = new URL(document.baseURI).pathname

// serviceURL 是浏览器访问本服务的根地址，不以 / 结尾
export const serviceURL = new URL(document.baseURI).origin + basePath.slice(0, -1)

// appURL 把以 / 开头的服务路径换成 basePath 下的路径，其他地址原样返回
export function appURL(path: string) {
  return path.startsWith('/') ? basePath + path.slice(1) : path
}

interface ErrorBody {
  error?: { code?: string; message?: string }
}

// api 以 JSON 调用同源管理 API，非 2xx 响应抛出 ApiError
export async function api<T>(
  path: string,
  init: RequestInit & { json?: unknown } = {},
): Promise<T> {
  const { json, ...rest } = init
  const headers = new Headers(rest.headers)
  headers.set('Accept', 'application/json')
  if (json !== undefined) headers.set('Content-Type', 'application/json')
  let response: Response
  try {
    response = await fetch(appURL(path), {
      ...rest,
      headers,
      body: json === undefined ? (rest.body ?? null) : JSON.stringify(json),
      credentials: 'same-origin',
    })
  } catch (error) {
    throw new ApiError(
      0,
      'network',
      localizeError('network', error instanceof Error ? error.message : String(error)),
    )
  }
  if (response.status === 204) return undefined as T
  const text = await response.text()
  let body: unknown
  try {
    body = text ? JSON.parse(text) : undefined
  } catch (error) {
    if (response.ok) throw error
  }
  if (!response.ok) {
    const detail = (body as ErrorBody | undefined)?.error
    const code = detail?.code ?? 'http_' + String(response.status)
    const message = detail?.message ?? (response.statusText || 'HTTP ' + String(response.status))
    throw new ApiError(response.status, code, localizeError(code, message))
  }
  return body as T
}
