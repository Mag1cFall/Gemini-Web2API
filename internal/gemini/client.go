package gemini

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"golang.org/x/sync/semaphore"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

const (
	geminiOrigin = "https://gemini.google.com"
	pathInit     = "/app"
	pathBatch    = "/_/BardChatUi/data/batchexecute"
	pathGenerate = "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
)

// Client 表示与单一账号身份绑定的 Gemini Web 协议客户端
type Client struct {
	httpClient  tls_client.HttpClient
	accountID   string
	language    string
	clientID    string
	saveHistory bool
	// guest 表示未登录的访客会话
	guest       bool
	reqID       atomic.Int64
	contextSize atomic.Int64
	requestSlot *semaphore.Weighted
	// concurrency 是请求槽数量
	concurrency int
	busy        atomic.Int32
	usage       atomic.Pointer[UsageInfo]
	// authUser 是目标账号在多账号 Cookie 中的序号，pathPrefix 是它在官网路径中的 /u/N 前缀，默认账号为 0 与空
	authUser   int
	pathPrefix string

	bootstrapMu sync.RWMutex
	bootstrap   *Bootstrap

	cookieMu    sync.Mutex
	cookies     map[string]Cookie
	save        func([]Cookie) error
	refresh     func(context.Context) ([]Cookie, error)
	refreshSlot *semaphore.Weighted
}

// ContextWindow 返回账号套餐对应的网页输入窗口
func (c *Client) ContextWindow() int {
	return int(c.contextSize.Load())
}

// Busy 报告账号的请求槽是否已全部占用
func (c *Client) Busy() bool {
	return int(c.busy.Load()) >= c.concurrency
}

// Pending 返回占用或等待账号请求槽的请求数
func (c *Client) Pending() int {
	return int(c.busy.Load())
}

// CachedUsage 返回最近一次读取的官网用量，尚未读取时为 nil
func (c *Client) CachedUsage() *UsageInfo {
	return c.usage.Load()
}

// AcquireRequest 占用账号的一个请求槽直到完整上游请求链结束，后台生成的轮询阶段让出
func (c *Client) AcquireRequest(ctx context.Context) (func(), error) {
	c.busy.Add(1)
	if err := c.requestSlot.Acquire(ctx, 1); err != nil {
		c.busy.Add(-1)
		return nil, err
	}
	return func() {
		c.requestSlot.Release(1)
		c.busy.Add(-1)
	}, nil
}

// NewClient 创建固定 Cookie、代理和语言的账号客户端
func NewClient(source AccountSource, proxyURL string, saveHistory bool) (*Client, error) {
	language := strings.TrimSpace(source.Language)
	if language == "" {
		language = defaultLanguage
	}
	concurrency := max(source.Concurrency, 1)
	httpClient, err := NewHTTPClient(requestCeilingSeconds, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}
	clientID, err := newProtocolID()
	if err != nil {
		return nil, err
	}
	client := &Client{
		httpClient:  httpClient,
		accountID:   source.ID,
		language:    language,
		clientID:    clientID,
		saveHistory: saveHistory,
		requestSlot: semaphore.NewWeighted(int64(concurrency)),
		concurrency: concurrency,
		refreshSlot: semaphore.NewWeighted(1),
		cookies:     make(map[string]Cookie, len(source.Cookies)),
		save:        source.Save,
		refresh:     source.Refresh,
	}
	if source.AuthUser > 0 {
		client.authUser = source.AuthUser
		client.pathPrefix = fmt.Sprintf("/u/%d", source.AuthUser)
	}
	reqID, err := initialReqID()
	if err != nil {
		return nil, err
	}
	client.reqID.Store(reqID)
	if err := client.replaceCookies(source.Cookies); err != nil {
		return nil, err
	}
	return client, nil
}

// GuestID 是访客会话在账号池、日志与账本中的 ID
const GuestID = "guest"

// ErrGuestAttachments 表示带附件的请求落到访客会话，官网访客页面的上传入口全部禁用
var ErrGuestAttachments = &ProtocolError{HTTPStatus: 503, Message: "访客会话不能上传附件，需要可用的登录账号"}

