package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
	"github.com/gin-gonic/gin"
)

// responseJob 是进行中的后台响应
type responseJob struct {
	cancel    func()
	cancelled bool
}

// responseJobs 保存进行中的后台响应，取消时据此停止生成
var responseJobs = struct {
	sync.Mutex
	jobs map[string]*responseJob
}{jobs: make(map[string]*responseJob)}

// cancelResponseJob 停止进行中的后台响应，返回该响应是否仍在运行
func cancelResponseJob(id string) bool {
	responseJobs.Lock()
	job := responseJobs.jobs[id]
	if job != nil {
		job.cancelled = true
	}
	responseJobs.Unlock()
	if job == nil {
		return false
	}
	job.cancel()
	return true
}

// finishResponseJob 移除后台响应并返回它是否已被取消
func finishResponseJob(id string) bool {
	responseJobs.Lock()
	defer responseJobs.Unlock()
	job := responseJobs.jobs[id]
	delete(responseJobs.jobs, id)
	return job != nil && job.cancelled
}

// storesResponse 判断响应是否保存：store 不为 false 或以后台模式创建
func (request ResponsesRequest) storesResponse() bool {
	return request.Store == nil || *request.Store || request.Background
}

// applyRequestFields 把请求的后台模式、保存选项与 metadata 写入响应对象
func applyRequestFields(response gin.H, request ResponsesRequest) {
	response["background"] = request.Background
	response["store"] = request.storesResponse()
	if len(request.Metadata) > 0 && string(request.Metadata) != "null" {
		response["metadata"] = request.Metadata
	}
	if request.Instructions != "" {
		response["instructions"] = request.Instructions
	}
}

// responsesInputItems 把请求输入整理为带 ID 的输入项：字符串输入与字符串内容转为内容数组，缺少 ID 的项按位置编号
func responsesInputItems(raw json.RawMessage, responseID string) json.RawMessage {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		raw, _ = json.Marshal([]gin.H{{"role": "user", "content": text}})
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return json.RawMessage("[]")
	}
	stored := make([]json.RawMessage, 0, len(items))
	for index, rawItem := range items {
		var item map[string]json.RawMessage
		var head struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rawItem, &item) != nil || json.Unmarshal(rawItem, &head) != nil {
			continue
		}
		if head.Type == "" || head.Type == "message" {
			item["type"] = json.RawMessage(`"message"`)
			var content string
			if json.Unmarshal(head.Content, &content) == nil {
				part := gin.H{"type": "input_text", "text": content}
				if head.Role == "assistant" {
					part = gin.H{"type": "output_text", "text": content, "annotations": []any{}}
				}
				item["content"], _ = json.Marshal([]any{part})
			}
			if _, ok := item["status"]; !ok {
				item["status"] = json.RawMessage(`"completed"`)
			}
		}
		if head.ID == "" {
			prefix := "item"
			if head.Type == "" || head.Type == "message" {
				prefix = "msg"
			}
			item["id"], _ = json.Marshal(fmt.Sprintf("%s_%s_%d", prefix, responseID, index))
		}
		encoded, _ := json.Marshal(item)
		stored = append(stored, encoded)
	}
	encoded, _ := json.Marshal(stored)
	return encoded
}

// saveResponse 保存响应对象与输入项
func saveResponse(ctx context.Context, store *requestdb.Store, request ResponsesRequest, id string, created int64, response gin.H) {
	if store == nil || !request.storesResponse() {
		return
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return
	}
	status, _ := response["status"].(string)
	if err := store.SaveResponse(context.WithoutCancel(ctx), requestdb.StoredResponse{
		ID: id, CreatedAt: time.Unix(created, 0), PreviousID: request.PreviousResponseID, Background: request.Background,
		Status: status, Response: encoded, Input: responsesInputItems(request.Input, id),
	}); err != nil {
		logging.Warn(logging.SourceRequest, "保存响应失败", "响应", id, "错误", err)
	}
}

