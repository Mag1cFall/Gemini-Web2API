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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	httpClient tls_client.HttpClient
	// mediaClient 是下载请求中远程媒体的客户端，与账号同一请求身份与代理，不带账号 Cookie
	mediaClient tls_client.HttpClient
	accountID   string
	language    string
	clientID    string
	saveHistory bool
	// guest 表示未登录的访客会话
	guest bool
	// reqSeq 是已分配的 _reqid 序号，reqBase 是首个请求创建时本地时间的当日秒数
	reqSeq      atomic.Int64
	reqBaseOnce sync.Once
	reqBase     int64
	// timer 是模型扩展头第 19 槽的页面计时
	timer       pageTimer
	contextSize atomic.Int64
	requestSlot *semaphore.Weighted
	// concurrency 是请求槽数量
	concurrency int
	busy        atomic.Int32
	// releaseHook 在请求槽释放后调用，由账号池设置
	releaseHook atomic.Pointer[func()]
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
	// cookieVersion 是续签成功的次数，请求发出前读取，续签时与之比较以复用其他请求刚完成的续签
	cookieVersion atomic.Uint64
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

// SetUsageForTest 供测试设置用量快照
func (c *Client) SetUsageForTest(usage *UsageInfo) {
	c.usage.Store(usage)
}

// SetBootstrapForTest 供测试设置运行参数
func (c *Client) SetBootstrapForTest(bootstrap *Bootstrap) {
	c.bootstrapMu.Lock()
	defer c.bootstrapMu.Unlock()
	c.bootstrap = bootstrap
}

// AcquireRequest 占用账号的一个请求槽直到完整上游请求链结束，后台生成的轮询阶段让出
func (c *Client) AcquireRequest(ctx context.Context) (func(), error) {
	c.busy.Add(1)
	if err := c.requestSlot.Acquire(ctx, 1); err != nil {
		c.busy.Add(-1)
		return nil, err
	}
	return c.releaseRequest, nil
}

// TryAcquireRequest 在账号有空闲请求槽时占用一个并返回释放函数
func (c *Client) TryAcquireRequest() (func(), bool) {
	if !c.requestSlot.TryAcquire(1) {
		return nil, false
	}
	c.busy.Add(1)
	return c.releaseRequest, true
}

// SetReleaseHook 设置请求槽释放后调用的函数
func (c *Client) SetReleaseHook(hook func()) {
	c.releaseHook.Store(&hook)
}

// releaseRequest 释放一个请求槽
func (c *Client) releaseRequest() {
	c.requestSlot.Release(1)
	c.busy.Add(-1)
	c.notifyRelease()
}

// notifyRelease 调用账号池设置的请求槽释放函数
func (c *Client) notifyRelease() {
	if hook := c.releaseHook.Load(); hook != nil {
		(*hook)()
	}
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
	mediaClient, err := NewHTTPClient(requestCeilingSeconds, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}
	clientID, err := newProtocolID()
	if err != nil {
		return nil, err
	}
	client := &Client{
		httpClient:  httpClient,
		mediaClient: mediaClient,
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
		timer:       pageTimer{origin: time.Now()},
	}
	if source.AuthUser > 0 {
		client.authUser = source.AuthUser
		client.pathPrefix = fmt.Sprintf("/u/%d", source.AuthUser)
	}
	if err := client.replaceCookies(source.Cookies); err != nil {
		return nil, err
	}
	return client, nil
}

// GuestID 是访客会话在账号池、日志与账本中的 ID
const GuestID = "guest"

// maxPromptLength 是官网输入框在高级模式下的字符上限，上游对更长的提示词不返回候选
const maxPromptLength = 1_000_000

// longPromptInstruction 是超长提示词改为 message.txt 附件后生成请求中的提示词
const longPromptInstruction = "The complete input for this turn is in the attached message.txt. Treat its content as the user's message and respond to it directly."

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

// tierCode 返回生成请求模型头中的账号层级编码：账号取用量接口给出的编码，访客为免费层 1
func (c *Client) tierCode() int {
	if c.guest {
		return 1
	}
	if usage := c.usage.Load(); usage != nil {
		return usage.TierCode
	}
	return 0
}

