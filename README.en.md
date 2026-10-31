# Gemini-Web2API (Go Version)

[简体中文](README.md) | [English](README.en.md)

Convert Google Gemini Web into OpenAI, Claude, and Gemini compatible APIs.

## Features

- **OpenAI compatible**: `/v1/chat/completions`, `/v1/completions`, `/v1/responses`, `/v1/files`, `/v1/audio/*`, `/v1/videos`, `/v1/models`
- **Claude compatible**: `/v1/messages`, Files API
- **Gemini compatible**: `/v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:predictLongRunning`, Files API
- **Streaming**: streams text and reasoning deltas; each protocol's thinking controls map to the official Low, Medium, and High levels, and a model name can also carry a suffix such as `(high)`, `(low)`, or `(-1)` to set the thinking level
- **Structured output**: OpenAI `response_format`, Anthropic `output_format`, and Gemini `responseSchema` and `responseFormat` (including `text/x.enum` enums) constrain the answer with a JSON Schema
- **Function tools**: function tools in all four protocols are bridged through the web prompt; text the model writes before a call is returned with the call, and an unparseable call is retried on another account before any output is visible
- **Images**: supports multimodal input, image generation, and image editing; generated images are returned at full size through the official "Download full size image" request
- **Official tools**: Create video, Create music, Canvas, Deep Research, Guided Learning, and Task mode are available as separate model IDs
- **Multi-account balancing**: schedules Google accounts by model availability; usage and rate limits cool down only the affected model, with configurable concurrency and routing strategy
- **Guest session**: when no account is available, serves the models the Gemini website offers without sign-in (currently `gemini-3.5-flash-lite`, without website tools or attachment uploads) through a signed-out session; ready accounts are always preferred
- **Console and desktop app**: the web console shows live logs, active requests, usage and cost statistics, account import and management, the model catalog, settings, and a playground; desktop apps for Windows, macOS, and Linux add a tray icon, single instance, and launch at login
- **Run logs**: colored columnar console logs that record each request's start, account selection, uploads, first output, account switches, waits, and completion
- **Request ledger**: SQLite stores per-request usage, with statistics by model, account, and protocol and cost estimates at Gemini API prices
- **Gems**: each account's own Gems and Google's pre-made Gems are available as `gem-<Gem ID>` model IDs
- **Proxy pool**: multiple HTTP, HTTPS, SOCKS4 and SOCKS5 proxies form a pool; each account stays bound to one of them, proxies are checked periodically, and accounts move automatically when their proxy fails; fixed per-account proxies, direct connections and the system proxy are supported
- **Model aliases**: maps external model names to current Gemini models
- **Protocol-only runtime**: runs without a browser or frontend scripts after account setup

## Models

| Model ID | Name | Input window / capability | Default |
| --- | --- | ---: | --- |
| `gemini-3.8-flash` | 3.8 Flash | 32,768-1,048,576 | Yes |
| `gemini-3.1-pro` | 3.1 Pro | 32,768-1,048,576 |  |
| `gemini-3.5-flash-lite` | 3.5 Flash-Lite | 32,768-1,048,576 |  |
| `gemini-4-argon` | 4 Argon | 32,768-1,048,576 |  |
| `gemini-auto` | Auto | Free accounts |  |
| `gemini-3.8-flash-task`, `gemini-4-argon-task` | Task | Tasks performed on your behalf |  |
| `gemini-3.1-flash-image` | Nano Banana 2 | Image generation and editing |  |
| `gemini-video` | Video | Video generation |  |
| `gemini-music` | Music | Music generation |  |
| `gemini-canvas` | Canvas | Documents and code |  |
| `gemini-deep-research` | Deep Research | Research reports |  |
| `gemini-guided-learning` | Guided Learning | Guided learning |  |

