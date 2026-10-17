# 开发与贡献

Gemini-Web2API 以纯 Go 进程连接 Gemini Web，再将一条规范事件流投影为 OpenAI、Claude 和 Gemini API。协议结论来自实时网页请求与纯 HTTP 重放。

## 本地开发

源码构建需要 Go 1.25.0 或更高版本，以及构建管理端前端的 Node.js 与 pnpm。发布版可执行文件可直接运行。Windows 首次运行可以直接执行根目录的 `start.bat`，开发时使用以下命令：

```powershell
pnpm -C web install
pnpm -C web build
go run ./cmd/gemini-web2api setup
go run ./cmd/gemini-web2api
```

`start.bat` 优先启动现有可执行文件，文件不存在时先构建前端再编译。修改源码后使用 `go build -o gemini-web2api.exe ./cmd/gemini-web2api` 重建；不带参数启动会打开浏览器，开发调试时加 `--open-ui=false`。

程序从当前目录读取 `.env`。`setup` 与日常服务共同使用 `PROXY`；`setup --proxy` 为本次导入的账号指定代理，并写入认证文件作为该账号运行时的固定出口。认证状态保存在 `auth/`，不得提交 Cookie、refresh token、wrapped binding key、请求日志或账号信息。

无需读取本机 Chrome 时，可以用 `setup --cookie "<Cookie value>" --id <name>` 导入请求头中 `Cookie` 字段的一行值。这种状态只使用现有 Cookie，失效后重新导入。

项目结构按职责划分：

```text
cmd/gemini-web2api/  命令入口
internal/app/        配置加载、实例锁、HTTP 路由与服务生命周期
internal/accounts/   账号初始化、续签、Cookie 轮换与运行期增删
internal/admin/      管理端 /api 接口、登录与 .env 写回
internal/chromeauth/ Chrome 账号发现、OAuthMultilogin、Cookie 导入与远程配对
internal/auth/       账号状态和原子写回
internal/gemini/     Gemini Web 请求、解码与规范事件
internal/balancer/   账号能力调度、冷却与会话粘连
internal/proxypool/  代理池绑定、检测与系统代理
internal/adapter/    OpenAI、Claude 和 Gemini 协议投影
internal/claude/     Claude 协议类型与流式事件
internal/tokencount/ Gemini tokenizer 本地计数
internal/logging/    运行日志与请求阶段跟踪
internal/requestdb/  SQLite 请求账本、用量汇总与公开接口存储
internal/pricing/    Gemini API 等价单价
internal/streamio/   HTTP 流刷新与网络错误传播
internal/desktop/    桌面窗口与托盘（build tag desktop）
internal/webui/      管理端前端构建产物的内嵌与静态服务
web/                 管理端前端源码（React、TypeScript、Vite、Tailwind）
build/               桌面版图标、Windows 清单与版本信息、macOS 与 Linux 打包配置
```

## 管理端前端

管理端网页在 `web/` 用 pnpm 开发，构建产物输出到 `internal/webui/dist`，由 `go:embed` 内嵌进可执行文件；`/api`、`/v1`、`/v1beta`、`/upload` 之外的 GET 请求回落到 `index.html`。开发用以下命令：

```powershell
cd web
pnpm install
pnpm dev          # 开发服务器，/api、/v1、/v1beta 代理到 127.0.0.1:8007
pnpm test         # vitest 单元测试
pnpm lint         # eslint
pnpm build        # 构建到 ../internal/webui/dist
```

改动前端后运行 `pnpm build` 再重建 Go 可执行文件，内嵌资源才会更新。桌面壳用 `?shell=desktop` 显示自绘标题栏的窗口按钮。

前端源码的组织方式：

| 目录 | 内容 |
| --- | --- |
| `src/app/` | 路由、布局、顶栏、实时事件订阅 |
| `src/components/ui/` | 按钮、表格、对话框等基础组件 |
| `src/features/<功能>/` | 各页面组件与该功能的查询、类型、文案 |
| `src/lib/` | 接口请求、多语言、格式化、桌面壳工具 |
| `src/index.css` | `@theme` 设计 token 与全局样式 |

