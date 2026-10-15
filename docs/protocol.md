# Gemini Web 协议参考

Gemini-Web2API 复现 `gemini.google.com` 当前网页请求链，并将响应统一为内部事件后投影到 OpenAI、Claude 和 Gemini API。实现依据位于 `internal/gemini/`，协议入口位于 `internal/adapter/`。

| 项目 | 当前实现 |
| --- | --- |
| 网页入口 | `gemini.google.com/app` |
| 模型目录 | `otAQ7b` RPC 动态发现 |
| 生成接口 | `BardFrontendService/StreamGenerate` |
| 默认会话 | Temporary Chat |
| 运行依赖 | 完成 setup 后仅需 Go 服务进程 |
| 公开协议 | OpenAI Chat、Completions、Responses、Images、Files、Audio、Videos，Claude Messages 与 Files，Gemini GenerateContent、Files 与 predictLongRunning |

## 1. 架构

```text
Chrome / Cookie Header
        │
        ▼
认证状态 ──► Bootstrap ──► 模型目录 ──► 账号能力池
                                      │
API 请求 ──► 协议适配 ──► Gemini Web ─┤
                                      ▼
API 响应 ◄── 协议投影 ◄── 规范事件 ◄── 流式解码
```

| 层 | 目录 | 职责 |
| --- | --- | --- |
| 认证 | `internal/chromeauth` | Chrome 账号发现、DBSC、Cookie 导入与续签 |
| 状态 | `internal/auth` | 认证文件加载、Cookie 原子写回 |
| 网页协议 | `internal/gemini` | Bootstrap、模型、请求编码、流式解码、媒体上传 |
| 调度 | `internal/balancer` | 模型能力、上下文窗口、请求槽占用、冷却与会话粘连 |
| 适配 | `internal/adapter` | OpenAI、Claude 和 Gemini 请求与事件投影 |

`internal/gemini` 负责网页帧解码，各 API 适配器消费统一的规范事件。

每个账号有 `PER_ACCOUNT_CONCURRENCY` 个请求槽（默认 1），每个槽同一时间只执行一条上游请求链：生成请求从上传、发送到流结束，`/v1/accounts/usage` 的用量读取，Cookie 轮换与续签都占用请求槽，其余请求排队等待；视频与 Deep Research 的后台轮询阶段让出请求槽。没有会话绑定的请求按 `ROUTING_STRATEGY` 选择有空闲请求槽的账号：`round-robin` 按轮询顺序，`fill-first` 优先选择排序靠前的账号；可用账号全部占用时排队。

## 2. Bootstrap 与模型目录

### 初始化链路

| 顺序 | 请求 | 读取内容 |
| ---: | --- | --- |
| 1 | `GET https://gemini.google.com/app` | `SNlM0e`、`cfb2h`、`FdrFJe`、`TuX5cc`、`oPEP7c` 登录邮箱、上传参数、响应 Cookie |
| 2 | `POST /_/BardChatUi/data/batchexecute?rpcids=otAQ7b` | 当前账号可见模型与思考强度 |
| 3 | `POST ...batchexecute?rpcids=ESY5D` | 账号偏好 |
| 4 | `POST ...batchexecute?rpcids=jSf9Qc&source-path=/usage` | 套餐、用量窗口、重置时间 |

任一步失败的账号在启动时标为不可用，之后按 `ACCOUNT_COOLDOWN` 间隔重新执行整条链路，成功后加入调度并开始轮换 Cookie。

### 多账号 Cookie

浏览器登录多个 Google 账号时，Cookie 中的会话按登录顺序编号，官网以路径前缀 `/u/N` 访问第 `N` 个账号，不带前缀时为第 0 个。账号状态 `authUser` 为 `N` 时，服务按官网 `/u/N` 页面的请求发送：

| 请求 | 默认账号 | 第 N 个账号 |
| --- | --- | --- |
| 首页 | `/app` | `/u/N/app` |
| batchexecute、StreamGenerate、ProcessFile | `/_/BardChatUi/...` | `/u/N/_/BardChatUi/...` |
| batchexecute `source-path` | `/app`、`/usage` | `/u/N/app`、`/u/N/usage` |
| 附件上传开始 | `push.clients6.google.com/upload/` | `push.clients6.google.com/upload/?authuser=N` |

`Referer` 都为 `https://gemini.google.com/`，`gemini.google.com` 请求不带 `x-goog-authuser`；上传完成请求使用开始响应 `x-goog-upload-url` 给出的地址。首页 `oPEP7c` 为当前路径下的登录邮箱，服务在账号就绪日志中输出。

### 访客会话

服务启动时另建一个不带 Cookie 的访客会话，账号池中的 ID 为 `guest`。访客会话按官网未登录页面执行初始化链路的前两步：首页没有 `SNlM0e`，模型目录只有 `3.5 Flash-Lite`（mode 6），不读取偏好与用量；官网访客页按首页 `Lz8rbb` 登录标志禁用全部输入框工具与上传入口，访客会话只公开对话模型，带附件的请求在只有访客会话可调度时返回 503；输入窗口按免费层 32K tokens 计算，之后每 10 分钟重读首页与模型目录。调度时先在账号中选择，没有可调度的账号时才使用访客会话；`/readyz` 与管理端在只有访客会话就绪时也报告服务可用，访客会话不计入账号数与官网用量。

| 项 | 登录会话 | 访客会话 |
| --- | --- | --- |
| 表单 | `f.req=...&at=SNlM0e&` | `f.req=...&` |
| 扩展头第 8 槽 | `[4,5,6,8,16,19,20]` | `[4,6,16,19,20]` |
| Temporary Chat | 由 `GEMINI_SAVE_HISTORY` 决定 | 不使用 |
| `inner[67]` | 模型目录 `payload[21]` 有 Personal Intelligence 时为 `0` | `null` |
| `inner[80]`、扩展头第 15 槽 | 思考强度编号 | 模型没有思考档位时为 `null` |

### Bootstrap 原始字段

首页解析以下形态：

| 运行参数 | 接受的首页形态 |
| --- | --- |
| `SNlM0e` | `"SNlM0e":"..."`，访客首页没有 |
| `bl` | `"cfb2h":"..."` |
| `f.sid` | `"FdrFJe":"..."`、`"f.sid":"..."` |
| 界面语言 | `"TuX5cc":"..."`，取自账号语言设置，与请求的 `Accept-Language` 无关 |
| 上传端点 | `"h1eoVe":"..."` |
| 上传 `push-id` | `"qKIAYe":"..."` |
| 上传 `x-client-pctx` | `"Ylro7b":"..."` |
| 上传条款有效期 | 实验 `"45725082":秒数`，当前为 15552000（180 天） |
| 服务条款同意令牌 | `"thykhd":"..."`，每次加载首页都不同 |

各 RPC 使用同一种 BatchExecute 信封，`REQUEST` 为下表请求的 JSON 字符串：

```json
[[["RPC_ID","REQUEST",null,"generic"]]]
```

| RPC | `rpcids` | `source-path` | `hl` | `x-goog-ext-73010989-jspb` | 请求 |
| --- | --- | --- | --- | --- | --- |
| 模型目录 | `otAQ7b` | `/app` | `en` | `[]` | `[1]` |
| 读取账号偏好 | `ESY5D` | `/app` | 账号界面语言 | `[0]` | `[null,[字段号...]]`，字段号为官网首页请求的 188 个 |
| 写入账号偏好 | `L5adhe` | `/app` | 账号界面语言 | `[0]` | `[偏好消息,null,[写入的字段号]]` |
| 官网用量 | `jSf9Qc` | `/usage` | 账号界面语言 | `[0]` | `[]` |
| 服务条款同意 | `UVb0T` | `/app` | 账号界面语言 | `[0]` | `[0,界面语言,thykhd,null×10,12,null,null,2]` |
| 读取会话轮次 | `hNvQHb` | `/app/<CID 去掉 c_>` | 账号界面语言 | `[0]` | `["c_CID",10,null,1,[0],[4],null,1]` |
| 下载原图 | `c8o8Fe` | `/app` | 账号界面语言 | `[0]` | 见「图片生成」 |
| 停止回答 | `NkpXw` | `/app` | 账号界面语言 | `[0]` | `[["c_CID","r_RID"],["CONTROL"]]`，`CONTROL` 取自生成流首帧 `payload[2]["21"][0]` |
| Gem 列表 | `CNgdBe` | `/app` | 账号界面语言 | `[0]` | `[类型,[界面语言],0]`，页面加载时依次请求类型 `2`（账号自建）与 `1` |

各 RPC 同时发送 `bl`、`f.sid`、递增 `_reqid`、`rt=c`，表单按 `f.req=...&at=SNlM0e&` 的顺序与 `encodeURIComponent` 编码发送，末尾带 `&`，访客会话没有 `at`。`f.req` 各层 JSON 与页面 `JSON.stringify` 相同，`<`、`>`、`&` 保持原样。模型目录固定用 `hl=en` 请求，模型 ID 不随账号界面语言变化。共享扩展 header 为：

```json
[1,null,null,null,null,null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,MODE,LEVEL,"CLIENT_UUID",["BL"],null,TIMING]
```

页面加载阶段的模型目录与偏好读取不带 `MODE`、`LEVEL`（写 `null`）；选定模型后发出的 RPC（例如上传条款确认）写入当前模型 mode 与思考强度。`UVb0T` 的 `TIMING` 耗时项为空数组 `[]`。`c8o8Fe` 使用会话内请求头：第 7 槽为 Temporary Chat 状态（`1` 或 `0`），第 8 槽功能编号与生成请求一样重复两遍，第 4、11 槽为 `null`。

响应记录形态为：

```json
["wrb.fr","RPC_ID","JSON_STRING_ENCODED_PAYLOAD"]
```

### 动态模型字段

| 字段 | 用途 |
| --- | --- |
| `hash` | 模型选择 header 与响应真实模型校验 |
| `displayName` | 公开名称与模型 ID 来源 |
| `description` | `/v1/models` 描述 |
| `mode` | 生成请求模型模式 |
| `default` | 单账号官网默认聊天模型 |
| `thinkingLevels` | Chat 模式下可选的思考强度 |

公开模型 ID 由显示名称规范化得到，例如 `3.8 Flash` 生成 `gemini-3.8-flash`。官网更新显示名后，启动读取的新目录直接生成对应 ID；各账号可以同时处于不同模型版本。解析同时接受公开 ID、显示名称和 hash。目录中不存在的模型返回 `404 model_not_found`。


模型行位于 `payload[15]`。解析器只赋予以下槽位语义，其他槽位保留为官网原始数据：

| 行槽位 | 解析结果 |
| ---: | --- |
| `0` | 模型 hash |
| `4` | 层级，写入模型 header 第 11 槽 |
| `11` | 显示名称 |
| `12` | 描述 |
| `17` | 模型 mode |
| `19` | 显示名称回退 |

账号默认模型是 mode 等于 `payload[0]` 的行；没有行匹配时取第一行。行槽 `7`、`15` 为界面上的 New 标记，`8`、`16` 为对应模型代号。Pro 账号目录形态为（能力数字数组记为 `CAPS`）：

