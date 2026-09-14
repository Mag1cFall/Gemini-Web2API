# Gemini-Web2API (Go Version)

[简体中文](README.md) | [English](README.en.md)

Convert Google Gemini Web into OpenAI, Claude, and Gemini compatible APIs.

## Features

- **OpenAI compatible**: `/v1/chat/completions`, `/v1/responses`, `/v1/models`
- **Claude compatible**: `/v1/messages`
- **Gemini compatible**: `/v1beta/models/{model}:generateContent`, `:streamGenerateContent`
- **Streaming**: streams text and reasoning deltas with protocol-specific thinking controls
- **Images**: supports multimodal input, image generation, and image editing
- **Multi-account balancing**: schedules Google accounts by model availability
- **HTTP proxy**: supports a global proxy or a fixed per-account proxy
- **Model aliases**: maps external model names to current Gemini models
- **Protocol-only runtime**: runs without a browser or frontend scripts after account setup

## Models

| Model ID | Name | Input window / capability | Default |
| --- | --- | ---: | --- |
| `gemini-3.8-flash` | 3.8 Flash | 32,768-1,048,576 | Yes |
| `gemini-3.1-flash-image` | Nano Banana 2 | Image generation and editing |  |
| `gemini-3.1-pro` | 3.1 Pro | 32,768-1,048,576 |  |
| `gemini-3.5-flash-lite` | 3.5 Flash-Lite | 32,768-1,048,576 |  |
| `gemini-3.6-flash` | 3.6 Flash | 32,768 |  |

At startup, the service merges the live Gemini Web chat model catalog with the `gemini-3.1-flash-image` image capability ID; use `/v1/models` as the runtime source of truth. IDs are derived from official display names, so accounts receiving 3.8 Flash automatically expose `gemini-3.8-flash`. The `available_account_count` and `default` fields describe current account coverage and the default selection. The image ID works with conversational image requests and `/v1/images/*`. Context windows, model access, and quotas depend on the account's plan and region. A local Gemini tokenizer reports tokens for text input, visible output, and visible reasoning summaries. Account usage is available from `/v1/accounts/usage`.

## Quick Start

### One-click Windows start

Download the `windows-amd64.zip` package from [Releases](https://github.com/Mag1cFall/Gemini-Web2API/releases), extract it, and double-click `start.bat`, or invoke it from cmd, PowerShell, or Git Bash. The script starts the executable in the same directory, building from source with Go 1.25.0+ when the executable is absent. An invocation without arguments starts interactive setup when no account state exists; an invocation with arguments passes them directly to the executable.

```powershell
.\start.bat
.\start.bat --help
.\start.bat setup --email "name@example.com"
```

### Manual start

Build the executable:

```bash
go build -o gemini-web2api.exe ./cmd/gemini-web2api
```

On Windows, import accounts already signed into the local Chrome installation:

```powershell
.\gemini-web2api.exe setup
```

`setup` scans local Chrome accounts and briefly starts an isolated temporary Chrome process to read the App-Bound master key. Existing Chrome windows and profiles remain untouched.

An account can also be selected directly:

```powershell
.\gemini-web2api.exe setup --email "name@example.com"
```

You can also paste the one-line value of a request's `Cookie` header. This state works while those cookies remain valid and cannot perform device-bound renewal:

```powershell
.\gemini-web2api.exe setup --cookie "SAPISID=...; __Secure-1PSID=..." --id account-name
```

Setup creates `auth/<account>/storage-state.json` and verifies the live model catalog. Chrome import also saves the device-bound material needed to renew cookies after Chrome exits. Daily startup is:

```powershell
.\gemini-web2api.exe
```

The default auth directory is `auth`, and the default listener is `127.0.0.1:8007`. Linux and macOS can use existing cookies copied in an `auth` directory; device-bound renewal requires setup on the original Windows device.

### Optional configuration

The executable automatically reads `.env` from the current directory. Setup and the daily service share the same `PROXY`. Copy `.env.example` to `.env` only when configuration needs to change.

```dotenv
# Use Gemini Temporary Chat without writing website history
GEMINI_SAVE_HISTORY=false

# Local 2API access key
PROXY_API_KEY=

# HTTP, HTTPS, or SOCKS5 proxy
PROXY=
```

## API Endpoints

### OpenAI compatible

```text
POST /v1/chat/completions
POST /v1/responses
POST /v1/images/generations
POST /v1/images/edits
GET  /v1/models
```

### Claude compatible

```text
POST /v1/messages
POST /v1/messages/count_tokens
GET  /v1/models
```

### Gemini compatible

```text
POST /v1beta/models/{model}:generateContent
POST /v1beta/models/{model}:streamGenerateContent?alt=sse
POST /v1beta/models/{model}:countTokens
GET  /v1beta/models
```

### Service status

```text
GET  /healthz
GET  /readyz
GET  /v1/accounts/health
GET  /v1/accounts/usage
```

Authentication accepts `Authorization: Bearer xxx`, `?key=xxx`, and `x-goog-api-key`.

## Example

```bash
curl http://127.0.0.1:8007/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "a model ID returned by /v1/models",
    "messages": [{"role": "user", "content": "Hello"}],
    "reasoning_effort": "medium",
    "stream": true
  }'
```

Use `http://127.0.0.1:8007/v1` as the OpenAI SDK base URL. Claude and Google GenAI SDKs use `http://127.0.0.1:8007`.

## Layout

```text
cmd/gemini-web2api/  # Program entry point
internal/
  adapter/           # OpenAI, Claude, and Gemini projections
  auth/              # Account state loading and persistence
  balancer/          # Multi-account scheduling
  chromeauth/        # Chrome account setup
  config/            # Service configuration
  gemini/            # Gemini Web protocol client
```

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `GEMINI_SAVE_HISTORY` | Write conversations to Gemini Web history | `false` |
| `GEMINI_AUTH_STATES` | Auth file or directory, comma-separated | `auth` |
| `LISTEN_ADDR` | Service listener | `127.0.0.1:8007` |
| `PROXY_API_KEY` | Local 2API access key | empty |
| `PROXY` | Global HTTP, HTTPS, or SOCKS5 proxy | empty |
| `MODEL_MAPPING` | External model name to current model ID mapping | empty |
| `ACCOUNT_COOLDOWN` | Cooldown after an upstream account failure | `2m` |
| `SESSION_TTL` | Lifetime of explicit session state | `30m` |
| `INIT_TIMEOUT` | Per-account startup timeout | `20s` |

The `--auth` and `--listen` flags override auth paths and the listener address.

## Notes

Gemini Web is an internal protocol that can change with the website. Treat the live model catalog returned after startup as the source of truth. Development guidance is in [docs/development.md](docs/development.md); authentication, payload, and streaming details are in [docs/protocol.md](docs/protocol.md).
