package adapter

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
	"github.com/gin-gonic/gin"
)

const (
	// compactionRecentTokens 是压缩项原样保留的最近条目 token 预算
	compactionRecentTokens = 20000
	// compactionRetainedTokens 是压缩输出保留 user 消息的 token 预算
	compactionRetainedTokens = 64000
	// compactionImageTokens 是预算中每张图片的估算 token 数
	compactionImageTokens = 258
)

// compactionSummaryPrompt 是首次压缩的结构化摘要提示词
const compactionSummaryPrompt = `The conversation above is the complete history to summarize. The API replaces every message in this request with your summary; no message you leave out survives.

Summarize the conversation into a structured handoff summary for another LLM to resume the task. If the conversation ends with an unanswered question or a request awaiting user response, preserve that exact question or request.

Use this format and omit sections that do not apply:

## Goal
## Constraints & Preferences
## Progress
### Done
### In Progress
### Blocked
## Key Decisions
## Next Steps
## Critical Context

Keep sections concise. Preserve exact file paths, function names, identifiers, numbers, error messages, code, and relevant tool outputs or command results. Output only the summary and do not call any tools.`

// compactionUpdatePrompt 是已有摘要时合并新条目的增量摘要提示词
const compactionUpdatePrompt = `The first message above holds the previous handoff summary in <previous-summary> tags; the messages after it are new.

Update the summary for another LLM to resume the task: keep all previous information, add new progress, decisions and context, move completed work to Done, update Next Steps, and record any unanswered user question. Keep the same section format, preserve exact file paths, identifiers, numbers, error messages, code and relevant tool outputs. Output only the summary and do not call any tools.`

// compactionSummaryContext 是摘要还原为 user 消息时的文本模板
const compactionSummaryContext = "Prior model work and tool state are summarized below. Build on this work and do not repeat it.\n\n<summary>\n%s\n</summary>"

// compactionPayload 是 compaction 项 encrypted_content 解码后的内容：前文摘要与原样保留的最近条目
type compactionPayload struct {
	Version int               `json:"v"`
	Summary string            `json:"summary"`
	Items   []json.RawMessage `json:"items"`
}

// compactionWindow 是按最后一个 compaction 项拆分的输入：之前的条目、摘要、原样条目与之后的新条目
type compactionWindow struct {
	before  []json.RawMessage
	summary string
	recent  []json.RawMessage
	after   []json.RawMessage
}

// responsesItemType 返回输入条目的 type，没有 type 的消息为空串
func responsesItemType(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.Type
}

// splitCompaction 按最后一个 compaction 项拆分输入，丢弃更早的 compaction 项与和原样条目重复的 user 消息
func splitCompaction(items []json.RawMessage) (compactionWindow, error) {
	last := -1
	for index, item := range items {
		if responsesItemType(item) == "compaction" {
			last = index
		}
	}
	if last < 0 {
		return compactionWindow{after: items}, nil
	}
	var item struct {
		EncryptedContent string `json:"encrypted_content"`
	}
	var payload compactionPayload
	_ = json.Unmarshal(items[last], &item)
	decoded, err := base64.StdEncoding.DecodeString(item.EncryptedContent)
	if err != nil || json.Unmarshal(decoded, &payload) != nil || payload.Version != 1 {
		return compactionWindow{}, errors.New("compaction: encrypted_content was not produced by this service")
	}
	pending := make(map[string]int)
	for _, recent := range payload.Items {
		if text, ok := userMessageText(recent); ok {
			pending[text]++
		}
	}
	window := compactionWindow{summary: payload.Summary, recent: payload.Items, after: items[last+1:]}
	for index := last - 1; index >= 0; index-- {
		if responsesItemType(items[index]) == "compaction" {
			continue
		}
		if text, ok := userMessageText(items[index]); ok && pending[text] > 0 {
			pending[text]--
			continue
		}
		window.before = append(window.before, items[index])
	}
	slices.Reverse(window.before)
	return window, nil
}

