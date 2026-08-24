# Gemini Web 协议参考

Gemini-Web2API 复现 `gemini.google.com` 当前网页请求链，并将响应统一为内部事件后投影到 OpenAI、Claude 和 Gemini API。实现依据位于 `internal/gemini/`，协议入口位于 `internal/adapter/`。

| 项目 | 当前实现 |
| --- | --- |
| 网页入口 | `gemini.google.com/app` |
| 模型目录 | `otAQ7b` RPC 动态发现 |
| 生成接口 | `BardFrontendService/StreamGenerate` |
| 默认会话 | Temporary Chat |
| 运行依赖 | 完成 setup 后仅需 Go 服务进程 |
| 公开协议 | OpenAI Chat、OpenAI Responses、Claude Messages、Gemini GenerateContent |

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
| 调度 | `internal/balancer` | 模型能力、上下文窗口、冷却与会话粘连 |
| 适配 | `internal/adapter` | OpenAI、Claude 和 Gemini 请求与事件投影 |

**核心约束：** `internal/gemini` 只产生规范事件；各 API 适配器不直接解析网页帧。

## 2. Bootstrap 与模型目录

### 初始化链路

| 顺序 | 请求 | 读取内容 |
| ---: | --- | --- |
| 1 | `GET https://gemini.google.com/app` | `SNlM0e`、`bl`、`FdrFJe/f.sid`、响应 Cookie |
| 2 | `POST /_/BardChatUi/data/batchexecute?rpcids=otAQ7b` | 当前账号可见模型 |
| 3 | `POST ...batchexecute?rpcids=jSf9Qc&source-path=/usage` | 套餐、用量窗口、重置时间 |

### Bootstrap 原始字段

首页解析以下形态：

| 运行参数 | 接受的首页形态 |
| --- | --- |
| `SNlM0e` | `"SNlM0e":"..."` |
| `bl` | `"bl":"..."`、`data-bl="..."`、`boq_assistant-bard-web-server_*` |
| `f.sid` | `"FdrFJe":"..."`、`"f.sid":"..."` |

模型目录与 usage 使用同一种 BatchExecute 信封：

```json
[[["RPC_ID","[]",null,"generic"]]]
```

| RPC | `rpcids` | `source-path` | `x-goog-ext-73010989-jspb` |
| --- | --- | --- | --- |
| 模型目录 | `otAQ7b` | `/app` | `[]` |
| 官网用量 | `jSf9Qc` | `/usage` | `[0]` |

两者同时发送 `bl`、`f.sid`、`hl`、递增 `_reqid`、`rt=c`，表单发送 `f.req` 与 `at=SNlM0e`。共享扩展 header 为：

```json
[1,null,null,null,null,null,null,null,[4,5,6,8],null,null,null,null,null,null,null,"CLIENT_UUID"]
```

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
| `default` | 单账号官网默认聊天模型标记 |

公开模型 ID 由显示名称规范化得到，例如 `3.7 Flash` 生成 `gemini-3.7-flash`。解析同时接受公开 ID、显示名称和 hash。目录中不存在的模型返回 `404 model_not_found`。


模型行位于 `payload[15]`。解析器只赋予以下槽位语义，其他槽位保留为官网原始数据：

| 行槽位 | 解析结果 |
| ---: | --- |
| `0` | 模型 hash |
| `7` | 默认标记 A |
| `11` | 显示名称 |
| `12` | 描述 |
| `15` | 默认标记 B |
| `17` | 模型 mode |
| `19` | 显示名称回退 |

账号默认标记为 `bool(row[7]) || bool(row[15])`，单账号目录必须恰好得到一个默认模型。2026-08-24 的多账号原始目录均将 Flash 标为默认；Flash-Lite 与 Pro 均未标为默认。当前解析 fixture 为：

```json
[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,[
  ["8c46e95b1a07cecc","Flash-Lite","Fastest answers",[2,140],2,null,[],false,"",1,"Flash-Lite","3.5 Flash-Lite","Fastest answers",null,null,false,"",6,[],"3.5 Flash-Lite"],
  ["56fdd199312815e2","Flash","All-around help",[2,140],2,null,[],true,"Flash3p7",20,"Flash","3.7 Flash","All-around help",null,null,true,"Flash3p7",1,[],"3.7 Flash"],
  ["e6fa609c3fa255c0","Pro","Advanced reasoning",[2,140],2,null,[],false,"",4,"Pro","3.1 Pro","Advanced reasoning",null,null,false,"",3,[],"3.1 Pro"]
]]
```

