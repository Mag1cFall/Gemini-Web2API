# Gemini-Web2API (Go Version)

将 Google Gemini Web 网页版转换为 OpenAI 兼容的 API 格式。

## 特性

- **Go 语言重构**: 高效、轻量，单文件部署。
- **OpenAI 兼容**: 支持标准的 `/v1/chat/completions` 和 `/v1/models` 接口。
- **流式输出**: 支持 SSE (Server-Sent Events) 打字机效果。
- **自动/手动 Cookie**: 支持从 Firefox 自动获取 Cookie，或通过 `.env` 手动配置。
- **多模态支持**: 支持图片上传和识别。

## 快速开始

1. **运行**: 双击 `run.bat` 或在终端运行 `Gemini-Web2API.exe`。
   - 首次运行会自动编译。
2. **配置**: 
   - 程序会尝试自动读取 Firefox 的 Google Cookies。
   - 如果失败，请复制 `.env.example` 为 `.env` 并手动填入 `__Secure-1PSID` 和 `__Secure-1PSIDTS`。
3. **调用**:
   - 服务默认运行在 `http://127.0.0.1:8007`。
   - API Key 在 `.env` 中配置 `PROXY_API_KEY`。

## 目录说明

- `cmd/server`: 程序入口。
- `internal/`: 核心逻辑实现。

## 注意

这是一个半成品项目，请不要将其部署到生产环境。仅供学习用途。