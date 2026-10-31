package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
)

var accountFallback atomic.Bool

func init() {
	accountFallback.Store(true)
}

// ConfigureAccountFallback 设置当账号遇到模型或上游错误时是否自动换号重试
func ConfigureAccountFallback(enabled bool) {
	accountFallback.Store(enabled)
}

// AccountFallbackEnabled 报告是否开启了账号故障自动换号重试
func AccountFallbackEnabled() bool {
	return accountFallback.Load()
}

// generationMedia 是请求对生成媒体的要求
type generationMedia struct {
	// requireImage 表示请求必须返回生成图片
	requireImage bool
	// aspectRatio 为生成媒体的宽高比
	aspectRatio string
}

type generationResult struct {
	Accumulator    *EventAccumulator
	Client         *gemini.Client
	ConversationID string
	ResponseID     string
	Model          string
	ProviderModel  string
	// StopSequence 是截断正文的停止序列
	StopSequence string
}

type adapterEmitError struct{ err error }

func (e adapterEmitError) Error() string { return e.err.Error() }
func (e adapterEmitError) Unwrap() error { return e.err }

type accountContextError struct {
	err *gemini.ProtocolError
}

func (e *accountContextError) Error() string { return e.err.Error() }
func (e *accountContextError) Unwrap() error { return e.err }

// outputMismatchError 是回答不符合请求要求的输出形式时的可重试错误，validation 表示结构化输出没有通过解析或校验
type outputMismatchError struct {
	err        *gemini.ProtocolError
	validation bool
}

func (e *outputMismatchError) Error() string { return e.err.Error() }
func (e *outputMismatchError) Unwrap() error { return e.err }

type streamProjection struct {
	bufferText     bool
	bufferThought  bool
	includeThought bool
	emittedText    bool
	emittedThought bool
	textRunes      int
	visibleOutput  bool
	errorSent      bool
}

func newStreamProjection(bridge ToolBridge, includeThought bool) *streamProjection {
	disallowHostedTools := bridge.disallowsHostedTools()
	return &streamProjection{
		bufferText:     len(bridge.Definitions) > 0 || len(bridge.JSONSchema) > 0 || len(bridge.Enum) > 0 || bridge.WebSearch || bridge.RequireImageGeneration || bridge.RequireCodeExecution || disallowHostedTools,
		bufferThought:  bridge.RequireImageGeneration || disallowHostedTools,
		includeThought: includeThought,
	}
}

