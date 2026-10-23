package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// turnPollInterval 是读取会话中后台生成回答的轮询间隔
var turnPollInterval = 30 * time.Second

// turnMissingPolls 是会话中连续读不到本轮回答时放弃等待的轮询次数
const turnMissingPolls = 4

// deepResearch 按官网流程发送研究计划轮，以计划卡片的确认按钮文字发送确认轮，再轮询会话直到报告完成；计划、确认回复与研究进度作为思考输出，完成后的回答与报告作为正文输出；计划轮没有计划卡片时该轮回答即为结果
func (c *Client) deepResearch(ctx context.Context, request GenerateRequest, model Model, emit func(Event) error) error {
	if request.Conversation == nil {
		request.Conversation = NewConversationState()
	}
	var confirmLabel string
	var planEvents []Event
	plan := request
	plan.researchPlan = &confirmLabel
	if err := c.stream(ctx, plan, func(event Event) error {
		planEvents = append(planEvents, event)
		return nil
	}, true); err != nil {
		return err
	}
	output := emit
	thoughts := &researchThoughts{emit: emit}
	if confirmLabel != "" {
		output = thoughts.turn
	}
	for _, event := range planEvents {
		if err := output(event); err != nil {
			return err
		}
	}
	if confirmLabel == "" {
		return nil
	}
	confirm := request
	confirm.Prompt, confirm.Files, confirm.Branch, confirm.researchConfirm = confirmLabel, nil, false, true
	if err := c.stream(ctx, confirm, thoughts.turn, true); err != nil {
		return err
	}
	return c.awaitTurn(ctx, request.Conversation, nil, model, model.thinkingLevel(request.ThinkingLevel), thoughts.progress, nil, emit)
}

// researchThoughts 把 Deep Research 计划轮与确认轮的正文、研究进度转成思考增量
type researchThoughts struct {
	emit    func(Event) error
	written bool
	pending bool
	seen    map[string]struct{}
}

// turn 转发计划轮与确认轮的会话与元数据事件，正文改为思考，轮次之间以空行分隔
func (r *researchThoughts) turn(event Event) error {
	switch event.Kind {
	case EventText:
		if event.Operation != SnapshotAppend || event.Delta == "" {
			return nil
		}
		delta := event.Delta
		if r.written && !r.pending {
			delta = "\n\n" + delta
		}
		r.written, r.pending = true, true
		return r.emit(Event{Kind: EventThought, Candidate: event.Candidate, Operation: SnapshotAppend, Delta: delta})
	case EventDone:
		r.pending = false
		return nil
	case EventSession, EventMetadata, EventError:
		return r.emit(event)
	}
	return nil
}

// progress 把研究卡片 candidate[12]["58"] 中新出现的步骤与来源标题作为思考输出
func (r *researchThoughts) progress(candidate []any) error {
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	card, ok := richContentField(candidate, 57)
	if !ok {
		return nil
	}
	stepsValue, _ := valuePath(card, 1, 4, 2)
	steps, _ := stepsValue.([]any)
	for _, step := range steps {
		line, _ := stringPath(step, 4, 1)
		if line == "" {
			line, _ = stringPath(step, 4, 2, 2)
		}
		if _, seen := r.seen[line]; line == "" || seen {
			continue
		}
		r.seen[line] = struct{}{}
		if err := r.emit(Event{Kind: EventThought, Operation: SnapshotAppend, Delta: "\n\n" + line}); err != nil {
			return err
		}
	}
	return nil
}

// awaitTurn 在调用方持有账号请求槽时调用，轮询期间让出请求槽，以 hNvQHb 轮询会话中本轮回答，完成后重新占用请求槽并按生成流解析该回答，连续 turnMissingPolls 次读不到本轮回答时返回可重试错误；decoder 为已输出部分回答的生成流解码器，为 nil 时从头输出；progress 接收进行中的候选，researchConfirm 非空时写入该回答计划卡片的确认按钮文字
func (c *Client) awaitTurn(ctx context.Context, state *ConversationState, decoder *FrameDecoder, model Model, level ThinkingLevel, progress func([]any) error, researchConfirm *string, emit func(Event) error) error {
	trace := logging.FromContext(ctx)
	trace.Background(model.ID)
	c.requestSlot.Release(1)
	c.busy.Add(-1)
	c.notifyRelease()
	defer func() {
		c.busy.Add(1)
		c.requestSlot.Acquire(context.Background(), 1)
	}()
	session := state.Snapshot()
	request, err := json.Marshal([]any{session.CID, 10, nil, 1, []any{0}, []any{4}, nil, 1})
	if err != nil {
		return err
	}
	ticker := time.NewTicker(turnPollInterval)
	defer ticker.Stop()
	started := time.Now()
	missing := 0
	for polls := 1; ; polls++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		bootstrap, err := c.bootstrapSnapshot()
		if err != nil {
			return err
		}
		payload, err := c.batchExecute(ctx, bootstrap, batchRequest{
			rpcID: "hNvQHb", payload: string(request), sourcePath: "/app/" + strings.TrimPrefix(session.CID, "c_"),
			language: bootstrap.Language, extension: "[0]", mode: model.Mode, level: level,
		})
		if err != nil && errors.Is(err, ErrTransport) && ctx.Err() == nil {
			trace.Stage("后台生成轮询网络错误，下次重试", "次数", polls, "原因", err)
			continue
		}
		if err != nil {
			return err
		}
		turns, _ := arrayAt(payload, 0)
		if !slices.ContainsFunc(turns, func(turn any) bool { rid, _ := stringPath(turn, 0, 1); return rid == session.RID }) {
			if missing++; missing >= turnMissingPolls {
				return retryableProtocolError("gemini conversation has no turn for this request", nil)
			}
			continue
		}
		missing = 0
		for _, turn := range turns {
			if rid, _ := stringPath(turn, 0, 1); rid != session.RID {
				continue
			}
			candidate, _ := valuePath(turn, 3, 0, 0)
			values, _ := candidate.([]any)
			if phase, _ := intPath(candidate, 8, 0); Phase(phase) != PhaseComplete {
				trace.Stage("后台生成轮询", "次数", polls, "已等待", time.Since(started).Truncate(time.Second))
				if progress != nil {
					if err := progress(values); err != nil {
						return err
					}
				}
				break
			}
			hash, _ := stringPath(turn, 2, 4)
			frame := make([]any, 40)
			frame[1] = []any{session.CID, session.RID}
			frame[4] = []any{values}
			frame[39] = hash
			if decoder == nil {
				decoder = NewFrameDecoder()
				decoder.lastSession = state.Snapshot()
			}
			err := decoder.decodeTurn(frame, state, emit)
			if researchConfirm != nil {
				*researchConfirm = decoder.researchConfirm
			}
			return err
		}
	}
}
