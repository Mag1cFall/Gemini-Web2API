package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/claude"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
	"github.com/gin-gonic/gin"
)

// claudeClearedToolResult 是清理后的工具结果占位文本
const claudeClearedToolResult = "[cleared]"

// claudeCompactionPrompt 是 compact_20260112 没有 instructions 时追加在历史末尾的摘要提示词
const claudeCompactionPrompt = "You have written a partial transcript for the initial task above. Please write a summary of the transcript. The purpose of this summary is to provide continuity so you can continue to make progress towards solving the task in a future context, where the raw history above may not be accessible and will be replaced with this summary. Write down anything that would be helpful, including the state, next steps, learnings etc. You must wrap your summary in a <summary></summary> block."

// claudeContextResult 是上下文管理写入响应的内容：生效的编辑与本次压缩
type claudeContextResult struct {
	requested  bool
	compacting bool
	applied    []gin.H
	compaction *claudeCompaction
}

// claudeCompaction 是本次请求生成的压缩摘要与其用量
type claudeCompaction struct {
	summary string
	usage   *gemini.Usage
	pause   bool
}

// claudeBlockHead 是定位思考块、工具调用与工具结果所需的块字段
type claudeBlockHead struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

// compactedClaudeMessages 丢弃最后一个 compaction 块之前的全部内容，摘要成为首个 user 消息
func compactedClaudeMessages(messages []claude.Message) []claude.Message {
	for index := len(messages) - 1; index >= 0; index-- {
		var blocks []json.RawMessage
		if json.Unmarshal(messages[index].Content, &blocks) != nil {
			continue
		}
		for position := len(blocks) - 1; position >= 0; position-- {
			var block struct {
				Type    string  `json:"type"`
				Content *string `json:"content"`
			}
			if json.Unmarshal(blocks[position], &block) != nil || block.Type != "compaction" {
				continue
			}
			var compacted []claude.Message
			if block.Content != nil && *block.Content != "" {
				summary, _ := json.Marshal(fmt.Sprintf(compactionSummaryContext, *block.Content))
				compacted = append(compacted, claude.Message{Role: "user", Content: summary})
			}
			if position+1 < len(blocks) {
				rest, _ := json.Marshal(blocks[position+1:])
				compacted = append(compacted, claude.Message{Role: messages[index].Role, Content: rest})
			}
			return append(compacted, messages[index+1:]...)
		}
	}
	return messages
}

// claudeHistoryBlocks 把各消息的块数组拆为块，字符串内容为 nil
func claudeHistoryBlocks(messages []claude.Message) [][]json.RawMessage {
	history := make([][]json.RawMessage, len(messages))
	for index, message := range messages {
		_ = json.Unmarshal(message.Content, &history[index])
	}
	return history
}

// withClaudeBlocks 返回把改写后的块写回后的消息副本
func withClaudeBlocks(messages []claude.Message, history [][]json.RawMessage) []claude.Message {
	edited := slices.Clone(messages)
	for index, blocks := range history {
		if blocks != nil {
			edited[index].Content, _ = json.Marshal(blocks)
		}
	}
	return edited
}

// claudeInputTokens 以生成时的提示词编码返回请求的输入 token 数，不读取网页
func claudeInputTokens(c *gin.Context, pool *balancer.AccountPool, request claude.ClaudeRequest) (int, error) {
	bridge, err := claudeToolBridge(request)
	if err != nil {
		return 0, err
	}
	bridge.URLContext = false
	prompt, files, err := buildClaudePrompt(c, countClient(pool), request, false, bridge)
	if err != nil {
		return 0, err
	}
	return tokencount.Content(prompt) + mediaTokens(files), nil
}