// guestContextWindow 是访客会话的网页输入窗口，与免费层相同
const guestContextWindow = 32 * 1024

// NewGuestClient 创建以未登录会话访问官网的访客客户端
func NewGuestClient(proxyURL string, concurrency int) (*Client, error) {
	client, err := NewClient(AccountSource{ID: GuestID, Concurrency: concurrency}, proxyURL, false)
	if err != nil {
		return nil, err
	}
	client.guest = true
	client.contextSize.Store(guestContextWindow)
	return client, nil
}

// Guest 报告客户端是否为未登录的访客会话
func (c *Client) Guest() bool {
	return c.guest
}

// features 返回当前会话类型的页面在扩展头第 8 槽声明的功能编号
func (c *Client) features() []int {
	if c.guest {
		return guestClientFeatures
	}
	return clientFeatures
}

// Init 刷新首页动态参数并获取当前账号模型目录与偏好
func (c *Client) Init(ctx context.Context) error {
	return c.init(ctx, true)
}

func (c *Client) init(ctx context.Context, allowRefresh bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.pageURL(pathInit), nil)
	if err != nil {
		return err
	}
	c.applyNavigationHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return transportProtocolError("bootstrap request", err)
	}
	if resp.StatusCode != http.StatusOK {
		protocolErr := responseStatusError(resp, "bootstrap")
		if allowRefresh && isAuthenticationError(protocolErr) && c.refresh != nil {
			resp.Body.Close()
			if err := c.refreshCookies(ctx); err != nil {
				return err
			}
			return c.init(ctx, false)
		}
		resp.Body.Close()
		return protocolErr
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return transportProtocolError("read bootstrap page", err)
	}
	bootstrap, err := parseBootstrap(string(body))
	if err == nil && bootstrap.SNlM0e == "" && !c.guest {
		err = errBootstrapSignedOut
	}
	if err != nil {
		if errors.Is(err, errBootstrapSignedOut) {
			if allowRefresh && c.refresh != nil {
				if err := c.refreshCookies(ctx); err != nil {
					return err
				}
				return c.init(ctx, false)
			}
			return retryableProtocolError(ErrSignedOut.Error(), ErrSignedOut)
		}
		return retryableProtocolError(fmt.Sprintf("bootstrap: %v", err), err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		return err
	}
	catalog, err := c.fetchModelCatalog(ctx, bootstrap)
	var preferences Preferences
	if err == nil && !c.guest {
		preferences, err = c.fetchPreferences(ctx, bootstrap)
	}
	if err != nil {
		if allowRefresh && isAuthenticationError(err) && c.refresh != nil {
			if err := c.refreshCookies(ctx); err != nil {
				return err
			}
			return c.init(ctx, false)
		}
		return retryableProtocolError(fmt.Sprintf("initialize models and preferences: %v", err), err)
	}
	if c.guest {
		catalog.removeTools()
	} else if gems, err := c.fetchGems(ctx, bootstrap); err != nil {
		logging.Warn(c.displayAccountID(), "读取 Gem 列表失败", "原因", err)
	} else {
		catalog.addGems(gems)
	}
	bootstrap.Catalog = catalog
	bootstrap.Preferences = preferences
	c.bootstrapMu.Lock()
	c.bootstrap = &bootstrap
	c.bootstrapMu.Unlock()
	return nil
}

// Stream 执行一次生成并只输出规范事件，Deep Research 按官网计划、确认与报告流程执行
func (c *Client) Stream(ctx context.Context, request GenerateRequest, emit func(Event) error) error {
	if bootstrap, err := c.bootstrapSnapshot(); err == nil {
		if model, err := c.resolveRequestModel(bootstrap.Catalog, request.Model); err == nil && model.Feature == FeatureDeepResearch {
			return c.deepResearch(ctx, request, model, emit)
		}
	}
	return c.stream(ctx, request, emit, true)
}

