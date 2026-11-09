package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
)

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

// errSnapshotDiverged 是上游改写了已发给客户端的内容且找不到继续输出的位置时的错误，生成循环据此按已发出内容接续生成
var errSnapshotDiverged = &gemini.ProtocolError{HTTPStatus: 502, Message: "Gemini Web 改写了已发出的内容"}

type streamProjection struct {
	bridge        ToolBridge
	bufferText    bool
	bufferThought bool
	// gateCalls 表示正文扣到结束时仍实时输出调用块之前的部分
	gateCalls bool
	// heldText 是已到达但可能属于调用块开头的正文尾部
	heldText       string
	includeThought bool
	// sentText 是已发出的正文
	sentText strings.Builder
	// sentThought 是已发出的思考
	sentThought strings.Builder
	// textRunes 是已发出的正文在当前正文快照中的 rune 位置
	textRunes int
	// textRewritten 表示上游改写过已发出的正文
	textRewritten bool
	visibleOutput bool
}

func newStreamProjection(bridge ToolBridge, includeThought bool) *streamProjection {
	disallowHostedTools := bridge.disallowsHostedTools()
	holdAll := len(bridge.JSONSchema) > 0 || len(bridge.Enum) > 0 || bridge.WebSearch || bridge.RequireImageGeneration || bridge.RequireCodeExecution || bridge.RequireHostedTool || disallowHostedTools || fakeStream.Load()
	return &streamProjection{
		bridge:         bridge,
		bufferText:     len(bridge.Definitions) > 0 || holdAll,
		bufferThought:  bridge.RequireImageGeneration || disallowHostedTools || fakeStream.Load(),
		gateCalls:      len(bridge.Definitions) > 0 && !holdAll,
		includeThought: includeThought,
	}
}

func (p *streamProjection) project(event gemini.Event, emit func(gemini.Event) error) error {
	if usageOnly(event) && !p.visibleOutput {
		*p = *newStreamProjection(p.bridge, p.includeThought)
		return nil
	}
	if event.Candidate != 0 {
		return nil
	}
	if event.Kind == gemini.EventCode || event.Kind == gemini.EventMedia {
		p.bufferText = true
		p.gateCalls = false
		return nil
	}
	if event.Kind != gemini.EventText && event.Kind != gemini.EventThought {
		return nil
	}
	if event.Kind == gemini.EventThought && !p.includeThought {
		return nil
	}
	if event.Operation != gemini.SnapshotAppend {
		var aligned bool
		if event, aligned = p.realign(event); !aligned {
			return errSnapshotDiverged
		}
	}
	if event.Kind == gemini.EventText && p.gateCalls {
		safe, held, call := splitCallGate(p.sentText.String(), p.heldText+event.Delta)
		p.heldText = held
		p.gateCalls = !call
		if safe == "" {
			return nil
		}
		event.Delta = safe
		return p.send(event, emit)
	}
	buffered := p.bufferText
	if event.Kind == gemini.EventThought {
		buffered = p.bufferThought
	}
	if event.Kind == gemini.EventText && p.sentText.Len() == 0 {
		event.Delta = p.heldText + event.Delta
		if strings.TrimSpace(event.Delta) == "" {
			p.heldText = event.Delta
			return nil
		}
		p.heldText = ""
	}
	if buffered || event.Delta == "" {
		return nil
	}
	return p.send(event, emit)
}

// keepSentText 在上游改写过已发出内容时把最终正文换成客户端收到的内容加上尚未发出的部分
func (p *streamProjection) keepSentText(output *CandidateOutput) {
	if !p.textRewritten {
		return
	}
	text := []rune(output.Text)
	output.Text = p.sentText.String() + string(text[min(p.textRunes, len(text)):])
}

// send 发出一段增量并记录已发出的内容
func (p *streamProjection) send(event gemini.Event, emit func(gemini.Event) error) error {
	if err := emit(event); err != nil {
		return err
	}
	if event.Kind == gemini.EventThought {
		p.sentThought.WriteString(event.Delta)
	} else {
		p.sentText.WriteString(event.Delta)
		p.textRunes += len([]rune(event.Delta))
	}
	p.visibleOutput = true
	return nil
}