// applyClaudeContextEdits 按顺序执行思考与工具结果清理并改写 request.Messages，返回请求的上下文管理结果
func applyClaudeContextEdits(c *gin.Context, pool *balancer.AccountPool, request *claude.ClaudeRequest) (claudeContextResult, error) {
	result := claudeContextResult{requested: request.ContextManagement != nil, applied: []gin.H{}}
	if request.ContextManagement == nil {
		return result, nil
	}
	for _, edit := range request.ContextManagement.Edits {
		var applied gin.H
		var err error
		switch edit.Type {
		case "clear_thinking_20251015":
			applied, err = clearClaudeThinking(c, pool, request, edit)
		case "clear_tool_uses_20250919":
			applied, err = clearClaudeToolUses(c, pool, request, edit)
		case "compact_20260112":
			if edit.Trigger != nil && edit.Trigger.Type != "input_tokens" {
				return result, fmt.Errorf("context_management: compact_20260112 trigger type must be input_tokens")
			}
			result.compacting = true
		default:
			return result, fmt.Errorf("context_management: unsupported edit type %q", edit.Type)
		}
		if err != nil {
			return result, err
		}
		if applied != nil {
			result.applied = append(result.applied, applied)
		}
	}
	return result, nil
}

// clearClaudeThinking 去掉保留轮数之前的 assistant 轮中的 thinking 与 redacted_thinking 块，keep 省略或为 all 时保留全部
func clearClaudeThinking(c *gin.Context, pool *balancer.AccountPool, request *claude.ClaudeRequest, edit claude.ContextEdit) (gin.H, error) {
	if len(edit.Keep) == 0 || string(edit.Keep) == `"all"` {
		return nil, nil
	}
	var keep claude.EditAmount
	if err := json.Unmarshal(edit.Keep, &keep); err != nil || keep.Type != "all" && keep.Type != "thinking_turns" || keep.Value < 0 {
		return nil, fmt.Errorf(`context_management: clear_thinking_20251015 keep must be "all" or {"type":"thinking_turns","value":N} with N >= 0`)
	}
	if keep.Type == "all" {
		return nil, nil
	}
	history := claudeHistoryBlocks(request.Messages)
	var turns []int
	for index, blocks := range history {
		if request.Messages[index].Role == "assistant" && slices.ContainsFunc(blocks, claudeThinkingBlock) {
			turns = append(turns, index)
		}
	}
	if len(turns) <= keep.Value {
		return nil, nil
	}
	cleared := turns[:len(turns)-keep.Value]
	for _, index := range cleared {
		history[index] = slices.DeleteFunc(history[index], claudeThinkingBlock)
	}
	return commitClaudeEdit(c, pool, request, history, edit, gin.H{"type": edit.Type, "cleared_thinking_turns": len(cleared)})
}

// claudeThinkingBlock 判断块是否为 thinking 或 redacted_thinking
func claudeThinkingBlock(raw json.RawMessage) bool {
	var head claudeBlockHead
	_ = json.Unmarshal(raw, &head)
	return head.Type == "thinking" || head.Type == "redacted_thinking"
}

