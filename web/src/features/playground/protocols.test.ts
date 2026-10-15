import { describe, expect, it } from 'vitest'

import {
  type Attachment,
  type Delta,
  merge,
  modeModels,
  parseError,
  parseEvent,
  parseResponse,
  type PlaygroundInput,
  prepare,
  type Protocol,
  videoState,
} from '@/features/playground/protocols'

const png: Attachment = { name: 'a.png', type: 'image/png', data: 'iVBORw0K' }
const wav: Attachment = { name: 'a.wav', type: 'audio/wav', data: 'UklGRg==' }
const pdf: Attachment = { name: 'a.pdf', type: 'application/pdf', data: 'JVBERi0=' }

const input: PlaygroundInput = {
  mode: 'text',
  protocol: 'openai-chat',
  model: 'gemini-3-pro',
  apiKey: 'sk-test',
  prompt: 'Hi',
  system: 'Be brief.',
  stream: true,
  reasoning: 'medium',
  webSearch: false,
  codeExecution: false,
  schema: undefined,
  functions: [],
  attachments: [],
  aspectRatio: '',
  size: '',
  transcriptFormat: 'json',
}

const schema = { type: 'object', properties: { answer: { type: 'string' } } }
const weather = {
  name: 'get_weather',
  description: 'Weather',
  parameters: { type: 'object', properties: { city: { type: 'string' } } },
}

// collect 依次解析流事件并合并
function collect(protocol: Protocol, events: unknown[]): Delta {
  return events.reduce<Delta>(
    (result, event) =>
      merge(
        result,
        parseEvent(protocol, typeof event === 'string' ? event : JSON.stringify(event)),
      ),
    {},
  )
}

// fields 列出 multipart 请求的字段，文件写成名称与类型
function fields(body: unknown) {
  expect(body).toBeInstanceOf(FormData)
  return [...(body as FormData).entries()].map(([key, value]) => [
    key,
    typeof value === 'string' ? value : value.name + ' ' + value.type,
  ])
}

describe('modeModels', () => {
  it('图像、音乐、视频只用对应官网工具的条目，其余输出类型排除这三种工具', () => {
    const models = [
      { id: 'flash' },
      { id: 'image', tool: 'image' },
      { id: 'video', tool: 'video' },
      { id: 'music', tool: 'music' },
      { id: 'canvas', tool: 'canvas' },
    ]
    const ids = (mode: Parameters<typeof modeModels>[0]) =>
      modeModels(mode, models).map((model) => model.id)
    expect(ids('image')).toEqual(['image'])
    expect(ids('video')).toEqual(['video'])
    expect(ids('music')).toEqual(['music'])
    expect(ids('text')).toEqual(['flash', 'canvas'])
    expect(ids('speech')).toEqual(['flash', 'canvas'])
    expect(ids('transcription')).toEqual(['flash', 'canvas'])
  })
})

