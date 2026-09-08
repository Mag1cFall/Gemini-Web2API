package gemini

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"golang.org/x/sync/semaphore"
)

const (
	endpointInit     = "https://gemini.google.com/app"
	endpointBatch    = "https://gemini.google.com/_/BardChatUi/data/batchexecute"
	endpointGenerate = "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
)

// Client 表示与单一账号身份绑定的 Gemini Web 协议客户端
type Client struct {
	httpClient  tls_client.HttpClient
	accountID   string
	fingerprint Fingerprint
	clientID    string
	saveHistory bool
	reqID       atomic.Int64
	contextSize atomic.Int64
	requestSlot *semaphore.Weighted

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

// AcquireRequest 独占同一账号的完整上游请求链
func (c *Client) AcquireRequest(ctx context.Context) (func(), error) {
	if err := c.requestSlot.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { c.requestSlot.Release(1) }, nil
}

// NewClient 创建固定 Cookie、代理和指纹的账号客户端
func NewClient(source AccountSource, proxyURL string, saveHistory bool) (*Client, error) {
	fingerprint, profile, err := normalizeFingerprint(source.Fingerprint)
	if err != nil {
		return nil, err
	}
	httpClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), getClientOptions(profile, proxyURL)...)
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
		fingerprint: fingerprint,
		clientID:    clientID,
		saveHistory: saveHistory,
		requestSlot: semaphore.NewWeighted(1),
		refreshSlot: semaphore.NewWeighted(1),
		cookies:     make(map[string]Cookie, len(source.Cookies)),
		save:        source.Save,
		refresh:     source.Refresh,
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

// Init 刷新首页动态参数并获取当前账号模型目录
func (c *Client) Init(ctx context.Context) error {
	return c.init(ctx, true)
}