// clearClaudeToolUses 在触发条件满足时把保留数量之前的工具结果替换为占位文本，按 clear_tool_inputs 清空调用参数
func clearClaudeToolUses(c *gin.Context, pool *balancer.AccountPool, request *claude.ClaudeRequest, edit claude.ContextEdit) (gin.H, error) {
	trigger := claude.EditAmount{Type: "input_tokens", Value: 100000}
	if edit.Trigger != nil {
		trigger = *edit.Trigger
	}
	keep := claude.EditAmount{Type: "tool_uses", Value: 3}
	if len(edit.Keep) > 0 {
		if err := json.Unmarshal(edit.Keep, &keep); err != nil || keep.Type != "tool_uses" || keep.Value < 0 {
			return nil, fmt.Errorf(`context_management: clear_tool_uses_20250919 keep must be {"type":"tool_uses","value":N} with N >= 0`)
		}
	}
	clearInputs := func(string) bool { return false }
	if len(edit.ClearToolInputs) > 0 && string(edit.ClearToolInputs) != "null" {
		var all bool
		var names []string
		switch {
		case json.Unmarshal(edit.ClearToolInputs, &all) == nil:
			clearInputs = func(string) bool { return all }
		case json.Unmarshal(edit.ClearToolInputs, &names) == nil:
			clearInputs = func(name string) bool { return slices.Contains(names, name) }
		default:
			return nil, fmt.Errorf("context_management: clear_tool_inputs must be a boolean or an array of tool names")
		}
	}
	history := claudeHistoryBlocks(request.Messages)
	type toolUse struct {
		name         string
		call, result [2]int
		hasResult    bool
	}
	var uses []*toolUse
	byID := map[string]*toolUse{}
	for index, blocks := range history {
		for position, raw := range blocks {
			var head claudeBlockHead
			_ = json.Unmarshal(raw, &head)
			switch {
			case (head.Type == "tool_use" || head.Type == "server_tool_use" || head.Type == "mcp_tool_use") && head.ID != "":
				use := &toolUse{name: head.Name, call: [2]int{index, position}}
				uses = append(uses, use)
				byID[head.ID] = use
			case head.ToolUseID != "" && byID[head.ToolUseID] != nil:
				use := byID[head.ToolUseID]
				use.result, use.hasResult = [2]int{index, position}, true
			}
		}
	}
	switch trigger.Type {
	case "tool_uses":
		if len(uses) <= trigger.Value {
			return nil, nil
		}
	case "input_tokens":
		tokens, err := claudeInputTokens(c, pool, *request)
		if err != nil || tokens <= trigger.Value {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("context_management: clear_tool_uses_20250919 trigger type must be input_tokens or tool_uses")
	}
	if len(uses) <= keep.Value {
		return nil, nil
	}
	cleared := 0
	for _, use := range uses[:len(uses)-keep.Value] {
		if !use.hasResult || slices.Contains(edit.ExcludeTools, use.name) {
			continue
		}
		block := history[use.result[0]][use.result[1]]
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(block, &fields)
		if head := string(fields["type"]); head == `"tool_result"` || head == `"mcp_tool_result"` {
			fields["content"], _ = json.Marshal(claudeClearedToolResult)
			block, _ = json.Marshal(fields)
		} else {
			block, _ = json.Marshal(gin.H{"type": "text", "text": claudeClearedToolResult})
		}
		history[use.result[0]][use.result[1]] = block
		if clearInputs(use.name) {
			var call map[string]json.RawMessage
			_ = json.Unmarshal(history[use.call[0]][use.call[1]], &call)
			call["input"] = json.RawMessage(`{}`)
			history[use.call[0]][use.call[1]], _ = json.Marshal(call)
		}
		cleared++
	}
	if cleared == 0 {
		return nil, nil
	}
	return commitClaudeEdit(c, pool, request, history, edit, gin.H{"type": edit.Type, "cleared_tool_uses": cleared})
}

// commitClaudeEdit 计算编辑清理的输入 token，未达到 clear_at_least 时不改写请求
func commitClaudeEdit(c *gin.Context, pool *balancer.AccountPool, request *claude.ClaudeRequest, history [][]json.RawMessage, edit claude.ContextEdit, applied gin.H) (gin.H, error) {
	edited := *request
	edited.Messages = withClaudeBlocks(request.Messages, history)
	before, err := claudeInputTokens(c, pool, *request)
	if err != nil {
		return nil, err
	}
	after, err := claudeInputTokens(c, pool, edited)
	if err != nil {
		return nil, err
	}
	cleared := max(before-after, 0)
	if edit.ClearAtLeast != nil && cleared < edit.ClearAtLeast.Value {
		return nil, nil
	}
	request.Messages = edited.Messages
	applied["cleared_input_tokens"] = cleared
	return applied, nil
}

// compactClaude 在输入超过 compact_20260112 的触发值时生成摘要，摘要替换请求的全部历史，摘要的用量计入本次请求
func compactClaude(c *gin.Context, pool *balancer.AccountPool, request *claude.ClaudeRequest, responseID string, thinkingLevel gemini.ThinkingLevel) (*claudeCompaction, error) {
	index := slices.IndexFunc(request.ContextManagement.Edits, func(edit claude.ContextEdit) bool { return edit.Type == "compact_20260112" })
	edit := request.ContextManagement.Edits[index]
	trigger := 150000
	if edit.Trigger != nil {
		trigger = edit.Trigger.Value
	}
	tokens, err := claudeInputTokens(c, pool, *request)
	if err != nil || tokens <= trigger {
		return nil, err
	}
	instructions := edit.Instructions
	if instructions == "" {
		instructions = claudeCompactionPrompt
	}
	summarize := *request
	summarize.Tools, summarize.ToolChoice = nil, nil
	prompt, _ := json.Marshal(instructions)
	summarize.Messages = append(slices.Clone(request.Messages), claude.Message{Role: "user", Content: prompt})
	bridge := ToolBridge{Choice: "auto"}
	result, accountID, err := runGeneration(c.Request.Context(), pool, "", sessionCreate, request.Model, responseID+"_compaction", generationMedia{}, thinkingLevel, bridge,
		func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
			return buildClaudePrompt(c, client, summarize, false, bridge)
		}, nil, nil)
	c.Set("account_id", accountID)
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(result.Accumulator.Primary().Text)
	if start, end := strings.Index(summary, "<summary>"), strings.LastIndex(summary, "</summary>"); start >= 0 && end > start {
		summary = strings.TrimSpace(summary[start+len("<summary>") : end])
	}
	usage := result.Accumulator.Usage
	if usage != nil {
		logging.FromContext(c.Request.Context()).AddAttemptUsage(usage.PromptTokens, usage.ThoughtTokens, usage.CompletionTokens)
	}
	content, _ := json.Marshal(fmt.Sprintf(compactionSummaryContext, summary))
	request.Messages = []claude.Message{{Role: "user", Content: content}}
	return &claudeCompaction{summary: summary, usage: usage, pause: edit.PauseAfterCompaction}, nil
}