func (p *streamProjection) project(event gemini.Event, emit func(gemini.Event) error, emitError func(error) error) error {
	if event.Candidate != 0 {
		return nil
	}
	if event.Kind == gemini.EventCode || event.Kind == gemini.EventMedia {
		p.bufferText = true
		return nil
	}
	if event.Kind != gemini.EventText && event.Kind != gemini.EventThought {
		return nil
	}
	if event.Kind == gemini.EventThought && !p.includeThought {
		return nil
	}
	buffered := &p.bufferText
	emitted := &p.emittedText
	if event.Kind == gemini.EventThought {
		buffered = &p.bufferThought
		emitted = &p.emittedThought
	}
	if event.Operation == gemini.SnapshotAppend {
		if event.Kind == gemini.EventText && !p.emittedText && strings.TrimSpace(event.Delta) == "" {
			return nil
		}
		if *buffered || event.Delta == "" {
			return nil
		}
		if err := emit(event); err != nil {
			return err
		}
		*emitted = true
		if event.Kind == gemini.EventText {
			p.textRunes += len([]rune(event.Delta))
		}
		p.visibleOutput = true
		return nil
	}
	if !*emitted {
		*buffered = true
		return nil
	}
	err := fmt.Errorf("Gemini Web 在流式输出后改写了%s快照", streamEventName(event.Kind))
	p.errorSent = true
	if emitError != nil {
		if writeErr := emitError(err); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func (p *streamProjection) hasVisibleOutput() bool {
	return p.visibleOutput
}

func streamEventName(kind gemini.EventKind) string {
	if kind == gemini.EventThought {
		return "思考"
	}
	return "文本"
}

// sessionPolicy 是带会话键的请求在会话未登记或无法续接时的处理方式
type sessionPolicy int

const (
	// sessionCreate 在会话键未登记时新建会话并登记到该键
	sessionCreate sessionPolicy = iota
	// sessionContinue 只续接已登记的会话
	sessionContinue
	// sessionRebuild 续接已登记的会话，会话未登记或无法续接时在首个可见输出前以重建的完整历史新建会话
	sessionRebuild
)

// previousResponsePolicy 返回只有会话标识的请求的处理方式：带 previous_response_id 时只续接已登记的会话
func previousResponsePolicy(previousResponseID string) sessionPolicy {
	if previousResponseID != "" {
		return sessionContinue
	}
	return sessionCreate
}

func runGeneration(
	ctx context.Context,
	pool *balancer.AccountPool,
	sessionKey string,
	policy sessionPolicy,
	modelName string,
	responseID string,
	media generationMedia,
	thinkingLevel gemini.ThinkingLevel,
	bridge ToolBridge,
	prepare func(*gemini.Client, bool) (string, []gemini.FileData, error),
	onEvent func(gemini.Event) error,
	hasVisibleOutput func() bool,
) (generationResult, string, error) {
	trace := logging.FromContext(ctx)
	result, accountID, err := runGenerationAttempts(ctx, pool, sessionKey, policy, modelName, responseID, media, thinkingLevel, bridge, prepare, onEvent, hasVisibleOutput)
	err = accountSessionError(cancelledError(ctx, err))
	if err != nil {
		trace.Fail(upstreamStatus(err), err.Error())
		return result, accountID, err
	}
	if usage := result.Accumulator.Usage; usage != nil {
		trace.SetUsage(usage.PromptTokens, usage.ThoughtTokens, usage.CompletionTokens)
	}
	trace.SetResult(result.ProviderModel, result.ConversationID, string(result.Accumulator.FinishReason))
	if len(bridge.Definitions) > 0 {
		if _, calls, err := bridge.Parse(result.Accumulator.Primary().Text); err == nil {
			trace.SetToolCalls(len(calls))
		}
	}
	return result, accountID, nil
}

// cancelledError 在管理端取消请求时把失败换成 499 取消错误，其余错误原样返回
func cancelledError(ctx context.Context, err error) error {
	if err != nil && errors.Is(context.Cause(ctx), logging.ErrCancelled) {
		return &gemini.ProtocolError{HTTPStatus: logging.StatusClientClosed, Message: logging.ErrCancelled.Error()}
	}
	return err
}

// accountSessionError 把账号会话失效与续签失败改为写明账号原因的 503，客户端凭据错误只由本服务 API key 校验返回 401
func accountSessionError(err error) error {
	if !errors.Is(err, gemini.ErrSignedOut) {
		return err
	}
	return &gemini.ProtocolError{HTTPStatus: http.StatusServiceUnavailable, Message: "Gemini 账号会话失效：" + err.Error(), Retryable: true, Cause: err}
}

func runGenerationAttempts(
	ctx context.Context,
	pool *balancer.AccountPool,
	sessionKey string,
	policy sessionPolicy,
	modelName string,
	responseID string,
	media generationMedia,
	thinkingLevel gemini.ThinkingLevel,
	bridge ToolBridge,
	prepare func(*gemini.Client, bool) (string, []gemini.FileData, error),
	onEvent func(gemini.Event) error,
	hasVisibleOutput func() bool,
) (generationResult, string, error) {
	trace := logging.FromContext(ctx)
	trace.SetResponseID(responseID)
	requestedModel := config.MapModel(modelName)
	if err := modelKnown(pool, modelName); err != nil {
		return generationResult{}, "", err
	}
	rebuildable := policy == sessionRebuild || isHistoryKey(sessionKey)
	if isHistoryKey(sessionKey) && !pool.SessionIdle(sessionKey, requestedModel) {
		sessionKey = ""
	}
	if _, _, _, exists := conversations.get(sessionKey); policy == sessionRebuild && !exists {
		sessionKey = ""
	}
	releaseSession := func() {}
	if policy == sessionCreate && sessionKey != "" {
		var err error
		releaseSession, err = conversations.acquire(ctx, sessionKey)
		if err != nil {
			return generationResult{}, "", err
		}
	}
	defer releaseSession()
	newSession := policy == sessionCreate || sessionKey == ""
	if policy == sessionCreate && sessionKey != "" {
		_, _, _, exists := conversations.get(sessionKey)
		newSession = !exists
	}

	availableAccounts := pool.Size()
	if availableAccounts == 0 {
		return generationResult{}, "", &gemini.ProtocolError{HTTPStatus: 503, Message: "没有可用账号", Retryable: true}
	}
	fallbackEnabled := AccountFallbackEnabled()
	requestAttempts := 1
	if sessionKey == "" || newSession {
		requestAttempts = 2
	}
	if fallbackEnabled && rebuildable {
		requestAttempts = availableAccounts
	}
	excludedAccountIDs := make(map[string]struct{})
	usedRequestAttempts := 0
	lastAccountID := ""
	var lastErr error
	reconnectedAccounts := make(map[string]bool)
	for len(excludedAccountIDs) < availableAccounts {
		selectionKey := sessionKey
		if newSession {
			selectionKey = ""
		}
		trace.SetStage("等待账号")
		client, accountID, releaseRequest, err := pool.AcquireForModel(ctx, selectionKey, requestedModel, excludedAccountIDs)
		if err != nil {
			return generationResult{}, accountID, err
		}
		if client == nil {
			if sessionKey != "" && !newSession && accountID != "" {
				if rebuildable {
					sessionKey, newSession = "", true
					if fallbackEnabled {
						requestAttempts = availableAccounts
					} else {
						requestAttempts = 2
					}
					continue
				}
				return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 503, Message: fmt.Sprintf("会话所在账号 %s，会话没有保存的响应链，无法在其他账号续接", pool.AccountReason(accountID, requestedModel))}
			}
			if lastErr != nil {
				return generationResult{}, lastAccountID, lastErr
			}
			return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 503, Message: "没有可用账号", Retryable: true}
		}
		reconnected := false
		result, err := func() (generationResult, error) {
			defer releaseRequest()
			result, err := runGenerationWithClient(
				ctx, pool, client, accountID, sessionKey, newSession, requestedModel, responseID,
				media, thinkingLevel, bridge, prepare, onEvent,
			)
			if err == nil {
				pool.ReportSuccess(accountID, requestedModel)
				return result, nil
			}
			if ctx.Err() == nil && retryableAccountError(err) && !isOutputMismatchError(err) {
				if gemini.IsAuthenticationError(err) && (hasVisibleOutput == nil || !hasVisibleOutput()) && !reconnectedAccounts[accountID] {
					reconnected = true
					return result, err
				}
				if usageLimitError(err) {
					_, _ = client.FetchUsage(ctx)
				}
				pool.ReportFailure(accountID, requestedModel, err)
			}
			return result, err
		}()
		if err == nil {
			result.Model = requestedModel
			return result, accountID, nil
		}
		lastErr = err
		lastAccountID = accountID
		if ctx.Err() != nil {
			return generationResult{}, accountID, err
		}
		if reconnected {
			reconnectedAccounts[accountID] = true
			trace.Stage("会话失效，自动重新连接", "账号", accountID)
			logging.Info(accountID, "检测到账号会话失效，尝试自动重新连接一次")
			reconnectErr := pool.Reconnect(ctx, accountID)
			if reconnectErr == nil {
				logging.Info(accountID, "自动重新连接成功，无感重试当前请求")
				trace.Stage("自动重新连接成功，重试请求", "账号", accountID)
				delete(excludedAccountIDs, accountID)
				continue
			}
			logging.Warn(accountID, "自动重新连接失败，恢复原错误", "错误", reconnectErr)
			trace.Stage("自动重新连接失败", "账号", accountID, "错误", reconnectErr)
			pool.ReportFailure(accountID, requestedModel, err)
		}
		if errors.Is(err, gemini.ErrConversationLost) && rebuildable && !newSession && (hasVisibleOutput == nil || !hasVisibleOutput()) {
			trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
			sessionKey, newSession = "", true
			if fallbackEnabled {
				requestAttempts = availableAccounts
			} else {
				requestAttempts = 2
			}
			continue
		}
		if rebuildable && sessionKey != "" && !newSession && (isAccountContextError(err) || errors.Is(err, gemini.ErrGuestAttachments) || retryableAccountError(err) && (hasVisibleOutput == nil || !hasVisibleOutput())) {
			trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
			excludedAccountIDs[accountID] = struct{}{}
			sessionKey, newSession = "", true
			if fallbackEnabled {
				requestAttempts = availableAccounts
			} else {
				requestAttempts = 2
			}
			continue
		}
		if isAccountContextError(err) {
			if sessionKey != "" && !newSession {
				return generationResult{}, accountID, err
			}
			trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
			excludedAccountIDs[accountID] = struct{}{}
			continue
		}
		usedRequestAttempts++
		if usedRequestAttempts >= requestAttempts || !retryableAccountError(err) ||
			hasVisibleOutput != nil && hasVisibleOutput() {
			trace.AttemptFailed(accountID, upstreamStatus(err), err, false)
			return generationResult{}, accountID, err
		}
		trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
		excludedAccountIDs[accountID] = struct{}{}
	}
	return generationResult{}, lastAccountID, lastErr
}