func (c *Client) stream(ctx context.Context, request GenerateRequest, emit func(Event) error, allowRefresh bool) error {
	started := time.Now()
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return err
	}
	model, err := c.resolveRequestModel(bootstrap.Catalog, request.Model)
	if err != nil {
		return err
	}
	request.ModelMode = model.Mode
	request.ThinkingLevel = model.thinkingLevel(request.ThinkingLevel)
	if model.Feature != 0 {
		request.Feature = model.Feature
	}
	request.Gem = model.Gem
	request.tools = bootstrap.Catalog.toolSelection(request.Feature, request.AspectRatio)
	request.personalIntelligence = bootstrap.Catalog.personalIntelligence
	temporaryChat := !c.guest && !c.saveHistory && !request.Feature.savedChatOnly()
	taskTerms := false
	if request.Feature == FeatureTask {
		if taskTerms, err = c.onboardTask(ctx, bootstrap, model.Mode, request.ThinkingLevel); err != nil {
			return err
		}
	}
	if c.guest && len(request.Files) > 0 {
		return ErrGuestAttachments
	}
	trace := logging.FromContext(ctx)
	request.Files = append([]FileData(nil), request.Files...)
	uploads, uploadBytes := 0, 0
	for _, file := range request.Files {
		if file.URL == "" {
			uploads++
			uploadBytes += len(file.Data)
		}
	}
	uploadStarted := time.Now()
	if uploads > 0 {
		trace.SetStage("接受上传条款")
		if err := c.acceptUploadTerms(ctx, bootstrap, model.Mode, request.ThinkingLevel); err != nil {
			return err
		}
		trace.SetStage("上传附件")
	}
	for index := range request.Files {
		file := &request.Files[index]
		if file.URL == "" {
			if *file, err = c.uploadFile(ctx, bootstrap, *file); err != nil {
				return err
			}
		}
		if file.FileID == "" {
			if file.FileID, err = c.processFile(ctx, bootstrap, model, request.ThinkingLevel, *file); err != nil {
				return err
			}
		}
	}
	if uploads > 0 {
		trace.Stage("附件上传完成", "数量", uploads, "大小", formatBytes(uploadBytes), "耗时", time.Since(uploadStarted))
	}
	streamRequestID, err := newProtocolID()
	if err != nil {
		return err
	}
	contextID, err := newContextID()
	if err != nil {
		return err
	}
	previousMode := request.Conversation.Snapshot().ModelMode
	if previousMode == 0 {
		previousMode = selectedModelMode(bootstrap.Catalog, bootstrap.Preferences)
	}
	payload, err := buildGeneratePayload(request, bootstrap.Language, bootstrap.Preferences, streamRequestID, contextID, temporaryChat, model.Mode != previousMode)
	if err != nil {
		return err
	}
	streamCtx, stopWatch := watchStreamIdle(ctx)
	defer stopWatch()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, c.pageURL(pathGenerate), strings.NewReader(protocolForm(payload, bootstrap.SNlM0e)))
	if err != nil {
		return err
	}
	reqID := c.nextReqID()
	query := req.URL.Query()
	query.Set("bl", bootstrap.BL)
	query.Set("f.sid", bootstrap.FSID)
	query.Set("hl", bootstrap.Language)
	query.Set("_reqid", fmt.Sprintf("%d", reqID))
	query.Set("rt", "c")
	req.URL.RawQuery = query.Encode()
	c.applyXHRHeaders(req)
	chatMode := any(0)
	if temporaryChat {
		chatMode = 1
	}
	if request.researchConfirm {
		chatMode = nil
	}
	modelHeader, err := buildModelHeader(c.features(), model, chatMode, request.ThinkingLevel, c.clientID, bootstrap.BL, requestTiming(time.Since(started), time.Now()))
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-ext-525001261-jspb", modelHeader)
	requestHeader, _ := json.Marshal([]any{streamRequestID, 1})
	req.Header.Set("x-goog-ext-525005358-jspb", string(requestHeader))
	req.Header.Set("x-goog-ext-73010989-jspb", "[0]")
	req.Header.Set("x-goog-ext-73010990-jspb", "[0,0,0]")

	trace.SetStage("等待上游响应头")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if idle := streamIdleError(streamCtx); idle != nil {
			return idle
		}
		return transportProtocolError("generate request", err)
	}
	progress(streamCtx)
	trace.UpstreamResponse(resp.StatusCode)
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		protocolErr := responseStatusError(resp, "generate")
		if allowRefresh && isAuthenticationError(protocolErr) && c.refresh != nil {
			resp.Body.Close()
			if err := c.refreshCookies(ctx); err != nil {
				return err
			}
			if err := c.init(ctx, false); err != nil {
				return err
			}
			return c.stream(ctx, request, emit, false)
		}
		if err := emit(Event{Kind: EventError, Err: protocolErr, FinishReason: FinishError}); err != nil {
			return err
		}
		return protocolErr
	}
	if taskTerms {
		if err := c.acceptTaskTerms(ctx, bootstrap, model.Mode, request.ThinkingLevel); err != nil {
			return err
		}
	}
	next := request.Conversation.Snapshot()
	next.Turns++
	decodeState := NewConversationStateFrom(next)
	withDownload := func(event Event) error {
		if event.Media != nil && event.Media.download != nil {
			event.Media.download.mode, event.Media.download.level, event.Media.download.temporaryChat = model.Mode, request.ThinkingLevel, temporaryChat
		}
		return emit(event)
	}
	guard := newModelGuard(model, newFailureTextGuard(withDownload).Emit)
	decoder := NewFrameDecoder()
	decoder.heartbeat = trace.UpstreamHeartbeat
	decoder.awaitIncomplete = request.researchPlan != nil
	err = decoder.Decode(tracedReader{reader: resp.Body, trace: trace}, decodeState, func(event Event) error {
		progress(streamCtx)
		return guard.Emit(event)
	})
	if idle := streamIdleError(streamCtx); idle != nil && ctx.Err() == nil {
		snapshot := decodeState.Snapshot()
		c.stopGeneration(bootstrap, snapshot.CID, snapshot.RID, decoder.metadata.ControlToken, model.Mode, request.ThinkingLevel)
		return idle
	}
	if ctx.Err() != nil {
		snapshot := decodeState.Snapshot()
		c.stopGeneration(bootstrap, snapshot.CID, snapshot.RID, decoder.metadata.ControlToken, model.Mode, request.ThinkingLevel)
		return ctx.Err()
	}
	if request.researchPlan != nil {
		*request.researchPlan = decoder.researchConfirm
	}
	if err == nil && decoder.background && !request.researchConfirm {
		err = c.awaitTurn(ctx, decodeState, model, request.ThinkingLevel, nil, request.researchPlan, guard.Emit)
	}
	if err != nil {
		if allowRefresh && !guard.Emitted() && isAuthenticationError(err) && c.refresh != nil {
			if err := c.refreshCookies(ctx); err != nil {
				return err
			}
			if err := c.init(ctx, false); err != nil {
				return err
			}
			return c.stream(ctx, request, emit, false)
		}
		return err
	}
	if err := guard.Complete(); err != nil {
		return err
	}
	if request.Conversation != nil {
		snapshot := decodeState.Snapshot()
		snapshot.ModelMode = model.Mode
		request.Conversation.Update(snapshot)
	}
	return nil
}