- **Model catalog**: at startup the service reads each account's live Gemini Web model catalog and official tools; `/v1/models` is the runtime source of truth. IDs are derived from official display names, so new models appear automatically
- **Catalog fields**: `available_account_count` is the number of usable accounts, `default` marks the default model, and `thinking_levels` lists the available thinking levels
- **Tool models**: tool IDs appear only for accounts that have the corresponding official tool and run on that account's official default model
- **Images**: the image ID works with conversational image requests and `/v1/images/*`; `size` and `imageConfig.aspectRatio` support 1:1, 9:16, 3:4, 4:3, and 16:9
- **Video**: for video IDs, `imageConfig.aspectRatio` supports landscape 16:9 and portrait 9:16
- **Chat history**: Task, Deep Research, and video requests use saved chats, which remain in the account history
- **Deep Research**: the research plan and progress are returned as reasoning and the report as text, with the report's citations returned as web sources in each protocol's citation fields; a run usually takes over ten minutes, so set the client request timeout longer than that
- **Quotas**: context windows, model access, and quotas depend on the account's plan and region; account usage is shown on the admin Accounts page
- **Token usage**: text input, visible output, and reasoning summaries are counted with a local Gemini tokenizer; the token counting endpoints estimate images, audio, video, and PDFs with the Gemini API counting rules

## Quick Start

### One-click Windows start