describe('prepare', () => {
  it('按 Chat Completions 构造消息、流式用量、思考强度、搜索、JSON Schema 与函数工具', () => {
    const request = prepare({ ...input, webSearch: true, schema, functions: [weather] })
    expect(request.url).toBe('/v1/chat/completions')
    expect(request.headers.Authorization).toBe('Bearer sk-test')
    expect(request.body).toEqual({
      model: 'gemini-3-pro',
      messages: [
        { role: 'system', content: 'Be brief.' },
        { role: 'user', content: 'Hi' },
      ],
      stream: true,
      stream_options: { include_usage: true },
      reasoning_effort: 'medium',
      web_search_options: {},
      response_format: { type: 'json_schema', json_schema: { name: 'response', schema } },
      tools: [{ type: 'function', function: weather }],
    })
    expect(prepare({ ...input, reasoning: 'off' }).body).toHaveProperty('reasoning_effort', 'none')
  })

  it('Chat Completions 的图片为 image_url，wav 为 input_audio，其他文件为 file', () => {
    const body = prepare({ ...input, attachments: [png, wav, pdf] }).body as {
      messages: { content: unknown }[]
    }
    expect(body.messages[1]?.content).toEqual([
      { type: 'image_url', image_url: { url: 'data:image/png;base64,iVBORw0K' } },
      { type: 'input_audio', input_audio: { data: 'UklGRg==', format: 'wav' } },
      {
        type: 'file',
        file: { filename: 'a.pdf', file_data: 'data:application/pdf;base64,JVBERi0=' },
      },
      { type: 'text', text: 'Hi' },
    ])
  })

  it('按 Responses 构造输入内容、思考摘要、内置工具、函数与 text.format', () => {
    const request = prepare({
      ...input,
      protocol: 'openai-responses',
      stream: false,
      webSearch: true,
      codeExecution: true,
      schema,
      functions: [weather],
      attachments: [png, pdf],
    })
    expect(request.url).toBe('/v1/responses')
    expect(request.body).toEqual({
      model: 'gemini-3-pro',
      instructions: 'Be brief.',
      input: [
        {
          role: 'user',
          content: [
            { type: 'input_image', image_url: 'data:image/png;base64,iVBORw0K' },
            {
              type: 'input_file',
              filename: 'a.pdf',
              file_data: 'data:application/pdf;base64,JVBERi0=',
            },
            { type: 'input_text', text: 'Hi' },
          ],
        },
      ],
      stream: false,
      reasoning: { effort: 'medium', summary: 'auto' },
      tools: [
        { type: 'web_search' },
        { type: 'code_interpreter', container: { type: 'auto' } },
        { type: 'function', ...weather },
      ],
      text: { format: { type: 'json_schema', name: 'response', schema } },
    })
  })

  it('按 Anthropic Messages 携带版本头、base64 内容块、思考强度与工具', () => {
    const request = prepare({
      ...input,
      protocol: 'anthropic',
      webSearch: true,
      functions: [weather],
      attachments: [png, pdf],
    })
    expect(request.url).toBe('/v1/messages')
    expect(request.headers['x-api-key']).toBe('sk-test')
    expect(request.headers['anthropic-version']).toBe('2023-06-01')
    const body = request.body as Record<string, unknown>
    expect(body.max_tokens).toBeGreaterThan(0)
    expect(body.system).toBe('Be brief.')
    expect(body.messages).toEqual([
      {
        role: 'user',
        content: [
          { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'iVBORw0K' } },
          {
            type: 'document',
            source: { type: 'base64', media_type: 'application/pdf', data: 'JVBERi0=' },
          },
          { type: 'text', text: 'Hi' },
        ],
      },
    ])
    expect(body.thinking).toEqual({ type: 'adaptive' })
    expect(body.output_config).toEqual({ effort: 'medium' })
    expect(body.tools).toEqual([
      { type: 'web_search_20250305', name: 'web_search' },
      { name: 'get_weather', description: 'Weather', input_schema: weather.parameters },
    ])
    const off = prepare({ ...input, protocol: 'anthropic', reasoning: 'off' }).body
    expect(off).not.toHaveProperty('thinking')
    expect(off).not.toHaveProperty('output_config')
  })

  it('按 Gemini 选择流式路径、转义模型名并写入内联数据、思考、Schema 与工具', () => {
    const stream = prepare({
      ...input,
      protocol: 'gemini',
      model: 'models/a b',
      webSearch: true,
      codeExecution: true,
      schema,
      functions: [weather],
      attachments: [png],
    })
    expect(stream.url).toBe('/v1beta/models/models%2Fa%20b:streamGenerateContent?alt=sse')
    expect(stream.headers['x-goog-api-key']).toBe('sk-test')
    expect(stream.body).toEqual({
      systemInstruction: { parts: [{ text: 'Be brief.' }] },
      contents: [
        {
          role: 'user',
          parts: [{ inlineData: { mimeType: 'image/png', data: 'iVBORw0K' } }, { text: 'Hi' }],
        },
      ],
      generationConfig: {
        thinkingConfig: { thinkingLevel: 'medium', includeThoughts: true },
        responseMimeType: 'application/json',
        responseJsonSchema: schema,
      },
      tools: [{ googleSearch: {} }, { codeExecution: {} }, { functionDeclarations: [weather] }],
    })
    const unary = prepare({
      ...input,
      protocol: 'gemini',
      stream: false,
      system: ' ',
      reasoning: 'off',
    })
    expect(unary.url).toBe('/v1beta/models/gemini-3-pro:generateContent')
    expect(unary.body).toEqual({ contents: [{ role: 'user', parts: [{ text: 'Hi' }] }] })
  })

  it('Gemini 图像用 generateContent 与 imageConfig.aspectRatio，视频用 predictLongRunning', () => {
    const image = prepare({
      ...input,
      mode: 'image',
      protocol: 'gemini',
      aspectRatio: '16:9',
      attachments: [png],
    })
    expect(image.url).toBe('/v1beta/models/gemini-3-pro:generateContent')
    expect(image.body).toEqual({
      contents: [
        {
          role: 'user',
          parts: [{ inlineData: { mimeType: 'image/png', data: 'iVBORw0K' } }, { text: 'Hi' }],
        },
      ],
      generationConfig: { imageConfig: { aspectRatio: '16:9' } },
    })
    const video = prepare({
      ...input,
      mode: 'video',
      protocol: 'gemini',
      aspectRatio: '9:16',
      attachments: [png],
    })
    expect(video.url).toBe('/v1beta/models/gemini-3-pro:predictLongRunning')
    expect(video.body).toEqual({
      instances: [
        { prompt: 'Hi', image: { bytesBase64Encoded: 'iVBORw0K', mimeType: 'image/png' } },
      ],
      parameters: { aspectRatio: '9:16' },
    })
  })

  it('OpenAI 图像无图时为 generations JSON，带图时为 edits multipart', () => {
    const generation = prepare({
      ...input,
      mode: 'image',
      protocol: 'openai-images',
      size: '1024x1024',
    })
    expect(generation.url).toBe('/v1/images/generations')
    expect(generation.body).toEqual({
      model: 'gemini-3-pro',
      prompt: 'Hi',
      n: 1,
      size: '1024x1024',
    })
    const edit = prepare({
      ...input,
      mode: 'image',
      protocol: 'openai-images',
      attachments: [png, png],
    })
    expect(edit.url).toBe('/v1/images/edits')
    expect(edit.headers).not.toHaveProperty('Content-Type')
    expect(fields(edit.body)).toEqual([
      ['model', 'gemini-3-pro'],
      ['prompt', 'Hi'],
      ['n', '1'],
      ['image[]', 'a.png image/png'],
      ['image[]', 'a.png image/png'],
    ])
  })

  it('朗读为 speech JSON，转写与翻译为带 file 的 multipart，只有转写的 json 与 text 可流式', () => {
    const speech = prepare({ ...input, mode: 'speech', protocol: 'openai-speech' })
    expect(speech.url).toBe('/v1/audio/speech')
    expect(speech.body).toEqual({ model: 'gemini-3-pro', input: 'Hi' })
    const transcription = prepare({
      ...input,
      mode: 'transcription',
      protocol: 'openai-transcriptions',
      attachments: [wav],
    })
    expect(transcription.url).toBe('/v1/audio/transcriptions')
    expect(transcription.response).toBe('stream')
    expect(fields(transcription.body)).toEqual([
      ['model', 'gemini-3-pro'],
      ['response_format', 'json'],
      ['prompt', 'Hi'],
      ['stream', 'true'],
      ['file', 'a.wav audio/wav'],
    ])
    const subtitles = prepare({
      ...input,
      mode: 'transcription',
      protocol: 'openai-transcriptions',
      transcriptFormat: 'srt',
      prompt: '',
      attachments: [wav],
    })
    expect(fields(subtitles.body)).toEqual([
      ['model', 'gemini-3-pro'],
      ['response_format', 'srt'],
      ['file', 'a.wav audio/wav'],
    ])
    const translation = prepare({
      ...input,
      mode: 'transcription',
      protocol: 'openai-translations',
      attachments: [wav],
    })
    expect(translation.url).toBe('/v1/audio/translations')
    expect(fields(translation.body).map(([key]) => key)).not.toContain('stream')
  })

  it('OpenAI 视频按宽高比写 size，带参考图时为 input_reference multipart', () => {
    const json = prepare({
      ...input,
      mode: 'video',
      protocol: 'openai-videos',
      aspectRatio: '9:16',
    })
    expect(json.url).toBe('/v1/videos')
    expect(json.body).toEqual({ model: 'gemini-3-pro', prompt: 'Hi', size: '720x1280' })
    const reference = prepare({
      ...input,
      mode: 'video',
      protocol: 'openai-videos',
      attachments: [png],
    })
    expect(fields(reference.body)).toEqual([
      ['model', 'gemini-3-pro'],
      ['prompt', 'Hi'],
      ['input_reference', 'a.png image/png'],
    ])
  })

  it('没有 API key 时不带认证头', () => {
    for (const protocol of ['openai-chat', 'anthropic', 'gemini', 'openai-speech'] as const) {
      const { headers } = prepare({ ...input, protocol, apiKey: '' })
      expect(headers).not.toHaveProperty('Authorization')
      expect(headers).not.toHaveProperty('x-api-key')
      expect(headers).not.toHaveProperty('x-goog-api-key')
    }
  })
})