// items 返回展开后的输入条目：之前的条目、摘要 user 消息、原样条目与之后的新条目
func (window compactionWindow) items() []json.RawMessage {
	items := slices.Clone(window.before)
	if window.summary != "" {
		items = append(items, userTextMessage(fmt.Sprintf(compactionSummaryContext, window.summary)))
	}
	return slices.Concat(items, window.recent, window.after)
}

// expandCompaction 把输入中的 compaction 项展开为摘要 user 消息与原样保留的条目
func expandCompaction(raw json.RawMessage) (json.RawMessage, error) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return raw, nil
	}
	if !slices.ContainsFunc(items, func(item json.RawMessage) bool { return responsesItemType(item) == "compaction" }) {
		return raw, nil
	}
	window, err := splitCompaction(items)
	if err != nil {
		return nil, err
	}
	return json.Marshal(window.items())
}

// userTextMessage 返回只含一段文本的 user 消息条目
func userTextMessage(text string) json.RawMessage {
	raw, _ := json.Marshal(gin.H{"type": "message", "role": "user", "content": []gin.H{{"type": "input_text", "text": text}}})
	return raw
}

// userMessageText 返回 user 消息条目的全部文本
func userMessageText(raw json.RawMessage) (string, bool) {
	var message struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Type != "" && message.Type != "message" || message.Role != "user" {
		return "", false
	}
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		return text, true
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(message.Content, &parts)
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), true
}

// compactionItemTokens 返回条目的 token 数：消息计文本与图片，其他条目计去掉 encrypted_content 后的 JSON
func compactionItemTokens(raw json.RawMessage) int {
	var message struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &message) == nil && (message.Type == "" || message.Type == "message") {
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			return tokencount.Text(text)
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(message.Content, &parts)
		tokens := 0
		for _, part := range parts {
			if part.Type == "input_image" {
				tokens += compactionImageTokens
			} else {
				tokens += tokencount.Text(part.Text)
			}
		}
		return tokens
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return tokencount.Text(string(raw))
	}
	delete(object, "encrypted_content")
	encoded, _ := json.Marshal(object)
	return tokencount.Text(string(encoded))
}

// compactionItemSide 把条目分为 user 消息、工具输出、指令与模型侧条目
func compactionItemSide(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
		Role string `json:"role"`
	}
	_ = json.Unmarshal(raw, &head)
	switch {
	case (head.Type == "" || head.Type == "message") && head.Role == "user":
		return "user"
	case (head.Type == "" || head.Type == "message") && (head.Role == "system" || head.Role == "developer"):
		return "instruction"
	case strings.HasSuffix(head.Type, "_output"):
		return "output"
	}
	return "model"
}

// compactionCut 返回原样保留部分的起点：从新到旧按完整的模型侧条目组与 user 消息累计，最新一组总是保留，切点不落在工具输出上
func compactionCut(items []json.RawMessage) int {
	cut, tokens := -1, 0
	for index := len(items) - 1; index >= 0; index-- {
		tokens += compactionItemTokens(items[index])
		side := compactionItemSide(items[index])
		if side != "user" && (side != "model" || index > 0 && compactionItemSide(items[index-1]) == "model") {
			continue
		}
		if cut >= 0 && tokens > compactionRecentTokens {
			break
		}
		cut = index
	}
	return max(cut, 0)
}

// retainedUserMessages 从新到旧保留 user 消息直到预算用完，跨预算的一条保留首尾文本
func retainedUserMessages(items []json.RawMessage) []json.RawMessage {
	remaining := compactionRetainedTokens
	var retained []json.RawMessage
	for index := len(items) - 1; index >= 0 && remaining > 0; index-- {
		text, ok := userMessageText(items[index])
		if !ok {
			continue
		}
		tokens := max(compactionItemTokens(items[index]), 1)
		if tokens <= remaining {
			retained = append(retained, items[index])
			remaining -= tokens
			continue
		}
		runes := []rune(text)
		keep := len(runes) * remaining / tokens / 2
		retained = append(retained, userTextMessage(string(runes[:keep])+"…"+string(runes[len(runes)-keep:])))
		remaining = 0
	}
	slices.Reverse(retained)
	return retained
}