// realign 把改写快照的事件换算成已发出内容之后的追加，并把发出位置移到已发出内容在新快照中的结尾，已发出内容保持客户端收到的原文，找不到结尾时 aligned 为 false
func (p *streamProjection) realign(event gemini.Event) (result gemini.Event, aligned bool) {
	sent := p.sentText.String()
	if event.Kind == gemini.EventThought {
		sent = p.sentThought.String()
	}
	snapshot := []rune(event.Snapshot)
	position, aligned := alignedLength([]rune(sent), snapshot)
	if !aligned {
		return event, false
	}
	if event.Kind == gemini.EventText {
		p.textRunes, p.heldText = position, ""
		p.textRewritten = p.textRewritten || !strings.HasPrefix(event.Snapshot, sent)
	}
	event.Delta = string(snapshot[position:])
	event.Operation = gemini.SnapshotAppend
	return event, true
}

const (
	// alignWindow 是重新定位时比较的已发出内容末尾的最大 rune 数
	alignWindow = 200
	// alignMinimum 是认定为同一段内容的最短末尾 rune 数
	alignMinimum = 8
)

// alignedLength 返回已发出内容的结尾在新快照中的 rune 位置：已发出内容仍是快照前缀时为其长度，否则取其末尾在快照中最接近原位置的匹配，没有匹配时 aligned 为 false
func alignedLength(sent, snapshot []rune) (position int, aligned bool) {
	prefix := 0
	for prefix < len(sent) && prefix < len(snapshot) && sent[prefix] == snapshot[prefix] {
		prefix++
	}
	if prefix == len(sent) {
		return prefix, true
	}
	text, expected := string(snapshot), len(string(sent))
	distance := func(end int) int { return max(end-expected, expected-end) }
	for size := min(len(sent), alignWindow); size >= alignMinimum; size-- {
		tail := string(sent[len(sent)-size:])
		best := -1
		for offset := 0; ; {
			index := strings.Index(text[offset:], tail)
			if index < 0 {
				break
			}
			if end := offset + index + len(tail); best < 0 || distance(end) < distance(best) {
				best = end
			}
			offset += index + 1
		}
		if best >= 0 {
			return utf8.RuneCountInString(text[:best]), true
		}
	}
	return 0, false
}

// partialOutput 是上一次生成已发给客户端的内容，下一次生成接在它之后输出
type partialOutput struct {
	text    string
	thought string
}

// resume 返回已发出的内容并恢复生成开始时的暂扣状态，暂扣的正文作废
func (p *streamProjection) resume() partialOutput {
	fresh := newStreamProjection(p.bridge, p.includeThought)
	p.bufferText, p.bufferThought, p.gateCalls, p.heldText = fresh.bufferText, fresh.bufferThought, fresh.gateCalls, ""
	return partialOutput{text: p.sentText.String(), thought: p.sentThought.String()}
}

// assistantTurnStart 是转录末尾留出的助手轮开头
const assistantTurnStart = "<|im_start|>assistant\n"

// resumeInstruction 是接着已发出内容生成时写在助手轮之前的系统说明
const resumeInstruction = "上一次回答在输出途中被网络中断，下面的助手轮是已经发给用户的内容。从中断处的下一个字符接着写，不要重复已写内容，不要加任何说明。"

// resumedPrompt 把已发出的正文写在提示词末尾的助手轮中，模型从中断处接着写
func resumedPrompt(prompt, sent string) string {
	prompt, transcript := strings.CutSuffix(prompt, assistantTurnStart)
	if !transcript {
		prompt = "<|im_start|>user\n" + strings.TrimRightFunc(prompt, unicode.IsSpace) + "\n<|im_end|>\n"
	}
	return prompt + "<|im_start|>system\n" + resumeInstruction + "\n<|im_end|>\n" + assistantTurnStart + sent
}

// resumeAligner 去掉接续生成开头对已发出正文的复述
type resumeAligner struct {
	sent    string
	decided bool
	skip    int
	// held 是仍可能是复述而暂扣的快照
	held string
}