该 fixture 验证数组结构，不表示线上目录持续不变。网关的全局 `default` 按当前 ready 账号的官网默认票数、模型覆盖账号数、公开 ID 字典序生成；它不表示该模型在全部账号中可用。


**真实模型校验：** 解码器在输出正文前读取响应中的模型 hash。请求模型与响应模型不一致时终止响应，防止权限变化导致静默降级。

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
    [VALUE, USED_RATIO, 1, [[UNIX_SECONDS, NANOS]], ...],
    [VALUE, USED_RATIO, 2, [[UNIX_SECONDS, NANOS]], ...],
    [REMAINING_CREDITS, null, 3, ...]
  ],
  USE_OVERAGE_AI_CREDITS
]
```

| 值 | 语义 |
| --- | --- |
| `kind=1` | 当前用量窗口 |
| `kind=2` | 周用量窗口 |
| `kind=3` | 剩余 AI credits |
| `tier=1` | Free |
| `tier=2` | Pro |
| `tier=3/6` | Ultra |
| `tier=4` | Plus |

空 `record[2]` 或缺失 `jSf9Qc` 记录按未登录处理。

## 3. 认证与账号身份

### 两种导入方式

| 方式 | 输入 | 可自动续签 | 适用场景 |
| --- | --- | --- | --- |
| Chrome setup | Chrome Profile | 是 | Windows 日常运行 |
| Cookie Header | 一行 `Cookie` 值 | 否 | 快速导入、异机短期运行 |

### Chrome DBSC 链路

```text
Local State ──► App-Bound 主密钥
Web Data   ──► v20 refresh token + wrapped binding key
Preferences ─► 账号语言
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
| `fingerprint` | Chrome 146 请求模板与语言 |
| `oauth` | Gaia ID、refresh token、wrapped binding key |

### Chrome 本地材料

| 来源 | 原始字段 |
| --- | --- |
| `Local State` | `profile.info_cache`、`os_crypt.app_bound_encrypted_key` |
| Profile `Web Data` | `SELECT service, encrypted_token, binding_key FROM token_service` |
| Profile `Preferences` | `intl.accept_languages`，回退 `intl.selected_languages` |

`token_service` 必须恰好有一行，`service` 形态为 `AccountId-GAIA_ID`。refresh token 密文形态为：

```text
"v20" || NONCE_12_BYTES || AES_256_GCM_CIPHERTEXT_AND_TAG
```

AES-GCM 使用 32 字节 App-Bound 主密钥和空 AAD。解密结果必须为 103 字节并以 `1//0` 开头。`app_bound_encrypted_key` 解码后的形态为：

```text
"APPB" || CHROME_IELEVATOR_CIPHERTEXT
```

实现启动隐藏的独立 Chrome 146，向该进程载入 helper，通过 Chrome `IElevator::DecryptData` 取得 32 字节主密钥。现有 Chrome 进程与 Profile 不参与自动化操作。

### OAuthMultilogin 实现合同

当前本地证据没有保存包含完整 OAuthMultilogin JSON 的逐字网络响应；以下结构来自已实网通过的实现合同。

```http
POST /oauth/multilogin?source=ChromiumAccountReconcilorDice&reuseCookies=0
Host: accounts.google.com
Authorization: MultiOAuth BASE64URL_PROTOBUF
Content-Type: application/x-www-form-urlencoded
User-Agent: Chrome/146
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
    "fingerprint": {
      "browser": "Chrome",
      "version": "146",
      "platform": "Windows",
      "user_agent": "...Chrome/146...",
      "language": "en-US,en;q=0.9",
      "tls_profile": "chrome_146"
    },
    "oauth": {
      "gaiaId": "GAIA_ID",
      "refreshToken": "1//0...",
      "wrappedBindingKey": "BASE64_BYTES"
    }
  }
}
```

Cookie Header 导入将普通 Cookie 绑定到 `.google.com`，将 `__Host-*` 绑定到 `gemini.google.com`，统一使用 `/` 与 Secure 属性，并要求 `SAPISID`、`__Secure-1PSID`。该方式没有 OAuth 续签材料。

服务吸收每次响应的 `Set-Cookie` 并原子写回。每条请求最多执行一次续签、重新初始化与原请求重试：