- **接口调用**：页面只调用管理端 `/api/*`，统一经 `src/lib/api.ts` 的 `api()` 发出，数据查询与缓存用 TanStack Query，写在各功能的 `queries.ts`
- **接口类型**：各功能的 `types.ts` 按 `internal/admin` 与 `internal/requestdb` 返回的 JSON 手写，后端字段变化时同步修改
- **实时数据**：`src/app/events.ts` 用 `EventSource` 订阅 `/api/events`，接收服务状态、账号、模型、冷却、进行中与最近结束的请求以及日志，再更新查询缓存
- **多语言**：每个功能的 `messages.ts` 用 `src/lib/i18n.tsx` 的 `defineMessages` 声明 zh-CN、zh-TW、en、ja、ko、fr、de 七种语言，键以简体中文为准，类型检查要求七种语言的键一致；页面用 `useMessages` 取当前语言的文案
- **样式**：颜色、间距、字号、圆角、阴影与动效只在 `src/index.css` 的 `@theme` 中定义，组件只使用这些 token
- **提交前检查**：`pnpm lint`、`pnpm format:check`、`pnpm typecheck`、`pnpm test`、`pnpm build`，与 CI 的 `web` 任务相同

## 桌面版

桌面版与命令行版是同一程序，以 build tag `desktop` 编译 Wails v3 窗口与托盘，数据目录在 Windows 上为可执行文件所在目录，其他系统为用户配置目录下的 `Gemini-Web2API`。Windows 本地构建：

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.29
wails3 generate syso -arch amd64 -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out cmd/gemini-web2api/wails_windows_amd64.syso
go build -tags desktop,production -ldflags="-H windowsgui" -o bin/gemini-web2api.exe ./cmd/gemini-web2api
```

生成的 `wails_windows_amd64.syso` 同样作用于命令行版：保留该文件执行 `go build -o gemini-web2api.exe ./cmd/gemini-web2api`，Windows 可执行文件即带应用图标与版本信息，Release 的 Windows 命令行包按此构建。

Windows 便携包为桌面版可执行文件与 README、LICENSE、`.env.example` 的 zip。macOS 与 Linux 需要 CGO，Linux 另需 `libgtk-4-dev` 与 `libwebkitgtk-6.0-dev`；完整打包步骤见 `.github/workflows/build.yml`。`--hidden` 启动时只显示托盘图标，开机启动使用该参数。

## 请求链路

一条生成请求依次经过以下各层：

| 步骤 | 位置 | 作用 |
| --- | --- | --- |
| 路由 | `internal/app/router.go` | 注册公开端点与管理端 `/api` |
| 中间件 | `internal/adapter/middleware.go` | 跨域、请求 ID、跟踪、账本、鉴权 |
| 协议解析 | `internal/adapter` 各处理文件 | 请求转为统一生成参数 |
| 提示词 | `internal/adapter/prompt.go`、`tool_bridge.go` | 消息序列与工具契约 |
| 会话 | `internal/adapter/conversation.go` | 会话键与网页会话三元组 |
| 选号与重试 | `internal/adapter/stream.go` | 按模型选账号，可重试错误换号 |
| 调度 | `internal/balancer/balancer.go` | 账号状态、冷却、会话粘连 |
| 上游请求 | `internal/gemini/client.go`、`request.go`、`payload.go` | 请求槽、header、99 槽载荷 |
| 解码 | `internal/gemini/decoder.go`、`event.go` | 生成流帧转为规范事件 |
| 汇总 | `internal/adapter/events.go` | 规范事件累积为候选输出 |
| 输出 | `internal/adapter` 各处理文件、`keepalive.go` | 流式事件、完整响应、保活 |

- **入口函数**：OpenAI Chat 为 `ChatCompletionHandler`（`openai_chat.go`），Completions 为 `CompletionHandler`（`completions.go`），Responses 为 `ResponsesHandler`（`responses_handler.go`），Anthropic 为 `ClaudeMessagesHandler`（`claude_handler.go`），Gemini 原生入口由 `GeminiRouterHandler`（`gemini_handler.go`）按 `:action` 分发
- **中间件顺序**：`router.go` 的公开接口组依次挂 `CORSMiddleware`、`RequestMiddleware`、`AuthMiddleware`，未匹配的路径按前缀返回各协议的 404 或回落到管理端页面
- **生成执行**：处理函数调用 `runGeneration`，它经 `runGenerationAttempts` 用 `AccountPool.NextForModelExcluding` 取账号并调用 `gemini.Client.Stream`；成功时 `ReportSuccess`，可重试错误时 `ReportFailure` 后换下一个账号；换号只发生在首个可见输出之前
- **请求槽**：`gemini.Client` 用 `requestSlot` 信号量实现 `PER_ACCOUNT_CONCURRENCY`，生成、用量读取与 Cookie 轮换共用
- **事件流**：`Client.Stream` 把 `decoder.go` 解出的 `gemini.Event` 交给回调，适配层用 `EventAccumulator.Apply` 累积；流式请求在回调中实时写出，非流式请求在结束后组装响应
- **非生成端点**：Files、Responses 读取与删除、视频任务查询只读写 `internal/requestdb`，不经过调度

## 账号生命周期

| 阶段 | 位置 | 行为 |
| --- | --- | --- |
| 加载 | `internal/auth` | 读取 `auth/<账号>/storage-state.json` |
| 初始化 | `accounts.Manager.Start` | 执行初始化链路，失败按间隔重试 |
| 轮换 | `internal/accounts`、`gemini/rotate.go` | 按官网周期轮换 Cookie 并重读目录 |
| 续签 | `internal/chromeauth` | Chrome 导入的账号在登录态失效时续签 |
| 冷却 | `AccountPool.ReportFailure` | 按模型或整个账号移出调度 |
| 运行期变更 | `Manager.Add`、`Remove`、`SetEnabled`、`SetProxy`、`Verify` | 导入、删除、停用、改代理、重新连接 |
| 代理 | `internal/accounts/proxies.go`、`internal/proxypool` | 分配代理，定时检测，故障时改绑 |
| 写回 | `internal/auth` | 变化的 Cookie 原子写回认证文件 |

- **启动**：`internal/app` 读取配置后创建 `AccountPool` 与 `accounts.Manager`，`Start` 为每个账号启动独立的初始化与轮换循环，并另建访客会话
- **管理端操作**：`internal/admin` 的账号接口调用 `Manager` 的对应方法，Chrome 导入、Cookie 导入与远程配对分别经 `ImportChrome`、`ImportCookie`、`ImportState` 写入认证目录后加入调度
- **状态读取**：管理端与 `/v1/accounts/health` 读取 `AccountPool.Status`，账号用量读取 `AccountPool.Usage`
- **冷却原因**：以代码记录，取值为 `usage_limit`、`rate_limited`、`model_mismatch`、`ip_restricted`、`captcha` 与 `upstream_error`，管理端按代码显示当前语言的文案

## 日志与请求账本

- **请求跟踪**：`RequestMiddleware` 用 `logging.NewRequest` 为每个公开请求创建跟踪对象并放进请求 context；处理函数用 `logging.FromContext` 取出，调用 `Begin` 标记为生成请求，之后在各阶段调用 `Stage`、`AccountReady`、`AttemptFailed`、`SetUsage`、`SetResult`、`Fail`
- **控制台与管理端**：`internal/logging` 把阶段与结果写到控制台，并经 `logging.Subscribe` 推送给管理端 `/api/events`；日志格式与各条消息见 [logging.md](logging.md)
- **账本写入**：请求结束时 `RequestMiddleware` 调用 `Finish` 取得结果，调用过 `Begin` 的请求由 `requestdb.Store.Record` 写入 `data/requests.db`；`REQUEST_BODY_LOG=true` 时同时写入截断的请求与响应正文
- **其他数据**：同一数据库保存 Responses、文件、视频任务与会话登记，分别在 `internal/requestdb` 的 `responses.go`、`files.go`、`videos.go`、`sessions.go`
- **管理端查询**：`/api/usage`、`/api/usage/records`、`/api/usage/records.csv` 与 `/api/requests/:id/body` 由 `internal/requestdb/http.go` 提供，费用按 `internal/pricing` 的单价计算

## 常见修改

增加公开端点或协议字段：

1. 在 `internal/app/router.go` 的公开接口组注册路由，使其经过鉴权与账本中间件
2. 在 `internal/adapter` 编写处理函数：解析请求，生成时调用 `runGeneration`，错误用 `errors.go` 中该协议的写出函数
3. 需要进入上游的字段加到 `internal/gemini/request.go` 的 `GenerateRequest`，并在 `payload.go` 写入对应载荷槽
4. 新的上游输出先在 `internal/gemini/event.go` 定义事件、由 `decoder.go` 产生，再在各协议的处理文件中输出
5. 同步更新 README 的端点列表与 [protocol.md](protocol.md)

增加官网工具：

1. 在 `internal/gemini/request.go` 定义该工具的 `Feature` 编号
2. 在 `internal/gemini/models.go` 的 `featureTools` 加一行：公开 ID、显示名称、说明、功能编号与账号能力表中需要的能力号
3. 在 `internal/gemini/payload.go` 写入该功能的载荷槽；成品不在生成流内的工具按 `research.go` 的方式轮询会话
4. 适配层按该工具的成品类型输出文本、图片、音频或视频

普通模型、思考强度与 Gem 都从账号的实时目录生成（`parseModelCatalog`、`parseThinkingLevels`、`gems.go`），不需要改代码。

增加配置项：

1. 在 `internal/config/config.go` 读取环境变量并给出默认值
2. 在 `internal/admin/settings.go` 加入设置视图、校验与写回；`.env` 由 `internal/config/envfile.go` 写入，需要重启生效的项加入 `restart_fields`
3. 在 `web/src/features/settings/` 加表单项与七种语言文案
4. 同步更新 `.env.example` 与 README 的环境变量表

## 修改协议

协议修改从一个最短真实场景开始：保存网页端成功请求，重放相同请求，逐项确认动态字段来源，再修改对应编码器或解码器。原始抓包只放在本地实验目录，提交内容只包含脱敏后的结构结论。

抓包用浏览器开发者工具：在已登录 Gemini 网页的 Chrome 中打开 Network 面板并勾选 Preserve log，完成一次最短操作，右键目标请求选择 Copy as cURL (bash)，得到含 Cookie、header 与 `f.req` 载荷的完整请求。在同一出口 IP 下执行这条 curl 即为纯 HTTP 重放；每次只改一个字段再执行，按响应变化确认该字段的作用。

每个结论至少包含请求入口、必需 header、载荷位置、响应事件顺序、Cookie 变化、错误状态和实际模型，并附上页面结果、官方网络记录与纯 HTTP 重放结果。

遇到前端更新时，先重新采集首页、模型目录和一条最短文本流，然后只修改已经变化的字段。模型名称、路由 hash、build、动态 token 和账号能力始终从当前账号运行时获取。

## 验收修改

账户和显式会话的排队等待使用请求 context；服务关闭会取消活动请求。流式写入错误直接终止输出，Responses 的结构化正文和终态复用已验证的响应对象。

代码格式和静态检查使用：

```powershell
gofmt -w <修改的 Go 文件>
go vet ./...
go build ./cmd/gemini-web2api
```

GitHub Actions 对 `main` 提交和 Pull Request 使用 `go.mod` 最低版本执行检查，再用当前稳定版 Go 构建 Windows amd64、Linux amd64/arm64、macOS amd64/arm64 发布包，以及 Windows 便携包、macOS 通用 dmg、Linux AppImage 与 deb 桌面版。推送 `v*` 版本标签后自动创建 Release，附件包含二进制、Windows 启动文件、示例配置和文档；含 `-` 的标签发布为预发布版本。普通构建产物保留七天，Release 附件长期保留。

提交前在本地运行与 CI 相同的检查：

```powershell
go test ./...
go vet ./...
pnpm -C web lint
pnpm -C web format:check
pnpm -C web typecheck
pnpm -C web test
```

CI 由 `.github/workflows/build.yml` 定义：

| 任务 | 触发 | 作用 |
| --- | --- | --- |
| `web` | 全部 | 解析版本号，前端检查与构建 |
| `headless` | 全部 | Go 测试、静态检查、五平台命令行包 |
| `desktop-windows` | 全部 | Windows 便携包 |
| `desktop-macos` | 全部 | macOS 通用 dmg |
| `desktop-linux` | 全部 | Linux AppImage 与 deb |
| `release` | `v*` 标签 | 创建 Release 并上传全部包 |
| `docker` | `v*` 标签 | 推送 `ghcr.io` 多架构镜像 |

- **构建产物**：`web` 任务上传的前端产物由其余任务下载到 `internal/webui/dist` 后编译
- **版本号**：标签构建取标签名，其余构建取提交号前 7 位，经 `-ldflags -X .../internal/app.version` 写入；本地构建未写入时显示 Go 模块版本与提交

功能验收直接运行服务，并按修改范围调用真实入口。协议修改至少验证非流式、流式、取消和一次后续请求；认证修改至少验证首次导入、服务重启、Cookie 轮换和固定代理出口；适配器修改使用对应官方 SDK 或主流 Coding Agent 完成一轮真实请求。

## 提交贡献

Issue 请提供入口协议、模型 ID、HTTP 状态、最短请求和已脱敏的响应形状。涉及账号或地区的问题，请同时说明代理协议、setup 与服务是否使用同一出口。不要上传 Cookie、token、邮箱、完整抓包或绝对路径。

Pull Request 应只处理一个完整能力组，并附上可重复的真实验收命令和结果。工具调用、图片、思考、引用等上游能力先进入 `internal/gemini` 的规范事件，再由适配器分别输出。

二次开发可以直接复用 `internal/gemini` 的 bootstrap、模型目录、请求编码和帧解码，也可以只使用某一个协议适配器。协议原理见 [protocol.md](protocol.md)。