// align 返回生成快照中接在已发出正文之后的部分，快照仍可能是复述时 ok 为 false
func (a *resumeAligner) align(snapshot string) (rest string, ok bool) {
	if !a.decided {
		if strings.Contains(a.sent, snapshot) {
			a.held = snapshot
			return "", false
		}
		a.decide(snapshot)
	}
	return snapshot[min(a.skip, len(snapshot)):], true
}

// decide 按快照开头与已发出正文结尾的重合确定跳过的长度
func (a *resumeAligner) decide(snapshot string) {
	a.decided = true
	for size := min(len(a.sent), len(snapshot)); size >= alignMinimum && a.skip == 0; size-- {
		if strings.HasSuffix(a.sent, snapshot[:size]) {
			a.skip = size
		}
	}
}

// finish 在生成结束时结算暂扣的快照，返回其中接在已发出正文之后的部分
func (a *resumeAligner) finish() string {
	if a.decided || a.held == "" {
		return ""
	}
	a.decide(a.held)
	return a.held[min(a.skip, len(a.held)):]
}

// partialCallPattern 匹配正文末尾可能是调用块开头前缀的部分
var partialCallPattern = regexp.MustCompile(`\\?\[\s*(?:C(?:a(?:l(?:l\s*(?:\\?(?::[^\]\r\n\\]*\\?)?)?)?)?)?)?$|\\$`)

