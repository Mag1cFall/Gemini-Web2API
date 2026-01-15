# Gemini-Web2API (Go Version)

将 Google Gemini Web 网页版转换为 OpenAI/Claude 兼容的 API 格式。

## 特性

- **OpenAI 兼容**: `/v1/chat/completions`, `/v1/models`, `/v1/images/generations`
- **Claude 兼容**: `/v1/messages`, `/v1/messages/count_tokens`
- **流式输出**: SSE (Server-Sent Events) 打字机效果
- **思考过程**: 支持提取模型思考过程 (`reasoning_content`)
- **图片生成**: 支持 Nano Banana / Nano Banana Pro 生图
- **图片上传**: 支持多模态图片输入
- **多账户负载均衡**: 支持配置多个 Google 账户
- **模型映射**: 支持将 Claude/OpenAI 模型名映射到 Gemini 模型

## 支持的模型

| 模型名 | 说明 |
|--------|------|
| `gemini-2.5-flash` | 快速模型 |
| `gemini-3-pro-preview` | Pro 预览版 |
| `gemini-3-flash-preview` | Flash 预览版 |
| `gemini-3-flash-preview-no-thinking` | Flash 无思考模式 |
| `gemini-2.5-flash-image` | Nano Banana 生图 |
| `gemini-3-pro-image-preview` | Nano Banana Pro 生图 |

## 快速开始

### 1. 运行
```bash
# 编译
go build -o Gemini-Web2API.exe ./cmd/server

# 运行
./Gemini-Web2API.exe
```

### 2. 配置 Cookie

**方式一：自动获取 (Firefox)**

程序会自动从 Firefox 读取 Google Cookies（默认账户）。

**方式二：Chrome 批量获取（推荐）**
```bash
# 1. 关闭 Chrome 浏览器
# 2. 运行命令
./Gemini-Web2API.exe --fetch-cookies

# 3. 选择配置文件（输入 1,2,3 或 ALL）
```
详见 [internal/browser/README.md](internal/browser/README.md)

**方式三：手动配置**
```bash
cp .env.example .env
# 编辑 .env 填入 Cookie
```

多账户配置（带后缀）：
```
__Secure-1PSID_Account1=xxx
__Secure-1PSIDTS_Account1=yyy
__Secure-1PSID_Account2=xxx
__Secure-1PSIDTS_Account2=yyy
```

### 3. 模型映射（可选）
将外部模型名映射到 Gemini 模型：
```
MODEL_MAPPING=claude-haiku-4-5-20251001:gemini-3-flash-preview-no-thinking
```

## API 端点

### OpenAI 兼容
```
POST /v1/chat/completions
POST /v1/images/generations
GET  /v1/models
```

### Claude 兼容
```
POST /v1/messages
POST /v1/messages/count_tokens
GET  /v1/models/claude
```

## 使用示例

### 聊天
```bash
curl http://127.0.0.1:8007/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3-flash-preview",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

### 图片生成
```bash
curl http://127.0.0.1:8007/v1/images/generations \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-image",
    "prompt": "a cat wearing a hat",
    "n": 1,
    "size": "1024x1024",
    "response_format": "b64_json"
  }'
```
或者直接在 `v1/chat/completions` 端点使用，回复将自动格式化为 `![Generated Image 1](data:image/png;base64,xxx)`

## 目录结构

```
cmd/server/         # 程序入口
internal/
  adapter/          # OpenAI/Claude 协议适配
  balancer/         # 多账户负载均衡
  browser/          # Cookie 获取
  claude/           # Claude 协议类型
  config/           # 配置（模型映射）
  gemini/           # Gemini Web API 客户端
```

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `PORT` | 服务端口 | 8007 |
| `PROXY_API_KEY` | API 密钥 | (空=无认证) |
| `MODEL_MAPPING` | 模型映射 | (空) |
| `SNAPSHOT_STREAMING` | 启用快照流式（实验性） | 0 |

## 注意

不适用于生产安全级。欢迎提Issue提PR。