| 请求 | 自动续签条件 |
| --- | --- |
| Bootstrap | HTTP `401/403`；首页解析失败且正文包含 `accounts.google.com` |
| 模型目录 | HTTP `401/403` |
| Usage | HTTP `401/403`；登录页；空或缺失 `jSf9Qc` |
| StreamGenerate | HTTP `401/403`；首个公开事件前收到帧内 `401/403` |
| Upload、媒体下载 | 当前没有自动续签分支 |

**设备边界：** wrapped binding key 绑定原 Windows 用户密钥环境。异机复制的 Cookie 可使用至失效；自动续签需要在目标 Windows 设备重新 setup。

**出口边界：** setup、OAuthMultilogin、模型验证和日常请求使用同一账号代理。账号状态中的代理优先于全局 `PROXY`。

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
| Query | `hl` | 账号语言 |
| Query | `_reqid` | 进程内递增请求号 |
| Query | `rt=c` | 分块响应模式 |
| Form | `f.req` | 97 槽生成载荷 |
| Form | `at` | `SNlM0e` token |

### 关键 header

2026-08-24 的普通搜索与官网生图请求使用相同结构：

```json
x-goog-ext-525001261-jspb =
[1,null,null,null,"MODEL_HASH",null,null,1,[4,5,6,8,4,5,6,8],null,null,2,null,null,MODEL_MODE,THINKING_MODE,"CLIENT_UUID"]

x-goog-ext-525005358-jspb =
["REQUEST_UUID",1]

x-goog-ext-73010989-jspb = [0]
x-goog-ext-73010990-jspb = [0,0,0]
```

| Header | 数组位置 | 内容 |
| --- | ---: | --- |
| `x-goog-ext-525001261-jspb` | `4` | 模型 hash |
| 同上 | `7` | 2026-08-24 当前构建观测值 `1` |
| 同上 | `14` | 模型 mode |
| 同上 | `15` | 思考模式编号 |
| 同上 | `16` | 协议客户端 UUID |
| `x-goog-ext-525005358-jspb` | `0` | 本次生成 UUID |

`THINKING_MODE` 为标准 `1`、扩展 `2`。每个账号的 `gemini.Client` 在生命周期内保持自己的客户端 UUID；生成 UUID 每次请求重新创建，并同时写入 `f.req[59]`。

### `f.req` 关键槽位

| 槽位 | 内容 |
| ---: | --- |
| `0` | 提示词、附件 URL 与文件名 |
| `1` | 账号语言 |
| `2` | `CID`、`RID`、`RCID` 会话三元组 |
| `6` | 普通会话 `[0]`；Temporary Chat `[1]` |
| `17` | 当前固定值 `[[0]]` |
| `45` | Temporary Chat 标记 `1` |
| `49` | 当前官网生成请求与最小编码器均为 `null` |
| `59` | 本次生成 UUID |
| `61` | 当前固定空数组 |
| `68` | 普通会话 `1`；Temporary Chat `2` |
| `79` | 模型 mode |
| `80` | 思考模式编号 |
| `96` | 普通会话 `1`；Temporary Chat `0` |

`GEMINI_SAVE_HISTORY=false` 使用 Temporary Chat。显式网页 session 保存三元组并固定到产生它的账号；无 session 请求相互独立。

### 完整 `f.req` 编码

表单字段是二层 JSON：

```javascript
fReq = JSON.stringify([
  null,
  JSON.stringify(inner97)
])
```

附件形态为：

```javascript
FILES = [
  [["UPLOAD_ID", 1], "FILE_NAME"]
]
```

当前最小可用编码器的完整 97 槽形态如下：

```jsonc
[
  /* 00 */ [PROMPT,0,null,FILES_OR_NULL,null,null,0],
  /* 01 */ [HL],
  /* 02 */ [CID,RID,RCID,null,null,null,null,null,null,""],
  /* 03 */ null,
  /* 04 */ null,
  /* 05 */ null,
  /* 06 */ TEMPORARY_CHAT ? [1] : [0],
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
  /* 17 */ [[0]],
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
  /* 30 */ [4],
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
  /* 41 */ [1],
  /* 42 */ null,
  /* 43 */ null,
  /* 44 */ null,
  /* 45 */ TEMPORARY_CHAT ? 1 : null,
  /* 46 */ null,
  /* 47 */ null,
  /* 48 */ null,
  /* 49 */ null,
  /* 50 */ null,
  /* 51 */ null,
  /* 52 */ null,
  /* 53 */ 0,
  /* 54 */ null,
  /* 55 */ null,
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
  /* 67 */ TEMPORARY_CHAT ? 0 : null,
  /* 68 */ TEMPORARY_CHAT ? 2 : 1,
  /* 69 */ null,
  /* 70 */ null,
  /* 71 */ null,
  /* 72 */ null,
  /* 73 */ null,
  /* 74 */ null,
  /* 75 */ null,
  /* 76 */ null,
  /* 77 */ null,
  /* 78 */ null,
  /* 79 */ MODEL_MODE,
  /* 80 */ THINKING_MODE,
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
  /* 91 */ 0,
  /* 92 */ null,
  /* 93 */ null,
  /* 94 */ null,
  /* 95 */ null,
  /* 96 */ TEMPORARY_CHAT ? 0 : 1
]
```