// startBackgroundResponse 保存 queued 响应并立即返回，生成在独立的请求跟踪中进行，结果写入账本库
func startBackgroundResponse(c *gin.Context, pool *balancer.AccountPool, store *requestdb.Store, request ResponsesRequest, run responsesRun) {
	shell := responseShell(run.id, config.MapModel(request.Model), run.created, "queued")
	applyRequestFields(shell, request)
	saveResponse(c.Request.Context(), store, request, run.id, run.created, shell)
	trace, ctx := logging.NewRequest(context.Background(), c.Request.Method, c.Request.URL.Path, c.ClientIP())
	trace.Begin("responses", request.Model, false, responsesInputCount(request.Input), len(request.Tools))
	responseJobs.Lock()
	responseJobs.jobs[run.id] = &responseJob{cancel: trace.Cancel}
	responseJobs.Unlock()
	background := c.Copy()
	background.Request = c.Request.Clone(ctx)
	c.Header("X-Response-ID", run.id)
	c.JSON(http.StatusOK, shell)

	go func() {
		running := responseShell(run.id, config.MapModel(request.Model), run.created, "in_progress")
		applyRequestFields(running, request)
		saveResponse(ctx, store, request, run.id, run.created, running)
		result, _, err := runGeneration(ctx, pool, run.sessionKey, run.policy, request.Model, run.id, run.media, run.thinkingLevel, run.bridge,
			responsesPrompt(background, request, run.rebuilt, run.sessionKey, run.bridge), nil, nil)
		if err == nil {
			err = inlineResultMedia(ctx, &result)
		}
		var response gin.H
		if err == nil {
			response, err = buildResponsesObject(run.id, run.created, request.PreviousResponseID, result, run.bridge, run.includeThoughts)
		}
		if err == nil {
			rememberHistory(pool, result, request.Instructions, run.bridge, run.turns)
		}
		status := http.StatusOK
		cancelled := finishResponseJob(run.id)
		if err != nil {
			status = upstreamStatus(err)
			response = responseShell(run.id, config.MapModel(request.Model), run.created, "failed")
			response["error"] = gin.H{"code": "server_error", "message": publicMessage(background, err.Error())}
			trace.Fail(status, err.Error())
		}
		applyRequestFields(response, request)
		if !cancelled {
			saveResponse(ctx, store, request, run.id, run.created, response)
		}
		store.Record(trace.Finish(status, false), nil)
	}()
}

// interruptedMessage 是服务重启前未完成的后台任务的错误说明
const interruptedMessage = "The service restarted before this background task finished."

// failInterruptedResponses 把上次运行未完成的后台响应保存为 failed
func failInterruptedResponses(store *requestdb.Store) {
	if store == nil {
		return
	}
	ctx := context.Background()
	responses, err := store.UnfinishedResponses(ctx)
	if err != nil {
		logging.Warn(logging.SourceService, "读取未完成的后台响应失败", "错误", err)
		return
	}
	for _, stored := range responses {
		var response map[string]any
		if json.Unmarshal(stored.Response, &response) != nil {
			continue
		}
		response["status"] = "failed"
		response["error"] = gin.H{"code": "server_error", "message": interruptedMessage}
		stored.Status = "failed"
		stored.Response, _ = json.Marshal(response)
		if err := store.SaveResponse(ctx, stored); err != nil {
			logging.Warn(logging.SourceService, "保存响应失败", "响应", stored.ID, "错误", err)
		}
	}
}

// saveFailedResponse 保存以错误结束的响应
func saveFailedResponse(ctx context.Context, store *requestdb.Store, request ResponsesRequest, id string, created int64, err error) {
	if finishResponseJob(id) {
		return
	}
	response := responseShell(id, config.MapModel(request.Model), created, "failed")
	response["error"] = gin.H{"code": "server_error", "message": err.Error()}
	applyRequestFields(response, request)
	saveResponse(ctx, store, request, id, created, response)
}