// iterations 在请求带 compact_20260112 时返回各次采样的用量：压缩一次、回答一次，暂停时只有压缩
func (result claudeContextResult) iterations(message *gemini.Usage) []map[string]any {
	if !result.compacting {
		return nil
	}
	iteration := func(kind string, value *gemini.Usage) gin.H {
		entry := gin.H{"type": kind, "input_tokens": 0, "output_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
		if value != nil {
			entry["input_tokens"], entry["output_tokens"] = value.PromptTokens, value.OutputTokens()
		}
		return entry
	}
	var iterations []map[string]any
	if result.compaction != nil {
		iterations = append(iterations, iteration("compaction", result.compaction.usage))
	}
	if result.compaction == nil || !result.compaction.pause {
		iterations = append(iterations, iteration("message", message))
	}
	return iterations
}

// management 返回响应的 context_management 字段，请求没有 context_management 时为 nil
func (result claudeContextResult) management() gin.H {
	if !result.requested {
		return nil
	}
	return gin.H{"applied_edits": result.applied}
}

// contentBlock 返回非流式 Message 中的 compaction 块
func (compaction claudeCompaction) contentBlock() claude.ContentBlock {
	content, _ := json.Marshal(compaction.summary)
	return claude.ContentBlock{Type: "compaction", Content: content, EncryptedContent: json.RawMessage("null")}
}

// writeClaudeCompactionPause 在 pause_after_compaction 时只返回 compaction 块，stop_reason 为 compaction
func writeClaudeCompactionPause(c *gin.Context, pool *balancer.AccountPool, request claude.ClaudeRequest, responseID string, contextResult claudeContextResult) {
	model := publicModelID(pool, request.Model)
	if !request.Stream {
		usage := &claude.Usage{Iterations: contextResult.iterations(nil)}
		c.JSON(http.StatusOK, claude.ClaudeResponse{
			ID: responseID, Type: "message", Role: "assistant", Model: model, Content: []claude.ContentBlock{contextResult.compaction.contentBlock()},
			StopReason: "compaction", Usage: usage, ContextManagement: contextResult.management(),
		})
		return
	}
	setSSEHeaders(c)
	processor := claude.NewStreamProcessor(model, c.Writer, responseID)
	if err := processor.EmitCompaction(contextResult.compaction.summary); err != nil {
		return
	}
	processor.SetContextManagement(contextResult.management(), contextResult.iterations(nil))
	_ = processor.Finish("compaction")
}
