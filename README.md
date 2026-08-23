# Gemini-Web2API (Go Version)

将 Google Gemini Web 网页版转换为 OpenAI、Claude 和 Gemini 兼容 API。

## 特性

- **OpenAI 兼容**：`/v1/chat/completions`、`/v1/responses`、`/v1/models`
- **Claude 兼容**：`/v1/messages`
- **Gemini 兼容**：`/v1beta/models/{model}:generateContent`、`:streamGenerateContent`
- **流式输出**：文本与思考内容实时增量返回，并映射各协议的思考强度
- **图片能力**：支持多模态输入、图片生成和图片编辑
- **多账户负载均衡**：按模型可用性调度多个 Google 账户
- **HTTP 代理**：支持全局代理和每账号固定代理
- **模型映射**：支持将外部模型名映射到当前 Gemini 模型
- **纯协议运行**：完成账号配置后无需浏览器和前端脚本

## 支持的模型

服务启动时通过 Gemini Web 原生模型目录接口读取每个账号当前可用的模型，不维护静态模型表。运行后访问 `/v1/models` 获取可用模型 ID。服务按 Gemini tokenizer 返回输入、可见输出和思考摘要的 token usage；账号用量通过 `/v1/accounts/usage` 查看。

## 快速开始

### 1. 编译

```bash
go build -o gemini-web2api.exe ./cmd/gemini-web2api
```

### 2. 配置账号

Windows 首次使用时，直接运行交互式配置：

```powershell
.\gemini-web2api.exe setup
```

`setup` 会扫描本机 Chrome 账号，并短暂启动独立临时 Chrome 进程读取 App-Bound 主密钥。现有 Chrome 窗口与 Profile 保持原状。

也可以直接指定 Chrome 中已登录的账号：

```powershell
.\gemini-web2api.exe setup --email "name@example.com"
```

`setup` 会生成 `auth/<账号>/storage-state.json` 并验证模型目录。日常启动只需运行：

```powershell
.\gemini-web2api.exe
```

默认读取 `auth` 目录并监听 `127.0.0.1:8007`。Linux 和 macOS 可以直接使用已经生成的 `auth` 目录。

### 3. 可选配置

程序启动时自动读取当前目录的 `.env`。默认配置可以直接使用；需要调整时复制 `.env.example` 为 `.env`。

```dotenv
# 默认使用 Gemini Temporary Chat，不写入官网历史记录
GEMINI_SAVE_HISTORY=false

# 本地 2API 访问密钥
PROXY_API_KEY=

# HTTP、HTTPS 或 SOCKS5 代理
PROXY=
```

## API 端点

### OpenAI 兼容

```text
POST /v1/chat/completions
POST /v1/responses
POST /v1/images/generations
POST /v1/images/edits
GET  /v1/models
```

### Claude 兼容

```text
POST /v1/messages
POST /v1/messages/count_tokens
GET  /v1/models
```

### Gemini 兼容

```text
POST /v1beta/models/{model}:generateContent
POST /v1beta/models/{model}:streamGenerateContent?alt=sse
POST /v1beta/models/{model}:countTokens
GET  /v1beta/models
```

### 服务状态

```text
GET  /healthz
GET  /readyz
GET  /v1/accounts/health
GET  /v1/accounts/usage
```

认证支持 `Authorization: Bearer xxx`、`?key=xxx` 和 `x-goog-api-key`。

## 使用示例

```bash
curl http://127.0.0.1:8007/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "从 /v1/models 获取的模型 ID",
    "messages": [{"role": "user", "content": "Hello"}],
    "reasoning_effort": "medium",
    "stream": true
  }'
```

OpenAI SDK 的 base URL 使用 `http://127.0.0.1:8007/v1`；Claude 和 Google GenAI SDK 使用 `http://127.0.0.1:8007`。

## 目录结构

```text
cmd/gemini-web2api/  # 程序入口
internal/
  adapter/           # OpenAI、Claude、Gemini 协议适配
  auth/              # 账号状态加载与写回
  balancer/          # 多账户调度
  chromeauth/        # Chrome 账号配置
  config/            # 服务配置
  gemini/            # Gemini Web 协议客户端
```

## 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `GEMINI_SAVE_HISTORY` | 是否写入 Gemini 官网历史记录 | `false` |
| `GEMINI_AUTH_STATES` | 认证文件或目录，多个路径用逗号分隔 | `auth` |
| `LISTEN_ADDR` | 服务监听地址 | `127.0.0.1:8007` |
| `PROXY_API_KEY` | 本地 2API 访问密钥 | 空 |
| `PROXY` | 全局 HTTP、HTTPS 或 SOCKS5 代理 | 空 |
| `MODEL_MAPPING` | 外部模型名到当前模型 ID 的映射 | 空 |
| `ACCOUNT_COOLDOWN` | 账号失败后的冷却时间 | `2m` |
| `SESSION_TTL` | 显式会话状态的存活时间 | `30m` |
| `INIT_TIMEOUT` | 单账号启动初始化超时 | `20s` |

命令行 `--auth` 和 `--listen` 可以覆盖认证路径与监听地址。

## 注意

Gemini Web 内部协议可能随官网更新，模型 ID 以服务启动后返回的动态目录为准。欢迎提 Issue 和 PR。
