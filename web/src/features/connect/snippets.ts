// SnippetInput 是生成客户端配置所需的地址、key 与模型
export interface SnippetInput {
  origin: string
  key: string
  model: string
  smallModel: string
  models: { id: string; name: string }[]
}

// Shell 是环境变量写法对应的终端
export type Shell = 'unix' | 'windows'

// posixQuote 返回 POSIX shell 的单引号字面量
function posixQuote(value: string) {
  return "'" + value.replaceAll("'", "'\\''") + "'"
}

// powershellQuote 返回 PowerShell 的单引号字面量，PowerShell 把弯单引号也当作单引号
function powershellQuote(value: string) {
  return "'" + value.replace(/['‘’‚‛]/g, '$&$&') + "'"
}

// envBlock 按终端写出设置环境变量的命令，command 是随后执行的命令
function envBlock(shell: Shell, vars: [string, string][], command: string) {
  const lines =
    shell === 'windows'
      ? vars.map(([name, value]) => `$env:${name} = ${powershellQuote(value)}`)
      : vars.map(([name, value]) => `export ${name}=${posixQuote(value)}`)
  return [...lines, command].join('\n')
}

// claudeVars 是 Claude Code 使用的环境变量
function claudeVars(input: SnippetInput): [string, string][] {
  return [
    ['ANTHROPIC_BASE_URL', input.origin],
    ['ANTHROPIC_AUTH_TOKEN', input.key],
    ['ANTHROPIC_MODEL', input.model],
    ['ANTHROPIC_DEFAULT_HAIKU_MODEL', input.smallModel],
  ]
}

// claudeEnv 是启动 Claude Code 的终端命令
export function claudeEnv(input: SnippetInput, shell: Shell) {
  return envBlock(shell, claudeVars(input), 'claude')
}

// claudeSettings 是 ~/.claude/settings.json 的 env 段
export function claudeSettings(input: SnippetInput) {
  return JSON.stringify({ env: Object.fromEntries(claudeVars(input)) }, null, 2)
}

// codexConfig 是 ~/.codex/config.toml，以 Responses 协议接入
export function codexConfig(input: SnippetInput) {
  return [
    `model = "${input.model}"`,
    'model_provider = "gemini-web2api"',
    '',
    '[model_providers.gemini-web2api]',
    'name = "Gemini-Web2API"',
    `base_url = "${input.origin}/v1"`,
    'env_key = "GEMINI_WEB2API_KEY"',
    'wire_api = "responses"',
  ].join('\n')
}

// codexEnv 是设置 Codex 所读 key 的终端命令
export function codexEnv(input: SnippetInput, shell: Shell) {
  return envBlock(shell, [['GEMINI_WEB2API_KEY', input.key]], 'codex')
}

// opencodeConfig 是 ~/.config/opencode/opencode.json 中的自定义提供商
export function opencodeConfig(input: SnippetInput) {
  return JSON.stringify(
    {
      $schema: 'https://opencode.ai/config.json',
      provider: {
        'gemini-web2api': {
          npm: '@ai-sdk/openai-compatible',
          name: 'Gemini-Web2API',
          options: { baseURL: input.origin + '/v1', apiKey: input.key },
          models: Object.fromEntries(input.models.map((model) => [model.id, { name: model.name }])),
        },
      },
      model: 'gemini-web2api/' + input.model,
    },
    null,
    2,
  )
}

// openclawConfig 是 OpenClaw 的配置文件，默认模型为所选模型
export function openclawConfig(input: SnippetInput) {
  return JSON.stringify(
    {
      agents: { defaults: { model: { primary: 'gemini-web2api/' + input.model } } },
      models: {
        providers: {
          'gemini-web2api': {
            baseUrl: input.origin + '/v1',
            apiKey: input.key,
            api: 'openai-completions',
            models: input.models,
          },
        },
      },
    },
    null,
    2,
  )
}

// ompModels 是 omp 的模型配置文件，经 Gemini 协议接入
export function ompModels(input: SnippetInput) {
  return [
    'providers:',
    '  gemini-web2api:',
    `    baseUrl: ${input.origin}/v1beta`,
    '    api: google-generative-ai',
    '    apiKey: GEMINI_WEB2API_KEY',
    '    models:',
    ...input.models.map((model) => `      - id: ${model.id}`),
  ].join('\n')
}

// ompEnv 是设置 key 后启动 omp 的终端命令
export function ompEnv(input: SnippetInput, shell: Shell) {
  return envBlock(
    shell,
    [['GEMINI_WEB2API_KEY', input.key]],
    `omp --provider gemini-web2api --model ${input.model}`,
  )
}

// magpieImport 是把本服务导入 Magpie 的命令，模型由 Magpie 从 /v1/models 读取
export function magpieImport(input: SnippetInput) {
  const query = new URLSearchParams({
    name: 'Gemini-Web2API',
    chat: input.origin + '/v1',
    responses: input.origin + '/v1',
    anthropic: input.origin,
    gemini: input.origin + '/v1beta',
    key: input.key,
  })
  return `magpie import 'magpie://import?${query.toString()}'`
}

// curlOpenai 是 OpenAI Chat Completions 的流式请求
export function curlOpenai(input: SnippetInput) {
  return [
    `curl ${posixQuote(input.origin + '/v1/chat/completions')} \\`,
    `  -H ${posixQuote('Authorization: Bearer ' + input.key)} \\`,
    '  -H "Content-Type: application/json" \\',
    `  -d ${posixQuote(`{"model": ${JSON.stringify(input.model)}, "stream": true, "messages": [{"role": "user", "content": "Hello"}]}`)}`,
  ].join('\n')
}

// curlAnthropic 是 Anthropic Messages 请求
export function curlAnthropic(input: SnippetInput) {
  return [
    `curl ${posixQuote(input.origin + '/v1/messages')} \\`,
    `  -H ${posixQuote('x-api-key: ' + input.key)} \\`,
    '  -H "anthropic-version: 2023-06-01" \\',
    '  -H "Content-Type: application/json" \\',
    `  -d ${posixQuote(`{"model": ${JSON.stringify(input.model)}, "max_tokens": 1024, "messages": [{"role": "user", "content": "Hello"}]}`)}`,
  ].join('\n')
}

// curlGemini 是 Gemini generateContent 请求
export function curlGemini(input: SnippetInput) {
  return [
    `curl ${posixQuote(`${input.origin}/v1beta/models/${input.model}:generateContent`)} \\`,
    `  -H ${posixQuote('x-goog-api-key: ' + input.key)} \\`,
    '  -H "Content-Type: application/json" \\',
    `  -d '{"contents": [{"role": "user", "parts": [{"text": "Hello"}]}]}'`,
  ].join('\n')
}
