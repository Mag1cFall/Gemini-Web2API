import { createParser } from 'eventsource-parser'

import {
  authHeaders,
  type Delta,
  merge,
  parseError,
  parseEvent,
  parseResponse,
  type Prepared,
  type Protocol,
  videoState,
} from '@/features/playground/protocols'

// Run 是一次请求的进度与结果，firstMs 为流式响应首个事件的耗时，progress 为视频任务的当前状态
export interface Run {
  phase: 'running' | 'done' | 'cancelled'
  protocol: Protocol
  request: Prepared
  status?: number
  requestId?: string
  firstMs?: number
  durationMs?: number
  progress?: string
  result: Delta
  raw: string
}

// Update 把一次进度变化写入 Run
export type Update = (change: Partial<Run>) => void

// videoPollMs 是视频任务的查询间隔
const videoPollMs = 10_000

// wait 等待指定毫秒，停止时立即结束
function wait(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const timer = setTimeout(resolve, ms)
    signal.addEventListener(
      'abort',
      () => {
        clearTimeout(timer)
        reject(new Error('aborted'))
      },
      { once: true },
    )
  })
}

// readStream 逐个解析 SSE 事件并在每段数据到达时回调最新进度
async function readStream(response: Response, protocol: Protocol, started: number, update: Update) {
  let result: Delta = {}
  let raw = ''
  let firstMs: number | undefined
  const parser = createParser({
    onEvent: (event) => {
      result = merge(result, parseEvent(protocol, event.data))
      firstMs ??= performance.now() - started
    },
  })
  if (!response.body) return
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    raw += value
    parser.feed(value)
    update({ raw, result, ...(firstMs === undefined ? {} : { firstMs }) })
  }
}

// download 下载生成的媒体并返回 blob URL，失败时返回错误说明
async function download(url: string, headers: Record<string, string>, signal: AbortSignal) {
  const response = await fetch(url, { headers, signal })
  if (!response.ok) return { status: response.status, error: parseError(await response.text()) }
  const blob = await response.blob()
  return { status: response.status, media: { type: blob.type, url: URL.createObjectURL(blob) } }
}

// followVideo 轮询视频任务直到完成，再下载成品
async function followVideo(
  text: string,
  protocol: Protocol,
  apiKey: string,
  signal: AbortSignal,
  update: Update,
) {
  const headers = authHeaders(protocol, apiKey)
  let raw = text
  let state = videoState(protocol, raw)
  update({ progress: state.status })
  while (!state.done) {
    await wait(videoPollMs, signal)
    const poll = await fetch(state.poll, { headers, signal })
    raw = await poll.text()
    if (!poll.ok) {
      update({ status: poll.status, raw, result: { error: parseError(raw) } })
      return
    }
    state = videoState(protocol, raw)
    update({ raw, progress: state.status })
  }
  if (state.error || !state.content) {
    update({ result: { error: state.error ?? state.status } })
    return
  }
  const content = await download(state.content, headers, signal)
  update({
    status: content.status,
    result: content.media ? { media: [content.media] } : { error: content.error },
  })
}

// transfer 发送请求并按响应方式读取结果
async function transfer(
  request: Prepared,
  protocol: Protocol,
  apiKey: string,
  signal: AbortSignal,
  started: number,
  update: Update,
) {
  const response = await fetch(request.url, {
    method: 'POST',
    headers: request.headers,
    body: request.body instanceof FormData ? request.body : JSON.stringify(request.body),
    signal,
  })
  update({ status: response.status, requestId: response.headers.get('X-Request-ID') ?? '' })
  if (!response.ok) {
    const raw = await response.text()
    update({ raw, result: { error: parseError(raw) } })
    return
  }
  switch (request.response) {
    case 'stream':
      await readStream(response, protocol, started, update)
      return
    case 'audio': {
      const blob = await response.blob()
      update({
        raw: JSON.stringify({ content_type: blob.type, size: blob.size }, null, 2),
        result: { media: [{ type: blob.type, url: URL.createObjectURL(blob) }] },
      })
      return
    }
    case 'video': {
      const raw = await response.text()
      update({ raw })
      await followVideo(raw, protocol, apiKey, signal, update)
      return
    }
    case 'json': {
      const raw = await response.text()
      update({ raw, result: parseResponse(protocol, raw) })
    }
  }
}

// execute 完成一次请求并记录总耗时，网络错误写入结果，停止后不再更新
export async function execute(
  request: Prepared,
  protocol: Protocol,
  apiKey: string,
  signal: AbortSignal,
  update: Update,
  networkError: (message: string) => string,
) {
  const started = performance.now()
  try {
    await transfer(request, protocol, apiKey, signal, started, update)
    update({ phase: 'done', durationMs: performance.now() - started })
  } catch (error) {
    if (signal.aborted) return
    update({
      phase: 'done',
      durationMs: performance.now() - started,
      result: { error: networkError(error instanceof Error ? error.message : String(error)) },
    })
  }
}