// stopTimeout 是客户端取消后通知上游停止生成的最长等待
const stopTimeout = 10 * time.Second

// stopGeneration 在客户端取消后发送官网“停止回答”的 NkpXw 请求，参数为会话、回答标识与首帧的控制令牌
func (c *Client) stopGeneration(bootstrap Bootstrap, cid string, rid string, token string, mode int, level ThinkingLevel) {
	if cid == "" || rid == "" || token == "" {
		return
	}
	payload, err := json.Marshal([]any{[]any{cid, rid}, []any{token}})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	started := time.Now()
	_, err = c.batchExecute(ctx, bootstrap, batchRequest{
		rpcID: "NkpXw", payload: string(payload), sourcePath: "/app", language: bootstrap.Language, extension: "[0]", mode: mode, level: level,
	})
	if err != nil {
		logging.Warn(c.displayAccountID(), "通知上游停止生成失败", "原因", err)
		return
	}
	logging.Info(c.displayAccountID(), "已通知上游停止生成", "耗时", time.Since(started))
}

// Models 返回当前账号初始化得到的模型目录
func (c *Client) Models() []Model {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return nil
	}
	return bootstrap.Catalog.List()
}

// Email 返回首页 oPEP7c 给出的当前登录邮箱，访客会话与尚未初始化时为空
func (c *Client) Email() string {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return ""
	}
	return bootstrap.Email
}