// splitCallGate 把 sent 之后待输出的正文分为可以输出的部分与暂扣的尾部：代码以外出现调用块开头时从调用块起全部暂扣，前面有未闭合的反引号串时所在行写完才报告 call，其余情况立即报告；否则暂扣可能是调用块开头前缀的尾部与末尾空白
func splitCallGate(sent, text string) (safe string, held string, call bool) {
	cut := len(text)
	if callStartPattern.MatchString(text) {
		all := sent + text
		if location := firstCallStart(all); location != nil {
			_, unclosed := codeSpanAt(all, location[0])
			cut, call = max(location[0]-len(sent), 0), !unclosed || strings.Contains(all[location[1]:], "\n")
		}
	}
	if location := partialCallPattern.FindStringIndex(text); location != nil && !call && location[0] < cut {
		cut = location[0]
	}
	cut = len(strings.TrimRightFunc(text[:cut], unicode.IsSpace))
	return text[:cut], text[cut:], call
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
	projection *streamProjection,
) (generationResult, string, error) {
	trace := logging.FromContext(ctx)
	result, accountID, err := runGenerationAttempts(ctx, pool, sessionKey, policy, modelName, responseID, media, thinkingLevel, bridge, prepare, onEvent, projection)
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
	projection *streamProjection,
) (generationResult, string, error) {
	trace := logging.FromContext(ctx)
	trace.SetResponseID(responseID)
	visibleOutput := func() bool { return projection != nil && projection.visibleOutput }
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

	excludedAccountIDs := make(map[string]struct{})
	availableAccounts := pool.Size()
	if availableAccounts == 0 {
		return generationResult{}, "", noAccountError(ctx, pool, modelName)
	}
	requestAttempts := 1
	if sessionKey == "" || newSession {
		requestAttempts = availableAccounts
	}
	usedRequestAttempts := 0
	regenerated, regenerating := 0, false
	var resume partialOutput
	lastAccountID := ""
	var lastErr error
	// recoverAccounts 在账号都因频率限制不可用且一分钟内恢复时等待恢复并重新选号，首个可见输出之后不等待
	recoverAccounts := func() bool {
		if visibleOutput() || regenerated >= regenerations || !awaitAccountRecovery(ctx, pool, requestedModel) {
			return false
		}
		regenerated++
		usedRequestAttempts = 0
		clear(excludedAccountIDs)
		return true
	}
	// attemptsLeft 判断还能继续选号，账号都已试过且最后的错误是频率限制时先等待恢复
	attemptsLeft := func() bool {
		if len(excludedAccountIDs) < availableAccounts || regenerating {
			return true
		}
		return upstreamStatus(lastErr) == http.StatusTooManyRequests && recoverAccounts()
	}
	for attemptsLeft() {
		selectionKey := sessionKey
		if newSession {
			selectionKey = ""
		}
		trace.SetStage("等待账号")
		client, accountID, releaseRequest, err := pool.AcquireForModel(ctx, selectionKey, requestedModel, excludedAccountIDs)
		if err != nil {
			return generationResult{}, accountID, err
		}
		if client == nil && regenerating {
			regenerating = false
			clear(excludedAccountIDs)
			continue
		}
		regenerating = false
		if client == nil {
			if sessionKey != "" && !newSession && accountID != "" {
				if rebuildable {
					sessionKey, newSession, requestAttempts = "", true, availableAccounts
					continue
				}
				return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 503, Message: fmt.Sprintf("会话所在账号 %s，会话没有保存的响应链，无法在其他账号续接", pool.AccountReason(ctx, accountID, requestedModel))}
			}
			if (lastErr == nil || upstreamStatus(lastErr) == http.StatusTooManyRequests) && recoverAccounts() {
				continue
			}
			if lastErr != nil {
				return generationResult{}, lastAccountID, lastErr
			}
			return generationResult{}, accountID, noAccountError(ctx, pool, requestedModel)
		}
		attemptSessionKey, attemptNewSession, attemptResume := sessionKey, newSession, resume
		run := func(ctx context.Context, client *gemini.Client, accountID string, emit func(gemini.Event) error) (generationResult, error) {
			return runGenerationWithClient(ctx, pool, client, accountID, attemptSessionKey, attemptNewSession, requestedModel, responseID, media, thinkingLevel, bridge, attemptResume, prepare, emit)
		}
		var target *hedgeTarget
		if delay, ok := hedgeDelay(requestedModel); ok && (newSession || rebuildable) {
			hedgeSessionKey := ""
			if newSession {
				hedgeSessionKey = sessionKey
			}
			target = &hedgeTarget{delay: delay, excluded: excludedAccountIDs, run: func(ctx context.Context, client *gemini.Client, accountID string, emit func(gemini.Event) error) (generationResult, error) {
				return runGenerationWithClient(ctx, pool, client, accountID, hedgeSessionKey, true, requestedModel, responseID, media, thinkingLevel, bridge, attemptResume, prepare, emit)
			}}
		}
		result, winnerID, err := raceFirstEvent(ctx, pool, requestedModel, client, accountID, releaseRequest, run, onEvent, target)
		accountID = winnerID
		if err == nil {
			result.Model = requestedModel
			if projection != nil {
				projection.keepSentText(result.Accumulator.candidate(0))
			}
			return result, accountID, nil
		}
		if lastErr == nil || !isAccountContextError(err) {
			lastErr, lastAccountID = err, accountID
		}
		if ctx.Err() != nil {
			return generationResult{}, accountID, err
		}
		if errors.Is(err, gemini.ErrConversationLost) && rebuildable && !newSession && !visibleOutput() {
			trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
			sessionKey, newSession, requestAttempts = "", true, availableAccounts
			continue
		}
		if errors.Is(err, gemini.ErrGuestAttachments) && !pool.RetryAfter(ctx, requestedModel).IsZero() && (sessionKey == "" || newSession || rebuildable) {
			retrying := recoverAccounts()
			trace.AttemptFailed(accountID, upstreamStatus(err), err, retrying)
			if !retrying {
				return generationResult{}, accountID, noAccountError(ctx, pool, requestedModel)
			}
			sessionKey, newSession, requestAttempts = "", true, availableAccounts
			continue
		}
		if rebuildable && sessionKey != "" && !newSession && (isAccountContextError(err) || errors.Is(err, gemini.ErrGuestAttachments) || retryableAccountError(err) && !visibleOutput()) {
			trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
			excludedAccountIDs[accountID] = struct{}{}
			sessionKey, newSession, requestAttempts = "", true, availableAccounts
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
		if (gemini.TransientError(err) || isOutputMismatchError(err)) && (sessionKey == "" || newSession) && regenerated < regenerations && !visibleOutput() {
			regenerated++
			trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
			excludedAccountIDs[accountID] = struct{}{}
			regenerating = true
			continue
		}
		if (gemini.TransientError(err) || errors.Is(err, gemini.ErrUpstreamFailureText) || errors.Is(err, errSnapshotDiverged)) && (sessionKey == "" || newSession || rebuildable) && regenerated < regenerations && visibleOutput() {
			regenerated++
			trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
			excludedAccountIDs[accountID] = struct{}{}
			regenerating = true
			if sessionKey != "" && !newSession {
				sessionKey, newSession, requestAttempts = "", true, availableAccounts
			}
			resume = projection.resume()
			continue
		}
		usedRequestAttempts++
		if usedRequestAttempts >= requestAttempts || !retryableAccountError(err) || visibleOutput() {
			retrying := upstreamStatus(err) == http.StatusTooManyRequests && retryableAccountError(err) && recoverAccounts()
			trace.AttemptFailed(accountID, upstreamStatus(err), err, retrying)
			if retrying {
				continue
			}
			return generationResult{}, accountID, err
		}
		trace.AttemptFailed(accountID, upstreamStatus(err), err, true)
		excludedAccountIDs[accountID] = struct{}{}
	}
	return generationResult{}, lastAccountID, lastErr
}