// detachedWriter 在客户端断开后丢弃写入，后台流式响应据此在断开后继续生成并保存结果
type detachedWriter struct {
	gin.ResponseWriter
	gone bool
}

// Write 写入输出，客户端断开后丢弃
func (w *detachedWriter) Write(data []byte) (int, error) {
	if !w.gone {
		if _, err := w.ResponseWriter.Write(data); err != nil {
			w.gone = true
		}
	}
	return len(data), nil
}

// WriteString 写入输出，客户端断开后丢弃
func (w *detachedWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// FlushError 刷新输出，客户端断开后不执行
func (w *detachedWriter) FlushError() error {
	if !w.gone && http.NewResponseController(w.ResponseWriter).Flush() != nil {
		w.gone = true
	}
	return nil
}

// Flush 刷新输出，客户端断开后不执行
func (w *detachedWriter) Flush() {
	_ = w.FlushError()
}

// responsesRun 是一次 Responses 生成共用的参数
type responsesRun struct {
	id              string
	created         int64
	sessionKey      string
	policy          sessionPolicy
	rebuilt         *ResponsesRequest
	media           generationMedia
	thinkingLevel   gemini.ThinkingLevel
	bridge          ToolBridge
	includeThoughts bool
	turns           []historyTurn
}

// storedResponseOrNotFound 读取保存的响应，不存在时写入 404
func storedResponseOrNotFound(c *gin.Context, store *requestdb.Store, id string) (requestdb.StoredResponse, bool) {
	if store == nil {
		writeResponseNotFound(c, id)
		return requestdb.StoredResponse{}, false
	}
	stored, err := store.Response(c.Request.Context(), id)
	if errors.Is(err, requestdb.ErrNotFound) {
		writeResponseNotFound(c, id)
		return requestdb.StoredResponse{}, false
	}
	if err != nil {
		writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
		return requestdb.StoredResponse{}, false
	}
	return stored, true
}

// writeResponseNotFound 返回响应不存在的 404
func writeResponseNotFound(c *gin.Context, id string) {
	writeOpenAIError(c, http.StatusNotFound, "not_found", fmt.Sprintf("Response with id '%s' not found.", id))
}

// responsePollInterval 是流式取回进行中的后台响应时重新读取账本库的间隔
const responsePollInterval = 500 * time.Millisecond

// ResponseRetrieveHandler 返回保存的响应对象；stream=true 时等后台响应结束，再按创建时的流式事件回放，starting_after 之前的事件不输出
func ResponseRetrieveHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id := c.Param("response")
		stored, ok := storedResponseOrNotFound(c, store, id)
		if !ok {
			return
		}
		if stream, _ := strconv.ParseBool(c.Query("stream")); !stream {
			c.Data(http.StatusOK, "application/json; charset=utf-8", stored.Response)
			return
		}
		startingAfter := -1
		if raw := c.Query("starting_after"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "starting_after must be a non-negative integer")
				return
			}
			startingAfter = value
		}
		setSSEHeaders(c)
		defer startKeepalive(c, openAIKeepalive)()
		for stored.Status == "queued" || stored.Status == "in_progress" {
			select {
			case <-c.Request.Context().Done():
				return
			case <-time.After(responsePollInterval):
			}
			next, err := store.Response(c.Request.Context(), id)
			if err != nil {
				return
			}
			stored = next
		}
		_ = replayResponse(c.Writer, stored, startingAfter)
	}
}

