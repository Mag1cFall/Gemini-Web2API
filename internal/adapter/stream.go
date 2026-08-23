package adapter

import (
	"context"
	"errors"
	"fmt"

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
	ProviderModel  string
}

type adapterEmitError struct{ err error }

func (e adapterEmitError) Error() string { return e.err.Error() }
func (e adapterEmitError) Unwrap() error { return e.err }

type streamProjection struct {
	bufferText     bool
	bufferThought  bool
	emittedText    bool
	emittedThought bool
	errorSent      bool
}

func newStreamProjection(bridge ToolBridge) *streamProjection {
	return &streamProjection{bufferText: len(bridge.Definitions) > 0 || len(bridge.JSONSchema) > 0}
}

func (p *streamProjection) project(event gemini.Event, emit func(gemini.Event) error, emitError func(error) error) error {
	if event.Candidate != 0 || (event.Kind != gemini.EventText && event.Kind != gemini.EventThought) {
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
		if *buffered || event.Delta == "" {
			return nil
		}
		if err := emit(event); err != nil {
			return err
		}
		*emitted = true
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

func streamEventName(kind gemini.EventKind) string {
	if kind == gemini.EventThought {
		return "思考"
	}
	return "文本"
}

func modelStatus(pool *balancer.AccountPool, modelName string) (bool, bool) {
	modelID := config.MapModel(modelName)
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
) (generationResult, string, error) {
	requestedModel := config.MapModel(modelName)
	if !pool.HasModel(requestedModel) {
		return generationResult{}, "", &gemini.ProtocolError{HTTPStatus: 404, Message: fmt.Sprintf("模型 %q 不可用", modelName)}
	}
	releaseSession := func() {}
	if allowNewSession && sessionKey != "" {
		releaseSession = conversations.acquire(sessionKey)
	}
	defer releaseSession()

	client, accountID := pool.NextForModel(sessionKey, requestedModel)
	if client == nil {
		return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 503, Message: "没有可用账号", Retryable: true}
	}
	releaseRequest := client.AcquireRequest()
	defer releaseRequest()

	model, err := client.ResolveModel(requestedModel)
	if err != nil {
		return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 404, Message: err.Error()}
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
				return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 400, Message: fmt.Sprintf("未知会话标识 %q", sessionKey)}
			}
			conversation = gemini.NewConversationState()
			pool.BindSession(sessionKey, accountID)
			boundAccountID = accountID
		} else {
			continuation = true
		}
		if boundAccountID != accountID {
			return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 409, Message: "会话绑定账号已变化，请开始新会话"}
		}
	}
	prompt, files, err := prepare(client, continuation)
	if err != nil {
		return generationResult{}, accountID, &gemini.ProtocolError{HTTPStatus: 400, Message: err.Error()}
	}
	promptTokens := contextTokens + tokencount.Content(prompt)
	if onEvent != nil {
		usage := &gemini.Usage{PromptTokens: promptTokens, TotalTokens: promptTokens}
		if err := onEvent(gemini.Event{Kind: gemini.EventMetadata, Usage: usage}); err != nil {
			return generationResult{}, accountID, adapterEmitError{err: err}
		}
	}

	accumulator := NewEventAccumulator()
	err = client.Stream(ctx, gemini.GenerateRequest{
		Prompt:          prompt,
		Model:           model.ID,
		ThinkingMode:    thinkingMode,
		Files:           files,
		Conversation:    conversation,
		ImageGeneration: imageGeneration,
	}, func(event gemini.Event) error {
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
		var emitErr adapterEmitError
		if !errors.Is(err, context.Canceled) && !errors.As(err, &emitErr) {
			pool.ReportFailure(accountID)
		}
		return generationResult{}, accountID, err
	}
	pool.ReportSuccess(accountID)
	if accumulator.Usage == nil {
		primary := accumulator.Primary()
		completionTokens := tokencount.Text(primary.Text)
		thoughtTokens := tokencount.Text(primary.Thought)
		accumulator.Usage = &gemini.Usage{
			PromptTokens: promptTokens, CompletionTokens: completionTokens, ThoughtTokens: thoughtTokens,
			TotalTokens: promptTokens + completionTokens + thoughtTokens,
		}
	}
	nextContextTokens := accumulator.Usage.TotalTokens
	primary := accumulator.Primary()
	if primary.Text != "" || primary.Thought != "" || len(primary.Images) > 0 {
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
		accumulator.Session = snapshot
	}

	return generationResult{
		Accumulator:    accumulator,
		Client:         client,
		ConversationID: accumulator.Session.CID,
		ResponseID:     responseID,
		ProviderModel:  model.ID,
	}, accountID, nil
}
