package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/fieldorder"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/Mag1cFall/Gemini-Web2API/internal/streamio"
	"github.com/gin-gonic/gin"
)

// interactionStream 按 Interactions 事件格式写出交互流：interaction.created、各步骤的 step.start、step.delta、step.stop 与 interaction.completed；numbered 为真时事件带从 1 起的 event_id
type interactionStream struct {
	mu       sync.Mutex
	w        http.ResponseWriter
	numbered bool
	sequence int
	// skip 为不写出的前若干个事件，取回流时据此从 last_event_id 之后继续
	skip   int
	index  int
	active string
	// signature 是思考步骤结束前写出的签名
	signature string
	// text 是当前 model_output 步骤已写出的正文，offset 是它在回答正文中的起始字符位置，written 是已写出的回答正文字符数
	text      strings.Builder
	offset    int
	written   int
	citations []gemini.Citation
	codes     codeStepIDs
	// log 非空时记下写出的每个事件
	log *interactionLog
}

// interactionLog 是后台流式交互已写出的事件，取回流时据此从 last_event_id 之后跟随到结束
type interactionLog struct {
	mu     sync.Mutex
	frames [][]byte
	done   bool
	// changed 在追加事件或结束时关闭并换新
	changed chan struct{}
}

// interactionLogs 按交互 ID 保存后台流式交互的事件记录，交互结束 interactionLogRetention 后删除
var interactionLogs sync.Map

// interactionLogRetention 是后台流式交互结束后保留事件记录的时长
const interactionLogRetention = time.Hour

// append 追加一个事件
func (log *interactionLog) append(frame []byte) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.frames = append(log.frames, frame)
	close(log.changed)
	log.changed = make(chan struct{})
}

// finish 标记事件记录结束，interactionLogRetention 后删除
func (log *interactionLog) finish(id string) {
	log.mu.Lock()
	log.done = true
	close(log.changed)
	log.changed = make(chan struct{})
	log.mu.Unlock()
	time.AfterFunc(interactionLogRetention, func() { interactionLogs.CompareAndDelete(id, log) })
}