// awaitAccountRecovery 在账号全部冷却且最早的账号在 modelRecoveryWindow 内恢复时等到恢复，返回是否等待完成
func awaitAccountRecovery(ctx context.Context, pool *balancer.AccountPool, modelID string) bool {
	recoverAt := pool.RetryAfter(ctx, modelID)
	wait := time.Until(recoverAt)
	if recoverAt.IsZero() || wait > modelRecoveryWindow {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

// regenerations 是生成流中断、5xx、固定失败文字或输出不符合请求要求时重新生成的次数上限，优先换用其他账号，没有其他账号时回到失败的账号；客户端已收到内容时新的生成接在已发出内容之后
const regenerations = 3

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
	resume partialOutput,
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
	if resume.text != "" {
		prompt = resumedPrompt(prompt, resume.text)
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
	accumulator.candidate(0).Text, accumulator.candidate(0).Thought = resume.text, resume.thought
	cutter := newStopCutter(bridge.Stop)
	if cutter != nil {
		cutter.text, cutter.sent, cutter.toolCalls = resume.text, len(resume.text), len(bridge.Definitions) > 0
	}
	aligner := resumeAligner{sent: resume.text}
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	if onEvent != nil {
		if err := onEvent(gemini.Event{Kind: gemini.EventMetadata, Usage: &gemini.Usage{PromptTokens: promptTokens, TotalTokens: promptTokens}}); err != nil {
			return generationResult{}, adapterEmitError{err: err}
		}
	}
	handle := func(event gemini.Event) error {
		if cutter != nil && cutter.matched != "" {
			return nil
		}
		if event.Candidate == 0 && event.Kind == gemini.EventText && resume.text != "" {
			decided := aligner.decided
			rest, ok := aligner.align(event.Snapshot)
			if !ok {
				return nil
			}
			if !decided {
				event.Delta = rest
			}
			event.Snapshot = resume.text + rest
		}
		if event.Candidate == 0 && event.Kind == gemini.EventThought {
			event.Snapshot = resume.thought + event.Snapshot
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
			} else {
				event.Snapshot = cutter.text[:cutter.sent]
			}
		}
		if onEvent != nil {
			if err := onEvent(event); err != nil {
				return adapterEmitError{err: err}
			}
		}
		return nil
	}
	err = client.Stream(streamCtx, gemini.GenerateRequest{
		Prompt:        prompt,
		Model:         model.ID,
		ThinkingLevel: thinkingLevel,
		Feature:       feature,
		AspectRatio:   media.aspectRatio,
		Files:         files,
		Conversation:  conversation,
		Branch:        branch,
	}, handle)
	if err == nil {
		if rest := aligner.finish(); rest != "" {
			err = handle(gemini.Event{Kind: gemini.EventText, Operation: gemini.SnapshotAppend, Delta: rest, Snapshot: aligner.held})
		}
	}
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
	if strings.TrimSpace(primary.Text) == "" && len(primary.Codes) == 0 && len(primary.Media) == 0 && (cutter == nil || cutter.matched == "") {
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

// usageLimitError 判断错误是否为官网用量上限 1037 或用量用满后的模型降级，此时先读取账号用量再按其重置时刻冷却
func usageLimitError(err error) bool {
	var protocolErr *gemini.ProtocolError
	return errors.As(err, &protocolErr) && protocolErr.Code == 1037 || errors.Is(err, gemini.ErrModelMismatch)
}