// ResponsesCompactHandler 处理 /v1/responses/compact：较早的条目由模型摘要，最近的条目原样保留，返回保留的 user 消息与一个 compaction 项
func ResponsesCompactHandler(pool *balancer.AccountPool, store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		var request ResponsesRequest
		if err := decodeRequest(c, &request); err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		logging.FromContext(c.Request.Context()).Begin("compact", request.Model, false, responsesInputCount(request.Input), len(request.Tools))
		bindScope(c, pool, request.Model)
		if request.Model == "" {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "model is required")
			return
		}
		if err := awaitModel(c, pool, request.Model); err != nil {
			writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
			return
		}
		id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
		var items []json.RawMessage
		if request.PreviousResponseID != "" {
			stored, ok := storedResponseOrNotFound(c, store, request.PreviousResponseID)
			if !ok {
				return
			}
			items = append(responseChainItems(c.Request.Context(), store, stored), responseItems(stored)...)
		}
		var input []json.RawMessage
		_ = json.Unmarshal(responsesInputItems(request.Input, id), &input)
		window, err := splitCompaction(append(items, input...))
		if err != nil {
			writeOpenAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		work := slices.Concat(window.recent, window.after)
		cut := compactionCut(work)
		summary := window.summary
		var usage *gemini.Usage
		if cut > 0 {
			history := work[:cut]
			prompt := compactionSummaryPrompt
			if summary != "" {
				history = append([]json.RawMessage{userTextMessage("<previous-summary>\n" + summary + "\n</previous-summary>")}, history...)
				prompt = compactionUpdatePrompt
			}
			history = append(slices.Clone(history), userTextMessage(prompt))
			summaryRequest := ResponsesRequest{Model: request.Model, Instructions: request.Instructions}
			summaryRequest.Input, _ = json.Marshal(history)
			result, accountID, err := runGeneration(c.Request.Context(), pool, "", sessionContinue, request.Model, id, generationMedia{}, gemini.ThinkingLow, ToolBridge{Choice: "auto"},
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildResponsesPrompt(c, client, summaryRequest, false, ToolBridge{Choice: "auto"})
				}, nil, nil)
			c.Set("account_id", accountID)
			if err != nil {
				writeOpenAIError(c, upstreamStatus(err), openAIUpstreamCode(err), err.Error())
				return
			}
			summary = strings.TrimSpace(result.Accumulator.Primary().Text)
			usage = result.Accumulator.Usage
		}
		payload, _ := json.Marshal(compactionPayload{Version: 1, Summary: summary, Items: work[cut:]})
		output := make([]any, 0)
		for _, message := range retainedUserMessages(slices.Concat(window.before, work[:cut])) {
			output = append(output, message)
		}
		output = append(output, gin.H{"id": "cmp_" + id, "type": "compaction", "encrypted_content": base64.StdEncoding.EncodeToString(payload)})
		response := gin.H{"id": id, "object": "response.compaction", "created_at": time.Now().Unix(), "output": output, "usage": compactionUsage(usage)}
		c.JSON(http.StatusOK, response)
	}
}

// compactionUsage 返回压缩摘要生成的 Responses 用量
func compactionUsage(usage *gemini.Usage) gin.H {
	if usage == nil {
		usage = &gemini.Usage{}
	}
	output := usage.OutputTokens()
	return gin.H{
		"input_tokens": usage.PromptTokens, "output_tokens": output, "total_tokens": usage.PromptTokens + output,
		"input_tokens_details": gin.H{"cached_tokens": 0}, "output_tokens_details": gin.H{"reasoning_tokens": usage.ThoughtTokens},
	}
}