// ResolveModel 在当前账号模型目录中解析公开 ID
func (c *Client) ResolveModel(id string) (Model, error) {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return Model{}, err
	}
	return bootstrap.Catalog.Resolve(id)
}

func (c *Client) fetchModelCatalog(ctx context.Context, bootstrap Bootstrap) (ModelCatalog, error) {
	payload, err := c.batchExecute(ctx, bootstrap, batchRequest{rpcID: "otAQ7b", payload: "[1]", sourcePath: "/app", language: catalogLanguage, extension: "[]"})
	if err != nil {
		return ModelCatalog{}, err
	}
	return parseModelCatalog(payload)
}

// batchRequest 是一次 batchexecute RPC 的参数，mode 与 level 为页面当前选中的模型 mode 与思考强度，页面加载阶段为零
type batchRequest struct {
	rpcID      string
	payload    string
	sourcePath string
	language   string
	extension  string
	mode       int
	level      ThinkingLevel
	// conversation 表示会话内请求，请求头第 7 槽写 Temporary Chat 状态，功能编号与生成请求相同重复两遍
	conversation  bool
	temporaryChat bool
	// untimed 表示请求头计时槽的耗时为空数组，页面发送 UVb0T 时如此
	untimed bool
}

// batchExecute 以页面 XHR 发送单个 batchexecute RPC 并返回其 payload
func (c *Client) batchExecute(ctx context.Context, bootstrap Bootstrap, call batchRequest) ([]any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	started := time.Now()
	rpcID := call.rpcID
	encodedRequest, err := pageJSON([]any{[]any{[]any{rpcID, call.payload, nil, "generic"}}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.pageURL(pathBatch), strings.NewReader(protocolForm(encodedRequest, bootstrap.SNlM0e)))
	if err != nil {
		return nil, err
	}
	query := req.URL.Query()
	query.Set("rpcids", rpcID)
	query.Set("source-path", c.pathPrefix+call.sourcePath)
	query.Set("bl", bootstrap.BL)
	query.Set("f.sid", bootstrap.FSID)
	query.Set("hl", call.language)
	query.Set("_reqid", fmt.Sprintf("%d", c.nextReqID()))
	query.Set("rt", "c")
	req.URL.RawQuery = query.Encode()
	c.applyXHRHeaders(req)
	timing := requestTiming(time.Since(started), time.Now())
	if call.untimed {
		timing[0] = []any{}
	}
	header := buildGenericHeader(c.features(), c.clientID, bootstrap.BL, call.mode, call.level, timing)
	if call.conversation {
		header = buildConversationHeader(c.features(), c.clientID, bootstrap.BL, call.temporaryChat, call.mode, call.level, timing)
	}
	req.Header.Set("x-goog-ext-525001261-jspb", header)
	req.Header.Set("x-goog-ext-73010989-jspb", call.extension)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		logging.Debug(c.displayAccountID(), "上游 RPC 失败", "rpc", rpcID, "耗时", time.Since(started), "错误", err)
		return nil, transportProtocolError(rpcID+" request", err)
	}
	logging.Debug(c.displayAccountID(), "上游 RPC", "rpc", rpcID, "状态", resp.StatusCode, "耗时", time.Since(started))
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseStatusError(resp, rpcID)
	}

	var payload []any
	found := false
	err = scanProtocolRecords(resp.Body, func(record []any) error {
		tag, _ := stringAt(record, 0)
		recordID, _ := stringAt(record, 1)
		if tag != "wrb.fr" || recordID != rpcID {
			return nil
		}
		encoded, _ := stringAt(record, 2)
		if encoded == "" {
			return rpcStatusError(rpcID, record)
		}
		if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
			return fmt.Errorf("decode %s payload: %w", rpcID, err)
		}
		found = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, retryableProtocolError(fmt.Sprintf("%s response has no payload", rpcID), nil)
	}
	return payload, nil
}