func runGenerationWithClient(
	ctx context.Context,
	pool *balancer.AccountPool,
	client *gemini.Client,
	accountID string,
	sessionKey string,
	allowNewSession bool,
	requestedModel string,
	responseID string,
	media generationMedia,
	thinkingLevel gemini.ThinkingLevel,
	bridge ToolBridge,
	prepare func(*gemini.Client, bool) (string, []gemini.FileData, error),
	onEvent func(gemini.Event) error,
) (generationResult, error) {
	trace := logging.FromContext(ctx)
	trace.AccountReady(accountID, requestedModel)

	model, err := client.ResolveModel(requestedModel)
	if err != nil {
		return generationResult{}, &gemini.ProtocolError{HTTPStatus: 404, Message: err.Error()}
	}
	var conversation *gemini.ConversationState
	continuation := false
	branch := false
	contextTokens := 0
	if sessionKey != "" {
		var boundAccountID string
		var ok bool
		conversation, boundAccountID, contextTokens, ok = conversations.get(sessionKey)
		if !ok {
			if !allowNewSession {
				pool.ClearSession(sessionKey)
				return generationResult{}, &gemini.ProtocolError{HTTPStatus: 400, Message: fmt.Sprintf("未知会话标识 %q", sessionKey)}
			}
			conversation = gemini.NewConversationState()
			boundAccountID = accountID
		} else {
			continuation = true
			branch = !conversations.isLatest(conversation.Snapshot())
		}
		if boundAccountID != accountID {
			return generationResult{}, &gemini.ProtocolError{HTTPStatus: 409, Message: "会话绑定账号已变化，请开始新会话"}
		}
	}
	prompt, files, err := prepare(client, continuation)
	trace.SetMedia(len(files))
	if err != nil {
		var protocolErr *gemini.ProtocolError
		if errors.As(err, &protocolErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return generationResult{}, err
		}
		return generationResult{}, &gemini.ProtocolError{HTTPStatus: 400, Message: err.Error()}
	}
	promptTokens := contextTokens + tokencount.Content(prompt) + mediaTokens(files)
	if contextWindow := client.ContextWindow(); contextWindow > 0 && promptTokens > contextWindow {
		return generationResult{}, &accountContextError{err: &gemini.ProtocolError{
			HTTPStatus: 413,
			Message:    fmt.Sprintf("账号上下文窗口为 %d tokens，当前请求需要 %d tokens", contextWindow, promptTokens),
		}}
	}
	feature := gemini.Feature(0)
	if media.requireImage {
		feature = gemini.FeatureImage
	}
	accumulator := NewEventAccumulator()
	initialUsageSent := false
	cutter := newStopCutter(bridge.Stop)
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	err = client.Stream(streamCtx, gemini.GenerateRequest{
		Prompt:        prompt,
		Model:         model.ID,
		ThinkingLevel: thinkingLevel,
		Feature:       feature,
		AspectRatio:   media.aspectRatio,
		Files:         files,
		Conversation:  conversation,
		Branch:        branch,
	}, func(event gemini.Event) error {
		if cutter != nil && cutter.matched != "" {
			return nil
		}
		switch event.Kind {
		case gemini.EventText, gemini.EventThought, gemini.EventCode, gemini.EventMedia, gemini.EventImageProgress:
			if event.Candidate == 0 {
				trace.UpstreamEvent(string(event.Kind))
			}
		}
		if event.Kind == gemini.EventMetadata && event.Metadata != nil {
			metadata := *event.Metadata
			metadata.ModelName = actualProviderModel(client, model.ID, event.Metadata)
			event.Metadata = &metadata
		}
		if onEvent != nil && !initialUsageSent && event.Kind != gemini.EventError {
			initialUsageSent = true
			usage := &gemini.Usage{PromptTokens: promptTokens, TotalTokens: promptTokens}
			if err := onEvent(gemini.Event{Kind: gemini.EventMetadata, Usage: usage}); err != nil {
				return adapterEmitError{err: err}
			}
		}
		if err := accumulator.Apply(event); err != nil {
			return err
		}
		if cutter != nil && event.Kind == gemini.EventText && event.Candidate == 0 {
			delta, hit := cutter.cut(accumulator.Primary().Text)
			if hit {
				stopStream()
			}
			if event.Operation == gemini.SnapshotAppend {
				if delta == "" {
					return nil
				}
				event = gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: delta}
			}
		}
		if onEvent != nil {
			if err := onEvent(event); err != nil {
				return adapterEmitError{err: err}
			}
		}
		return nil
	})
	if cutter != nil && cutter.matched != "" && errors.Is(err, context.Canceled) && ctx.Err() == nil {
		err = nil
	}
	if err == nil && cutter != nil {
		if cutter.matched != "" {
			accumulator.candidate(0).Text = cutter.text[:cutter.cutAt]
			accumulator.FinishReason = gemini.FinishStop
		} else if rest := cutter.rest(); rest != "" && onEvent != nil {
			if err := onEvent(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: rest}); err != nil {
				return generationResult{}, adapterEmitError{err: err}
			}
		}
	}
	if err != nil {
		if usage := estimatedUsage(accumulator, promptTokens); usage.CompletionTokens+usage.ThoughtTokens > 0 {
			trace.SetUsage(usage.PromptTokens, usage.ThoughtTokens, usage.CompletionTokens)
		}
		return generationResult{}, err
	}
	if media.requireImage {
		generatedImage := false
		for _, media := range accumulator.Primary().Media {
			if media.Type == gemini.MediaGeneratedImage {
				generatedImage = true
				break
			}
		}
		if !generatedImage {
			return generationResult{}, &outputMismatchError{err: &gemini.ProtocolError{HTTPStatus: 502, Message: "Gemini Web 未返回生成图片", Retryable: true}}
		}
	}
	primary := accumulator.Primary()
	if primary.Text == "" && len(primary.Codes) == 0 && len(primary.Media) == 0 && (cutter == nil || cutter.matched == "") {
		return generationResult{}, &outputMismatchError{err: &gemini.ProtocolError{HTTPStatus: 502, Message: "Gemini Web 完成了回答但没有正文、代码、媒体或工具调用", Retryable: true}}
	}
	if len(bridge.Definitions) > 0 || len(bridge.JSONSchema) > 0 || len(bridge.Enum) > 0 {
		if _, _, err := bridge.Parse(primary.Text); err != nil {
			return generationResult{}, &outputMismatchError{err: &gemini.ProtocolError{HTTPStatus: 502, Message: err.Error(), Retryable: true}, validation: len(bridge.Definitions) == 0}
		}
	}
	if accumulator.Usage == nil {
		accumulator.Usage = estimatedUsage(accumulator, promptTokens)
	}
	nextContextTokens := accumulator.Usage.PromptTokens + accumulator.Usage.CompletionTokens + 1

	if conversation == nil && accumulator.Session.CID != "" {
		session := accumulator.Session
		session.ModelMode = model.Mode
		conversation = gemini.NewConversationStateFrom(session)
	}
	if conversation != nil {
		snapshot := conversation.Snapshot()
		if accumulator.Session.CID != "" {
			conversation.Update(accumulator.Session)
			snapshot = conversation.Snapshot()
		}
		keys := []string{responseID, snapshot.CID}
		remember := allowNewSession && sessionKey != "" && !isHistoryKey(sessionKey)
		if remember {
			keys = append(keys, sessionKey)
		}
		conversations.put(conversation, accountID, nextContextTokens, keys...)
		pool.BindSession(responseID, accountID)
		pool.BindSession(snapshot.CID, accountID)
		if remember {
			pool.BindSession(sessionKey, accountID)
		}
		accumulator.Session = snapshot
	}

	providerModel := actualProviderModel(client, model.ID, accumulator.Metadata)
	for _, media := range accumulator.Primary().Media {
		if media.Type == gemini.MediaGeneratedImage && media.Generator != "" {
			providerModel = media.Generator
			break
		}
	}
	result := generationResult{
		Accumulator:    accumulator,
		Client:         client,
		ConversationID: accumulator.Session.CID,
		ResponseID:     responseID,
		ProviderModel:  providerModel,
	}
	if cutter != nil {
		result.StopSequence = cutter.matched
	}
	return result, nil
}

