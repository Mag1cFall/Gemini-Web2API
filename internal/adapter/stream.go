package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
)

type generationResult struct {
	Accumulator    *EventAccumulator
	Client         *gemini.Client
	ConversationID string
	ResponseID     string
	Model          string
	ProviderModel  string
}

type adapterEmitError struct{ err error }

func (e adapterEmitError) Error() string { return e.err.Error() }
func (e adapterEmitError) Unwrap() error { return e.err }

type accountContextError struct {
	err *gemini.ProtocolError
}

func (e *accountContextError) Error() string { return e.err.Error() }
func (e *accountContextError) Unwrap() error { return e.err }

type outputMismatchError struct {
	err *gemini.ProtocolError
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
		bufferText:     len(bridge.Definitions) > 0 || len(bridge.JSONSchema) > 0 || bridge.WebSearch || bridge.RequireImageGeneration || bridge.RequireCodeExecution || disallowHostedTools,
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
		if !p.emittedText {
			p.bufferText = true
		}
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

func modelStatus(pool *balancer.AccountPool, modelName string) (bool, bool) {
	modelID := config.MapModel(modelName)
	if providerModel, imageModel := resolveImageProvider(pool, modelID); imageModel {
		return true, providerModel != "" && pool.HasReadyModel(providerModel)
	}
	return pool.HasModel(modelID), pool.HasReadyModel(modelID)
}

func runGeneration(
	ctx context.Context,
	pool *balancer.AccountPool,
	sessionKey string,
	allowNewSession bool,
	modelName string,
	responseID string,
	imageGeneration bool,
	thinkingMode gemini.ThinkingMode,
	prepare func(*gemini.Client, bool) (string, []gemini.FileData, error),
	onEvent func(gemini.Event) error,
	hasVisibleOutput func() bool,
) (generationResult, string, error) {
	publicModel := config.MapModel(modelName)
	requestedModel := publicModel
	if providerModel, imageModel := resolveImageProvider(pool, requestedModel); imageModel {
		if providerModel == "" {
			return generationResult{}, "", &gemini.ProtocolError{HTTPStatus: 503, Message: fmt.Sprintf("模型 %q 当前没有可用的网页协调模型", modelName), Retryable: true}
		}
		requestedModel = providerModel
	}
	if !pool.HasModel(requestedModel) {
		return generationResult{}, "", &gemini.ProtocolError{HTTPStatus: 404, Message: fmt.Sprintf("模型 %q 不可用", modelName)}
	}
	releaseSession := func() {}
	if allowNewSession && sessionKey != "" {
		releaseSession = conversations.acquire(sessionKey)
	}
	defer releaseSession()
	newSession := allowNewSession
	if allowNewSession && sessionKey != "" {
		_, _, _, exists := conversations.get(sessionKey)
		newSession = !exists
	}

	requestAttempts := 1
	if sessionKey == "" || newSession {
		requestAttempts = 2
	}
	excludedAccountIDs := make(map[string]struct{})
	availableAccounts := pool.Size()
	if availableAccounts == 0 {
		return generationResult{}, "", &gemini.ProtocolError{HTTPStatus: 503, Message: "没有可用账号", Retryable: true}
	}
	usedRequestAttempts := 0
	lastAccountID := ""
	var lastErr error
	for len(excludedAccountIDs) < availableAccounts {
		selectionKey := sessionKey
		if newSession {
			selectionKey = ""
		}
		client, accountID := pool.NextForModelExcluding(selectionKey, requestedModel, excludedAccountIDs)
		if client == nil {
			if lastErr != nil {
				return generationResult{}, lastAccountID, lastErr
			}
			return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 503, Message: "没有可用账号", Retryable: true}
		}
		result, err := runGenerationWithClient(
			ctx, pool, client, accountID, sessionKey, newSession, requestedModel, responseID,
			imageGeneration, thinkingMode, prepare, onEvent,
		)
		if err == nil {
			pool.ReportSuccess(accountID)
			result.Model = publicModel
			return result, accountID, nil
		}
		lastErr = err
		lastAccountID = accountID
		if isAccountContextError(err) {
			if sessionKey != "" && !newSession {
				return generationResult{}, accountID, err
			}
			excludedAccountIDs[accountID] = struct{}{}
			continue
		}
		usedRequestAttempts++
		if retryableAccountError(err) && !isOutputMismatchError(err) {
			pool.ReportFailure(accountID)
		}
		if usedRequestAttempts >= requestAttempts || !retryableAccountError(err) ||
			hasVisibleOutput != nil && hasVisibleOutput() {
			return generationResult{}, accountID, err
		}
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
	imageGeneration bool,
	thinkingMode gemini.ThinkingMode,
	prepare func(*gemini.Client, bool) (string, []gemini.FileData, error),
	onEvent func(gemini.Event) error,
) (generationResult, error) {
	releaseRequest := client.AcquireRequest()
	defer releaseRequest()

	model, err := client.ResolveModel(requestedModel)
	if err != nil {
		return generationResult{}, &gemini.ProtocolError{HTTPStatus: 404, Message: err.Error()}
	}
	var conversation *gemini.ConversationState
	continuation := false
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
		}
		if boundAccountID != accountID {
			return generationResult{}, &gemini.ProtocolError{HTTPStatus: 409, Message: "会话绑定账号已变化，请开始新会话"}
		}
	}
	prompt, files, err := prepare(client, continuation)
	if err != nil {
		var protocolErr *gemini.ProtocolError
		if errors.As(err, &protocolErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return generationResult{}, err
		}
		return generationResult{}, &gemini.ProtocolError{HTTPStatus: 400, Message: err.Error()}
	}
	if imageGeneration {
		prompt = "Generate or edit an image according to this request:\n\n" + prompt
	}
	promptTokens := contextTokens + tokencount.Content(prompt)
	if contextWindow := client.ContextWindow(); contextWindow > 0 && promptTokens > contextWindow {
		return generationResult{}, &accountContextError{err: &gemini.ProtocolError{
			HTTPStatus: 413,
			Message:    fmt.Sprintf("账号上下文窗口为 %d tokens，当前请求需要 %d tokens", contextWindow, promptTokens),
		}}
	}
	accumulator := NewEventAccumulator()
	initialUsageSent := false
	err = client.Stream(ctx, gemini.GenerateRequest{
		Prompt:       prompt,
		Model:        model.ID,
		ThinkingMode: thinkingMode,
		Files:        files,
		Conversation: conversation,
	}, func(event gemini.Event) error {
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
		if onEvent != nil {
			if err := onEvent(event); err != nil {
				return adapterEmitError{err: err}
			}
		}
		return nil
	})
	if err != nil {
		return generationResult{}, err
	}
	if imageGeneration {
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
	if accumulator.Usage == nil {
		primary := accumulator.Primary()
		completionTokens := tokencount.Text(primary.Text)
		for _, code := range primary.Codes {
			completionTokens += tokencount.Text(code.Content)
		}
		thoughtTokens := tokencount.Text(primary.Thought)
		accumulator.Usage = &gemini.Usage{
			PromptTokens: promptTokens, CompletionTokens: completionTokens, ThoughtTokens: thoughtTokens,
			TotalTokens: promptTokens + completionTokens + thoughtTokens,
		}
	}
	nextContextTokens := accumulator.Usage.TotalTokens
	primary := accumulator.Primary()
	if primary.Text != "" || primary.Thought != "" || len(primary.Codes) > 0 || len(primary.Media) > 0 {
		nextContextTokens++
	}

	if conversation == nil && accumulator.Session.CID != "" {
		conversation = gemini.NewConversationStateFrom(accumulator.Session)
	}
	if conversation != nil {
		snapshot := conversation.Snapshot()
		if accumulator.Session.CID != "" {
			conversation.Update(accumulator.Session)
			snapshot = conversation.Snapshot()
		}
		keys := []string{responseID, snapshot.CID}
		if allowNewSession && sessionKey != "" {
			keys = append(keys, sessionKey)
		}
		conversations.put(conversation, accountID, nextContextTokens, keys...)
		pool.BindSession(responseID, accountID)
		pool.BindSession(snapshot.CID, accountID)
		if allowNewSession && sessionKey != "" {
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
	return generationResult{
		Accumulator:    accumulator,
		Client:         client,
		ConversationID: accumulator.Session.CID,
		ResponseID:     responseID,
		ProviderModel:  providerModel,
	}, nil
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
	return errors.As(err, &mismatch)
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