func (c *Client) resolveRequestModel(catalog ModelCatalog, id string) (Model, error) {
	if strings.TrimSpace(id) == "" {
		return catalog.Default()
	}
	return catalog.Resolve(id)
}

func (c *Client) bootstrapSnapshot() (Bootstrap, error) {
	c.bootstrapMu.RLock()
	defer c.bootstrapMu.RUnlock()
	if c.bootstrap == nil {
		return Bootstrap{}, retryableProtocolError("账号尚未完成初始化", nil)
	}
	return *c.bootstrap, nil
}

// navigationHeaderOrder 是 Chrome 稳定版打开首页的请求头顺序
var navigationHeaderOrder = []string{
	"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-full-version", "sec-ch-ua-arch",
	"sec-ch-ua-platform", "sec-ch-ua-platform-version", "sec-ch-ua-model", "sec-ch-ua-bitness",
	"sec-ch-ua-wow64", "sec-ch-ua-full-version-list", "sec-ch-ua-form-factors",
	"upgrade-insecure-requests", "user-agent", "accept",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// xhrHeaderOrder 是 Chrome 稳定版页面 batchexecute 与 StreamGenerate XHR 的请求头顺序
var xhrHeaderOrder = []string{
	"content-length", "sec-ch-ua-full-version-list", "sec-ch-ua-platform", "sec-ch-ua",
	"sec-ch-ua-bitness", "sec-ch-ua-model", "sec-ch-ua-mobile", "x-same-domain",
	"sec-ch-ua-wow64", "sec-ch-ua-form-factors", "sec-ch-ua-arch", "x-goog-ext-73010989-jspb",
	"sec-ch-ua-full-version", "content-type", "x-goog-ext-525001261-jspb",
	"x-goog-ext-525005358-jspb", "x-goog-ext-73010990-jspb", "user-agent",
	"sec-ch-ua-platform-version", "accept", "origin",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// pseudoHeaderOrder 是 Chrome 的 HTTP/2 伪头顺序
var pseudoHeaderOrder = []string{":method", ":authority", ":scheme", ":path"}

func (c *Client) applyNavigationHeaders(req *http.Request) {
	applyChromeHeaders(req, c.language, true)
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("sec-fetch-dest", "document")
	req.Header.Set("sec-fetch-mode", "navigate")
	req.Header.Set("sec-fetch-site", "none")
	req.Header.Set("sec-fetch-user", "?1")
	req.Header.Set("upgrade-insecure-requests", "1")
	req.Header.Set("priority", "u=0, i")
	req.Header[http.HeaderOrderKey] = navigationHeaderOrder
	req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder
}

func (c *Client) applyXHRHeaders(req *http.Request) {
	applyChromeHeaders(req, c.language, true)
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("content-type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("origin", "https://gemini.google.com")
	req.Header.Set("referer", "https://gemini.google.com/")
	req.Header.Set("x-same-domain", "1")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("priority", "u=1, i")
	req.Header[http.HeaderOrderKey] = xhrHeaderOrder
	req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder
}

// formEscaper 把 url.QueryEscape 的结果改写为浏览器 encodeURIComponent 的形式
var formEscaper = strings.NewReplacer("+", "%20", "%21", "!", "%27", "'", "%28", "(", "%29", ")", "%2A", "*")

// pageJSON 按页面 JSON.stringify 编码，HTML 字符保持原样
func pageJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// protocolForm 按网页 XHR 的字段顺序与编码构造 f.req、at 表单，末尾带 &，访客会话没有 at
func protocolForm(request string, token string) string {
	form := "f.req=" + formEscaper.Replace(url.QueryEscape(request)) + "&"
	if token == "" {
		return form
	}
	return form + "at=" + formEscaper.Replace(url.QueryEscape(token)) + "&"
}

// nextReqID 按网页客户端规则在四位随机基数上逐次增加 100000
func (c *Client) nextReqID() int64 {
	return c.reqID.Add(100000)
}

// pageURL 返回目标账号在官网下的页面或接口地址
func (c *Client) pageURL(path string) string {
	return geminiOrigin + c.pathPrefix + path
}

func (c *Client) displayAccountID() string {
	if strings.TrimSpace(c.accountID) == "" {
		return "default"
	}
	return c.accountID
}

// refreshCookies 在请求生命周期内串行更新账户凭证
func (c *Client) refreshCookies(ctx context.Context) error {
	if err := c.refreshSlot.Acquire(ctx, 1); err != nil {
		return err
	}
	defer c.refreshSlot.Release(1)
	if c.refresh == nil {
		return retryableProtocolError(ErrSignedOut.Error(), ErrSignedOut)
	}
	started := time.Now()
	logging.Info(c.displayAccountID(), "账号认证续签")
	cookies, err := c.refresh(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		logging.Error(c.displayAccountID(), "账号认证续签失败", "耗时", time.Since(started), "错误", err)
		return retryableProtocolError(fmt.Sprintf("续签 Cookie 失败: %v", err), errors.Join(ErrSignedOut, err))
	}
	if len(cookies) == 0 {
		return retryableProtocolError("续签没有返回 Cookie", ErrSignedOut)
	}
	if err := c.replaceCookies(cookies); err != nil {
		return retryableProtocolError(fmt.Sprintf("安装续签的 Cookie 失败: %v", err), err)
	}
	logging.Info(c.displayAccountID(), "账号认证续签完成", "耗时", time.Since(started))
	c.cookieMu.Lock()
	snapshot := c.cookieSnapshotLocked()
	c.cookieMu.Unlock()
	if c.save != nil {
		if err := c.save(snapshot); err != nil {
			return retryableProtocolError(fmt.Sprintf("persist refreshed account cookies: %v", err), err)
		}
	}
	return nil
}

// tracedReader 把读取到的上游响应字节计入请求跟踪
type tracedReader struct {
	reader io.Reader
	trace  *logging.Request
}

func (r tracedReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.trace.UpstreamRead(n)
	return n, err
}

// formatBytes 把字节数格式化为 B、KB 或 MB
func formatBytes(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	}
	return fmt.Sprintf("%d B", size)
}

// CloseIdleConnections 关闭协议客户端的空闲连接
func (c *Client) CloseIdleConnections() {
	c.httpClient.CloseIdleConnections()
}

// ProbeProxy 经代理以浏览器导航请求官网首页，首页返回 200 时报告耗时
func ProbeProxy(ctx context.Context, proxyURL string) (time.Duration, error) {
	httpClient, err := NewHTTPClient(requestCeilingSeconds, proxyURL)
	if err != nil {
		return 0, err
	}
	defer httpClient.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geminiOrigin+pathInit, nil)
	if err != nil {
		return 0, err
	}
	(&Client{language: defaultLanguage}).applyNavigationHeaders(req)
	started := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, responseStatusError(resp, "proxy probe")
	}
	return time.Since(started), nil
}