describe('parseEvent', () => {
  it('合并 Chat Completions 的正文、reasoning_content、分段工具调用与末尾用量', () => {
    const result = collect('openai-chat', [
      { choices: [{ delta: { role: 'assistant', reasoning_content: '想' } }] },
      { choices: [{ delta: { content: '你' } }] },
      { choices: [{ delta: { content: '好' } }] },
      {
        choices: [
          { delta: { tool_calls: [{ index: 0, function: { name: 'f', arguments: '{"a"' } }] } },
        ],
      },
      { choices: [{ delta: { tool_calls: [{ index: 0, function: { arguments: ':1}' } }] } }] },
      {
        choices: [],
        usage: {
          prompt_tokens: 10,
          completion_tokens: 5,
          prompt_tokens_details: { cached_tokens: 4 },
        },
      },
      '[DONE]',
    ])
    expect(result).toEqual({
      text: '你好',
      reasoning: '想',
      tools: '\nf {"a":1}',
      usage: { input: 10, cached: 4, output: 5 },
    })
  })

  it('解析 Responses 的文本、思考摘要、完成事件中的工具、图片与用量及错误事件', () => {
    expect(
      collect('openai-responses', [
        { type: 'response.reasoning_summary_text.delta', delta: 'r' },
        { type: 'response.output_text.delta', delta: 'a' },
        { type: 'response.output_text.delta', delta: 'b' },
        {
          type: 'response.completed',
          response: {
            output: [
              { type: 'message', content: [{ type: 'output_text', text: 'ab' }] },
              { type: 'function_call', name: 'f', arguments: '{}' },
              { type: 'image_generation_call', result: 'AAAA' },
            ],
            usage: {
              input_tokens: 7,
              output_tokens: 3,
              input_tokens_details: { cached_tokens: 2 },
            },
          },
        },
      ]),
    ).toEqual({
      text: 'ab',
      reasoning: 'r',
      tools: 'f {}\n',
      media: [{ type: 'image/png', url: 'data:image/png;base64,AAAA' }],
      usage: { input: 7, cached: 2, output: 3 },
    })
    expect(
      parseEvent('openai-responses', JSON.stringify({ type: 'error', message: 'boom' })),
    ).toEqual({
      error: 'boom',
    })
  })

  it('Anthropic 的 input 加回缓存读写，output 取 message_delta，tool_use 参数分段到达', () => {
    expect(
      collect('anthropic', [
        {
          type: 'message_start',
          message: {
            usage: {
              input_tokens: 3,
              cache_read_input_tokens: 90,
              cache_creation_input_tokens: 7,
              output_tokens: 1,
            },
          },
        },
        {
          type: 'content_block_delta',
          index: 0,
          delta: { type: 'thinking_delta', thinking: 'hm' },
        },
        {
          type: 'content_block_delta',
          index: 0,
          delta: { type: 'signature_delta', signature: 'x' },
        },
        { type: 'content_block_delta', index: 1, delta: { type: 'text_delta', text: 'ok' } },
        {
          type: 'content_block_start',
          index: 2,
          content_block: { type: 'tool_use', id: 't', name: 'f', input: {} },
        },
        {
          type: 'content_block_delta',
          index: 2,
          delta: { type: 'input_json_delta', partial_json: '{}' },
        },
        { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 12 } },
        { type: 'message_stop' },
      ]),
    ).toEqual({
      text: 'ok',
      reasoning: 'hm',
      tools: '\nf {}',
      usage: { input: 100, cached: 90, output: 12 },
    })
  })

  it('Gemini 的 thought 部分计为思考，inlineData 为媒体，输出用量含思考 token', () => {
    expect(
      collect('gemini', [
        { candidates: [{ content: { parts: [{ text: 'plan', thought: true }] } }] },
        {
          candidates: [
            {
              content: {
                parts: [{ text: 'done' }, { inlineData: { mimeType: 'audio/mpeg', data: 'SUQz' } }],
              },
            },
          ],
          usageMetadata: {
            promptTokenCount: 20,
            cachedContentTokenCount: 8,
            candidatesTokenCount: 2,
            thoughtsTokenCount: 6,
          },
        },
      ]),
    ).toEqual({
      text: 'done',
      reasoning: 'plan',
      media: [{ type: 'audio/mpeg', url: 'data:audio/mpeg;base64,SUQz' }],
      usage: { input: 20, cached: 8, output: 8 },
    })
  })

  it('转写流的增量拼成正文，done 事件只提供 tokens 用量', () => {
    expect(
      collect('openai-transcriptions', [
        { type: 'transcript.text.delta', delta: 'Hel' },
        { type: 'transcript.text.delta', delta: 'lo' },
        {
          type: 'transcript.text.done',
          text: 'Hello',
          usage: { type: 'tokens', input_tokens: 40, output_tokens: 2, total_tokens: 42 },
        },
      ]),
    ).toEqual({ text: 'Hello', usage: { input: 40, output: 2 } })
  })
})

