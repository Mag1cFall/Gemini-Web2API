# 开发与贡献

Gemini-Web2API 以纯 Go 进程连接 Gemini Web，再将一条规范事件流投影为 OpenAI、Claude 和 Gemini API。代码保持小型、分层和可审计；协议结论来自实时网页请求与纯 HTTP 重放。

## 本地开发

需要 Go 1.25.1 或更高版本。Windows 首次运行可以直接执行根目录的 `start.bat`，开发时使用以下命令：

```powershell
go run ./cmd/gemini-web2api setup
go run ./cmd/gemini-web2api
```

程序从当前目录读取 `.env`。`setup` 与日常服务共同使用 `PROXY`；`--proxy` 只覆盖本次账号导入。认证状态保存在 `auth/`，不得提交 Cookie、refresh token、wrapped binding key、请求日志或账号信息。

无需读取本机 Chrome 时，可以用 `setup --cookie "<Cookie value>" --id <name>` 导入请求头中 `Cookie` 字段的一行值。这种状态只使用现有 Cookie，失效后重新导入。

项目结构按职责划分：

```text
cmd/gemini-web2api/  命令入口和 HTTP 路由
internal/chromeauth/ Chrome 账号发现与 OAuthMultilogin
internal/auth/       账号状态和原子写回
internal/gemini/     Gemini Web 请求、解码与规范事件
internal/balancer/   账号能力调度与会话粘连
internal/adapter/    OpenAI、Claude 和 Gemini 协议投影
internal/streamio/   HTTP 流刷新与网络错误传播
```

## 修改协议

协议修改从一个最短真实场景开始：保存网页端成功请求，重放相同请求，逐项确认动态字段来源，再修改对应编码器或解码器。原始抓包只放在本地实验目录，提交内容只包含脱敏后的结构结论。

每个结论至少包含请求入口、必需 header、载荷位置、响应事件顺序、Cookie 变化、错误状态和实际模型。页面显示只能证明交互结果；网络记录证明官方前端使用的协议；移除浏览器后再次成功才证明纯协议链可用。

遇到前端更新时，先重新采集首页、模型目录和一条最短文本流，然后只修改已经变化的字段。模型名称、路由 hash、build、动态 token 和账号能力始终从当前账号运行时获取。

## 验收修改

账户和显式会话的排队等待使用请求 context；服务关闭会取消活动请求。流式写入错误直接终止输出，Responses 的结构化正文和终态复用已验证的响应对象。

代码格式和静态检查使用：

```powershell
gofmt -w <修改的 Go 文件>
go vet ./...
go build ./cmd/gemini-web2api
```

功能验收直接运行服务，并按修改范围调用真实入口。协议修改至少验证非流式、流式、取消和一次后续请求；认证修改至少验证首次导入、服务重启、Cookie 轮换和固定代理出口；适配器修改使用对应官方 SDK 或主流 Coding Agent 完成一轮真实请求。一次成功的目标场景与一次明确的失败场景足以形成证据，不需要为相同路径堆叠重复脚本。

## 提交贡献

Issue 请提供入口协议、模型 ID、HTTP 状态、最短请求和已脱敏的响应形状。涉及账号或地区的问题，请同时说明代理协议、setup 与服务是否使用同一出口。不要上传 Cookie、token、邮箱、完整抓包或绝对路径。

Pull Request 应只处理一个完整能力组，并附上可重复的真实验收命令和结果。工具调用、图片、思考、引用等上游能力先进入 `internal/gemini` 的规范事件，再由适配器分别输出，避免三套协议各自解析网页响应。

二次开发可以直接复用 `internal/gemini` 的 bootstrap、模型目录、请求编码和帧解码边界，也可以只使用某一个协议适配器。协议原理见 [protocol.md](protocol.md)。