// isAuthenticationError 判断错误是否为 HTTP 或帧内的 401、403，以及重定向到登录页的 302，BardErrorInfo 1060 等出口限制不属于认证错误
func isAuthenticationError(err error) bool {
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		return false
	}
	if protocolErr.HTTPStatus == http.StatusUnauthorized || protocolErr.HTTPStatus == http.StatusForbidden {
		return true
	}
	if protocolErr.HTTPStatus == http.StatusFound || protocolErr.HTTPStatus == http.StatusSeeOther || protocolErr.HTTPStatus == http.StatusTemporaryRedirect {
		return errors.Is(protocolErr.Cause, ErrSignedOut) || strings.Contains(protocolErr.Message, "登录") || strings.Contains(protocolErr.Message, "ServiceLogin")
	}
	return errors.Is(protocolErr.Cause, ErrSignedOut)
}

func newProtocolID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create protocol id: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(value[0:4]),
		binary.BigEndian.Uint16(value[4:6]),
		binary.BigEndian.Uint16(value[6:8]),
		binary.BigEndian.Uint16(value[8:10]),
		value[10:16],
	)), nil
}

// newContextID 返回生成请求 inner[4] 使用的 32 位十六进制上下文标识
func newContextID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create context id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func initialReqID() (int64, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, fmt.Errorf("create initial request id: %w", err)
	}
	return int64(1000 + binary.BigEndian.Uint32(value[:])%9000), nil
}