describe('parseResponse', () => {
  it('解析各协议的非流式响应', () => {
    expect(
      parseResponse(
        'openai-responses',
        JSON.stringify({
          output: [
            { type: 'reasoning', summary: [{ type: 'summary_text', text: 'r' }] },
            { type: 'message', content: [{ type: 'output_text', text: 'a' }] },
          ],
          usage: { input_tokens: 1, output_tokens: 2 },
        }),
      ),
    ).toEqual({ text: 'a', reasoning: 'r', usage: { input: 1, cached: 0, output: 2 } })
    expect(
      parseResponse(
        'anthropic',
        JSON.stringify({
          content: [
            { type: 'thinking', thinking: 't' },
            { type: 'text', text: 'x' },
            { type: 'tool_use', id: 'u', name: 'f', input: { a: 1 } },
          ],
          usage: { input_tokens: 4, output_tokens: 1 },
        }),
      ),
    ).toEqual({
      text: 'x',
      reasoning: 't',
      tools: 'f {"a":1}\n',
      usage: { input: 4, cached: 0, output: 1 },
    })
    expect(
      parseResponse(
        'openai-chat',
        JSON.stringify({
          choices: [
            {
              message: {
                content: null,
                tool_calls: [
                  { id: 'c', type: 'function', function: { name: 'f', arguments: '{}' } },
                ],
              },
            },
          ],
        }),
      ),
    ).toEqual({ tools: 'f {}\n' })
  })

  it('OpenAI 图像的 b64_json 按 output_format 成为图片', () => {
    expect(
      parseResponse(
        'openai-images',
        JSON.stringify({ created: 1, output_format: 'jpeg', data: [{ b64_json: '/9j/' }] }),
      ),
    ).toEqual({ media: [{ type: 'image/jpeg', url: 'data:image/jpeg;base64,/9j/' }] })
  })

  it('转写的 json 取 text 与 tokens 用量，text、srt、vtt 原样作为正文', () => {
    expect(
      parseResponse(
        'openai-transcriptions',
        JSON.stringify({
          text: 'hi',
          usage: { type: 'tokens', input_tokens: 9, output_tokens: 1 },
        }),
      ),
    ).toEqual({ text: 'hi', usage: { input: 9, output: 1 } })
    expect(
      parseResponse(
        'openai-translations',
        JSON.stringify({ text: 'hi', usage: { type: 'duration', seconds: 3 } }),
      ),
    ).toEqual({ text: 'hi' })
    const srt = '1\n00:00:00,000 --> 00:00:01,000\nhi\n\n'
    expect(parseResponse('openai-transcriptions', srt)).toEqual({ text: srt })
  })
})