// follow 从第 skip 个事件之后写出事件记录，直到记录结束或客户端断开
func (log *interactionLog) follow(ctx context.Context, w http.ResponseWriter, skip int) {
	position := skip
	for {
		log.mu.Lock()
		frames := log.frames[min(position, len(log.frames)):]
		done, changed := log.done, log.changed
		log.mu.Unlock()
		for _, frame := range frames {
			if _, err := w.Write(frame); err != nil {
				return
			}
		}
		position += len(frames)
		if len(frames) > 0 && streamio.Flush(w) != nil || done {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

// newInteractionStream 返回写到 w 的交互流写出器，id 为交互 ID
func newInteractionStream(w http.ResponseWriter, id string, numbered bool) *interactionStream {
	return &interactionStream{w: w, numbered: numbered, index: -1, signature: thinkingSignature(id, 0), codes: codeStepIDs{prefix: id}}
}

// send 写出一个事件
func (s *interactionStream) send(kind string, value gin.H) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value["event_type"] = kind
	if s.numbered {
		s.sequence++
		value["event_id"] = strconv.Itoa(s.sequence)
		if s.sequence <= s.skip {
			return nil
		}
	}
	data, err := json.Marshal(fieldorder.Official("interactions", value))
	if err != nil {
		return err
	}
	frame := fmt.Appendf(nil, "event: %s\ndata: %s\n\n", kind, data)
	if s.log != nil {
		s.log.append(frame)
	}
	if _, err := s.w.Write(frame); err != nil {
		return err
	}
	return streamio.Flush(s.w)
}

// open 结束进行中的步骤并开始下一个步骤
func (s *interactionStream) open(step gin.H) error {
	if err := s.stop(); err != nil {
		return err
	}
	s.index++
	s.active, _ = step["type"].(string)
	if s.active == "model_output" {
		s.text.Reset()
		s.offset = s.written
	}
	return s.send("step.start", gin.H{"index": s.index, "step": step})
}

// stop 结束进行中的步骤：思考步骤先写出签名，正文步骤先写出落在其中的搜索来源
func (s *interactionStream) stop() error {
	switch s.active {
	case "":
		return nil
	case "thought":
		if err := s.delta(gin.H{"type": "thought_signature", "signature": s.signature}); err != nil {
			return err
		}
	case "model_output":
		if annotations := interactionAnnotations(s.text.String(), s.offset, s.citations); len(annotations) > 0 {
			if err := s.delta(gin.H{"type": "text_annotation_delta", "annotations": annotations}); err != nil {
				return err
			}
		}
	}
	s.active = ""
	return s.send("step.stop", gin.H{"index": s.index})
}

// delta 写出进行中步骤的一段增量
func (s *interactionStream) delta(delta gin.H) error {
	return s.send("step.delta", gin.H{"index": s.index, "delta": delta})
}

// thought 写出一段思考摘要，必要时先开始思考步骤
func (s *interactionStream) thought(text string) error {
	if s.active != "thought" {
		if err := s.open(gin.H{"type": "thought", "summary": []any{}}); err != nil {
			return err
		}
	}
	return s.delta(gin.H{"type": "thought_summary", "content": gin.H{"type": "text", "text": text}})
}

// content 写出一个正文或媒体内容块，必要时先开始 model_output 步骤
func (s *interactionStream) content(block gin.H) error {
	if s.active != "model_output" {
		if err := s.open(gin.H{"type": "model_output", "content": []any{}}); err != nil {
			return err
		}
	}
	if text, ok := block["text"].(string); ok {
		s.text.WriteString(text)
		s.written += len([]rune(text))
	}
	return s.delta(block)
}

// live 写出实时投影的思考与正文增量
func (s *interactionStream) live(event gemini.Event) error {
	if event.Kind == gemini.EventThought {
		return s.thought(event.Delta)
	}
	return s.content(gin.H{"type": "text", "text": event.Delta})
}

// tail 写出实时投影之后剩余的内容段与之后的代码、函数调用步骤，并结束最后一个步骤
func (s *interactionStream) tail(parts []gin.H) error {
	for _, part := range parts {
		text, hasText := part["text"].(string)
		if isThought, _ := part["thought"].(bool); isThought {
			if text != "" {
				if err := s.thought(text); err != nil {
					return err
				}
			}
			continue
		}
		if hasText && text == "" {
			continue
		}
		if block := interactionContentBlock(part); block != nil {
			if err := s.content(block); err != nil {
				return err
			}
			continue
		}
		if step := s.codes.step(part); step != nil {
			if err := s.open(step); err != nil {
				return err
			}
			if err := s.stop(); err != nil {
				return err
			}
		}
	}
	return s.stop()
}

// streamInteraction 以事件流返回交互；background 时保存 in_progress 的交互，客户端断开后生成继续并保存结果
func streamInteraction(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, run interactionRun) {
	setSSEHeaders(c)
	ctx := c.Request.Context()
	if run.request.Background {
		requestContext := ctx
		detached, cancel := context.WithCancelCause(context.WithoutCancel(requestContext))
		defer cancel(nil)
		logging.FromContext(requestContext).Detach(cancel)
		ctx = detached
		responseJobs.Lock()
		responseJobs.jobs[run.id] = &responseJob{cancel: func() { cancel(nil) }}
		responseJobs.Unlock()
		defer finishResponseJob(run.id)
		c.Writer = &detachedWriter{ResponseWriter: c.Writer}
		saveInteraction(ctx, store, run, run.object("in_progress", nil))
	}
	keepalive, stop := startStreamWriter(c, openAIKeepalive, !run.request.Background)
	defer stop()
	stream := newInteractionStream(c.Writer, run.id, run.request.Background)
	if run.request.Background {
		stream.log = &interactionLog{changed: make(chan struct{})}
		interactionLogs.Store(run.id, stream.log)
		defer stream.log.finish(run.id)
	}
	if err := stream.send("interaction.created", gin.H{"interaction": run.object("in_progress", nil)}); err != nil {
		return
	}
	projection := newStreamProjection(run.bridge, run.includeThoughts)
	object, response, err := run.execute(c, ctx, pool, func(event gemini.Event) error {
		if !usageOnly(event) {
			keepalive.release()
		}
		return projection.project(event, stream.live)
	}, projection)
	if err == nil {
		stream.citations = interactionCitations(object)
		err = stream.tail(geminiResponseParts(projectGeminiStreamTail(response, projection)))
	}
	if err != nil {
		if keepalive.reject() {
			writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
			return
		}
		if !finishResponseJob(run.id) {
			saveInteraction(ctx, store, run, failedInteraction(c, run, err))
		}
		_ = stream.send("error", gin.H{"error": gin.H{"code": geminiUpstreamStatus(err), "message": streamFailure(c, err).Error()}})
		return
	}
	if !finishResponseJob(run.id) {
		saveInteraction(ctx, store, run, object)
	}
	completed := maps.Clone(object)
	delete(completed, "steps")
	_ = stream.send("interaction.completed", gin.H{"interaction": completed})
}

// interactionCitations 从交互对象的正文 annotations 还原回答正文中的搜索来源位置
func interactionCitations(object gin.H) []gemini.Citation {
	var citations []gemini.Citation
	offset := 0
	steps, _ := object["steps"].([]gin.H)
	for _, step := range steps {
		contents, _ := step["content"].([]gin.H)
		for _, content := range contents {
			text, _ := content["text"].(string)
			annotations, _ := content["annotations"].([]gin.H)
			for _, annotation := range annotations {
				start, _ := annotation["start_index"].(int)
				end, _ := annotation["end_index"].(int)
				url, _ := annotation["url"].(string)
				title, _ := annotation["title"].(string)
				citations = append(citations, gemini.Citation{URL: url, Title: title, Start: start + offset, End: end + offset})
			}
			offset += len([]rune(text))
		}
	}
	return citations
}

// startBackgroundInteraction 保存 in_progress 的交互并立即返回，生成在独立的请求跟踪中进行，结果写入账本库
func startBackgroundInteraction(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, run interactionRun) {
	started := run.object("in_progress", nil)
	saveInteraction(c.Request.Context(), store, run, started)
	trace, ctx := logging.NewRequest(context.WithoutCancel(c.Request.Context()), c.Request.Method, c.Request.URL.Path, c.ClientIP())
	trace.Begin("interactions", run.model, false, len(run.input), len(run.request.Tools))
	responseJobs.Lock()
	responseJobs.jobs[run.id] = &responseJob{cancel: trace.Cancel}
	responseJobs.Unlock()
	background := c.Copy()
	background.Request = c.Request.Clone(ctx)
	c.JSON(http.StatusOK, fieldorder.Official("interactions", started))

	go func() {
		object, _, err := run.execute(background, ctx, pool, nil, nil)
		status := http.StatusOK
		if err != nil {
			status = upstreamStatus(err)
			object = failedInteraction(background, run, err)
			trace.Fail(status, err.Error())
		}
		if !finishResponseJob(run.id) {
			saveInteraction(ctx, store, run, object)
		}
		store.Record(trace.Finish(status, false), nil)
	}()
}

// storedInteraction 读取保存的交互，不存在时写入 404
func storedInteraction(c *gin.Context, store *requestdb.Store) (requestdb.StoredResponse, bool) {
	id := c.Param("interaction")
	if store != nil && strings.HasPrefix(id, interactionIDPrefix) {
		stored, err := store.Response(c.Request.Context(), id)
		if err == nil {
			return stored, true
		}
		if !errors.Is(err, requestdb.ErrNotFound) {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return requestdb.StoredResponse{}, false
		}
	}
	writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("interaction %q was not found", id))
	return requestdb.StoredResponse{}, false
}

// InteractionRetrieveHandler 返回保存的交互：include_input 时在步骤前加入请求输入；stream 时等后台交互结束，再按创建时的流式事件回放 last_event_id 之后的事件
func InteractionRetrieveHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		stored, ok := storedInteraction(c, store)
		if !ok {
			return
		}
		if stream, _ := strconv.ParseBool(c.Query("stream")); stream {
			skip := 0
			if raw := c.Query("last_event_id"); raw != "" {
				value, err := strconv.Atoi(raw)
				if err != nil || value < 1 {
					writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("last_event_id %q was not found", raw))
					return
				}
				skip = value
			}
			setSSEHeaders(c)
			_, stop := startStreamWriter(c, openAIKeepalive, false)
			defer stop()
			if log, ok := interactionLogs.Load(stored.ID); ok {
				log.(*interactionLog).follow(c.Request.Context(), c.Writer, skip)
				return
			}
			for stored.Status == "in_progress" {
				select {
				case <-c.Request.Context().Done():
					return
				case <-time.After(responsePollInterval):
				}
				next, err := store.Response(c.Request.Context(), stored.ID)
				if err != nil {
					return
				}
				stored = next
			}
			replayInteraction(c.Writer, stored, skip)
			return
		}
		if include, _ := strconv.ParseBool(c.Query("include_input")); include {
			var object map[string]json.RawMessage
			var input, steps []json.RawMessage
			_ = json.Unmarshal(stored.Response, &object)
			_ = json.Unmarshal(stored.Input, &input)
			_ = json.Unmarshal(object["steps"], &steps)
			object["steps"], _ = json.Marshal(append(input, steps...))
			c.JSON(http.StatusOK, fieldorder.Official("interactions", object))
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", stored.Response)
	}
}