// estimatedUsage 按已收到的正文、代码与思考的本地计数估算用量
func estimatedUsage(accumulator *EventAccumulator, promptTokens int) *gemini.Usage {
	primary := accumulator.Primary()
	completionTokens := tokencount.Text(primary.Text)
	for _, code := range primary.Codes {
		completionTokens += tokencount.Text(code.Content)
	}
	thoughtTokens := tokencount.Text(primary.Thought)
	return &gemini.Usage{
		PromptTokens: promptTokens, CompletionTokens: completionTokens, ThoughtTokens: thoughtTokens,
		TotalTokens: promptTokens + completionTokens + thoughtTokens,
	}
}

func retryableAccountError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var emitErr adapterEmitError
	if errors.As(err, &emitErr) {
		return false
	}
	var protocolErr *gemini.ProtocolError
	if errors.As(err, &protocolErr) {
		return protocolErr.Retryable
	}
	return true
}

func isOutputMismatchError(err error) bool {
	var mismatch *outputMismatchError
	return errors.As(err, &mismatch) || errors.Is(err, gemini.ErrUpstreamFailureText)
}

func isAccountContextError(err error) bool {
	var contextErr *accountContextError
	return errors.As(err, &contextErr)
}

func actualProviderModel(client *gemini.Client, requestedModel string, metadata *gemini.EventMetadataData) string {
	if metadata == nil {
		return requestedModel
	}
	for _, value := range []string{metadata.ModelHash, metadata.ModelName} {
		if value == "" {
			continue
		}
		if model, err := client.ResolveModel(value); err == nil {
			return model.ID
		}
	}
	if metadata.ModelName != "" {
		return metadata.ModelName
	}
	if metadata.ModelHash != "" {
		return metadata.ModelHash
	}
	return requestedModel
}

// usageLimitError 判断错误是否为官网用量上限 1037、1173 或用量用满后的模型降级，此时先读取账号用量再按其重置时刻冷却
func usageLimitError(err error) bool {
	var protocolErr *gemini.ProtocolError
	return errors.As(err, &protocolErr) && (protocolErr.Code == 1037 || protocolErr.Code == 1173) || errors.Is(err, gemini.ErrModelMismatch)
}