```jsonc
payload[0] = 1
payload[15] = [
  ["8c46e95b1a07cecc","Flash-Lite","Fastest answers",CAPS,2,null,CAPS,false,"",1,"Flash-Lite","3.5 Flash-Lite","Fastest answers",null,null,false,"",6,CAPS,"3.5 Flash-Lite"],
  ["56fdd199312815e2","Flash","All-around help",CAPS,2,null,CAPS,false,"",20,"Flash","3.8 Flash","All-around help",null,null,false,"",1,CAPS,"3.8 Flash"],
  ["e6fa609c3fa255c0","Pro","Advanced reasoning",CAPS,2,null,CAPS,false,"",4,"Pro","3.1 Pro","Advanced reasoning",null,null,false,"",3,CAPS,"3.1 Pro"],
  ["95a4c0abcba24364","Argon","Complex tasks",CAPS,2,null,null,true,"Argon4p0",4,"Argon","4 Argon","Complex tasks",null,null,true,"Argon4p0",8,CAPS,"4 Argon"]
]
payload[24] = ["launch_ultra_mode_pro_policy",[
  [["",1,false,null,"Flash","All-around help",null,false,null,null,false,null,"3.8 Flash",CAPS,null,2,"Thinking level"],
   [[1,"Low",...],[4,"Medium",...],[2,"High",...]],null,[1]],
  [["",1,false,null,"Flash","All-around help",null,false,null,null,false,null,"3.8 Flash",CAPS,null,1,null],
   [[1,"Standard",...]],null,[2]],
  ...
]]
```

免费账号目录只有一行 `cf41b0e0dd7d53e5`（mode `6`），公开 ID 为 `gemini-auto`。

### 工具条目

官网输入框工具菜单写死在页面脚本中，账号可用的工具由 `payload[17]` 的账号能力号决定。目录为下列工具生成独立公开 ID，协调模型取账号默认模型，与在官网模型选择器选中默认模型后再选工具发送一致：

| 公开 ID | 官网工具 | featureMode | 能力号 |
| --- | --- | ---: | ---: |
| `gemini-3.1-flash-image` | Create image | `14` | `82` |
| `gemini-video` | Create video | `11` | `71`、`140` |
| `gemini-music` | Create music | `21` | `139` |
| `gemini-canvas` | Canvas | `2` | `51` |
| `gemini-deep-research` | Deep Research | `1` | `50` |
| `gemini-guided-learning` | Guided Learning | `24` | `117` |

`payload[24]` 中界面号为 `2` 的策略行是 Task 模式可选的模型与思考强度（3.8 Flash、4 Argon，只有 `1` Standard）。目录为这些模型生成 `<模型 ID>-task`，生成请求写 featureMode `40`。

账号初始化时按官网页面加载顺序以 `CNgdBe` 读取类型 `2` 与 `1` 的 Gem 列表，每个 Gem 生成公开 ID `gem-<Gem ID>`，账号自建 Gem 同时出现在两个列表中时只生成一个模型，显示名称与说明取 Gem 行 `[ID,[名称,说明],[指令]]`，协调模型取账号默认模型，生成请求在 `inner[19]` 写 Gem ID。读取失败的账号不提供 Gem，其余模型照常可用。

Task、Deep Research 只在普通会话中提供，视频成品在生成流结束后从会话读取；这三类请求在 `GEMINI_SAVE_HISTORY=false` 时也使用普通会话，对话保存在账号历史中。

`payload[19]` 是工具选项组。组槽 `0` 为 featureMode，`3` 为组名，`7` 为选项；选项槽 `0` 为选项 ID，`2` 为默认选中，`3` 为显示名称。Pro 账号的选项组为：

| featureMode | 组 | 选项 |
| ---: | --- | --- |
| `1` | Sources | `1` Search（默认）、`3` Gmail、`4` Drive、`12` Chat |
| `14` | Aspect ratio | `61` 1:1、`59` 9:16、`60` 3:4、`62` 4:3、`63` 16:9 |
| `11` | Aspect ratio | `16` Landscape (16:9)（默认）、`17` Portrait (9:16) |
| `21` | Length、Vocals、Genre | 时长、人声与曲风 |

生成请求的 `f.req[55]` 写入所选工具各组的默认项，Sources 组的选中项另以 `[[[[[选项 ID,...]]]]]` 写入 `f.req[54]`；请求指定宽高比时，Aspect ratio 组取比例最接近的一项；Create image 另在 `f.req[0]` 第 9 项写入 `[null×6,[null,[null,"<选项标签>"]]]`，例如 9:16 为 `[null×6,[null,[null,"9:16"]]]`。OpenAI Images 的 `size`、Responses `image_generation` 工具的 `size` 与 Gemini `generationConfig.imageConfig.aspectRatio` 都按此映射。

### 思考强度

`payload[24][1]` 的每一项为 `[HEADER, LEVELS, null, [SURFACE]]`：`HEADER[1]` 为模型 mode，`SURFACE` 为 `1`（Chat）或 `2`（Task）。服务读取 Chat 项：

| 强度 | 协议编号 |
| --- | ---: |
| Low | `1` |
| Medium | `4` |
| High | `2` |
| Standard（Task） | `1` |

请求的强度不在模型列表中时，取不高于请求的最强一档；没有这样的档位时取最弱一档。所选编号写入模型 header 第 15 槽与 `f.req[80]`。

### 账号偏好

`ESY5D` 响应 `payload[0]` 是偏好消息：字段号 `n` 位于数组下标 `n-1`，超出数组的字段位于末尾稀疏对象，键为字段号。服务使用的字段：

| 字段 | 内容 | 用途 |
| ---: | --- | --- |
| `25` | 布尔 | `f.req[41]`：真为 `[1]`，否则 `[2]` |
| `145` | 最近一次同意上传条款的 Unix 秒数 | 上传前判断条款确认是否过期 |
| `100` | 模型选择器当前选中模型的 hash，官网页面打开时预选该模型 | 新会话 `f.req[96]` 的上一轮模型 |
| `222` | 布尔，已完成 Task 首次使用确认 | 首次发送 Task 前写入 |
| `261` | 布尔 | `f.req[91]`：真为 `1`，否则 `0` |

网关的全局 `default` 按当前 ready 账号的官网默认票数、模型覆盖账号数、公开 ID 字典序生成。各模型的账号覆盖数由 `available_account_count` 返回。


**真实模型校验：** 模型 hash 可以晚于正文到达，标识确认前的事件按原顺序暂存；匹配后依次发布，不匹配或流结束时仍缺标识则终止响应。

### 上下文窗口

| 套餐 | 输入窗口 |
| --- | ---: |
| Free | 32,768 |
| AI Plus | 131,072 |
| AI Pro / Ultra | 1,048,576 |

`jSf9Qc` 返回套餐级别。模型目录公开可用输入窗口范围；单次请求按实际账号窗口选择路由。

`jSf9Qc` 的核心形态为：

```text
payload = [
  TIER_CODE,
  [
    [REMAINING, USED_RATIO, 1, [[UNIX_SECONDS, NANOS]], ...],
    [REMAINING, USED_RATIO, 2, [[UNIX_SECONDS, NANOS]], ...],
    [REMAINING_CREDITS, null, 3, ...]
  ],
  USE_OVERAGE_AI_CREDITS,
  [[SMART_MODEL_SELECTION]]
]
```

| 值 | 语义 |
| --- | --- |
| `kind=1` | 五小时用量窗口 |
| `kind=2` | 周用量窗口 |
| `kind=3` | 剩余 AI credits |
| `tier=1` | Free |
| `tier=2` | Pro |
| `tier=3/6` | Ultra |
| `tier=4` | Plus |

`USED_RATIO` 为已用比例，页面乘 100 取整显示；`payload[3]` 为免费账号的“自动选取模型”设置。空 `record[2]` 或缺失 `jSf9Qc` 记录按未登录处理。官网用量页在设置菜单的 “Usage limits” 入口内路由，发出 `jSf9Qc`；部分账号的 `/usage` 是服务端渲染页面，用量写在页面内嵌的 `getUsageInfo` 中，这些账号的 `jSf9Qc` 仍返回同样的数据。

## 3. 认证与账号身份

### 两种导入方式

| 方式 | 输入 | 可自动续签 | 适用场景 |
| --- | --- | --- | --- |
| Chrome setup | Chrome Profile 中的一个 Google 账号 | 是 | Windows 日常运行 |
| Cookie Header | 一行 `Cookie` 值与账号序号 | 否 | 快速导入、异机短期运行 |