官网保存样本还在 `inner[3]` 携带 1531 字符的不透明页面令牌，在 `inner[4]` 携带 32 字符上下文 ID。该 Temporary Chat 样本的非 `null` 槽完整集合为：

```text
0,1,2,3,4,6,7,10,11,17,18,27,30,41,45,53,59,61,67,68,79,80,91,96
```

当前最小 encoder 的集合只删除 `3,4`，纯协议重放仍能成功；因此两槽属于官网 capture 原始值，不属于当前最小必需字段。2026-08-22 的普通文本 capture 曾在生成 header slot 7 使用 `0`，2026-08-24 的搜索与图片 capture 使用 `1`，该槽只记录观测值，不赋予图片开关语义。

### 附件上传

```http
POST https://content-push.googleapis.com/upload
Push-ID: feeds/mcudyrk2a4khkz
Content-Type: multipart/form-data; boundary=...
Origin: https://gemini.google.com

--boundary
Content-Disposition: form-data; name="file"; filename="FILE_NAME"

RAW_FILE_BYTES
--boundary--
```

HTTP 200 响应正文整体作为不透明 `UPLOAD_ID` 写入 `f.req[0][3][*][0][0]`。当前本地证据没有保存完整 multipart 逐字抓包；该结构来自已实网通过的实现合同。

### 图片生成

`otAQ7b` 只列出聊天协调模型，没有独立图片行。官网首发生图请求与同日普通搜索请求使用相同 header 和相同 97 槽结构，`inner[49]` 均为 `null`；已观察到的差异只有提示词、会话令牌和请求 UUID。官网在请求前调用过图片额度 RPC `ku4Jyf`，其中出现类别 `14`，但当前纯协议生成不需要该预请求。

成功图片响应同时披露三层身份：

| 身份 | 原始位置 | 2026-08-24 实测值 |
| --- | --- | --- |
| 聊天协调模型 hash | `payload[39]` | `56fdd199312815e2` |
| 图片产品名称 | `payload[42]` | `Nano Banana 2` |
| 内部生成器 tag | `candidate[12][7][0][0][3][18]` | `imagen_default.ultra` |

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

网关公开 `gemini-3.1-flash-image` 作为图片能力 ID，内部优先选择可用的官网默认非 Lite Flash，其次选择账号覆盖最广的可用 Flash，并允许已实测成功的 Flash-Lite 作为协调模型。请求提示词会加入明确的生成或编辑语义。该 ID 支持对话入口、Gemini `generateContent` 与 `/v1/images/generations`、`/v1/images/edits`。现场经 3.7 Flash 与 3.5 Flash-Lite 协调时得到 `imagen_default.ultra`，经 3.6 Flash 协调时得到 `imagen_default`；OpenAI Chat/Responses 通过 `provider_model` 返回实际 generator tag。当前只公开一个首发生图能力 ID。`Redo with Pro` 在前端资源中存在，尚未取得其二段请求与响应，不公开 Pro 图片 ID。

图片生成期间还会先出现根级稀疏进度：

```text
payload[2]["7"] = [null,["data_analysis_tool",[null,null,"Creating your image",""],STATUS,[null,[null,null,null,null,null,null,null,[20]],false]]]
```

类别 `20` 标识图片任务；已由同一抓包及前端状态判断验证 `STATUS=1` 为进行中、`STATUS=4` 为工具成功。媒体 URL 在后续 candidate 帧才出现，因此状态 `4` 仍不能视为图片数据已经可用。

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

模型 hash 必须在正文、思考、代码、引用、媒体或 phase 事件之前出现。hash 与请求模型不一致、内容先于 hash、终止前始终没有 hash，均产生可重试 `502`。

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