// Init 刷新首页动态参数并获取当前账号模型目录与偏好
func (c *Client) Init(ctx context.Context) error {
	return c.init(ctx, true)
}

func (c *Client) init(ctx context.Context, allowRefresh bool) error {
	version := c.cookieVersion.Load()
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
		if allowRefresh && renewable(protocolErr) && c.refresh != nil {
			resp.Body.Close()
			if err := c.refreshCookies(ctx, version); err != nil {
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
				if err := c.refreshCookies(ctx, version); err != nil {
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
		if allowRefresh && renewable(err) && c.refresh != nil {
			if err := c.refreshCookies(ctx, version); err != nil {
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
	version := c.cookieVersion.Load()
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
	if allowRefresh && c.freeTier() {
		if listed, signedIn, err := c.listAccountsSignedIn(ctx); err == nil && listed && !signedIn {
			return c.restream(ctx, version, request, emit)
		}
	}
	taskTerms := false
	if request.Feature == FeatureTask {
		if taskTerms, err = c.onboardTask(ctx, bootstrap, model.Mode, request.ThinkingLevel); err != nil {
			return err
		}
	}
	request.Files = append([]FileData(nil), request.Files...)
	if utf8.RuneCountInString(request.Prompt) > maxPromptLength {
		request.Files = append(request.Files, FileData{Data: []byte(request.Prompt), FileName: "message.txt", MimeType: "text/plain"})
		request.Prompt = longPromptInstruction
	}
	if c.guest && len(request.Files) > 0 {
		return ErrGuestAttachments
	}
	trace := logging.FromContext(ctx)
	trace.SetThinking(request.ThinkingLevel.String())
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
	req.URL.RawQuery = c.frontendQuery(bootstrap, bootstrap.Language)
	c.applyXHRHeaders(req)
	chatMode := any(0)
	if temporaryChat {
		chatMode = 1
	}
	if request.researchConfirm {
		chatMode = nil
	}
	modelHeader, err := buildModelHeader(c.features(), model, chatMode, c.tierCode(), request.ThinkingLevel, c.clientID, bootstrap.BL, c.pageTiming())
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
		if allowRefresh && renewable(protocolErr) && c.refresh != nil {
			resp.Body.Close()
			return c.restream(ctx, version, request, emit)
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
		go c.stopGeneration(bootstrap, snapshot.CID, snapshot.RID, decoder.metadata.ControlToken, model.Mode, request.ThinkingLevel)
		return idle
	}
	if ctx.Err() != nil {
		snapshot := decodeState.Snapshot()
		go c.stopGeneration(bootstrap, snapshot.CID, snapshot.RID, decoder.metadata.ControlToken, model.Mode, request.ThinkingLevel)
		return ctx.Err()
	}
	if request.researchPlan != nil {
		*request.researchPlan = decoder.researchConfirm
	}
	if err == nil && decoder.background && !request.researchConfirm {
		err = c.awaitTurn(ctx, decodeState, nil, model, request.ThinkingLevel, nil, request.researchPlan, guard.Emit)
	}
	if snapshot := decodeState.Snapshot(); streamInterrupted(err) && !temporaryChat && !request.researchConfirm && snapshot.CID != "" && snapshot.RID != next.RID {
		trace.Stage("生成流中断，读取会话取回回答", "原因", err)
		err = c.awaitTurn(ctx, decodeState, decoder, model, request.ThinkingLevel, nil, request.researchPlan, guard.Emit)
	}
	if errors.Is(err, ErrModelMismatch) && !guard.Emitted() {
		if listed, signedIn, listErr := c.listAccountsSignedIn(ctx); listErr == nil && listed && !signedIn {
			err = &ProtocolError{
				HTTPStatus: http.StatusUnauthorized, Code: http.StatusUnauthorized, Retryable: true, Cause: ErrSignedOut,
				Message: fmt.Sprintf("%v，ListAccounts 显示会话已失效", err),
			}
		}
	}
	if err != nil {
		if allowRefresh && !guard.Emitted() && IsAuthenticationError(err) && c.refresh != nil {
			return c.restream(ctx, version, request, emit)
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

// restream 续签 Cookie、重新初始化后重发生成请求，重发时不再续签
func (c *Client) restream(ctx context.Context, version uint64, request GenerateRequest, emit func(Event) error) error {
	if err := c.refreshCookies(ctx, version); err != nil {
		return err
	}
	if err := c.init(ctx, false); err != nil {
		return err
	}
	return c.stream(ctx, request, emit, false)
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
	request := batchRequest{rpcID: "otAQ7b", payload: "[1]", sourcePath: "/app", language: catalogLanguage, extension: "[]"}
	payload, err := c.batchExecute(ctx, bootstrap, request)
	if err != nil {
		return ModelCatalog{}, err
	}
	status, ok := intAt(payload, 14)
	if ok && !c.guest && (status == 1040 || status == 1042) {
		mode, _ := intAt(payload, 0)
		if err := c.acceptTerms(ctx, bootstrap, mode); err != nil {
			return ModelCatalog{}, fmt.Errorf("%w；同意服务条款失败：%v", c.accountStatusError(status), err)
		}
		if payload, err = c.batchExecute(ctx, bootstrap, request); err != nil {
			return ModelCatalog{}, err
		}
		status, ok = intAt(payload, 14)
	}
	if ok && !c.guest {
		if err := c.accountStatusError(status); err != nil {
			return ModelCatalog{}, err
		}
	}
	return parseModelCatalog(payload)
}

// acceptTerms 按官网前端在账号状态不可用时的流程同意服务条款：F6qrid 读取条款版本，读取失败时与页面一样按版本 0；UVb0T 发送条款版本、界面语言与首页 thykhd 令牌，mode 为目录默认模型的 mode
func (c *Client) acceptTerms(ctx context.Context, bootstrap Bootstrap, mode int) error {
	version := 0
	payload, err := c.batchExecute(ctx, bootstrap, batchRequest{rpcID: "F6qrid", payload: "[]", sourcePath: "/app", language: bootstrap.Language, extension: "[0]", mode: mode})
	if err != nil {
		logging.Warn(c.displayAccountID(), "读取服务条款版本失败，按版本 0 同意", "原因", err)
	} else {
		version, _ = intAt(payload, 0)
	}
	request, err := json.Marshal([]any{version, bootstrap.Language, bootstrap.TermsToken})
	if err != nil {
		return err
	}
	if _, err := c.batchExecute(ctx, bootstrap, batchRequest{rpcID: "UVb0T", payload: string(request), sourcePath: "/app", language: bootstrap.Language, extension: "[0]", mode: mode}); err != nil {
		return err
	}
	logging.Info(c.displayAccountID(), "已同意服务条款", "版本", version)
	return nil
}

// accountStatusReasons 是 otAQ7b payload[14] 中账号无法使用 Gemini 的状态码与原因
var accountStatusReasons = map[int]string{
	1014: "访问暂时受限",
	1021: "账号访问被拒绝",
	1040: "需要在 Gemini 官网同意最新的服务条款",
	1042: "服务条款已更新，需要在 Gemini 官网重新同意",
	1054: "访问被家长或监护人阻止",
	1057: "访问需要家长或监护人批准",
	1060: "所在国家或地区不支持 Gemini",
}

// ErrAccountRestricted 表示模型目录的账号状态码显示账号暂时不能使用 Gemini，例如待同意条款、地区或监护人限制
var ErrAccountRestricted = errors.New("Gemini 账号不可用")

// accountStatusError 把登录账号目录中的账号状态码转为错误：1016 为会话失效，accountStatusReasons 中的状态为账号不可用，写明原因与官网处理入口（该账号的 Gemini 首页）
func (c *Client) accountStatusError(status int) error {
	if status == 1016 {
		return &ProtocolError{HTTPStatus: http.StatusUnauthorized, Code: status, Retryable: true, Cause: ErrSignedOut, Message: "模型目录显示会话已失效 (1016)"}
	}
	if reason, ok := accountStatusReasons[status]; ok {
		return &ProtocolError{
			HTTPStatus: http.StatusServiceUnavailable, Code: status, Cause: ErrAccountRestricted,
			Message: fmt.Sprintf("Gemini 账号不可用：%s (%d)，在 Gemini 官网 %s 以该账号处理", reason, status, c.pageURL(pathInit)),
		}
	}
	return nil
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
	req.URL.RawQuery = c.batchQuery(bootstrap, rpcID, call.sourcePath, call.language)
	c.applyXHRHeaders(req)
	var timing []any
	if call.untimed {
		timing = []any{[]any{}, pageDuration(float64(time.Now().UnixMilli()))}
	} else {
		timing = c.pageTiming()
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
	req.Header.Set("content-type", "application/x-www-form-urlencoded;charset=utf-8")
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

// nextReqID 按官网页面脚本返回 1 加首个请求创建时本地时间的当日秒数加序号乘 100000，序号按请求创建顺序从 0 起
func (c *Client) nextReqID() int64 {
	c.reqBaseOnce.Do(func() {
		now := time.Now()
		c.reqBase = int64(now.Hour()*3600 + now.Minute()*60 + now.Second())
	})
	return 1 + c.reqBase + (c.reqSeq.Add(1)-1)*100000
}

// frontendQuery 按官网页面的参数顺序返回 bl、f.sid、hl、_reqid、rt 查询串
func (c *Client) frontendQuery(bootstrap Bootstrap, language string) string {
	return "bl=" + url.QueryEscape(bootstrap.BL) + "&f.sid=" + url.QueryEscape(bootstrap.FSID) + "&hl=" + url.QueryEscape(language) +
		"&_reqid=" + strconv.FormatInt(c.nextReqID(), 10) + "&rt=c"
}

// batchQuery 按官网页面的参数顺序返回 batchexecute 查询串，rpcids 与 source-path 在 frontendQuery 的参数之前
func (c *Client) batchQuery(bootstrap Bootstrap, rpcID, sourcePath, language string) string {
	return "rpcids=" + url.QueryEscape(rpcID) + "&source-path=" + url.QueryEscape(c.pathPrefix+sourcePath) + "&" + c.frontendQuery(bootstrap, language)
}

// pageTimer 是官网页面的 TimeUsageTracker，按 performance.now 的 0.1 ms 精度累计自上一个带计时的请求起的时长
type pageTimer struct {
	mu     sync.Mutex
	origin time.Time
	last   float64
}

// pageTiming 返回模型扩展头末尾的耗时与发送时间，耗时自上一个带计时的请求或客户端创建起算
func (c *Client) pageTiming() []any {
	now := time.Now()
	c.timer.mu.Lock()
	performance := float64(now.Sub(c.timer.origin)/(100*time.Microsecond)) / 10
	elapsed := performance - c.timer.last
	c.timer.last = performance
	c.timer.mu.Unlock()
	return requestTiming(elapsed, now)
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

// refreshCookies 串行续签账号 Cookie，请求发出时读到的 version 之后已有续签成功时复用其结果
func (c *Client) refreshCookies(ctx context.Context, version uint64) error {
	if err := c.refreshSlot.Acquire(ctx, 1); err != nil {
		return err
	}
	defer c.refreshSlot.Release(1)
	if c.cookieVersion.Load() != version {
		return nil
	}
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
		if errors.Is(err, ErrTransport) {
			return retryableProtocolError(fmt.Sprintf("续签 Cookie 失败: %v", err), err)
		}
		return retryableProtocolError(fmt.Sprintf("续签 Cookie 失败: %v", err), errors.Join(ErrSignedOut, err))
	}
	if len(cookies) == 0 {
		return retryableProtocolError("续签没有返回 Cookie", ErrSignedOut)
	}
	if err := c.replaceCookies(cookies); err != nil {
		return retryableProtocolError(fmt.Sprintf("安装续签的 Cookie 失败: %v", err), err)
	}
	c.cookieVersion.Add(1)
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
	c.mediaClient.CloseIdleConnections()
}

// ProxyDiagnostic 是代理检测或诊断的完整详细结果
type ProxyDiagnostic struct {
	OK        bool   `json:"ok"`
	Status    string `json:"status"` // "ok", "captcha_blocked", "rate_limited", "network_error", "timeout", "http_error", "invalid_format"
	LatencyMS int64  `json:"latency_ms"`
	Message   string `json:"message"`
	Error     string `json:"error,omitempty"`
}

// isGoogleCaptchaRedirect 判断响应是否为 Google 人机验证（/sorry）重定向
func isGoogleCaptchaRedirect(resp *http.Response) (bool, *url.URL) {
	if resp == nil {
		return false, nil
	}
	location, err := resp.Location()
	if err != nil || location == nil {
		return false, nil
	}
	host := strings.ToLower(location.Hostname())
	isGoogle := host == "google.com" || strings.HasSuffix(host, ".google.com")
	if isGoogle && strings.HasPrefix(location.Path, "/sorry") {
		return true, location
	}
	return false, nil
}

// isGoogleLoginRedirect 判断响应是否为 Google 登录页重定向（会话失效，需重新登录）
func isGoogleLoginRedirect(resp *http.Response) (bool, *url.URL) {
	if resp == nil {
		return false, nil
	}
	location, err := resp.Location()
	if err != nil || location == nil {
		return false, nil
	}
	host := strings.ToLower(location.Hostname())
	isAccounts := host == "accounts.google.com" || strings.HasSuffix(host, ".accounts.google.com")
	if isAccounts || strings.Contains(location.Path, "ServiceLogin") || strings.Contains(location.Path, "InteractiveLogin") {
		return true, location
	}
	return false, nil
}

// DiagnoseProxy 经代理以浏览器导航请求 Gemini 官网首页，返回详细的连通性、延迟与风控诊断信息
func DiagnoseProxy(ctx context.Context, proxyURL string) ProxyDiagnostic {
	timeout := 15 * time.Second
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpClient, err := NewHTTPClient(15, strings.TrimSpace(proxyURL))
	if err != nil {
		return ProxyDiagnostic{
			OK:      false,
			Status:  "network_error",
			Message: fmt.Sprintf("初始化代理客戶端失敗: %v", err),
			Error:   err.Error(),
		}
	}
	defer httpClient.CloseIdleConnections()

	req, err := http.NewRequestWithContext(testCtx, http.MethodGet, geminiOrigin+pathInit, nil)
	if err != nil {
		return ProxyDiagnostic{
			OK:      false,
			Status:  "network_error",
			Message: fmt.Sprintf("建立請求失敗: %v", err),
			Error:   err.Error(),
		}
	}
	(&Client{language: defaultLanguage}).applyNavigationHeaders(req)
	started := time.Now()
	resp, err := httpClient.Do(req)
	latencyMS := time.Since(started).Milliseconds()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || (testCtx.Err() != nil && errors.Is(testCtx.Err(), context.DeadlineExceeded)) {
			return ProxyDiagnostic{
				OK:        false,
				Status:    "timeout",
				LatencyMS: latencyMS,
				Message:   "代理連線逾時（15 秒內未回應）",
				Error:     err.Error(),
			}
		}
		errMsg := err.Error()
		msg := fmt.Sprintf("連線失敗: %v", err)
		if strings.Contains(errMsg, "lookup") {
			msg = fmt.Sprintf("DNS 解析失敗（請檢查域名或改用直接 IP）: %v", err)
		} else if strings.Contains(errMsg, "socks") && (strings.Contains(errMsg, "auth") || strings.Contains(errMsg, "user/password")) {
			msg = "SOCKS5 帳號密碼認證失敗"
		} else if strings.Contains(errMsg, "connection refused") {
			msg = "連線被拒絕（代理伺服器未運行或連接埠錯誤）"
		}
		return ProxyDiagnostic{
			OK:        false,
			Status:    "network_error",
			LatencyMS: latencyMS,
			Message:   msg,
			Error:     errMsg,
		}
	}
	defer resp.Body.Close()

	if isCaptcha, _ := isGoogleCaptchaRedirect(resp); isCaptcha || resp.StatusCode == http.StatusTooManyRequests {
		return ProxyDiagnostic{
			OK:        false,
			Status:    "captcha_blocked",
			LatencyMS: latencyMS,
			Message:   "被 Google 人機驗證攔截（HTTP 302 重定向到 sorry/index），該出口 IP 受限",
			Error:     "Google 人機驗證頁攔截 (302 sorry/index)",
		}
	}

	if resp.StatusCode == http.StatusOK {
		return ProxyDiagnostic{
			OK:        true,
			Status:    "ok",
			LatencyMS: latencyMS,
			Message:   fmt.Sprintf("連線正常！延遲 %d ms，Google Gemini 存取許可", latencyMS),
		}
	}

	return ProxyDiagnostic{
		OK:        false,
		Status:    "http_error",
		LatencyMS: latencyMS,
		Message:   fmt.Sprintf("Google 首頁返回狀態碼 HTTP %d", resp.StatusCode),
		Error:     resp.Status,
	}
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

// renewable 判断错误是否续签后重试：会话失效（401、403、ErrSignedOut）与人机验证页拦截
func renewable(err error) bool {
	return IsAuthenticationError(err) || errors.Is(err, ErrCaptcha)
}

// freeTier 报告账号是否为免费账号，免费账号的回答与未登录会话来自同一模型
func (c *Client) freeTier() bool {
	usage := c.usage.Load()
	return usage != nil && usage.Tier == "free"
}

// isAuthenticationError 判断错误是否为上游 HTTP 401、403 或 batchexecute 帧内状态 7；公开状态为 403 的 BardErrorInfo 1060 是出口 IP 限制，不属于认证错误
func isAuthenticationError(err error) bool {
	var protocolErr *ProtocolError
	return errors.As(err, &protocolErr) && protocolErr.Code != 1060 && (protocolErr.HTTPStatus == http.StatusUnauthorized || protocolErr.HTTPStatus == http.StatusForbidden)
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

// ErrCaptcha 表示 Google 以人机验证页（/sorry）拦截了请求，出口 IP 与账号会话被判定为异常流量
var ErrCaptcha = errors.New("请求被 Google 人机验证页拦截")

// responseStatusError 把非 200 响应转为协议错误，重定向到 google.com/sorry 时为人机验证拦截，按 429 处理；/u/N 页面重定向到 Gemini 其他页面时表示 Cookie 会话中没有第 N 个账号，按会话失效处理
func responseStatusError(resp *http.Response, operation string) *ProtocolError {
	if isCaptcha, location := isGoogleCaptchaRedirect(resp); isCaptcha {
		return &ProtocolError{
			HTTPStatus: http.StatusTooManyRequests, Code: http.StatusTooManyRequests, Retryable: true, Cause: ErrCaptcha,
			Message: fmt.Sprintf("gemini %s 被 Google 人机验证页拦截（HTTP %d 重定向到 %s%s）", operation, resp.StatusCode, location.Host, location.Path),
		}
	}
	if isLogin, location := isGoogleLoginRedirect(resp); isLogin {
		return &ProtocolError{
			HTTPStatus: http.StatusUnauthorized, Code: http.StatusUnauthorized, Retryable: true, Cause: ErrSignedOut,
			Message: fmt.Sprintf("gemini %s 重定向到登录页（HTTP %d 重定向到 %s%s），账号需要重新登录", operation, resp.StatusCode, location.Host, location.Path),
		}
	}
	location, err := resp.Location()
	if err == nil && location.Hostname() == resp.Request.URL.Hostname() && strings.HasPrefix(resp.Request.URL.Path, "/u/") {
		return &ProtocolError{
			HTTPStatus: http.StatusUnauthorized, Code: http.StatusUnauthorized, Retryable: true, Cause: ErrSignedOut,
			Message: fmt.Sprintf("gemini %s 的 %s 重定向到 %s，Cookie 会话中没有该账号序号", operation, resp.Request.URL.Path, location.Path),
		}
	}
	return httpStatusError(resp.StatusCode, operation)
}

// httpStatusError 把上游 HTTP 状态转为协议错误，401、403 表示账号会话失效
func httpStatusError(status int, operation string) *ProtocolError {
	err := &ProtocolError{
		HTTPStatus: status,
		Code:       status,
		Message:    fmt.Sprintf("gemini %s returned HTTP %d", operation, status),
		Retryable:  status == 401 || status == 403 || status == 429 || status >= 500,
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		err.Cause = ErrSignedOut
	}
	return err
}

// streamInterrupted 判断生成流是否在回答完成前因网络中断或缺少结束帧而结束
func streamInterrupted(err error) bool {
	return errors.Is(err, ErrTransport) || errors.Is(err, errStreamEnded)
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