Download the `windows-amd64.zip` package from [Releases](https://github.com/Mag1cFall/Gemini-Web2API/releases), extract it, and double-click `gemini-web2api.exe`:

- The console opens in a desktop window that uses the system WebView2 runtime; the app stays in the tray after the window closes, and the tray menu copies the API address and toggles launch at login
- Double-clicking `gemini-web2api.exe` again brings back the running window
- Import accounts from local browsers (Chrome, Brave, Edge, Firefox), cookies, or remote pairing on the console's Import account page
- `.env`, `auth/`, and `data/` all live in the folder containing `gemini-web2api.exe`

To run commands from cmd or PowerShell, write them as `cmd /c start /wait gemini-web2api.exe <arguments>` so the prompt returns after the program exits:

```powershell
cmd /c start /wait gemini-web2api.exe --help
cmd /c start /wait gemini-web2api.exe setup --email "name@example.com"
```

### macOS and Linux desktop app

Download the desktop build for your system from [Releases](https://github.com/Mag1cFall/Gemini-Web2API/releases):

| System | Package | Data directory |
| --- | --- | --- |
| macOS | `desktop-…-darwin-universal.tar.gz` | `~/Library/Application Support/Gemini-Web2API` |
| Linux | `desktop-…-linux-amd64.tar.gz` or `desktop-…-linux-arm64.tar.gz` | `~/.config/Gemini-Web2API` |

- Every desktop build is portable: on macOS, extract it and open `Gemini-Web2API.app`; on Linux, extract it and run `gemini-web2api`, which needs the system GTK 4 and WebKitGTK 6.0
- The desktop app opens the console in a window and stays in the tray after the window closes; the tray menu copies the API address and toggles launch at login
- Import accounts from local browsers (Chrome, Brave, Edge, Firefox), cookies, or remote pairing on the console's Import account page
- `.env`, `auth/`, and `data/` live in the data directory listed above

### Manual start

Build the console frontend, then the executable:

```bash
pnpm -C web install
pnpm -C web build
go build -tags nomsgpack -o gemini-web2api.exe ./cmd/gemini-web2api
```

On Windows, import accounts already signed into a local browser:

```powershell
.\gemini-web2api.exe setup
```

`setup` scans local browser accounts (specify `--browser` with `chrome`, `brave`, `edge`, or `firefox`; automatically checks other browsers if Chrome is not installed). Existing browser windows and profiles remain untouched.

An account can also be selected directly. When a profile is signed in to several Google accounts, each account is listed and imported separately:

```powershell
.\gemini-web2api.exe setup --browser brave --email "name@example.com"
```

You can also paste the one-line value of a request's `Cookie` header. This state works while those cookies remain valid and cannot perform OAuth renewal:

```powershell
.\gemini-web2api.exe setup --cookie "SAPISID=...; __Secure-1PSID=..." --id account-name
```

Import a cookie JSON exported by a browser extension or a Netscape `cookies.txt` file with `--cookie-file`:

```powershell
.\gemini-web2api.exe setup --cookie-file cookies.txt --id account-name
```

When the cookies come from a browser signed in to several Google accounts, select the account with `--authuser`, the `N` in `gemini.google.com/u/N/app` (default 0). The console's cookie import has the same Account index field:

```powershell
.\gemini-web2api.exe setup --cookie-file cookies.txt --id account-name --authuser 1
```

When the service runs on a server without Chrome, generate a pairing token on the console's Import account page, then upload accounts from a computer signed in to Chrome. The token is read from standard input. Remote addresses other than the local machine require HTTPS; through an SSH tunnel such as `ssh -L 8007:127.0.0.1:8007 <server>`, use `http://127.0.0.1:8007`:

```powershell
.\gemini-web2api.exe setup --email "name@example.com" --remote https://example.com
```

Setup creates `auth/<account>/storage-state.json` and verifies the live model catalog. Chrome import also saves the OAuth renewal material needed to renew cookies after Chrome exits. Daily startup is:

```powershell
.\gemini-web2api.exe
```

The default auth directory is `auth`, and the default listener is `127.0.0.1:8007`. Linux and macOS can use existing cookies copied in an `auth` directory; renewal requires setup on the original Windows device.

While running, the service rotates each account's sign-in cookies every 10 minutes, as the Gemini website does. Cookies that change in a response are written back to `auth/<account>/storage-state.json` and reused after a restart. Chrome-imported accounts renew automatically when their session expires.

### Optional configuration

The executable automatically reads `.env` from the current directory. Setup and the daily service share the same `PROXY`. Write `.env` from the repository's [`.env.example`](.env.example) only when configuration needs to change.

```dotenv
# Use Gemini Temporary Chat without writing website history
GEMINI_SAVE_HISTORY=false

# Local 2API access keys, comma-separated
PROXY_API_KEY=

# Proxy pool, comma-separated; HTTP, HTTPS, SOCKS4 and SOCKS5
PROXY=
```

### Docker

```bash
docker compose up -d
```

- `docker-compose.yml` uses `ghcr.io/mag1cfall/gemini-web2api:latest` and mounts the data directory at `./data`, which holds `.env`, `auth/`, and the request ledger
- Set `PROXY_API_KEY` when the port is reachable from other hosts; opening the console from outside the container requires `ADMIN_AUTH_ENABLED=true` and `ADMIN_PASSWORD`
- Add accounts with the console's cookie import or remote pairing, or copy `auth/<account>/` from another computer to `data/auth/`
- Run `docker compose restart` after editing `data/.env`

### Proxy

`PROXY` is the proxy pool, comma-separated, for example `PROXY=socks5://127.0.0.1:1080,http://user:pass@10.0.0.2:7890`. Supported schemes are `http`, `https`, `socks4`, `socks4a`, `socks5` and `socks5h`. In the console, edit it one proxy per line under Settings → Service → Egress proxy; changes apply on save.

- **Assignment**: accounts without a fixed proxy and the guest session each stay bound to one proxy; a new account goes to the proxy with the fewest accounts, the earlier one in the list on a tie. When the proxy list changes, accounts are rebalanced with the fewest moves
- **Checks**: at startup, every 5 minutes, when an account fails to connect, and when you click Check now in the console, the service requests the Gemini home page through each proxy
- **Failover**: accounts on an unavailable proxy move to an available proxy and re-initialize; when every proxy is unavailable, accounts keep their proxy
- **Status**: the proxy pool card on the settings page shows each proxy's status, latency and the accounts using it
- **System proxy**: when `PROXY` is empty, the `HTTPS_PROXY` environment variable is used; without it, Windows uses the system proxy setting (per-protocol settings are taken in the order https, http, socks, and socks is SOCKS4); otherwise connections are direct

An account can also have a fixed proxy, which takes priority over the pool; `direct` makes that account connect without a proxy. Set it with Edit proxy on the account page, or when importing:

```powershell
.\gemini-web2api.exe setup --email "name@example.com" --proxy "socks5://127.0.0.1:1080"
```

The proxy given to `setup --proxy` or in the console import form is written to `geminiWeb2api.proxy` in the account auth file `auth/<account>/storage-state.json`. Without one, import requests go through a proxy from `PROXY` and the account gets a proxy from the pool at runtime. The proxy exit must be in a region where Gemini is available.

## API Endpoints

### OpenAI compatible

```text
POST   /v1/chat/completions
POST   /v1/completions
POST   /v1/responses
GET    /v1/responses/{id}
DELETE /v1/responses/{id}
GET    /v1/responses/{id}/input_items
POST   /v1/responses/{id}/cancel
POST   /v1/responses/input_tokens
POST   /v1/responses/compact
POST   /v1/images/generations
POST   /v1/images/edits
POST   /v1/audio/speech
POST   /v1/audio/transcriptions
POST   /v1/audio/translations
POST   /v1/videos
GET    /v1/videos
GET    /v1/videos/{id}
GET    /v1/videos/{id}/content
DELETE /v1/videos/{id}
POST   /v1/files
GET    /v1/files
GET    /v1/files/{id}
GET    /v1/files/{id}/content
DELETE /v1/files/{id}
GET    /v1/models
```

- **Responses**: stored in `data/requests.db` by default and can be retrieved, have their input items listed, and be deleted; `store: false` skips storage. `background: true` returns `queued` immediately for later polling or cancellation
- **Continuation**: `previous_response_id` and conversation IDs keep working across service restarts within `SESSION_TTL`; clients that resend the full history continue the same web conversation and send only the new messages when the history matches the previous answer
- **Files**: uploaded files are stored in `data/files/` and referenced from generation requests by `file_id`
- **Speech**: `/v1/audio/speech` uses the website's read-aloud feature and returns mp3, opus, wav, flac, or pcm; `voice` and `speed` have no effect
- **Transcription and translation**: performed by the selected chat model
- **Video**: `/v1/videos` uses `gemini-video`
- **Capabilities Gemini Web lacks**: the Embeddings, Moderations, and Realtime endpoints return 404 `model_not_found`

### Claude compatible

```text
POST   /v1/messages
POST   /v1/messages/count_tokens
POST   /v1/files
GET    /v1/files
GET    /v1/files/{id}
DELETE /v1/files/{id}
GET    /v1/models
```

`/v1/files` requests with an `anthropic-version` or `anthropic-beta` header follow the Anthropic Files API; messages reference them with `source.file_id`.

### Gemini compatible

```text
POST   /v1beta/models/{model}:generateContent
POST   /v1beta/models/{model}:streamGenerateContent?alt=sse
POST   /v1beta/models/{model}:countTokens
POST   /v1beta/models/{model}:predictLongRunning
GET    /v1beta/operations/{id}
POST   /upload/v1beta/files
GET    /v1beta/files
GET    /v1beta/files/{name}
DELETE /v1beta/files/{name}
GET    /v1beta/models
```

`streamGenerateContent` without `alt=sse` returns a JSON array. Uploaded files are referenced with `fileData.fileUri`, and YouTube links are read as video content the same way as on the website. `predictLongRunning` uses `gemini-video`.

### Service status

```text
GET  /readyz
```

Authentication accepts `Authorization: Bearer xxx`, `?key=xxx`, `x-goog-api-key`, and `x-api-key`. Without `PROXY_API_KEY`, only web pages opened from a local address can call the API from a browser; pages on other sites need a key configured on the service and sent with each request.

### Console

Open `http://127.0.0.1:8007/` in a browser for the console, which uses the `/api/*` endpoints.

- **Remote access**: only local connections are accepted by default; with `ADMIN_AUTH_ENABLED=true`, `ADMIN_PASSWORD`, and `LISTEN_ADDR` on an externally reachable address, other devices can sign in; after 5 consecutive failed sign-ins, sign-in returns 429 with `Retry-After` for 60 seconds
- **Reverse proxy**: the proxy must pass the browser's original `Host` header to the service; in nginx, use `proxy_set_header Host $host;`
- **Applying settings**: settings saved in the console are written back to `.env`; the listen address, auth paths, and log format take effect after a restart, and everything else applies immediately
- **Online update**: the About page shows the latest GitHub release and its notes; after confirmation the command-line and desktop builds download the package for the current platform, verify its SHA-256, replace the executable, and restart into the new version; downloads go through the first proxy in `PROXY`, or the system proxy when it is unset; Docker deployments update with the image

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
  accounts/          # Account initialization, renewal, cookie rotation, and runtime changes
  adapter/           # OpenAI, Claude, and Gemini projections
  admin/             # Console API
  app/               # Configuration, instance lock, and service lifecycle
  auth/              # Account state loading and persistence
  balancer/          # Multi-account scheduling
  chromeauth/        # Chrome and cookie account import
  claude/            # Claude protocol types and stream events
  config/            # Service configuration
  desktop/           # Desktop window and tray
  gemini/            # Gemini Web protocol client
  logging/           # Run logs and request tracing
  pricing/           # Equivalent API prices
  requestdb/         # Request ledger
  streamio/          # HTTP stream flushing
  tokencount/        # Local Gemini tokenizer counting
  webui/             # Embedded console pages
web/                 # Console frontend source
build/               # Desktop icons and packaging configuration
```

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `GEMINI_SAVE_HISTORY` | Write conversations to Gemini Web history | `false` |
| `GEMINI_AUTH_STATES` | Auth file or directory, comma-separated | `auth` |
| `LISTEN_ADDR` | Service listener | `127.0.0.1:8007` |
| `PROXY_API_KEY` | Local 2API access keys, comma-separated | empty |
| `PROXY` | Proxy pool, comma-separated | empty |
| `MODEL_MAPPING` | External model name to current model ID mapping | empty |
| `ACCOUNT_COOLDOWN` | Cooldown after an upstream account failure | `2m` |
| `SESSION_TTL` | Lifetime of explicit session state | `30m` |
| `INIT_TIMEOUT` | Per-account startup timeout | `20s` |
| `PER_ACCOUNT_CONCURRENCY` | Concurrent upstream requests per account | `1` |
| `ROUTING_STRATEGY` | Routing strategy for requests without a session | `round-robin` |
| `LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `LOG_FORMAT` | Console log format: `text` or `json` | `text` |
| `REQUEST_BODY_LOG` | Keep the latest 1000 request and response bodies in the ledger | `false` |
| `LOG_FILE` | Also write logs to `data/logs/gemini-web2api.log`, rotating every 10 MB | `true` |
| `LOG_MAX_TOTAL_MB` | Size limit of the log directory in MB; the oldest files are deleted beyond it, at least 20 | `500` |
| `ADMIN_AUTH_ENABLED` | Console sign-in | `false` |
| `ADMIN_USERNAME` | Console username | `admin` |
| `ADMIN_PASSWORD` | Console password | empty |

`MODEL_MAPPING` takes comma-separated `alias:model-id` pairs. Accounts that fail startup initialization retry at the `ACCOUNT_COOLDOWN` interval. With `ROUTING_STRATEGY=round-robin`, requests rotate across accounts; with `fill-first`, the first idle account is preferred.

The `--auth` and `--listen` flags override auth paths and the listener address; `--open-ui` controls whether the console opens in the browser after startup, and defaults to on when the program starts without arguments.

## Troubleshooting

### Windows Port Reserved by System

If startup reports that the port configured by `LISTEN_ADDR` is unavailable while Task Manager shows no owning process, Hyper-V, WSL2, or Docker NAT may have reserved the port range.

Run the following commands from an elevated PowerShell or CMD window.

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

If `8007` falls inside a reserved range, change `LISTEN_ADDR`, or restart WinNAT and inspect the range again:

```powershell
net stop winnat
net start winnat
```

When the port is free, it can be reserved persistently:

```powershell
netsh int ipv4 add excludedportrange protocol=tcp startport=8007 numberofports=1 store=persistent
```

## Notes

Gemini Web is an internal protocol that can change with the website. Treat the live model catalog returned after startup as the source of truth. Issues and PRs are welcome. Development guidance is in [docs/development.md](docs/development.md); authentication, payload, and streaming details are in [docs/protocol.md](docs/protocol.md); logs and the request ledger are described in [docs/logging.md](docs/logging.md).