describe('parseError', () => {
  it('取出各协议错误正文中的 message，非 JSON 正文原样返回', () => {
    expect(parseError(JSON.stringify({ type: 'error', error: { message: 'overloaded' } }))).toBe(
      'overloaded',
    )
    expect(parseError(JSON.stringify({ error: { code: 429, message: 'quota' } }))).toBe('quota')
    expect(parseError('Bad Gateway')).toBe('Bad Gateway')
  })
})

describe('videoState', () => {
  it('OpenAI 视频对象按 status 轮询 /v1/videos/{id}，完成后从 content 下载', () => {
    expect(
      videoState(
        'openai-videos',
        JSON.stringify({ id: 'video_1', status: 'in_progress', progress: 10 }),
      ),
    ).toEqual({ done: false, status: 'in_progress 10%', poll: '/v1/videos/video_1' })
    expect(
      videoState(
        'openai-videos',
        JSON.stringify({ id: 'video_1', status: 'completed', progress: 100 }),
      ),
    ).toMatchObject({ done: true, content: '/v1/videos/video_1/content' })
    expect(
      videoState(
        'openai-videos',
        JSON.stringify({ id: 'video_1', status: 'failed', error: { message: 'no video' } }),
      ),
    ).toMatchObject({ done: true, error: 'no video' })
  })

  it('Gemini 长时操作按 name 轮询，done 后取 generatedSamples 的视频 uri', () => {
    const name = 'models/veo/operations/op1'
    expect(videoState('gemini', JSON.stringify({ name }))).toEqual({
      done: false,
      status: 'running',
      poll: '/v1beta/' + name,
    })
    expect(
      videoState(
        'gemini',
        JSON.stringify({
          name,
          done: true,
          response: {
            generateVideoResponse: { generatedSamples: [{ video: { uri: 'http://x/v.mp4' } }] },
          },
        }),
      ),
    ).toMatchObject({ done: true, content: 'http://x/v.mp4' })
    expect(
      videoState(
        'gemini',
        JSON.stringify({ name, done: true, error: { code: 14, message: 'busy' } }),
      ),
    ).toMatchObject({ done: true, error: 'busy' })
  })
})
