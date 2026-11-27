# 官网采集与重新对齐

Gemini 官网的前端与接口会在没有公告的情况下变化，服务突然报错、模型目录缺项或回答解析不全时，先采集一份官网现场，再与 `internal/gemini` 逐项对照。本文只需要一个浏览器与一个能登录 Gemini 的 Google 账号，各字段的含义见 [协议规范](protocol.md)。

## 采集

1. 在 Chrome 中打开 `https://gemini.google.com/app`，按 F12 打开开发者工具，切到 Network 面板，勾选 Preserve log，过滤框填 `batchexecute`
2. 刷新页面，依次出现 `rpcids=otAQ7b`（模型目录）、`ESY5D`（账号偏好）、`GPRiHf`（账号的使用限制警告）；打开用量页时出现 `jSf9Qc`
3. 发一条最短的消息，过滤框改为 `processSession` 看到生成会话：握手（`count=0`）、上行（`count=2`）、长轮询（`TYPE=xmlhttp`）、结束消息与关闭（`TYPE=terminate`）；带附件时另有 `upload` 与 `ProcessFile`（过滤框填 `BardChatUi`）
4. 对每个要对照的请求右键选 Copy → Copy as cURL (bash)，保存到本地文件；响应在 Response 标签页中全选复制保存
5. 页面源码中的启动数据在 Elements 面板搜索 `WIZ_global_data`，其中 `SNlM0e`（CSRF 令牌 `at`）、`cfb2h`（构建号 `bl`）、`FdrFJe`（`f.sid`）与 `TuX5cc`（界面语言）是服务构造请求时要用的字段
6. 官网前端代码从页面中以 `_.m` 开头的模块脚本加载，搜索某个 RPC ID（如 `otAQ7b`）可以找到它的请求描述符，搜索 `x-goog-ext-` 后的编号可以找到该扩展头对应的 JSPB 消息
7. 官网页面的生成会话以二进制 protobuf 上行，握手 URL 的 `$httpHeaders` 中 `-bin` 扩展头为 Base64 的 protobuf；用 `protoc --decode_raw` 解码后按字段号对照，字段号减一即服务 JSPB 编码中的数组下标

保存的请求含 Cookie、`at` 令牌与账号信息，只放在本地；公开 Issue 中只给出数组形状、事件顺序、模型 ID 与错误状态。

## 对照

| 现场 | 代码位置 | 对照内容 |
| --- | --- | --- |
| 页面启动数据 | `internal/gemini/bootstrap.go` | `WIZ_global_data` 各键名与上传参数 |
| 请求查询参数与表单 | `internal/gemini/client.go` 的 `batchExecute` | `rpcids`、`source-path`、`bl`、`f.sid`、`hl`、`_reqid`、`rt` 的顺序与 `f.req`、`at` 的编码 |
| batchexecute 与 ProcessFile 扩展头 | `internal/gemini/models.go` 的 `buildGenericHeader`、`buildConversationHeader`、`withVariantAndTimezone`，ProcessFile 在 `upload.go` | `x-goog-ext-525001261-jspb` 的槽位 |
| 生成会话请求头 | `internal/gemini/channel.go` 的 `channelHeaders`、`models.go` 的 `buildModelHeader` | `$httpHeaders` 各项的取值与顺序、模型头的槽位 |
| 生成会话上行 | `internal/gemini/payload.go`、`client.go` 的 `stream` | 会话配置消息与输入消息；99 槽 `inner` 中消息、附件、工具、会话三元组与模型模式的位置 |
| 生成会话下行 | `internal/gemini/channel.go` 的 `frame`、`decoder.go` | 长轮询帧的分隔与种类，生成载荷中候选快照、正文、思考、媒体、续接令牌与收尾帧的下标 |
| 浏览器身份 | `internal/gemini/identity.go`、`identity_builds.go`、`identity_transport.go` | 各 Chrome 版本的请求头、TLS 与 HTTP/2 指纹 |
| 模型目录 | `internal/gemini/models.go` | `otAQ7b` 响应中模型行、默认模式与思考强度的下标 |
| 账号偏好、用量、使用限制警告 | `internal/gemini/preferences.go`、`usage.go`、`abuse.go` | `ESY5D`、`jSf9Qc`、`GPRiHf` 的字段号 |
| 附件上传 | `internal/gemini/upload.go`、`media.go` | 上传两步的地址、头与 `ProcessFile` 的返回 |

按下面的顺序定位变化：

1. 查询参数、表单与扩展头：官网请求中多出、少掉或改名的项先改 `batchExecute` 与请求头编码
2. 载荷槽位：把官网生成会话的上行解码后逐层展开、batchexecute 的 `f.req` 逐层展开，与 `payload.go` 写入的下标比对，只改位置不同的槽
3. 响应下标：把长轮询正文按 `长度\nJSON` 拆成帧、batchexecute 响应按 `wrb.fr` 记录拆开，与 `channel.go`、`decoder.go` 读取的路径比对
4. 目录与偏好：官网 `otAQ7b`、`ESY5D` 的字段号变化时同步改解析，模型 ID 由目录生成，不写死

## 回归

1. 把采集到的响应去掉账号信息后存为测试的 testdata，解码测试断言从官网现场读到的值
2. `go test ./internal/gemini/...` 通过后重建可执行文件，用官方 SDK 或 Claude Code 等客户端发一次真实流式请求
3. 在 [协议规范](protocol.md) 中更新变化的字段，`docs/protocol.md` 第 7 节记录当前已解码与未解码的范围