func (c *Client) init(ctx context.Context, allowRefresh bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointInit, nil)
	if err != nil {
		return err
	}
	c.applyNavigationHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return transportProtocolError(fmt.Sprintf("account %q bootstrap request", c.displayAccountID()), err)
	}
	if resp.StatusCode != http.StatusOK {
		protocolErr := httpStatusError(resp.StatusCode, "bootstrap")
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
	if err != nil {
		if strings.Contains(string(body), "accounts.google.com") {
			if allowRefresh && c.refresh != nil {
				if err := c.refreshCookies(ctx); err != nil {
					return err
				}
				return c.init(ctx, false)
			}
			return retryableProtocolError(fmt.Sprintf("account %q is not signed in", c.displayAccountID()), nil)
		}
		return retryableProtocolError(fmt.Sprintf("account %q bootstrap: %v", c.displayAccountID(), err), err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		return err
	}
	catalog, err := c.fetchModelCatalog(ctx, bootstrap)
	if err != nil {
		if allowRefresh && isAuthenticationError(err) && c.refresh != nil {
			if err := c.refreshCookies(ctx); err != nil {
				return err
			}
			return c.init(ctx, false)
		}
		return retryableProtocolError(fmt.Sprintf("account %q initialize models: %v", c.displayAccountID(), err), err)
	}
	bootstrap.Catalog = catalog
	c.bootstrapMu.Lock()
	c.bootstrap = &bootstrap
	c.bootstrapMu.Unlock()
	return nil
}

// Stream 执行一次生成并只输出规范事件
func (c *Client) Stream(ctx context.Context, request GenerateRequest, emit func(Event) error) error {
	return c.stream(ctx, request, emit, true)
}

func (c *Client) stream(ctx context.Context, request GenerateRequest, emit func(Event) error, allowRefresh bool) error {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return err
	}
	model, err := c.resolveRequestModel(bootstrap.Catalog, request.Model)
	if err != nil {
		return err
	}
	request.ModelMode = model.Mode
	streamRequestID, err := newProtocolID()
	if err != nil {
		return err
	}
	modelHeader, err := buildModelHeader(model.Hash, model.Mode, request.ThinkingMode, c.clientID)
	if err != nil {
		return err
	}
	payload, err := buildGeneratePayload(request, c.fingerprint.languageCode(), streamRequestID, !c.saveHistory)
	if err != nil {
		return err
	}
	form := url.Values{"f.req": {payload}, "at": {bootstrap.SNlM0e}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointGenerate, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	reqID := c.nextReqID()
	query := req.URL.Query()
	query.Set("bl", bootstrap.BL)
	query.Set("f.sid", bootstrap.FSID)
	query.Set("hl", c.fingerprint.languageCode())
	query.Set("_reqid", fmt.Sprintf("%d", reqID))
	query.Set("rt", "c")
	req.URL.RawQuery = query.Encode()
	c.applyXHRHeaders(req)
	req.Header.Set("x-goog-ext-525001261-jspb", modelHeader)
	requestHeader, _ := json.Marshal([]any{streamRequestID, 1})
	req.Header.Set("x-goog-ext-525005358-jspb", string(requestHeader))
	req.Header.Set("x-goog-ext-73010989-jspb", "[0]")
	req.Header.Set("x-goog-ext-73010990-jspb", "[0,0,0]")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return transportProtocolError(fmt.Sprintf("account %q generate request", c.displayAccountID()), err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		protocolErr := httpStatusError(resp.StatusCode, "generate")
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
	decodeState := NewConversationState()
	if request.Conversation != nil {
		decodeState = NewConversationStateFrom(request.Conversation.Snapshot())
	}
	guard := newModelGuard(model, emit)
	err = NewFrameDecoder().Decode(resp.Body, decodeState, guard.Emit)
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
		request.Conversation.Update(decodeState.Snapshot())
	}
	return nil
}

// Models 返回当前账号初始化得到的模型目录
func (c *Client) Models() []Model {
	bootstrap, err := c.bootstrapSnapshot()
	if err != nil {
		return nil
	}
	return bootstrap.Catalog.List()
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
	encodedRequest, err := json.Marshal([]any{[]any{[]any{"otAQ7b", "[]", nil, "generic"}}})
	if err != nil {
		return ModelCatalog{}, err
	}
	form := url.Values{"f.req": {string(encodedRequest)}, "at": {bootstrap.SNlM0e}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointBatch, strings.NewReader(form.Encode()))
	if err != nil {
		return ModelCatalog{}, err
	}
	query := req.URL.Query()
	query.Set("rpcids", "otAQ7b")
	query.Set("source-path", "/app")
	query.Set("bl", bootstrap.BL)
	query.Set("f.sid", bootstrap.FSID)
	query.Set("hl", c.fingerprint.languageCode())
	query.Set("_reqid", fmt.Sprintf("%d", c.nextReqID()))
	query.Set("rt", "c")
	req.URL.RawQuery = query.Encode()
	c.applyXHRHeaders(req)
	genericHeader, _ := json.Marshal([]any{1, nil, nil, nil, nil, nil, nil, nil, []int{4, 5, 6, 8}, nil, nil, nil, nil, nil, nil, nil, c.clientID})
	req.Header.Set("x-goog-ext-525001261-jspb", string(genericHeader))
	req.Header.Set("x-goog-ext-73010989-jspb", "[]")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ModelCatalog{}, transportProtocolError("model catalog request", err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		resp.Body.Close()
		return ModelCatalog{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ModelCatalog{}, httpStatusError(resp.StatusCode, "model catalog")
	}

	var catalog ModelCatalog
	found := false
	err = scanProtocolRecords(resp.Body, func(record []any) error {
		tag, _ := stringAt(record, 0)
		rpcID, _ := stringAt(record, 1)
		if tag != "wrb.fr" || rpcID != "otAQ7b" {
			return nil
		}
		encoded, _ := stringAt(record, 2)
		var payload []any
		if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
			return fmt.Errorf("decode model catalog payload: %w", err)
		}
		parsed, err := parseModelCatalog(payload, c.clientID)
		if err != nil {
			return err
		}
		catalog = parsed
		found = true
		return nil
	})
	if err != nil {
		return ModelCatalog{}, err
	}
	if !found {
		return ModelCatalog{}, retryableProtocolError("model catalog response has no otAQ7b payload", nil)
	}
	return catalog, nil
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
		return Bootstrap{}, retryableProtocolError(fmt.Sprintf("account %q is not initialized", c.displayAccountID()), nil)
	}
	return *c.bootstrap, nil
}

func (c *Client) applyNavigationHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.fingerprint.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", c.fingerprint.Language)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	c.applyClientHints(req)
	c.applyHeaderOrder(req, true)
}