同一 Profile 登录多个 Google 账号时，setup 逐个列出，选中的账号单独经 OAuthMultilogin 签发只含它自己的 Cookie。Cookie 来自登录多个账号的浏览器时，账号序号为官网地址 `gemini.google.com/u/N/app` 中的 `N`，服务按 [多账号 Cookie](#多账号-cookie) 访问该账号。

### Chrome DBSC 链路

```text
Local State ──► App-Bound 主密钥
Web Data   ──► v20 refresh token + wrapped binding key
Preferences ─► Accept-Language
                       │
                       ▼
OAuthMultilogin challenge
                       │ ES256 设备绑定断言
                       ▼
OAuthMultilogin directed response
	                       │ 逐个 HPKE 解密 cookies[].value
                       ▼
完整 Google Cookie
```

| 状态字段 | 内容 |
| --- | --- |
| `cookies` | 完整 Cookie、域、路径、到期时间与属性 |
| `geminiWeb2api.id` | 本地账号标识 |
| `proxy` | 账号固定出口 |
| `language` | 请求的 `Accept-Language` |
| `oauth` | Gaia ID、refresh token、wrapped binding key |

### Chrome 本地材料

| 来源 | 原始字段 |
| --- | --- |
| `Local State` | `profile.info_cache`、`os_crypt.app_bound_encrypted_key` |
| Profile `Preferences` | `account_info[].gaia`、`account_info[].email`：Profile 中登录的全部账号 |
| Profile `Web Data` | `SELECT service, encrypted_token, binding_key FROM token_service WHERE service = 'AccountId-GAIA_ID'` |
| Profile `Preferences` | `intl.accept_languages`，回退 `intl.selected_languages`；按 Chrome 规则在地区语言后补基础语言、q 值从 0.9 起逐项减 0.1（最低 0.1），得到 `Accept-Language` |

`token_service` 每个登录账号一行，`service` 形态为 `AccountId-GAIA_ID`；`account_info` 中没有对应行的账号不可导入。refresh token 密文形态为：

```text
"v20" || NONCE_12_BYTES || AES_256_GCM_CIPHERTEXT_AND_TAG
```

AES-GCM 使用 32 字节 App-Bound 主密钥和空 AAD。解密结果必须为 103 字节并以 `1//0` 开头。`app_bound_encrypted_key` 解码后的形态为：

```text
"APPB" || CHROME_IELEVATOR_CIPHERTEXT
```

实现启动本机 Chrome 的隐藏独立进程，向该进程载入 helper，通过 Chrome `IElevator::DecryptData` 取得 32 字节主密钥。现有 Chrome 进程与 Profile 不参与自动化操作。

### OAuthMultilogin 实现

```http
POST /oauth/multilogin?source=ChromiumAccountReconcilorDice&reuseCookies=0
Host: accounts.google.com
Authorization: MultiOAuth BASE64URL_PROTOBUF
Content-Type: application/x-www-form-urlencoded
User-Agent: Chrome/155
Content-Length: 1
```

`Authorization` 的无填充 Base64URL protobuf 结构为：

```proto
message MultiOAuth {
  Account account = 1;
}
message Account {
  bytes gaia_id = 1;
  bytes refresh_token = 2;
  bytes assertion = 3;
}
```

第一轮 `assertion` 为 `DBSC_CHALLENGE_IF_REQUIRED`，响应挑战核心形态为：

```json
{
  "status": "RETRY",
  "failed_accounts": [{
    "token_binding_retry_response": {"challenge": "CHALLENGE"}
  }]
}
```

第二轮 assertion 是 ES256 紧凑 JWS：

```json
{"alg":"ES256","typ":"jwt","schema":"DEVICE_BOUND_SESSION_CREDENTIALS_ASSERTION"}
```

```json
{
  "sub": "77185425430.apps.googleusercontent.com",
  "aud": "https://accounts.google.com/accountmanager",
  "jti": "CHALLENGE",
  "iss": "BASE64URL_SHA256_DEVICE_SPKI",
  "namespace": "TokenBinding",
  "ephemeral_key": {
    "kty": "type.googleapis.com/google.crypto.tink.EciesAeadHkdfPublicKey",
    "TinkKeysetPublicKeyInfo": "BASE64URL_TINK_HPKE_KEYSET"
  }
}
```

设备键通过 Microsoft Platform Crypto Provider 导入并生成 P-256 ES256 签名；临时 HPKE 键使用 X25519。第二轮响应核心形态为：

```json
{
  "status": "OK",
  "cookies": [{
    "name": "SAPISID",
    "value": "BASE64URL_HPKE_CIPHERTEXT",
    "domain": ".google.com",
    "path": "/",
    "maxAge": 34560000,
    "isHttpOnly": false,
    "isSecure": true,
    "sameSite": "Lax"
  }],
  "token_binding_directed_response": {}
}
```

`token_binding_directed_response` 当前只检查存在。每个 `cookies[].value` 独立解密：

```text
BASE64URL(
  X25519_ENCAPSULATED_PUBLIC_KEY_32_BYTES ||
  AES_128_GCM_CIPHERTEXT_AND_TAG
)
```

HPKE 使用 Base mode、DHKEM(X25519, HKDF-SHA256)、HKDF-SHA256、AES-128-GCM、空 info 与空 AAD。结果必须至少包含 `SAPISID` 和 `__Secure-1PSID`。

### 认证状态

```json
{
  "cookies": [{
    "name": "...",
    "value": "...",
    "domain": ".google.com",
    "path": "/",
    "expires": 0,
    "httpOnly": false,
    "secure": true,
    "sameSite": "Lax"
  }],
  "origins": [],
  "geminiWeb2api": {
    "id": "ACCOUNT_ID",
    "proxy": "PROXY_URL",
    "source": {"browser":"chrome","profile":"Profile 1"},
    "language": "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7",
    "authUser": 1,
    "oauth": {
      "gaiaId": "GAIA_ID",
      "refreshToken": "1//0...",
      "wrappedBindingKey": "BASE64_BYTES"
    }
  }
}
```

Cookie Header 导入将普通 Cookie 绑定到 `.google.com`，将 `__Host-*` 绑定到 `gemini.google.com`，统一使用 `/` 与 Secure 属性，并要求 `SAPISID`、`__Secure-1PSID`。该方式没有 OAuth 续签材料。

服务吸收每次响应的 `Set-Cookie`，Cookie 有变化时原子写回。每条请求最多执行一次续签、重新初始化与原请求重试：

| 请求 | 自动续签条件 |
| --- | --- |
| Bootstrap | HTTP `401/403`；首页缺少 `SNlM0e` |
| 模型目录、账号偏好 | HTTP `401/403` |
| Usage | HTTP `401/403`；登录页；空或缺失 `jSf9Qc` |
| StreamGenerate | HTTP `401/403`；首个公开事件前收到帧内 `401/403` |
| Cookie 轮换 | 轮换页或 `RotateCookies` 返回 HTTP `401/403`；轮换后 `ListAccounts` 报告会话失效 |
| 上传条款、上传、ProcessFile、媒体下载 | 当前没有自动续签分支 |

**设备绑定：** wrapped binding key 绑定原 Windows 用户密钥环境。异机复制的 Cookie 可使用至失效；自动续签需要在目标 Windows 设备重新 setup。

**代理出口：** setup、OAuthMultilogin、模型验证和日常请求使用同一账号代理。账号状态中的代理优先于全局 `PROXY`。

### Cookie 轮换

官网首页嵌入轮换页 iframe，页面脚本按间隔调用 `RotateCookies`。服务为每个初始化成功的账号运行同样的循环（`internal/gemini/rotate.go` 的 `CookieRotator`），轮换、`ListAccounts` 与续签占用账号请求槽：

1. 初始化后加载一次轮换页，取页面脚本 `init('<会话值>', 658.0, 0.0, 0.0, 600.0)` 的参数
2. 立即发送一次 `RotateCookies`，之后按响应的 `identity.hfcr` 秒数（最少 60 秒）重复；失败后按同一间隔重试
3. 每次 `RotateCookies` 成功后，以 Chrome 浏览器进程的方式请求一次 `ListAccounts`，确认 Cookie 中第一个账号的会话有效
4. 会话有效时释放请求槽，重读首页、模型目录（`otAQ7b`）、偏好（`ESY5D`）与用量（`jSf9Qc`），模型目录、偏好与上下文窗口随账号套餐变化更新；启动或恢复初始化后的第一轮不重读；重读失败时保留原有参数，按轮换失败处理
5. 轮换页或 `RotateCookies` 返回 `401/403`，或 `ListAccounts` 报告会话失效时，带 OAuth 材料的账号在请求槽内经 OAuthMultilogin 续签并重读，下次轮换重新加载轮换页；没有 OAuth 材料的账号报告未登录。`ListAccounts` 请求本身失败按轮换失败处理

```text
GET  https://accounts.google.com/RotateCookiesPage?og_pid=658&rot=3&origin=https%3A%2F%2Fgemini.google.com&exp_id=0
sec-fetch-site: same-site / sec-fetch-mode: navigate / sec-fetch-dest: iframe
referer: https://gemini.google.com/

POST https://accounts.google.com/RotateCookies
content-type: application/json
origin: https://accounts.google.com
referer: <轮换页 URL>

[658,"<会话值>"]
```

```text
POST https://accounts.google.com/ListAccounts?gpsia=1&source=ChromiumBrowser&json=standard&laf=b64bin
origin: https://www.google.com
content-type: application/x-www-form-urlencoded
sec-fetch-site: none / sec-fetch-mode: no-cors / sec-fetch-dest: empty
priority: u=4, i

<一个空格>
```

`init` 的第 1 个参数为会话值，是带符号的十进制整数（可为负），按字符串原样写入正文数组第 1 位；第 2、3、4 个参数依次写入第 0、2、3 位，零值省略；第 5 个参数为轮换间隔秒数。响应为 `)]}'` 前缀的 `[["identity.hfcr",600],["di",<n>]]`。每次响应都以 `Set-Cookie` 刷新 `SIDCC`、`__Secure-1PSIDCC`、`__Secure-3PSIDCC`；`__Secure-1PSIDTS` 与 `__Secure-3PSIDTS` 由服务端决定何时下发新值。

OAuthMultilogin 响应带 `__Secure-1PSIDTS` 与 `__Secure-1PSIDRTS`（同值，后者 10 分钟到期）时，`RotateCookies` 不下发新的 `__Secure-1PSIDTS`，会话在签发 2 小时后失效；响应不带这两项时，`RotateCookies` 下发并更新 `__Secure-1PSIDTS`。会话失效后首页仍返回 200 但缺少 `SNlM0e`，轮换页与 `RotateCookies` 仍返回 200，只有 `ListAccounts` 反映会话状态。`ListAccounts` 响应为 `application/binary`，正文是 base64 编码的 protobuf，1 号字段为账号列表，按 Cookie 中的账号顺序排列。服务读取账号序号对应的一项，默认为第一项：

| 字段 | 有效会话 | 失效会话 |
| --- | --- | --- |
| 9（会话有效） | `1` | `0` |
| 14（已退出） | 缺省 | `1` |

9 号字段为 `1` 且 14 号字段不为 `1` 时会话有效，其余情况按失效续签。`ListAccounts` 只按 `accounts.google.com` 的主机 Cookie（`LSID`、`__Host-1PLSID`、`__Host-3PLSID`、`__Host-GAPS`、`ACCOUNT_CHOOSER`）列出账号，Cookie 中没有这组时返回 200 与空正文，即没有账号条目；Cookie Header 导入的账号属于这种情况。没有账号条目时服务不判断会话状态，照常重读账号状态，登录态由重读首页确认。

三类请求的请求头发送顺序：

```text
轮换页：sec-ch-ua, sec-ch-ua-mobile, sec-ch-ua-full-version, sec-ch-ua-arch, sec-ch-ua-platform,
sec-ch-ua-platform-version, sec-ch-ua-model, sec-ch-ua-bitness, sec-ch-ua-wow64,
sec-ch-ua-full-version-list, sec-ch-ua-form-factors, upgrade-insecure-requests, user-agent, accept,
x-browser-channel, x-browser-year, x-browser-validation, x-browser-copyright, sec-fetch-site,
sec-fetch-mode, sec-fetch-dest, referer, accept-encoding, accept-language, cookie, priority

RotateCookies：content-length, pragma, cache-control, sec-ch-ua-full-version-list, sec-ch-ua-platform,
sec-ch-ua, sec-ch-ua-bitness, sec-ch-ua-model, sec-ch-ua-mobile, sec-ch-ua-form-factors, sec-ch-ua-wow64,
sec-ch-ua-arch, sec-ch-ua-full-version, user-agent, content-type, sec-ch-ua-platform-version, accept,
origin, x-browser-channel, x-browser-year, x-browser-validation, x-browser-copyright, sec-fetch-site,
sec-fetch-mode, sec-fetch-dest, referer, accept-encoding, accept-language, cookie, priority

ListAccounts：content-length, origin, content-type, sec-fetch-site, sec-fetch-mode, sec-fetch-dest,
user-agent, accept-encoding, accept-language, cookie, priority
```

## 4. 生成请求

### HTTP 请求

```text
POST https://gemini.google.com/_/BardChatUi/data/
     assistant.lamda.BardFrontendService/StreamGenerate
```

| 位置 | 字段 | 内容 |
| --- | --- | --- |
| Query | `bl` | Bootstrap build label |
| Query | `f.sid` | Bootstrap session ID |
| Query | `hl` | 首页 `TuX5cc` 界面语言 |
| Query | `_reqid` | 四位随机基数，每次请求增加 `100000` |
| Query | `rt=c` | 分块响应模式 |
| Form | `f.req` | 99 槽生成载荷 |
| Form | `at` | `SNlM0e` token，访客会话不带 |

服务的每个上游请求从发出到读完响应最长 600 秒，StreamGenerate 的完整流也在其内。

### 关键 header

生成请求与官网 Chrome 155 的 Temporary Chat 请求逐项一致：

```json
x-goog-ext-525001261-jspb =
[1,null,null,null,"MODEL_HASH",null,null,CHAT_MODE,[4,5,6,8,16,19,20,4,5,6,8,16,19,20],null,null,TIER,null,null,MODEL_MODE,THINKING_LEVEL,"CLIENT_UUID",["BL"],null,[[ELAPSED_SECONDS,ELAPSED_NANOS],[UNIX_SECONDS,NANOS]]]

x-goog-ext-525005358-jspb =
["REQUEST_UUID",1]

x-goog-ext-73010989-jspb = [0]
x-goog-ext-73010990-jspb = [0,0,0]
```

| Header | 数组位置 | 内容 |
| --- | ---: | --- |
| `x-goog-ext-525001261-jspb` | `4` | 模型 hash |
| 同上 | `7` | Temporary Chat `1`，普通会话 `0`，确认 Deep Research 计划时为 `null` |
| 同上 | `8` | 能力数组 `[4,5,6,8,16,19,20]` 重复两次 |
| 同上 | `11` | 模型目录行 `row[4]`，免费账号为 `1` |
| 同上 | `14` | 模型 mode |
| 同上 | `15` | 思考强度编号 |
| 同上 | `16` | 协议客户端 UUID |
| 同上 | `17` | `[bl]` |
| 同上 | `19` | 构造请求耗时与发送时间，秒为零时写 `null`，时间精确到毫秒 |
| `x-goog-ext-525005358-jspb` | `0` | 本次生成 UUID |

每个账号的 `gemini.Client` 在生命周期内保持自己的客户端 UUID；生成 UUID 每次请求重新创建，并同时写入 `f.req[59]`。

### 请求身份

所有访问 Google 的请求使用同一个 Chrome 155.0.8059.40（Windows x64 稳定版）身份。tls-client 的 `Chrome_152` 模板与该版本首次连接的 TLS 与 HTTP/2 指纹一致：

| 项目 | 值 |
| --- | --- |
| JA4 | `t13d1517h2_8daaf6152771_cb7bf5808d99` |
| HTTP/2 Akamai | `1:65536;2:0;4:6291456;6:262144\|15663105\|0\|m,a,s,p` |
| `user-agent` | `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36` |
| `sec-ch-ua` | `"Google Chrome";v="155", "Chromium";v="155", "Not(A:Brand";v="24"` |
| `accept-language` | 认证状态的 `language`，缺省 `en-US,en;q=0.9` |
| `accept-encoding` | `gzip, deflate, br, zstd` |

发往 Google 域的请求另带高熵 Client Hints 与 `x-browser-*`：`sec-ch-ua-arch` `"x86"`、`sec-ch-ua-bitness` `"64"`、`sec-ch-ua-form-factors` `"Desktop"`、`sec-ch-ua-full-version` `"155.0.8059.40"`、`sec-ch-ua-full-version-list`、`sec-ch-ua-model` `""`、`sec-ch-ua-platform-version` `"10.0.0"`、`sec-ch-ua-wow64` `?0`，以及 `x-browser-channel`、`x-browser-year`、`x-browser-validation`、`x-browser-copyright` 的官网固定值。Cookie 按 RFC 6265 与公共后缀表匹配域，以 `.` 开头的域为域 Cookie，其余为 host-only Cookie。

Chrome 的请求头发送顺序由请求头名称集合决定，每类请求使用官网现场的顺序：

```text
首页导航：sec-ch-ua, sec-ch-ua-mobile, sec-ch-ua-full-version, sec-ch-ua-arch, sec-ch-ua-platform,
sec-ch-ua-platform-version, sec-ch-ua-model, sec-ch-ua-bitness, sec-ch-ua-wow64,
sec-ch-ua-full-version-list, sec-ch-ua-form-factors, upgrade-insecure-requests, user-agent, accept,
x-browser-channel, x-browser-year, x-browser-validation, x-browser-copyright, sec-fetch-site,
sec-fetch-mode, sec-fetch-user, sec-fetch-dest, accept-encoding, accept-language, cookie, priority

batchexecute 与 StreamGenerate：content-length, sec-ch-ua-full-version-list, sec-ch-ua-platform,
sec-ch-ua, sec-ch-ua-bitness, sec-ch-ua-model, sec-ch-ua-mobile, x-same-domain, sec-ch-ua-wow64,
sec-ch-ua-form-factors, sec-ch-ua-arch, x-goog-ext-73010989-jspb, sec-ch-ua-full-version,
content-type, x-goog-ext-525001261-jspb, x-goog-ext-525005358-jspb, x-goog-ext-73010990-jspb,
user-agent, sec-ch-ua-platform-version, accept, origin, x-browser-channel, x-browser-year,
x-browser-validation, x-browser-copyright, sec-fetch-site, sec-fetch-mode, sec-fetch-dest, referer,
accept-encoding, accept-language, cookie, priority

ProcessFile：content-length, sec-ch-ua-full-version-list, sec-ch-ua-platform, x-goog-ext-525001261-jspb,
sec-ch-ua, sec-ch-ua-bitness, sec-ch-ua-model, sec-ch-ua-mobile, x-same-domain, sec-ch-ua-wow64,
sec-ch-ua-form-factors, sec-ch-ua-arch, sec-ch-ua-full-version, user-agent, content-type,
sec-ch-ua-platform-version, accept, origin, x-browser-channel, x-browser-year, x-browser-validation,
x-browser-copyright, sec-fetch-site, sec-fetch-mode, sec-fetch-dest, referer, accept-encoding,
accept-language, cookie, priority

上传开始：content-length, sec-ch-ua-full-version-list, sec-ch-ua-platform, sec-ch-ua, sec-ch-ua-bitness,
sec-ch-ua-model, sec-ch-ua-mobile, sec-ch-ua-form-factors, sec-ch-ua-wow64, x-goog-upload-protocol,
sec-ch-ua-arch, sec-ch-ua-full-version, content-type, x-goog-upload-header-content-length, push-id,
x-client-pctx, x-goog-upload-command, user-agent, x-tenant-id, sec-ch-ua-platform-version, accept,
origin, x-browser-channel, x-browser-year, x-browser-validation, x-browser-copyright, sec-fetch-site,
sec-fetch-mode, sec-fetch-dest, referer, accept-encoding, accept-language, cookie, priority

上传完成：content-length, sec-ch-ua-full-version-list, sec-ch-ua-platform, sec-ch-ua, sec-ch-ua-bitness,
sec-ch-ua-model, sec-ch-ua-mobile, sec-ch-ua-form-factors, sec-ch-ua-wow64, sec-ch-ua-arch,
sec-ch-ua-full-version, content-type, push-id, x-client-pctx, x-goog-upload-command,
x-goog-upload-offset, x-tenant-id, user-agent, sec-ch-ua-platform-version, accept, origin,
x-browser-channel, x-browser-year, x-browser-validation, x-browser-copyright, sec-fetch-site,
sec-fetch-mode, sec-fetch-dest, referer, accept-encoding, accept-language, cookie, priority

生成图下载：sec-ch-ua-full-version-list, sec-ch-ua-platform, sec-ch-ua, sec-ch-ua-bitness,
sec-ch-ua-model, sec-ch-ua-mobile, sec-ch-ua-form-factors, sec-ch-ua-wow64, sec-ch-ua-arch,
sec-ch-ua-full-version, user-agent, sec-ch-ua-platform-version, accept, origin, x-browser-channel,
x-browser-year, x-browser-validation, x-browser-copyright, sec-fetch-site, sec-fetch-mode,
sec-fetch-dest, sec-fetch-storage-access, referer, accept-encoding, accept-language, cookie, priority
```

首页导航的 `accept` 含 `image/jxl`，`priority` 为 `u=0, i`；XHR 的 `priority` 为 `u=1, i`。HTTP/2 伪头顺序为 `:method, :authority, :scheme, :path`。

### `f.req` 关键槽位

| 槽位 | 内容 |
| ---: | --- |
| `0` | 提示词与附件；Create video 第 9 项为 `[null×6,[[null,null,null,方向]]]`（横向 1、竖向 2），Deep Research 为 `[null×6,[]]`，指定宽高比的 Create image 为 `[null×6,[null,[null,比例]]]` |
| `1` | 首页 `TuX5cc` 界面语言 |
| `2` | 从较早回答继续时为该回答的 `CID`、`RID`、`RCID`；其余为 `null` |
| `3` | 官网页面令牌；服务写 `null` |
| `4` | 每次请求随机的 32 位十六进制上下文 ID |
| `6` | `[0]` |
| `17` | `[[N]]`，`N` 为会话中本轮之前的轮数：首轮 `0`，续接与从较早回答继续时为该回答所在轮数 |
| `19` | Gem 会话写 Gem ID；官网在笔记本会话中写账户保存的 bot 名，服务写 `null` |
| `30` | `[4,16,19]` |
| `41` | 账号偏好字段 25 为真时 `[1]`，否则 `[2]` |
| `45` | Temporary Chat 标记 `1` |
| `49` | 输入框工具 featureMode：普通提问 `0`，Deep Research `1`，Canvas `2`，Create video `11`，Create image `14`，Create music `21`，Guided Learning `24`，Task `40` |
| `54` | 工具有选项组时为空数组，Deep Research 为 Sources 选中项 `[[[[[1]]]]]` |
| `55` | 工具有选项组时为选中选项 `[[选项 ID],...]` |
| `59` | 本次生成 UUID |
| `61` | 空数组 |
| `67` | 模型目录 `payload[21]` 提供 Personal Intelligence 开关时为 `0`（开关关闭时官网写 `1`），没有该开关的账号与访客会话为 `null`；确认 Deep Research 计划时为 `null` |
| `68` | `1` |
| `71` | 续接最新一轮时为 `[CID,null×6,TOKEN]`，`TOKEN` 为上一轮回答的 `payload[2]["26"]`，Temporary Chat 为空串 |
| `72` | 从较早回答继续时为 `2` |
| `79` | 模型 mode |
| `80` | 思考强度编号，模型没有思考档位时为 `null` |
| `91` | 账号偏好字段 261 为真时 `1`，否则 `0` |
| `96` | 本轮模型与上一轮不同时为 `1`，新会话以官网预选的模型（偏好字段 `100`，缺失时为账号默认模型）为上一轮；官网在模型选择器点选模型后的下一次发送写 `1` |
| `97` | 确认 Deep Research 计划时为 `[0,0]` |
| `98` | `1` |

`GEMINI_SAVE_HISTORY=false` 使用 Temporary Chat。显式网页 session 保存三元组与续接令牌并固定到产生它的账号；无 session 请求相互独立。客户端从会话最新一轮回答继续时按官网追问编码；从较早的回答继续时按官网“编辑该回答之后的提问”编码，从该回答分叉。

### 完整 `f.req` 编码

表单字段是二层 JSON：

```javascript
fReq = JSON.stringify([
  null,
  JSON.stringify(inner99)
])
```

附件形态为：

```javascript
FILES = [
  [["UPLOAD_PATH", 1, null, "MIME_TYPE", "FILE_ID"], "FILE_NAME", null, null, null, null, null, null, [0]]
]
```

`UPLOAD_PATH` 为上传完成响应的路径，`FILE_ID` 为 ProcessFile 返回的文件 UUID。

服务编码器的完整 99 槽形态如下：

```jsonc
[
  /* 00 */ [PROMPT,0,null,FILES_OR_NULL,null,null,0,...TOOL_MESSAGE],
  /* 01 */ [HL],
  /* 02 */ BRANCH ? [CID,RID,RCID,null,null,null,null,null,null,""] : null,
  /* 03 */ null,
  /* 04 */ CONTEXT_ID,
  /* 05 */ null,
  /* 06 */ [0],
  /* 07 */ 1,
  /* 08 */ null,
  /* 09 */ null,
  /* 10 */ 1,
  /* 11 */ 0,
  /* 12 */ null,
  /* 13 */ null,
  /* 14 */ null,
  /* 15 */ null,
  /* 16 */ null,
  /* 17 */ [[PREVIOUS_TURNS]],
  /* 18 */ 0,
  /* 19 */ null,
  /* 20 */ null,
  /* 21 */ null,
  /* 22 */ null,
  /* 23 */ null,
  /* 24 */ null,
  /* 25 */ null,
  /* 26 */ null,
  /* 27 */ 1,
  /* 28 */ null,
  /* 29 */ null,
  /* 30 */ [4,16,19],
  /* 31 */ null,
  /* 32 */ null,
  /* 33 */ null,
  /* 34 */ null,
  /* 35 */ null,
  /* 36 */ null,
  /* 37 */ null,
  /* 38 */ null,
  /* 39 */ null,
  /* 40 */ null,
  /* 41 */ PREFERENCE_25 ? [1] : [2],
  /* 42 */ null,
  /* 43 */ null,
  /* 44 */ null,
  /* 45 */ TEMPORARY_CHAT ? 1 : null,
  /* 46 */ null,
  /* 47 */ null,
  /* 48 */ null,
  /* 49 */ FEATURE_MODE,
  /* 50 */ null,
  /* 51 */ null,
  /* 52 */ null,
  /* 53 */ 0,
  /* 54 */ TOOL_OPTIONS ? [] : null,
  /* 55 */ TOOL_OPTIONS ? [[OPTION_ID],...] : null,
  /* 56 */ null,
  /* 57 */ null,
  /* 58 */ null,
  /* 59 */ REQUEST_UUID,
  /* 60 */ null,
  /* 61 */ [],
  /* 62 */ null,
  /* 63 */ null,
  /* 64 */ null,
  /* 65 */ null,
  /* 66 */ null,
  /* 67 */ PERSONAL_INTELLIGENCE && !RESEARCH_CONFIRM ? 0 : null,
  /* 68 */ 1,
  /* 69 */ null,
  /* 70 */ null,
  /* 71 */ CONTINUE_LATEST ? [CID,null,null,null,null,null,null,TEMPORARY_CHAT ? "" : TOKEN] : null,
  /* 72 */ BRANCH ? 2 : null,
  /* 73 */ null,
  /* 74 */ null,
  /* 75 */ null,
  /* 76 */ null,
  /* 77 */ null,
  /* 78 */ null,
  /* 79 */ MODEL_MODE,
  /* 80 */ THINKING_LEVEL || null,
  /* 81 */ null,
  /* 82 */ null,
  /* 83 */ null,
  /* 84 */ null,
  /* 85 */ null,
  /* 86 */ null,
  /* 87 */ null,
  /* 88 */ null,
  /* 89 */ null,
  /* 90 */ null,
  /* 91 */ PREFERENCE_261 ? 1 : 0,
  /* 92 */ null,
  /* 93 */ null,
  /* 94 */ null,
  /* 95 */ null,
  /* 96 */ MODEL_SWITCHED ? 1 : 0,
  /* 97 */ RESEARCH_CONFIRM ? [0,0] : null,
  /* 98 */ 1
]
```

官网 Chrome 155 Temporary Chat 首轮的非 `null` 槽完整集合为（选择有选项组的工具时另有 `54`、`55`）：

```text
0,1,3,4,6,7,10,11,17,18,27,30,41,45,49,53,59,61,67,68,79,80,91,96,98
```

`inner[3]` 是页面生成的约 2000 字符不透明令牌，服务写 `null`；带页面令牌的官网载荷与服务载荷在同一批计算题上的固定失败文字比例相同。保存历史的首轮与 Temporary Chat 首轮的非 `null` 槽只差 `45`；续接最新一轮另有 `71`，从较早回答继续另有 `2`、`72`。官网“重新生成”另写 `inner[32]=1`、`inner[63]=[1001,null,0]`、`inner[72]=5`。

### 附件上传

带附件的生成请求在 StreamGenerate 之前依次执行：

| 顺序 | 请求 | 内容 |
| ---: | --- | --- |
| 1 | `L5adhe` 写入账号偏好字段 145 | 仅在条款确认已过期时发送 |
| 2 | `POST` 首页 `h1eoVe` 端点 | 断点续传开始，响应头 `x-goog-upload-url` 给出本次上传地址 |
| 3 | `POST` 上传地址 | 写入文件内容并完成，响应正文为上传路径 |
| 4 | `POST .../BardFrontendService/ProcessFile` | 登记上传路径，返回文件 UUID |

**上传条款：** 官网首次上传或距上次确认超过有效期（首页实验 `45725082`，当前 180 天）时弹出上传条款对话框，“同意”按钮把当前 Unix 秒数写入账号偏好字段 145。服务按同一规则判断，过期时发送同样的写入：

```text
f.req = [[["L5adhe","[[null ×144, UNIX_SECONDS],null,[145]]",null,"generic"]]]
响应 payload = [1]
```

**上传开始与完成：** 两步都是跨站 XHR（`sec-fetch-site: same-site`），带 `push-id`（首页 `qKIAYe`）、`x-client-pctx`（首页 `Ylro7b`）与 `x-tenant-id: bard-storage`：

```http
POST https://push.clients6.google.com/upload/
content-type: application/x-www-form-urlencoded;charset=UTF-8
x-goog-upload-protocol: resumable
x-goog-upload-command: start
x-goog-upload-header-content-length: FILE_SIZE

File name: FILE_NAME
```

```http
POST UPLOAD_URL
content-type: application/x-www-form-urlencoded;charset=utf-8
x-goog-upload-command: upload, finalize
x-goog-upload-offset: 0

RAW_FILE_BYTES
```

完成响应正文为 `/contrib_service/ttl_1d/...` 形态的上传路径。附件未声明 MIME 类型时按内容嗅探。

**ProcessFile：** 与 batchexecute 使用同样的 query 与表单编码，模型 header 带模型 hash、mode 与思考强度、不带第 7、11 槽：

```text
f.req = [null,"[[[UPLOAD_PATH,null,1,MIME_TYPE],FILE_NAME,null,null,null,null,null,null,[0]],null,1,[HL]]"]
x-goog-ext-525001261-jspb = [1,null,null,null,"MODEL_HASH",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,MODEL_MODE,THINKING_LEVEL,"CLIENT_UUID",["BL"],null,TIMING]
```

响应 `wrb.fr` 记录的 `payload[3][0]` 为文件 UUID。

### 图片生成

官网输入框工具菜单的 Create image 把 `f.req[49]` 设为 featureMode `14`，`f.req[54]` 设为空数组，`f.req[55]` 写入 Aspect ratio 选项（未选择时为空数组），其余槽与普通提问相同；生成请求由 `StreamGenerate` 执行。官网通过 `ku4Jyf` 查询类别 `14` 的图片额度。工具菜单对所有模型开放，4 Argon 选择 Create image 时只返回文字。

成功图片响应同时披露三层身份：

| 身份 | 原始位置 | Pro 账号 3.8 Flash | 免费账号默认模型 |
| --- | --- | --- | --- |
| 聊天协调模型 hash | `payload[39]` | `56fdd199312815e2` | `cf41b0e0dd7d53e5` |
| 图片产品名称 | `payload[42]` | `Nano Banana 2.1` | `Nano Banana 2` |
| 类别号 | `candidate[12][7][0][0][3][0]` | `23` | `20` |
| 内部生成器 tag | `candidate[12][7][0][0][3][18]` | `imagen_default.ultra` | `imagen_default.complaintflow` |

脱敏后的原始媒体项形态为：

```jsonc
candidate[12][7] = [[[
  [null,null,null,[
    null,1,"<FILE_NAME>","<MEDIA_URL>",null,"<OPAQUE>",
    null,null,null,[1787543442,751863480],null,"image/jpeg",
    null,null,null,[1408,768,512969]
  ]],
  ["<PLACEHOLDER_URL>"],
  null,
  [20,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,false,null,"imagen_default.ultra"]
]],null,[true]];
```

同一 payload 的镜像结构也在 `payload[26][0][0][0][9][0][0][3][18]` 重复 generator tag；当前解码器以候选 rich content 为主读取路径。

网关公开的 `gemini-3.1-flash-image` 见「工具条目」，协调模型为账号默认模型。该 ID 支持对话入口、Gemini `generateContent` 与 `/v1/images/generations`、`/v1/images/edits`；Responses 的 `image_generation` 工具对所选模型发送 Create image。OpenAI Chat/Responses 通过 `provider_model` 返回实际 generator tag。

图片生成期间还会先出现根级稀疏进度：

```text
payload[2]["7"] = [null,["data_analysis_tool",[null,null,"Creating your image",""],STATUS,[null,[null,null,null,null,null,null,null,[23]],false]]]
```

类别 `20`（免费账号）或 `23`（Pro 账号）标识图片任务，`STATUS=1` 为进行中、`STATUS=4` 为工具成功。图片结果取自后续 candidate 帧中的媒体 URL，消费方应等到该 URL 到达后下载。

**原图下载。** 媒体项中的地址是 `=s1024-rj` 预览。服务按官网“Download full size image”按钮发送 `c8o8Fe`，载荷由媒体项逐字段重建：

```jsonc
[
  [
    [null,null,null,[null,null,null,null,null,"$图片令牌"]],   // 媒体项 [0][3][5]
    ["<PLACEHOLDER_URL>",VARIANT],                                // 媒体项 [1][0]，[1][1] 缺省 0
    null,
    [CATEGORY,"",null×14,FLAG,null,"<GENERATOR_TAG>",null,null,null,SIGNATURE], // 媒体项 [3] 的 0、1、16、18、22 项，布尔写 1/0
    null,null,null,null,
    "<ARTIFACT_ID>",                                              // 媒体项 [8]
    "<IMAGE_ID>"
  ],
  ["RID","RCID","CID",null,"<IMAGE_ID>"],
  1,              // 网页来源
  TEMPORARY_CHAT, // 1 或 0
  1               // 推荐尺寸
]
```

`IMAGE_ID` 为媒体项 [9]；缺省时由页面规则算出：对字符串 `RCID_VARIANT_序号` 做 32 位 `h=h*31+c` 哈希，取绝对值的 36 进制后 12 位，重复截成 16 位，也是下载文件名 `Gemini_Generated_Image_<IMAGE_ID>`。响应 `[0]` 为 `lh3.googleusercontent.com/gg-dl/…`（免费账号为 `/gg/…`），追加 `=d-I` 后按预览相同的下载链跟随：`?alr=yes` 首跳返回 `text/plain` 下一跳，Pro 账号中继为 `work.fife.usercontent.google.com/rd-gg-dl/`，免费账号为 `lh3.google.com/rd-gg/`，末跳返回原图（现场 2752×1536 JPEG、1800×1073 PNG）。

### 视频与音乐

Create video 写 featureMode `11`、`f.req[55]=[[16]]`（Landscape 默认），`f.req[0]` 第 9 项为 `[null×6,[[null,null,null,方向]]]`，方向为所选 Aspect ratio 在菜单中的序号：Landscape (16:9) 为 `1`，Portrait (9:16) 为 `2`（同时 `f.req[55]=[[17]]`）。生成流只返回根级进度后结束，没有候选：

```text
payload[2]["7"] = ["I'm generating your video…",["video_generation",[null,null,"Video Generation",ICON],1,[null,[null×8,[1]]]]]
```

视频在后台生成（现场约 3 分钟）。服务每 30 秒以 `hNvQHb` 读取会话轮次（新到旧，`payload[0][i]` 的 `[0]` 为 `[CID,RID]`，候选在 `[3][0][0]`），本轮候选 `phase=2` 后按生成流规则解析；某次轮询遇到连接、TLS 或代理错误时在下一个周期继续轮询；轮询期间让出账号请求槽，同一账号可处理其他请求。成品在 `candidate[12]` 稀疏键 `"60"`：`[0][0][0][0]` 为文件项，`[2]` 文件名，`[7][1]` 下载地址，`[11]` MIME，`[17]` 为 `[[秒],宽,高]`。

Create music 写 featureMode `21`、`f.req[55]=[]`，成品在生成流内完成（现场 44 秒，`payload[42]` 为 `Lyria 3.5`）。成品在 `candidate[12]` 稀疏键 `"87"`：`[0][1]` 为 mp3 文件项（`audio/mpeg`），`[1][1]` 为带封面的 mp4，`[6][0]` 为正文占位链接。

视频与音乐的下载地址为 `contribution.usercontent.google.com/download?...`，页面以带 Cookie 的 fetch 一次取得文件。正文中的 `generated_video_content`、`generated_music_content` 占位链接与图片占位一样从正文移除，媒体按占位位置插入。

### 朗读

官网回答菜单的 Listen 以 batchexecute `XqA3Ic`（`source-path=/app`）合成语音。`f.req` 载荷为 `[null,TEXT,LANGUAGE,null,FORMAT]`，回答为 `[BASE64_AUDIO]`：

| 槽位 | 取值 |
| --- | --- |
| `TEXT` | 朗读文本，单次最多 4096 字符，超过时回答只有 3 字节 |
| `LANGUAGE` | 首页 `TuX5cc` 的账号界面语言，例如 `en-JP`；文本为其他语言时同样合成，`null` 时没有音频 |
| `FORMAT` | `1` 为 24 kHz 单声道 MPEG-2 Layer III，`2` 为官网页面播放的 Ogg Opus |

`/v1/audio/speech` 调用该 RPC 并占用账号请求槽：`opus` 以 `FORMAT=2` 原样返回，其余格式以 `FORMAT=1` 取 MP3，`wav`、`flac` 与 24 kHz 单声道 `pcm` 由 MP3 解码转换。

### Deep Research

Deep Research 写 featureMode `1`，`f.req[54]=[[[[[1]]]]]`、`f.req[55]=[[1]]`（Sources 默认 Search），`f.req[0]` 第 9 项为 `[null×6,[]]`，只在普通会话中提供。流程为：

| 步骤 | 请求 | 响应 |
| --- | --- | --- |
| 计划 | 用户提问 | 正文含 `deep_research_confirmation_content` 占位；计划卡片在 `candidate[12]` 稀疏键 `"56"`：`[标题,[[1,"Research Websites",步骤],[2,"Analyze Results"],[3,"Create Report"]],"Ready in a few mins",["Start research"],...]` |
| 确认 | 以卡片 `[3][0]` 的按钮文字为提问续接；`f.req[67]=null`、`f.req[97]=[0,0]`，请求头第 7 槽为 `null` | 正文含文档占位；研究卡片在稀疏键 `"58"`，流结束时候选仍为 `phase=1` |
| 报告 | `hNvQHb` 轮询 | 本轮候选 `phase=2` 时正文为完成说明，报告 Markdown 在 `candidate[30][0][4]`，由 `"57"` 的占位映射插入正文 |

研究卡片 `"58"` 的 `[1][4][2]` 为步骤列表，每步 `[null×4,[1,"Researching websites...",来源,[]]]`，来源为 `[图标,URL,标题,...]`。现场确认后 12.5 分钟完成。报告轮询与视频相同，轮询期间让出账号请求槽。服务把计划、确认回复与研究步骤作为思考输出，报告作为正文输出；计划轮没有计划卡片时（例如上游直接回答），该轮回答即为结果，作为正文输出。

### Task

Task 写 featureMode `40`，没有 `f.req[54]`、`f.req[55]`，回答在生成流内完成。账号偏好字段 `222` 为假时，官网首次发送前显示 “Let's get started” 对话框，点 Continue 后依次发送：

1. `L5adhe` 写入 `[[null×221,1],null,[222]]`
2. `StreamGenerate`
3. `UVb0T` 服务条款同意 `[0,界面语言,thykhd,null×10,12,null,null,2]`，响应 `[""]`

服务对未确认的账号按同一顺序发送，`UVb0T` 在生成响应开始后发出。

## 5. 流式解码

Google 响应由长度提示与 JSON 记录交错组成，单条 JSON 记录可跨越多个网络 chunk。

| 记录 | 处理 |
| --- | --- |
| `wrb.fr` | RPC 数据帧 |
| `er` | 帧内协议错误 |
| 终止记录 | 校验完成阶段与结束原因 |

### 原始帧格式

```text
)]}'

DECIMAL_LENGTH_HINT
[["wrb.fr",RPC_ID_OR_NULL,"JSON_STRING_ENCODED_PAYLOAD"]]
DECIMAL_LENGTH_HINT
[["e",4,null,null,1]]
```

解码器跳过十进制长度行，对每个完整 JSON 行遍历内部记录；单行上限为 64 MiB。`wrb.fr[2]` 再次 JSON 解码，`er[5]` 是协议错误码，`e` 是终止标记。生成流不校验 `wrb.fr[1]`；模型和 usage 流分别校验 `otAQ7b`、`jSf9Qc`。

### 生成 payload 路径

| 数据 | 原始路径 |
| --- | --- |
| CID | `payload[1][0]` |
| RID | `payload[1][1]` |
| 候选容器 | `payload[4]` |
| RCID | `candidate[0]`，以 `rc_` 开头 |
| 正文累计快照 | `candidate[1][0]` |
| phase | `candidate[8][0]`，`1=generating`、`2=complete` |
| 思考累计快照 | `candidate[37][0][0]` |
| 标题 | `payload[2]["11"][0]` |
| 控制 token | `payload[2]["21"][0]` |
| 续接令牌 | `payload[2]["26"]` |
| 实际聊天模型 hash | `payload[39]` 或稀疏尾对象键 `"40"` |
| 实际模型名称 | `payload[42]` 或稀疏尾对象键 `"43"` |

候选最小形态为：

```jsonc
[
  null,
  ["CID","RID"],
  {"11":["TITLE"],"21":["CONTROL_TOKEN"]},
  null,
  [[
    "rc_RESPONSE_ID",
    ["TEXT_SNAPSHOT"],
    null,
    null,
    null,
    null,
    null,
    null,
    [1]
  ]]
]
```

**模型 hash 可以晚于正文到达。** 标识确认前的事件按原顺序暂存，收到与请求模型匹配的 hash 后依次发布。hash 与请求模型不一致或终止前始终没有 hash，产生可重试 `502`。

### Canvas 文档

Canvas 回答的正文在文档位置写占位链接 `http://googleusercontent.com/immersive_entry_chip/N`，文档内容在同一候选中随帧增长：

| 数据 | 原始路径 |
| --- | --- |
| 占位链接到文档 ID | `candidate[12]` 稀疏键 `"57"`：`[[[占位链接],文档 ID,null,标题,null,[时间戳],null,2]]` |
| 文档列表 | `candidate[30]`：`[[文档 ID,null,标题,null,内容,...]]` |
| 代码文档内容 | Markdown 代码块，例如 ```` ```html ```` |

回答完成前，解码器输出的正文截止到第一个占位链接；`phase=2` 时把每个占位链接换成对应文档内容，正文保持只追加。页面预览沙箱另以 `rJGHib` 取 Firebase 令牌，文档内容只来自生成流。

### Code Execution 原始文本

代码执行嵌在 `candidate[1][0]` 的累计字符串中：

````text
```python?code_reference&code_event_index=0
print("hello")
```
```?code_stdout&code_event_index=0
hello
```
```?code_stderr&code_event_index=0
...
```
````

事件类型只接受 `code_reference`、`code_stdout`、`code_stderr`。关闭围栏尚未出现时 `complete=false`。代码段会从正文移除并记录其可见文本 rune offset。

### Search 与 citations 原始数组

源码从 `candidate[2][1]` 读取引用组：

```jsonc
[
  [
    [CLAIM,null,null,[[START,END]]],
    null,
    [
      [URL,TITLE,FAVICON,SNIPPET,null,null,PUBLISHER]
    ],
    SOURCE_ID
  ]
]
```

| 字段 | 路径 |
| --- | --- |
| claim | `group[0][0]` |
| start/end | `group[0][3][0][0/1]` |
| entries | `group[2]` |
| source ID | `group[3]` |
| URL/title/favicon/snippet/publisher | `entry[0/1/2/3/6]` |

OpenAI Responses 的 `web_search_call` 由适配层根据 citations 生成，状态为 `completed`。

回答引用上传文件时，`candidate[12]` 稀疏尾对象的键 `"44"` 列出引用标记：每项为 `[["[cite: N]",0],[来源],[被引正文,0]]`，来源带文件名与 MIME；同一帧正文末尾带 `[cite: N]`。官网把这些标记渲染为来源标签，解码器把它们从正文中移除。

搜索回答的主候选正文有时以 Bento 卡片代码块开头：起始行为 ```` ```json?chameleon ````，内容是带标题、副标题、配图描述与链接的组件 JSON，之后才是回答正文，同一候选没有 `candidate[2]` 引用。官网页面不显示该代码块，只显示其后的正文；解码器同样去掉该代码块，代码块结束前不输出正文。

### 生成媒体原始数组

rich content 位于 `candidate[12][7]`，也接受 `candidate[12]` 稀疏尾对象的键 `"8"`：

| 字段 | 路径 |
| --- | --- |
| URL | `item[0][3][3]` |
| 文件名 | `item[0][3][2]` |
| MIME | `item[0][3][11]` |
| placeholder | `item[1][0]` |
| generator tag | `item[3][18]` |
| width/height/bytes | `item[0][3][15][0/1/2]` |

`googleusercontent.com/image_generation_content/*` 是占位地址，会从正文和媒体结果移除。实际生成媒体使用 `lh3.googleusercontent.com/gg-dl/*`。下载链为：

```text
GET https://lh3.googleusercontent.com/gg-dl/OPAQUE=s1024-rj?alr=yes
  -> 200 text/plain：https://work.fife.usercontent.google.com/rd-gg-dl/OPAQUE=s1024-rj?alr=yes
GET https://work.fife.usercontent.google.com/rd-gg-dl/...
  -> 200 text/plain：https://lh3.googleusercontent.com/rd-gg-dl/OPAQUE=s1024-rj?alr=yes
GET https://lh3.googleusercontent.com/rd-gg-dl/...
  -> 200 图片
```

三步都是页面发起的 CORS 请求（`origin` 与 `referer` 为 Gemini 首页，`accept: */*`）。`*.usercontent.google.com` 为 `sec-fetch-site: same-site` 并按域匹配带上 `.google.com` 的 Cookie；`lh3.googleusercontent.com` 为 `cross-site` 加 `sec-fetch-storage-access: active`，不带 Cookie。`=s1024-rj` 缺失时补上。下载最多跟随五次 text/plain 中继或 HTTP 重定向。

### 规范事件

| 事件 | 内容 |
| --- | --- |
| `session` | CID、RID、RCID |
| `text` | 正文累计快照变化 |
| `thought` | 可见思考摘要累计快照变化 |
| `code` | `code_reference`、stdout、stderr |
| `citations` | URL、标题、引用范围与来源信息 |
| `media` | 生成图片、视频与音频及其 MIME、尺寸、下载地址与 generator tag |
| `image_progress` | 根级图片任务状态 `1`、`4` |
| `phase` | generating / complete |
| `metadata` | 标题、控制 token、模型 hash 与名称 |
| `error` | HTTP 或帧内协议错误 |
| `done` | 当前生产路径为 stop / error |

正文与思考采用累计快照。解码器计算 `append`、`replace`、`truncate`，候选项之间保持独立状态。思考先于正文投影；正文发生后续改写时，适配器缓冲最终快照，维持协议合法顺序。

### 错误与重试

| 情况 | 行为 |
| --- | --- |
| 请求模型与响应模型不一致 | 终止并返回上游错误；会话失效后 StreamGenerate 仍返回 200，回答来自未登录时的默认模型，属于这种情况 |
| StreamGenerate HTTP 或帧内 `401/403` | 首个公开事件前续签并重试一次 |
| transport、空终止帧、可重试协议错误 | 首个可见输出前切换同能力账号；连接、TLS 与代理错误不冷却账号 |
| 生成流距上一个语义事件 600 s，期间只有心跳 | 中止并发送停止回答，按 `504` 可重试错误在首个可见输出前换号 |
| 客户端在生成中断开 | 发送停止回答 `NkpXw`，与官网点击停止相同 |
| 首页、RPC、生成或上传被 302 重定向到 `www.google.com/sorry`（人机验证页） | 出口 IP 与该账号会话被判定为异常流量，按 `429` 返回，整个账号按 `ACCOUNT_COOLDOWN` 冷却，首个可见输出前换号；初始化时账号保持不可用并按间隔重试 |
| 新会话首次失败 | 换账号后重新建立会话 |
| 已建立显式会话失败 | 保持账号粘连并返回错误 |
| 客户端参数错误 | 返回 `4xx`，账号保持健康 |
| batchexecute 的 `wrb.fr` 记录没有载荷、第 5 项为 `[7]` | 会话已失效，按 HTTP `403` 处理：初始化时带 OAuth 材料的账号续签并重新初始化 |
| 正文整段为已知固定失败文字 | 按可重试上游错误在首个可见输出前换账号，不计入账号冷却；重试用尽返回 `502` 与失败文字 |

生成流也可能只返回一条 `wrb.fr[2]` 为空的记录，错误码位于 `wrb.fr[5][2][0][1][0]`，形态为 `[8,null,[["type.googleapis.com/assistant.boq.bard.application.BardErrorInfo",[CODE]]]]`。服务按错误码返回：

| 错误码 | 含义 | HTTP | 换账号重试 |
| ---: | --- | ---: | --- |
| `1037` | 账号当前模型用量已达上限 | 429 | 是 |
| `1050` | 模型与会话历史不一致 | 400 | 否 |
| `1052` | 模型请求头无效或模型暂不可用 | 502 | 否 |
| `1060` | 出口 IP 被暂时限制 | 403 | 是 |
| `1095` | 账号请求频率受限，约一分钟后恢复 | 429 | 是 |
| 其他 | 上游临时错误，例如 `1003`（附件未绑定会话）、`1013`、`1097` | 502 | 是 |

Gemini Web 也会在 HTTP 200、无错误帧的正常候选中以正文返回固定失败文字，例如 `Sorry, something went wrong. Please try your request again.`、`I encountered an error doing what you asked. Could you try again?`、`I seem to be encountering an error. Can I try something else for you?`、`I'm having a hard time fulfilling your request. Can I help you with something else instead?`，以及 Create image 偶发的 `Normally I can help with things like this, but I don't seem to have access to that content. You can try again or ask me for something else.`。完整列表位于 `internal/gemini/failure_text.go`。上游临时故障时主候选正文也可能整段为错误卡片链接 `http://googleusercontent.com/card_content/<n>`，提示文字在 `candidate[22][0]`，官网显示重试按钮；服务按固定失败文字同样处理。主候选正文仍是其中某条文字的前缀时，服务暂缓转发该正文及其后的事件，正文出现前的思考等事件照常转发；正文一旦偏离即按原顺序放行。上游也会先输出其他正文（例如工具调用 JSON 的开头），再把整段快照改写为固定失败文字；服务在流结束时以最终正文判断，整段相等则转为上表的上游错误。

## 6. API 投影

| 内部事件 | OpenAI Chat | OpenAI Responses | Claude Messages | Gemini API |
| --- | --- | --- | --- | --- |
| `text` | `delta.content` | `output_text.delta` | `text_delta` | `parts.text` |
| `thought` | reasoning 字段 | `reasoning` item | `thinking` block | `thought: true` |
| 自定义函数 | `tool_calls` | `function_call` | `tool_use` | `functionCall` |
| Google Search | `annotations` 的 `url_citation` | `web_search_call` 与 `url_citation` | `server_tool_use`、`web_search_tool_result` 与带 `web_search_result_location` 的文本块 | `groundingMetadata` |
| Code Execution | Markdown 代码与结果 | `code_interpreter_call` | 代码与结果文本 | `executableCode` / `codeExecutionResult` |
| 生成媒体 | 图片与视频为 Markdown data URI，音乐为 `message.audio` | 图片为 `image_generation_call`，视频与音乐为 Markdown data URI | Markdown data URI | `inlineData` |

OpenAI Chat 与 Completions 的 `n` 大于 1 时与 `/v1/images` 一样逐个生成：第一个 choice 沿用请求的会话，其余各自新开会话；choices 依次编号，流式输出依次写出各 choice 的块，每个 choice 有自己的结束块；`usage` 为各次生成之和。`n` 小于 1 时返回 400 `integer_below_min_value`。

### 自定义工具

只有一条纯文本用户消息时，适配器与官网一样直接发送原文；其余消息序列编码为 `messages` JSON。存在自定义工具时再追加工具文本契约。Responses 的 `namespace` 工具组展开为组内函数，模型返回的调用项回填所属 `namespace`。适配器解析模型返回的调用对象，再将客户端的 tool result 放入下一轮记录。该桥接运行在网页普通提示词层，工具可靠性取决于模型服从能力。

实际提示形态为：

```text
{"messages":[
  {"role":"system","content":"..."},
  {"role":"user","content":"..."},
  {"role":"assistant","tool_calls":[...]},
  {"role":"tool","tool_call_id":"...","content":"..."}
]}

工具协议：
可用工具：[CLIENT_TOOL_DEFINITION]
工具选择：auto
需要调用工具时，只输出一个 JSON 对象，格式必须为 {"tool_calls":[{"name":"工具名称","arguments":{}}]}
```

以上内容整体写入 Gemini Web 的普通提示词槽。`none` 使用对话与已有工具结果直接作答，提示中省略函数定义和调用格式，显式响应 JSON Schema 继续生效；`required`/`any` 要求调用任一声明工具，显式函数选择要求调用指定工具。支持 `previous_response_id` 或显式 conversation ID 的客户端会继续同一网页 session；无会话请求默认创建彼此独立的 Temporary Chat。

Google Search 与 Code Execution 由网页上游执行。Responses 的 `web_search_call` 根据 citations 输出 `completed` item；Code Execution 当前实测由 3.1 Pro 与 3.6 Flash 执行。

图片下载完成后，OpenAI Responses 返回：

```json
{
  "id": "ig_RESPONSE_ID_0",
  "type": "image_generation_call",
  "status": "completed",
  "result": "BASE64_IMAGE"
}
```

Responses 流式入口在根级图片状态 `1`、`4` 到达时实时发送 `response.image_generation_call.in_progress`、`generating`；媒体下载完成后发送 `completed` 与 `response.output_item.done`。

### 思考与签名

| 协议 | 输入控制 | 输出 |
| --- | --- | --- |
| OpenAI Chat | `reasoning_effort` | reasoning 增量 |
| OpenAI Responses | `reasoning.effort/summary` | reasoning item 生命周期 |
| Claude | `thinking`、`output_config.effort` | thinking block 与 signature |
| Gemini | `thinkingConfig` | thought part 与 `thoughtSignature` |

各协议的思考强度映射到网页模型菜单的三档，再按账号目录的可选档位取值（见“思考强度”）：

| 输入 | 网页强度 |
| --- | --- |
| 未指定、`none`、`minimal`、`low`、`auto`、Claude `disabled`、`thinkingBudget=0` | Low |
| `medium` | Medium |
| `high`、`xhigh`、`max`、Claude `enabled`/`adaptive` 未给 effort、`thinkingBudget>0` | High |

`none`、`disabled` 与 `thinkingBudget=0` 同时关闭思考摘要回传。

代理签名使用稳定的 Base64 数据满足客户端回放结构。Gemini Web 只公开思考摘要，完整隐藏思考与官方签名均不在网页响应中。

### Token usage

| 可计数内容 | 方法 |
| --- | --- |
| 文本输入 | 本地 Gemini tokenizer |
| 可见正文 | 本地 Gemini tokenizer |
| 可见思考摘要 | 本地 Gemini tokenizer |
| 图片、音频、视频、PDF | 计数接口按 Gemini API 规则估算，生成用量不计入 |
| 完整隐藏思考 | 网页端不可观测 |
| Google 内置工具内部上下文 | 网页端不可观测 |

本地计数使用内嵌 Gemini SentencePiece 模型：

```text
prompt_tokens = previous_session_context + 1 + tokenize(prompt)
completion_tokens = tokenize(visible_text) + sum(tokenize(code_content))
thought_tokens = tokenize(visible_thought_summary)
total_tokens = prompt_tokens + completion_tokens + thought_tokens
```

流式入口在首个非错误事件前发送只有 prompt tokens 的初始 usage，最终响应发送完整本地计数。

Anthropic `count_tokens`、Gemini `countTokens` 与 Responses `input_tokens` 在文本计数之外按 Gemini API 规则估算附件：两边都不超过 384 像素的图片 258，较大的图片按 768×768 分块每块 258；音频每秒 32；视频每秒 263；PDF 每页 258。时长取自 WAV、FLAC、MP4 文件头、Ogg Opus 末页位置与 MP3 首帧码率。Gemini `countTokens` 另按模态返回 `promptTokensDetails`，也接受包装在 `generateContentRequest` 中的请求。

### 错误投影

| HTTP | OpenAI `type` | Anthropic `type` | Gemini `status` |
| ---: | --- | --- | --- |
| 400 | `invalid_request_error` | `invalid_request_error` | `INVALID_ARGUMENT` |
| 401 | `invalid_request_error` | `authentication_error` | `UNAUTHENTICATED` |
| 403 | `invalid_request_error` | `permission_error` | `PERMISSION_DENIED` |
| 404 | `invalid_request_error` | `not_found_error` | `NOT_FOUND` |
| 413 | `invalid_request_error` | `request_too_large` | `INVALID_ARGUMENT` |
| 429 | `requests` | `rate_limit_error` | `RESOURCE_EXHAUSTED` |
| 499 | `server_error` | `api_error` | `CANCELLED` |
| 500、502 | `server_error` | `api_error` | `INTERNAL` |
| 503 | `server_error` | `overloaded_error` | `UNAVAILABLE` |
| 504 | `server_error` | `api_error` | `DEADLINE_EXCEEDED` |

管理端取消进行中的请求时，客户端收到 499，OpenAI `code` 为 `request_cancelled`，消息为 `request cancelled by the service administrator`；已收到的正文按本地计数记入用量。429 与 503 带 `Retry-After`，取值为请求模型最早结束冷却的账号剩余秒数，没有冷却中的账号时不写。错误正文去掉上游请求 URL、查询串（含 `f.sid`、`_reqid`）与账号 ID。Gemini 原生入口中官网没有的方法返回与官方相同的 404 `NOT_FOUND`；`streamGenerateContent` 不带 `alt=sse` 时与官方一样输出 JSON 数组。

### 附属端点

| 端点 | 实现 |
| --- | --- |
| `/v1/completions` | 每个 prompt 生成 `n` 个 choice，序号为 prompt 序号乘 `n` 加生成序号；token 数组与 token 数组的数组按 Gemini 分词器还原为文本，词表之外的 token 返回 400；带 `suffix` 时 system 消息要求补出两段之间的文本，user 消息为 `{"prompt","suffix"}` |
| Responses 保存 | `store` 不为 false 的响应与输入项写入 `data/requests.db` 的 `responses` 表；`input_items` 沿 `previous_response_id` 链列出全部前序输入与输出；`GET /v1/responses/{id}?stream=true` 等后台响应结束后按创建时的流式事件回放，失败的响应以 `response.failed` 结束，其余以 `response.completed` 结束，`starting_after` 之前的事件不输出 |
| Responses 后台 | `background: true` 先保存 `queued`，在独立的请求跟踪中生成；流式后台响应在生成期间保存为 `in_progress`；`cancel` 停止生成并保存 `cancelled`；启动时把上次运行未完成的后台响应保存为 `failed` |
| `/v1/responses/input_tokens` | 与生成相同的提示词编码加附件估算，续接时计入会话上下文；`input` 可省略，只带 `tools` 或 `conversation` 时同样计数 |
| `/v1/responses/compact` | 最近约 20000 tokens 的条目原样保留，更早的条目由所选模型写成结构化摘要；`compaction` 项的 `encrypted_content` 是摘要与原样条目的 Base64 JSON，之后的请求把它展开为摘要 user 消息与原样条目 |
| 文件 | OpenAI、Anthropic 与 Gemini 文件共用 `files` 表与 `data/files/`；Gemini 文件 48 小时后过期，`uri` 为本服务的 `/v1beta/files/<ID>`；生成请求读取文件内容作为附件上传，Gemini 生成与 `countTokens` 引用不存在的文件时与官方一样返回 403 `PERMISSION_DENIED`；Anthropic 列表按 `page` 游标分页，还有下一页时 `next_page` 为其游标 |
| 视频 | 视频模型的 `supportedGenerationMethods` 含 `predictLongRunning`；`/v1/videos` 与 `predictLongRunning` 立即返回任务，后台以 Create video 生成并把成品写入文件存储；OpenAI 视频对象的 `seconds` 为 `10`；长时操作的 `error` 为 `google.rpc.Status`；启动时把上次运行未完成的任务保存为失败 |
| 转写与翻译 | 音频作为附件交给所选模型，空文件返回 400；`verbose_json`、`srt`、`vtt` 要求模型按 JSON Schema 返回分段与时间戳，不符合时返回 502 `output_validation_error` 并附模型返回内容的开头；`stream` 输出 `transcript.text.delta` 与 `transcript.text.done`，`srt`、`vtt`、`verbose_json` 与官方 whisper-1 一样忽略 `stream` |
| 不提供的能力 | Embeddings、Moderations 与 Realtime 返回 OpenAI 404 `model_not_found`；Gemini `embedContent`、`predict` 等方法与 Live 返回 404 `NOT_FOUND`；错误说明写明 Gemini Web 不提供该能力 |

消息中的 YouTube 链接与官网一样作为正文发送，由官网的 YouTube 扩展读取视频；Chat `image_url`、Responses `input_file` 与 Gemini `fileData.fileUri` 中的 YouTube 链接同样写入正文。

会话登记（CID、RID、RCID、续接令牌、轮数、模型 mode、所属账号与上下文 token 数）写入 `data/requests.db` 的 `sessions` 表并按到期时间清理；启动时恢复未过期的会话，所属账号就绪后重新绑定，`previous_response_id` 与会话 ID 在 `SESSION_TTL` 内继续同一网页会话。

### 鉴权与跨域

公开接口接受 `Authorization: Bearer`、`x-api-key`、`x-goog-api-key` 或 `?key=`，`PROXY_API_KEY` 可用逗号分隔多个 key，任一来源与其中任一 key 相同即通过；管理端设置页按行编辑这些 key，保存时去掉首尾空白与空项，概览复制与 Playground 使用第一个 key。`/upload/v1beta/files` 的断点续传后续请求凭开始上传时 `X-Goog-Upload-URL` 中的 `upload_id` 授权，不需要 key，与官方续传地址一致。鉴权错误与各协议官方同形：

| 协议 | 未带 key | key 错误 |
| --- | --- | --- |
| Gemini（`/v1beta/`、`/upload/v1beta/`、`/ws/google.ai.generativelanguage.*`） | `403 PERMISSION_DENIED` | `400 INVALID_ARGUMENT`，`API_KEY_INVALID` |
| OpenAI | `401 invalid_request_error`，`code` 为 null | `401 invalid_request_error`，`invalid_api_key` |
| Anthropic（`/v1/messages`） | `401 authentication_error` | `401 authentication_error` |

`PROXY_API_KEY` 为空时不校验 key；此时 `Origin` 为 `null` 或非 localhost、非回环地址的 http/https 网页发起的请求按“未带 key”返回，不带 `Origin` 的客户端与其他 scheme 照常处理。公开接口的响应允许任意 origin，预检回显 `Access-Control-Request-Headers` 并允许 `GET`、`POST`、`PUT`、`DELETE`、`OPTIONS`，以 `Access-Control-Expose-Headers: *` 暴露响应头；预检不经过 key 校验。全部响应带 `X-Frame-Options: DENY`、`Content-Security-Policy: frame-ancestors 'none'`、`X-Content-Type-Options: nosniff` 与 `Referrer-Policy: no-referrer`。

## 7. 协议现状与更新

| 项目 | 当前结论 |
| --- | --- |
| Gemini 产品级 system prompt | 网页协议没有已确认的清除字段 |
| API system message | 编码为 `messages` 中的 `system` 记录，仍属于网页普通提示词 |
| 任意客户端工具声明 | 通过提示协议桥接 |
| 官网历史 | 默认 Temporary Chat；可配置保存 |
| 语言与地区 | Profile 语言进入 `Accept-Language`；`hl` 与 `inner[1]` 使用首页 `TuX5cc` 账号界面语言；权限仍由账号决定 |
| 前端更新 | 重新采集 Bootstrap、模型目录、账号偏好和最短生成流 |

### 已知与未知字段

| 状态 | 内容 |
| --- | --- |
| 已解码 | `otAQ7b` 关键模型槽、思考强度、账号能力号与工具选项组，`ESY5D` 账号偏好字段 25/100/145/222/261，`jSf9Qc` 用量，`hNvQHb` 会话轮次，会话、正文、思考、phase、根级图片与视频进度、代码、citations、生成图片视频音乐、generator tag、Canvas 文档、Deep Research 计划与研究卡片、模型元数据与帧错误 |
| 当前编码 | 生成 header、99 槽载荷、上传条款确认、断点续传上传、ProcessFile、输入框工具与选项、Deep Research 计划确认、Task 首次确认、`c8o8Fe` 原图下载、`XqA3Ic` 朗读、Temporary Chat 与会话三元组 |
| 仅检查存在 | `token_binding_directed_response` |
| 视为不透明 | `inner[3]` 页面令牌、上传路径 |
| 适配层合成 | 自定义工具调用、Responses `web_search_call` 与 `compaction` 项、Anthropic `server_tool_use` 与 `web_search_tool_result`、Gemini `groundingMetadata`、签名与本地 token usage |
| 尚未解码 | 官网搜索 query/lifecycle、Nano Banana Pro redo、隐藏思考、官方 thought signature、任意 function declaration 槽 |

### 原始证据覆盖

| 协议面 | 保存的逐字 raw | 当前可确定内容 | 明确缺口 |
| --- | --- | --- | --- |
| `/app` Bootstrap | 有 | `SNlM0e`、`cfb2h`、`FdrFJe`、`TuX5cc`、上传参数、上传条款有效期 | 页面其余前端状态未逐字段命名 |
| `otAQ7b` | 有 | `payload[0]` 默认 mode、`payload[15]` 模型行、`payload[24]` 思考强度 | 能力数字数组与其余顶层槽语义未知 |
| `ESY5D`、`L5adhe` | 有 | 偏好消息字段号布局、字段 25/100/145/222/261、写入载荷 | 其余字段语义未知 |
| `jSf9Qc` | 有 | tier、窗口 kind、ratio、seconds/nanos、overage bool | 未出现的 credits 行保持可选 |
| OAuthMultilogin | 无完整响应 | 两阶段状态、challenge、JWS、Cookie HPKE 流程已实网通过 | challenge/OK 完整 JSON 与 directed response 内部字段 |
| 附件上传 | 有 | 断点续传两步、上传路径、ProcessFile 文件 UUID | 上传路径与文件 UUID 的内部结构 |
| StreamGenerate | 有 | query、header、官网 raw 与服务 encoder 两套 99 槽 | 大量保留槽尚无语义名 |
| Thought | 有 | `candidate[37][0][0]` 累计快照 | 隐藏 thought 与官方签名不可观测 |
| Code Execution | 无完整上游帧 | marker 语法与协议投影已端到端通过 | 当次 upstream 原始帧 |
| Search | 有 | citations 位于 `candidate[2][1]` | query、开始、结束与 status 不在 raw 中 |
| Generated media | 有 | `payload[2]["7"]` 进度、`candidate[12][7]`、`"60"` 视频、`"87"` 音乐、尺寸、MIME、URL、placeholder、generator tag、`c8o8Fe` 原图 | Pro redo、Maximum size 原图 |
| Canvas、Deep Research | 有 | `candidate[30]` 文档、`"56"` 计划卡片、`"57"` 占位映射、`"58"` 研究卡片、`hNvQHb` 轮次 | 研究卡片状态枚举 |
| Task | 有 | featureMode 40、偏好 222、`UVb0T` | 需要用户输入的长任务 |
| Session/model/error | 有 | CID/RID/RCID、payload 39/42、`er[5]`、`wrb.fr[5]` BardErrorInfo、`e` | 1037 限额耗尽的原始帧 |

协议变化时按以下顺序定位：

1. **Bootstrap：** 检查 `SNlM0e`、`cfb2h`、`FdrFJe`、上传参数
2. **模型：** 检查 `otAQ7b` 行结构、hash、mode、默认 mode 与思考强度
3. **请求：** 对比 header 数组和 99 槽载荷
4. **响应：** 对比记录分隔、候选快照、phase 与模型元数据
5. **认证：** 检查响应 Cookie、登录跳转与 OAuthMultilogin
6. **回归：** 使用官方 SDK 或 Coding Agent 完成一次真实流式请求

原始请求包含账号和认证材料，只保存在本地实验目录。公开 Issue 提供脱敏后的数组形状、事件顺序、模型 ID 与错误状态。

欢迎复用请求编码器、帧解码器和协议适配器进行二次开发。若项目有帮助，请给仓库一个 Star。