// replayResponse 按创建时的流式事件顺序输出保存的响应：created、in_progress、各输出项的事件，最后为 completed 或 failed
func replayResponse(writer http.ResponseWriter, stored requestdb.StoredResponse, startingAfter int) error {
	var decoded map[string]any
	if err := json.Unmarshal(stored.Response, &decoded); err != nil {
		return err
	}
	response, _ := ginValue(decoded).(gin.H)
	running := maps.Clone(response)
	running["status"], running["output"], running["usage"], running["error"], running["completed_at"] = "in_progress", []any{}, nil, nil, nil
	created := running
	if stored.Background {
		created = maps.Clone(running)
		created["status"] = "queued"
	}
	w := &responseSequenceWriter{writer: writer, startAt: startingAfter + 1, responseID: stored.ID, outputIndexes: make(map[string]int)}
	if err := w.emit("response.created", gin.H{"response": created}); err != nil {
		return err
	}
	if err := w.emit("response.in_progress", gin.H{"response": running}); err != nil {
		return err
	}
	if response["status"] == "failed" {
		return w.emit("response.failed", gin.H{"response": response})
	}
	return w.finishProjected(response, NewEventAccumulator(), &streamProjection{})
}

// ginValue 把 JSON 解码出的对象转为 gin.H、对象数组转为 []gin.H，供流式写出器按创建时的类型读取
func ginValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		object := make(gin.H, len(typed))
		for key, item := range typed {
			object[key] = ginValue(item)
		}
		return object
	case []any:
		objects := make([]gin.H, 0, len(typed))
		for _, item := range typed {
			object, ok := ginValue(item).(gin.H)
			if !ok {
				break
			}
			objects = append(objects, object)
		}
		if len(typed) > 0 && len(objects) == len(typed) {
			return objects
		}
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = ginValue(item)
		}
		return items
	}
	return value
}

// ResponseDeleteHandler 删除保存的响应，进行中的后台响应同时停止
func ResponseDeleteHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id := c.Param("response")
		if _, ok := storedResponseOrNotFound(c, store, id); !ok {
			return
		}
		cancelResponseJob(id)
		if _, err := store.DeleteResponse(c.Request.Context(), id); err != nil {
			writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "object": "response", "deleted": true})
	}
}