func (c *Client) applyXHRHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.fingerprint.UserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", c.fingerprint.Language)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	c.applyClientHints(req)
	c.applyHeaderOrder(req, false)
}

func (c *Client) applyClientHints(req *http.Request) {
	for name, value := range c.fingerprint.clientHints() {
		req.Header.Set(name, value)
	}
}

func (c *Client) applyHeaderOrder(req *http.Request, navigation bool) {
	req.Header[http.PHeaderOrderKey] = []string{":method", ":authority", ":scheme", ":path"}
	if c.fingerprint.Browser == "Firefox" {
		if navigation {
			req.Header[http.HeaderOrderKey] = []string{
				"user-agent", "accept", "accept-language", "accept-encoding",
				"upgrade-insecure-requests", "sec-fetch-dest", "sec-fetch-mode",
				"sec-fetch-site", "sec-fetch-user", "cookie",
			}
			return
		}
		req.Header[http.HeaderOrderKey] = []string{
			"user-agent", "accept", "accept-language", "accept-encoding",
			"content-type", "origin", "x-goog-ext-525001261-jspb",
			"x-goog-ext-525005358-jspb", "x-goog-ext-73010989-jspb",
			"x-goog-ext-73010990-jspb", "x-same-domain", "sec-fetch-dest",
			"sec-fetch-mode", "sec-fetch-site", "referer", "cookie",
		}
		return
	}
	if navigation {
		req.Header[http.HeaderOrderKey] = []string{
			"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
			"upgrade-insecure-requests", "user-agent", "accept",
			"sec-fetch-site", "sec-fetch-mode", "sec-fetch-user",
			"sec-fetch-dest", "accept-encoding", "accept-language", "cookie",
		}
		return
	}
	req.Header[http.HeaderOrderKey] = []string{
		"content-type", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
		"user-agent", "accept", "origin", "x-goog-ext-525001261-jspb",
		"x-goog-ext-525005358-jspb", "x-goog-ext-73010989-jspb",
		"x-goog-ext-73010990-jspb", "x-same-domain", "sec-fetch-site",
		"sec-fetch-mode", "sec-fetch-dest", "referer", "accept-encoding",
		"accept-language", "cookie",
	}
}

func (c *Client) nextReqID() int64 {
	return c.reqID.Add(1)
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
		return fmt.Errorf("account %q has no cookie refresh source", c.displayAccountID())
	}
	cookies, err := c.refresh(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return retryableProtocolError(fmt.Sprintf("account %q refresh cookies: %v", c.displayAccountID(), err), err)
	}
	if len(cookies) == 0 {
		return retryableProtocolError(fmt.Sprintf("account %q refresh returned no cookies", c.displayAccountID()), nil)
	}
	if err := c.replaceCookies(cookies); err != nil {
		return retryableProtocolError(fmt.Sprintf("account %q install refreshed cookies: %v", c.displayAccountID(), err), err)
	}
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

// CloseIdleConnections 关闭协议客户端的空闲连接
func (c *Client) CloseIdleConnections() {
	c.httpClient.CloseIdleConnections()
}

func isAuthenticationError(err error) bool {
	var protocolErr *ProtocolError
	return errors.As(err, &protocolErr) && (protocolErr.HTTPStatus == http.StatusUnauthorized || protocolErr.HTTPStatus == http.StatusForbidden || protocolErr.Code == http.StatusUnauthorized || protocolErr.Code == http.StatusForbidden)
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

func initialReqID() (int64, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, fmt.Errorf("create initial request id: %w", err)
	}
	return int64(100000 + binary.BigEndian.Uint32(value[:])%900000), nil
}

func httpStatusError(status int, operation string) *ProtocolError {
	return &ProtocolError{
		HTTPStatus: status,
		Code:       status,
		Message:    fmt.Sprintf("gemini %s returned HTTP %d", operation, status),
		Retryable:  status == 401 || status == 403 || status == 429 || status >= 500,
	}
}

func transportProtocolError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return retryableProtocolError(fmt.Sprintf("gemini %s: %v", operation, err), err)
}

func retryableProtocolError(message string, cause error) *ProtocolError {
	return &ProtocolError{HTTPStatus: http.StatusBadGateway, Code: http.StatusBadGateway, Message: message, Retryable: true, Cause: cause}
}