// replayInteraction 按创建时的流式事件写出保存的交互，前 skip 个事件不写出
func replayInteraction(w http.ResponseWriter, stored requestdb.StoredResponse, skip int) {
	var decoded map[string]any
	if json.Unmarshal(stored.Response, &decoded) != nil {
		return
	}
	object, _ := ginValue(decoded).(gin.H)
	steps, _ := object["steps"].([]gin.H)
	completed := maps.Clone(object)
	delete(completed, "steps")
	created := maps.Clone(completed)
	created["status"] = "in_progress"
	delete(created, "usage")
	delete(created, "errors")
	stream := &interactionStream{w: w, numbered: true, skip: skip, index: -1}
	if stream.send("interaction.created", gin.H{"interaction": created}) != nil {
		return
	}
	for index, step := range steps {
		start := maps.Clone(step)
		switch step["type"] {
		case "model_output":
			start["content"] = []any{}
		case "thought":
			start["summary"] = []any{}
			delete(start, "signature")
		}
		if stream.send("step.start", gin.H{"index": index, "step": start}) != nil {
			return
		}
		var deltas []gin.H
		switch step["type"] {
		case "model_output":
			contents, _ := step["content"].([]gin.H)
			for _, content := range contents {
				delta := maps.Clone(content)
				delete(delta, "annotations")
				deltas = append(deltas, delta)
				if annotations, ok := content["annotations"]; ok {
					deltas = append(deltas, gin.H{"type": "text_annotation_delta", "annotations": annotations})
				}
			}
		case "thought":
			summary, _ := step["summary"].([]gin.H)
			for _, content := range summary {
				deltas = append(deltas, gin.H{"type": "thought_summary", "content": content})
			}
			if signature, ok := step["signature"]; ok {
				deltas = append(deltas, gin.H{"type": "thought_signature", "signature": signature})
			}
		}
		for _, delta := range deltas {
			if stream.send("step.delta", gin.H{"index": index, "delta": delta}) != nil {
				return
			}
		}
		if stream.send("step.stop", gin.H{"index": index}) != nil {
			return
		}
	}
	if completed["status"] == "failed" {
		failures, _ := completed["errors"].([]gin.H)
		failure := gin.H{"code": "INTERNAL", "message": "interaction failed"}
		if len(failures) > 0 {
			failure = failures[0]
		}
		_ = stream.send("error", gin.H{"error": failure})
		return
	}
	_ = stream.send("interaction.completed", gin.H{"interaction": completed})
}

// InteractionDeleteHandler 删除保存的交互，进行中的后台交互同时停止
func InteractionDeleteHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		stored, ok := storedInteraction(c, store)
		if !ok {
			return
		}
		cancelResponseJob(stored.ID)
		if _, err := store.DeleteResponse(c.Request.Context(), stored.ID); err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	}
}

// InteractionCancelHandler 取消后台交互：进行中的交互停止生成并标为 cancelled，已结束的交互原样返回
func InteractionCancelHandler(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		stored, ok := storedInteraction(c, store)
		if !ok {
			return
		}
		if !stored.Background {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "only interactions created with background=true can be cancelled")
			return
		}
		if stored.Status != "in_progress" {
			c.Data(http.StatusOK, "application/json; charset=utf-8", stored.Response)
			return
		}
		cancelResponseJob(stored.ID)
		var object map[string]any
		if err := json.Unmarshal(stored.Response, &object); err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		object["status"], object["updated"] = "cancelled", time.Now().UTC().Format(time.RFC3339)
		encoded, _ := json.Marshal(fieldorder.Official("interactions", object))
		stored.Status, stored.Response = "cancelled", encoded
		if err := store.SaveResponse(c.Request.Context(), stored); err != nil {
			writeGeminiError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", encoded)
	}
}
