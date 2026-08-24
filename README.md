# Gemini-Web2API (Go Version)

[简体中文](README.md) | [English](README.en.md)

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

| 模型 ID | 名称 | 输入窗口 / 能力 | 默认 |
| --- | --- | ---: | --- |
| `gemini-3.7-flash` | 3.7 Flash | 32,768–1,048,576 | 是 |
| `gemini-3.1-flash-image` | Nano Banana 2 | 图片生成与编辑 |  |
| `gemini-3.1-pro` | 3.1 Pro | 32,768–1,048,576 |  |
| `gemini-3.5-flash-lite` | 3.5 Flash-Lite | 32,768–1,048,576 |  |
| `gemini-3.6-flash` | 3.6 Flash | 32,768 |  |

服务启动时将 Gemini Web 动态聊天模型目录与 `gemini-3.1-flash-image` 图片能力 ID 合并，运行后以 `/v1/models` 为准。图片 ID 可用于对话生图和 `/v1/images/*`。上下文窗口、模型权限和配额由 Gemini 网页账号的套餐与地区决定。服务按 Gemini tokenizer 返回文本输入、可见输出和思考摘要的 token usage；账号用量通过 `/v1/accounts/usage` 查看。

## 快速开始

### Windows 一键启动

双击根目录的 `start.bat`，或在 cmd、PowerShell、Git Bash 中运行它。有 Go 时脚本每次编译当前源码；没有 Go 时使用同目录的 Release 可执行文件。空参数启动会在缺少账号时进入交互式 setup，带参数调用则将参数直接传给程序。

```powershell
.\start.bat
.\start.bat --help
.\start.bat setup --email "name@example.com"
```

### 手动启动

先编译程序：

```bash
go build -o gemini-web2api.exe ./cmd/gemini-web2api
```

Windows 首次使用时，直接运行交互式配置：

```powershell
.\gemini-web2api.exe setup
```

`setup` 会扫描本机 Chrome 账号，并短暂启动独立临时 Chrome 进程读取 App-Bound 主密钥。现有 Chrome 窗口与 Profile 保持原状。

也可以直接指定 Chrome 中已登录的账号：

```powershell
.\gemini-web2api.exe setup --email "name@example.com"
```

也可以粘贴浏览器请求头中 `Cookie` 字段的一行值；这种状态在现有 Cookie 有效期内使用，不具备设备绑定续签能力：

```powershell
.\gemini-web2api.exe setup --cookie "SAPISID=...; __Secure-1PSID=..." --id account-name
```

`setup` 会生成 `auth/<账号>/storage-state.json` 并验证模型目录。Chrome 导入还会保存浏览器退出后续签所需的设备绑定材料。日常启动只需运行：

```powershell
.\gemini-web2api.exe
```

默认读取 `auth` 目录并监听 `127.0.0.1:8007`。Linux 和 macOS 可以在现有 Cookie 有效期内使用已经生成的 `auth` 目录；设备绑定续签仍需在原 Windows 设备执行。

### 可选配置

程序启动时自动读取当前目录的 `.env`，setup 与日常服务使用同一个 `PROXY`。默认配置可以直接使用；需要调整时复制 `.env.example` 为 `.env`。

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

开发与贡献见 [docs/development.md](docs/development.md)，认证、请求载荷和流式解码原理见 [docs/protocol.md](docs/protocol.md)。