// ResponseCancelHandler 取消后台响应：进行中的响应停止生成并标为 cancelled，已结束的响应原样返回
func ResponseCancelHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		id := c.Param("response")
		stored, ok := storedResponseOrNotFound(c, store, id)
		if !ok {
			return
		}
		if !stored.Background {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "Only responses created with background=true can be cancelled.")
			return
		}
		if stored.Status != "queued" && stored.Status != "in_progress" {
			c.Data(http.StatusOK, "application/json; charset=utf-8", stored.Response)
			return
		}
		cancelResponseJob(id)
		var response map[string]any
		if err := json.Unmarshal(stored.Response, &response); err != nil {
			writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		response["status"] = "cancelled"
		encoded, _ := json.Marshal(response)
		stored.Status, stored.Response = "cancelled", encoded
		if err := store.SaveResponse(c.Request.Context(), stored); err != nil {
			writeOpenAIError(c, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", encoded)
	}
}

// responseItems 返回保存的响应的输出项
func responseItems(stored requestdb.StoredResponse) []json.RawMessage {
	var response struct {
		Output []json.RawMessage `json:"output"`
	}
	_ = json.Unmarshal(stored.Response, &response)
	return response.Output
}

// itemID 返回条目的 id
func itemID(raw json.RawMessage) string {
	var head struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.ID
}

// rebuiltResponsesRequest 返回把 previous_response_id 的已保存响应链与本次输入合并为完整输入的请求，前序响应没有保存时返回 nil
func rebuiltResponsesRequest(ctx context.Context, store *requestdb.Store, request ResponsesRequest) *ResponsesRequest {
	if store == nil || request.PreviousResponseID == "" {
		return nil
	}
	previous, err := store.Response(ctx, request.PreviousResponseID)
	if err != nil {
		return nil
	}
	var input []json.RawMessage
	_ = json.Unmarshal(responsesInputItems(request.Input, ""), &input)
	items, _ := json.Marshal(slices.Concat(responseChainItems(ctx, store, previous), responseItems(previous), input))
	rebuilt := request
	rebuilt.PreviousResponseID, rebuilt.Input = "", items
	return &rebuilt
}

// responseChainItems 返回生成该响应时的全部输入项：续接链上各前序响应的输入与输出，以及本次输入
func responseChainItems(ctx context.Context, store *requestdb.Store, stored requestdb.StoredResponse) []json.RawMessage {
	var items []json.RawMessage
	_ = json.Unmarshal(stored.Input, &items)
	seen := map[string]bool{stored.ID: true}
	for previous := stored.PreviousID; previous != "" && !seen[previous]; {
		seen[previous] = true
		parent, err := store.Response(ctx, previous)
		if err != nil {
			break
		}
		var input []json.RawMessage
		_ = json.Unmarshal(parent.Input, &input)
		items = slices.Concat(input, responseItems(parent), items)
		previous = parent.PreviousID
	}
	return items
}

// ResponseInputItemsHandler 按 after、before、limit 与 order 分页返回生成该响应时的输入项，未知的游标返回空页
func ResponseInputItemsHandler(store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		limit := 20
		if raw := c.Query("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 100 {
				writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100")
				return
			}
			limit = value
		}
		order := c.DefaultQuery("order", "desc")
		if order != "asc" && order != "desc" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "order must be asc or desc")
			return
		}
		stored, ok := storedResponseOrNotFound(c, store, c.Param("response"))
		if !ok {
			return
		}
		items := responseChainItems(c.Request.Context(), store, stored)
		if order == "desc" {
			slices.Reverse(items)
		}
		start, end := 0, len(items)
		for _, cursor := range []string{"after", "before"} {
			value := c.Query(cursor)
			if value == "" {
				continue
			}
			index := slices.IndexFunc(items, func(item json.RawMessage) bool { return itemID(item) == value })
			if index < 0 {
				start, end = 0, 0
				break
			}
			if cursor == "after" {
				start = index + 1
			} else {
				end = index
			}
		}
		page := items[min(start, end):end]
		more := len(page) > limit
		if more && c.Query("before") != "" && c.Query("after") == "" {
			page = page[len(page)-limit:]
		} else if more {
			page = page[:limit]
		}
		list := gin.H{"object": "list", "data": page, "first_id": nil, "last_id": nil, "has_more": more}
		if len(page) > 0 {
			list["first_id"], list["last_id"] = itemID(page[0]), itemID(page[len(page)-1])
		}
		c.JSON(http.StatusOK, list)
	}
}

// ResponsesInputTokensHandler 以与生成相同的提示词编码计算 Responses 请求的输入 token，续接时计入会话已有上下文
func ResponsesInputTokensHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request ResponsesRequest
		if err := decodeRequest(c, &request); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if _, err := takeModelSuffix(&request.Model); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		bindScope(c, pool, request.Model)
		if request.Model != "" {
			if err := modelKnown(pool, request.Model); err != nil {
				writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
				return
			}
		}
		bridge, err := responsesToolBridge(request)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		conversationID, err := responsesConversationID(request.Conversation)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		contextTokens, continuation := 0, false
		if key := conversationKey(request.PreviousResponseID, conversationID, c.GetHeader("X-Conversation-ID")); key != "" {
			if _, _, tokens, ok := conversations.get(key); ok {
				contextTokens, continuation = tokens, true
			} else if request.PreviousResponseID != "" {
				writeResponseNotFound(c, request.PreviousResponseID)
				return
			}
		}
		if len(request.Input) == 0 {
			request.Input = json.RawMessage(`[]`)
		}
		prompt, files, err := buildResponsesPrompt(c, countClient(pool), request, continuation, bridge)
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"object": "response.input_tokens", "input_tokens": contextTokens + tokencount.Content(prompt) + mediaTokens(files)})
	}
}