事件类型只接受 `code_reference`、`code_stdout`、`code_stderr`。关闭围栏尚未出现时 `complete=false`。代码段会从正文移除并记录其可见文本 rune offset。当前本地证据已保存真实投影结果，尚未保存含这些 marker 的完整官网逐字响应。

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

当前源码没有解析官网搜索 query、开始、结束或状态。OpenAI Responses 的 `web_search_call` 根据 citations 在适配层合成。

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
https://lh3.googleusercontent.com/gg-dl/OPAQUE
  -> append =s1024-rj when absent
  -> add ?alr=yes
  -> optional text/plain relay URL
  -> https://*.usercontent.google.com/rd-gg-dl/*
  -> binary response
```

下载最多跟随五次受控重定向。

### 规范事件

| 事件 | 内容 |
| --- | --- |
| `session` | CID、RID、RCID |
| `text` | 正文累计快照变化 |
| `thought` | 可见思考摘要累计快照变化 |
| `code` | `code_reference`、stdout、stderr |
| `citations` | URL、标题、引用范围与来源信息 |
| `media` | 生成图片及其 MIME、尺寸与 generator tag |
| `image_progress` | 根级图片任务状态 `1`、`4` |
| `phase` | generating / complete |
| `metadata` | 标题、控制 token、模型 hash 与名称 |
| `error` | HTTP 或帧内协议错误 |
| `done` | 当前生产路径为 stop / error |

正文与思考采用累计快照。解码器计算 `append`、`replace`、`truncate`，候选项之间保持独立状态。思考先于正文投影；正文发生后续改写时，适配器缓冲最终快照，维持协议合法顺序。

### 错误与重试

| 情况 | 行为 |
| --- | --- |
| 请求模型与响应模型不一致 | 终止并返回上游错误 |
| StreamGenerate HTTP 或帧内 `401/403` | 首个公开事件前续签并重试一次 |
| transport、空终止帧、可重试协议错误 | 首个可见输出前切换同能力账号 |
| 新会话首次失败 | 换账号后重新建立会话 |
| 已建立显式会话失败 | 保持账号粘连并返回错误 |
| 客户端参数错误 | 返回 `4xx`，账号保持健康 |

## 6. API 投影

| 内部事件 | OpenAI Chat | OpenAI Responses | Claude Messages | Gemini API |
| --- | --- | --- | --- | --- |
| `text` | `delta.content` | `output_text.delta` | `text_delta` | `parts.text` |
| `thought` | reasoning 字段 | `reasoning` item | `thinking` block | `thought: true` |
| 自定义函数 | `tool_calls` | `function_call` | `tool_use` | `functionCall` |
| Google Search | 正文引用 | `web_search_call` | 引用文本 | `citationMetadata` |
| Code Execution | Markdown 代码与结果 | `code_interpreter_call` | 代码与结果文本 | `executableCode` / `codeExecutionResult` |
| 生成媒体 | Markdown data URI | `image_generation_call` | Markdown data URI | `inlineData` |

### 自定义工具

Gemini Web 当前没有已确认的任意 function declaration 载荷槽。适配器把各公开协议的消息序列编码为 `messages` JSON，并在存在自定义工具时追加工具文本契约。适配器解析模型返回的调用对象，再将客户端的 tool result 放入下一轮记录。该桥接运行在网页普通提示词层，工具可靠性取决于模型服从能力。

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

以上内容整体写入 Gemini Web 的普通提示词槽。`none` 禁止工具调用，`required`/`any` 要求调用任一声明工具，显式函数选择要求调用指定工具。支持 `previous_response_id` 或显式 conversation ID 的客户端会继续同一网页 session；无会话请求默认创建彼此独立的 Temporary Chat。

Google Search 与 Code Execution 是网页上游已经执行的内置能力。Responses 的 `web_search_call` 由 citations 合成 completed item；它不冒充官网原始搜索生命周期。Code Execution 当前实测由 3.1 Pro 与 3.6 Flash 执行。

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

网页协议当前只有标准与增强两档。`none`、`disabled` 和 `thinkingBudget=0` 使用标准档并关闭思考摘要回传。

代理签名使用稳定的 Base64 数据满足客户端回放结构。Gemini Web 只公开思考摘要，完整隐藏思考与官方签名均不在网页响应中。

### Token usage

| 可计数内容 | 方法 |
| --- | --- |
| 文本输入 | 本地 Gemini tokenizer |
| 可见正文 | 本地 Gemini tokenizer |
| 可见思考摘要 | 本地 Gemini tokenizer |
| 图片、音频、视频、PDF | 网页端无权威计数 |
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

## 7. 边界与协议更新

| 边界 | 当前结论 |
| --- | --- |
| Gemini 产品级 system prompt | 网页协议没有已确认的清除字段 |
| API system message | 编码为 `messages` 中的 `system` 记录，仍属于网页普通提示词 |
| 任意客户端工具声明 | 通过提示协议桥接 |
| 官网历史 | 默认 Temporary Chat；可配置保存 |
| 语言与地区 | Profile 语言进入 header、query 与 payload；权限仍由账号决定 |
| 前端更新 | 重新采集 Bootstrap、模型目录和最短生成流 |

### 已知与未知字段

| 状态 | 内容 |
| --- | --- |
| 已解码 | `otAQ7b` 关键模型槽、`jSf9Qc` 用量、会话、正文、思考、phase、根级图片进度、代码、citations、生成媒体、generator tag、模型元数据与帧错误 |
| 当前编码 | 生成 header、最小 97 槽载荷、附件上传、Temporary Chat 与会话三元组 |
| 仅检查存在 | `token_binding_directed_response` |
| 视为不透明 | `inner[3]` 页面令牌、`inner[4]` 上下文 ID、上传 HTTP 200 响应正文 |
| 适配层合成 | 自定义工具调用、Responses `web_search_call`、签名与本地 token usage |
| 尚未解码 | 官网搜索 query/lifecycle、Nano Banana Pro redo、隐藏思考、官方 thought signature、任意 function declaration 槽 |

### 原始证据边界

| 协议面 | 保存的逐字 raw | 当前可确定内容 | 明确缺口 |
| --- | --- | --- | --- |
| `/app` Bootstrap | 有 | `SNlM0e`、`FdrFJe/f.sid`、build label | 页面其余前端状态未逐字段命名 |
| `otAQ7b` | 有 | `payload[15]`、20 槽模型行、hash、display、description、两个 default bool、mode | 能力数字数组与其余顶层槽语义未知 |
| `jSf9Qc` | 有 | tier、窗口 kind、ratio、seconds/nanos、overage bool | 未出现的 credits 行保持可选 |
| OAuthMultilogin | 无完整响应 | 两阶段状态、challenge、JWS、Cookie HPKE 合同已实网通过 | challenge/OK 完整 JSON 与 directed response 内部字段 |
| Multipart upload | 无完整请求 | endpoint、Push-ID、multipart `file`、opaque upload ID 已实网通过 | boundary、逐字响应与服务端 ID 结构 |
| StreamGenerate | 有 | query、header、官网 raw 与最小 encoder 两套 97 槽 | 大量保留槽尚无语义名 |
| Thought | 有 | `candidate[37][0][0]` 累计快照 | 隐藏 thought 与官方签名不可观测 |
| Code Execution | 无完整上游帧 | marker 语法与协议投影已端到端通过 | 当次 upstream 原始帧 |
| Search | 有 | citations 位于 `candidate[2][1]` | query、开始、结束与 status 不在 raw 中 |
| Generated media | 有 | `payload[2]["7"]` 进度、`candidate[12][7]`、尺寸、MIME、URL、placeholder、generator tag | Pro redo 与独立 Lite generator |
| Session/model/error | 有 | CID/RID/RCID、payload 39/42、`er[5]`、`e` | 限额耗尽专用字段 |

协议变化时按以下顺序定位：

1. **Bootstrap：** 检查 `SNlM0e`、`bl`、`f.sid`
2. **模型：** 检查 `otAQ7b` 行结构、hash、mode、默认标记
3. **请求：** 对比 header 数组和 97 槽载荷
4. **响应：** 对比记录边界、候选快照、phase 与模型元数据
5. **认证：** 检查响应 Cookie、登录跳转与 OAuthMultilogin
6. **回归：** 使用官方 SDK 或 Coding Agent 完成一次真实流式请求

原始请求包含账号和认证材料，只保存在本地实验目录。公开 Issue 提供脱敏后的数组形状、事件顺序、模型 ID 与错误状态。

欢迎复用请求编码器、帧解码器和协议适配器进行二次开发。若项目有帮助，请给仓库一个 Star。