// ErrCaptcha 表示 Google 以人机验证页（/sorry）拦截了请求，出口 IP 与账号会话被判定为异常流量
var ErrCaptcha = errors.New("请求被 Google 人机验证页拦截")

// responseStatusError 把非 200 响应转为协议错误，重定向到 google.com/sorry 时为人机验证拦截，按 429 处理；重定向到登录页时按 401 认证失效处理
func responseStatusError(resp *http.Response, operation string) *ProtocolError {
	if location, err := resp.Location(); err == nil {
		if strings.HasSuffix(location.Hostname(), "google.com") && strings.HasPrefix(location.Path, "/sorry") {
			return &ProtocolError{
				HTTPStatus: http.StatusTooManyRequests, Code: http.StatusTooManyRequests, Retryable: true, Cause: ErrCaptcha,
				Message: fmt.Sprintf("gemini %s 被 Google 人机验证页拦截（HTTP %d 重定向到 %s%s）", operation, resp.StatusCode, location.Host, location.Path),
			}
		}
		if isLoginRedirect(location) {
			return &ProtocolError{
				HTTPStatus: http.StatusUnauthorized, Code: http.StatusUnauthorized, Retryable: true, Cause: ErrSignedOut,
				Message: fmt.Sprintf("gemini %s 登录态失效或账号序号偏移（HTTP %d 重定向到 %s）", operation, resp.StatusCode, location.String()),
			}
		}
	}
	return httpStatusError(resp.StatusCode, operation)
}

func isLoginRedirect(location *url.URL) bool {
	if location == nil {
		return false
	}
	host := strings.ToLower(location.Host)
	path := strings.ToLower(location.Path)
	if strings.Contains(host, "accounts.google.com") {
		if strings.Contains(path, "servicelogin") || strings.Contains(path, "interactivelogin") || strings.Contains(path, "signin") || strings.Contains(location.RawQuery, "continue=") {
			return true
		}
	}
	if strings.Contains(host, "gemini.google.com") {
		if path == "/app" || path == "/" || strings.HasPrefix(path, "/u/") {
			return true
		}
	}
	return false
}

func httpStatusError(status int, operation string) *ProtocolError {
	return &ProtocolError{
		HTTPStatus: status,
		Code:       status,
		Message:    fmt.Sprintf("gemini %s returned HTTP %d", operation, status),
		Retryable:  status == 401 || status == 403 || status == 429 || status >= 500,
	}
}

// ErrTransport 表示连接、TLS 或代理等网络层失败，与账号状态无关
var ErrTransport = errors.New("gemini web transport failure")

func transportProtocolError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return retryableProtocolError(fmt.Sprintf("gemini %s: %v", operation, err), errors.Join(ErrTransport, err))
}

// rpcStatusError 把没有载荷的 wrb.fr 记录第 5 项的 RPC 状态转为协议错误，会话失效时状态为 7（PERMISSION_DENIED），按 HTTP 403 处理
func rpcStatusError(rpcID string, record []any) *ProtocolError {
	status, _ := arrayAt(record, 5)
	if code, _ := intAt(status, 0); code == 7 {
		return httpStatusError(http.StatusForbidden, rpcID)
	}
	return retryableProtocolError(fmt.Sprintf("%s returned no payload, status %v", rpcID, status), nil)
}

func retryableProtocolError(message string, cause error) *ProtocolError {
	return &ProtocolError{HTTPStatus: http.StatusBadGateway, Code: http.StatusBadGateway, Message: message, Retryable: true, Cause: cause}
}

// IsAuthenticationError 判断错误是否表示账号会话失效，包括 HTTP 401、403 与续签失败
func IsAuthenticationError(err error) bool {
	return errors.Is(err, ErrSignedOut) || isAuthenticationError(err)
}
