package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/fieldorder"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/gin-gonic/gin"
)

// interactionIDPrefix 是 Interactions 资源 ID 的前缀
const interactionIDPrefix = "int_"

// interactionRequest 是 Interactions 创建请求
type interactionRequest struct {
	Model              string                      `json:"model"`
	Agent              string                      `json:"agent"`
	Input              json.RawMessage             `json:"input"`
	SystemInstruction  string                      `json:"system_instruction"`
	Tools              []interactionTool           `json:"tools"`
	ResponseFormat     json.RawMessage             `json:"response_format"`
	ResponseMIMEType   string                      `json:"response_mime_type"`
	ResponseModalities []string                    `json:"response_modalities"`
	Generation         interactionGenerationConfig `json:"generation_config"`
	Stream             bool                        `json:"stream"`
	Store              *bool                       `json:"store"`
	Background         bool                        `json:"background"`
	PreviousID         string                      `json:"previous_interaction_id"`
	CachedContent      string                      `json:"cached_content"`
	SafetySettings     json.RawMessage             `json:"safety_settings"`
	AgentConfig        json.RawMessage             `json:"agent_config"`
	ServiceTier        json.RawMessage             `json:"service_tier"`
	WebhookConfig      json.RawMessage             `json:"webhook_config"`
	Environment        json.RawMessage             `json:"environment"`
}

// interactionTool 是 Interactions 工具声明
type interactionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// interactionGenerationConfig 是 Interactions generation_config
type interactionGenerationConfig struct {
	Temperature         json.RawMessage `json:"temperature"`
	TopP                json.RawMessage `json:"top_p"`
	TopK                json.RawMessage `json:"top_k"`
	Seed                json.RawMessage `json:"seed"`
	MaxOutputTokens     *int            `json:"max_output_tokens"`
	StopSequences       []string        `json:"stop_sequences"`
	ThinkingLevel       string          `json:"thinking_level"`
	ThinkingSummaries   string          `json:"thinking_summaries"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
	SpeechConfig        json.RawMessage `json:"speech_config"`
	TranscriptionConfig json.RawMessage `json:"transcription_config"`
	VideoConfig         json.RawMessage `json:"video_config"`
	ImageConfig         *struct {
		AspectRatio string `json:"aspect_ratio"`
		ImageSize   string `json:"image_size"`
	} `json:"image_config"`
}

// interactionContent 是输入步骤或内容块，字段按 type 取用
type interactionContent struct {
	Type       string               `json:"type"`
	Text       *string              `json:"text"`
	Data       string               `json:"data"`
	URI        string               `json:"uri"`
	MIMEType   string               `json:"mime_type"`
	Content    []interactionContent `json:"content"`
	ID         string               `json:"id"`
	CallID     string               `json:"call_id"`
	Name       string               `json:"name"`
	Arguments  json.RawMessage      `json:"arguments"`
	Result     json.RawMessage      `json:"result"`
	IsError    bool                 `json:"is_error"`
	Resolution json.RawMessage      `json:"resolution"`
	Processing json.RawMessage      `json:"processing"`
}

// interactionContentTypes 是可直接作为 input 的内容块类型
var interactionContentTypes = []string{"text", "image", "audio", "video", "document"}

// interactionServerSteps 是服务端工具的调用与结果步骤，其结果已体现在模型输出中，转为提示词时跳过
var interactionServerSteps = []string{
	"google_search_call", "google_search_result", "url_context_call", "url_context_result", "google_maps_call", "google_maps_result",
	"file_search_call", "file_search_result", "mcp_server_tool_call", "mcp_server_tool_result", "retrieval_call", "retrieval_result",
	"processing_call", "processing_result",
}

// InteractionsHandler 处理 Interactions 创建请求：previous_interaction_id 续接已保存的交互，cached_content 展开显式缓存，store 不为 false 时保存交互，background 为 true 时立即返回 in_progress 的交互、生成在后台继续
func InteractionsHandler(pool *balancer.AccountPool, store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		var request interactionRequest
		if err := decodeRequest(c, &request); err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		model := interactionModel(request)
		suffix, err := takeModelSuffix(&model)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		input, err := interactionInputSteps(request.Input)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		if !request.Background || request.Stream {
			logging.FromContext(c.Request.Context()).Begin("interactions", model, request.Stream, len(input), len(request.Tools))
		}
		bindScope(c, pool, model)
		switch {
		case model == "":
			err = fmt.Errorf("model or agent is required")
		case len(input) == 0:
			err = fmt.Errorf("input is required")
		case request.Background && request.Store != nil && !*request.Store:
			err = fmt.Errorf("store=false is incompatible with background=true")
		case (request.Background || request.PreviousID != "") && store == nil:
			err = fmt.Errorf("background interactions and previous_interaction_id need the request ledger")
		}
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		history := input
		if request.PreviousID != "" {
			chain, err := interactionChain(c.Request.Context(), store, request.PreviousID)
			if err != nil {
				writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
				return
			}
			history = append(chain, input...)
		}
		contents, unsupported, err := interactionContents(history)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		generate, fields, err := request.geminiRequest(contents)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		cache, ok := applyGeminiCache(c, request.CachedContent, &generate)
		if !ok {
			return
		}
		if err := awaitModel(c, pool, model); err != nil {
			writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
			return
		}
		thinkingLevel, includeThoughts, err := geminiRequestThinkingSettings(generate)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		thinkingLevel, includeThoughts = suffix.apply(thinkingLevel, includeThoughts)
		bridge, bridgeFields, err := geminiToolBridge(generate)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		unsupported = append(append(unsupported, fields...), bridgeFields...)
		slices.Sort(unsupported)
		setUnsupportedParameters(c, slices.Compact(unsupported))

		run := interactionRun{
			id: fmt.Sprintf("%s%d", interactionIDPrefix, time.Now().UnixNano()), created: time.Now().UTC().Format(time.RFC3339),
			request: request, model: model, input: input, generate: generate, bridge: bridge, thinkingLevel: thinkingLevel,
			includeThoughts: includeThoughts, cache: cache, policy: sessionCreate,
		}
		if request.PreviousID != "" {
			run.sessionKey, run.policy = conversationKey(request.PreviousID, "", ""), sessionRebuild
		} else {
			run.system = geminiSystemText(generate.SystemInstruction)
			run.turns = geminiHistoryTurns(generate.Contents)
			run.sessionKey = continuedHistoryKey(run.system, bridge, run.turns)
		}
		switch {
		case request.Background && !request.Stream:
			startBackgroundInteraction(c, pool, store, run)
		case request.Stream:
			streamInteraction(c, pool, store, run)
		default:
			defer startNonstreamKeepalive(c)()
			object, response, err := run.execute(c, c.Request.Context(), pool, nil, nil)
			if err != nil {
				writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
				return
			}
			saveInteraction(c.Request.Context(), store, run, object)
			c.Header("X-Provider-Model", fmt.Sprint(response["modelVersion"]))
			c.JSON(http.StatusOK, fieldorder.Official("interactions", object))
		}
	}
}

// interactionModel 返回请求的模型 ID：agent 为 Deep Research 代理时对应 gemini-deep-research，其余 agent 按模型 ID 查找
func interactionModel(request interactionRequest) string {
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if model != "" {
		return model
	}
	agent := strings.TrimSpace(request.Agent)
	if strings.HasPrefix(agent, "deep-research") {
		return "gemini-deep-research"
	}
	return agent
}

// interactionRun 是一次交互生成共用的参数
type interactionRun struct {
	id              string
	created         string
	request         interactionRequest
	model           string
	input           []json.RawMessage
	generate        GeminiGenerateContentRequest
	bridge          ToolBridge
	thinkingLevel   gemini.ThinkingLevel
	includeThoughts bool
	cache           *geminiCache
	sessionKey      string
	policy          sessionPolicy
	system          string
	turns           []historyTurn
}

// execute 运行生成并返回带步骤、状态与用量的交互对象与生成结果的 Gemini 响应，onEvent 与 projection 非空时实时投影输出
func (run interactionRun) execute(c *gin.Context, ctx context.Context, pool *balancer.AccountPool, onEvent func(gemini.Event) error, projection *streamProjection) (gin.H, gin.H, error) {
	result, accountID, err := runGeneration(
		ctx, pool, run.sessionKey, run.policy, run.model, run.id, generationMedia{aspectRatio: run.bridge.AspectRatio}, run.thinkingLevel, run.bridge,
		func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
			return buildGeminiPrompt(c, client, run.generate, continuation, run.bridge.declared(continuation && isHistoryKey(run.sessionKey)))
		}, onEvent, projection,
	)
	c.Set("account_id", accountID)
	if err != nil {
		return nil, nil, err
	}
	if err := inlineResultMedia(ctx, &result); err != nil {
		return nil, nil, err
	}
	response, err := buildGeminiResponse(result, run.bridge, run.includeThoughts)
	if err != nil {
		return nil, nil, err
	}
	if run.policy == sessionCreate {
		rememberHistory(pool, result, run.system, run.bridge, run.turns)
	}
	object := run.object(interactionStatus(response), result.Accumulator.Usage)
	object["steps"] = interactionSteps(geminiResponseParts(response), result.Accumulator.Primary().Citations, run.id)
	return object, response, nil
}

// object 返回交互资源的标识、模型、状态与用量
func (run interactionRun) object(status string, usage *gemini.Usage) gin.H {
	object := gin.H{"id": run.id, "object": "interaction", "created": run.created, "updated": time.Now().UTC().Format(time.RFC3339), "status": status}
	if strings.TrimSpace(run.request.Model) == "" && run.request.Agent != "" {
		object["agent"] = run.request.Agent
	} else {
		object["model"] = config.MapModel(run.model)
	}
	if run.request.PreviousID != "" {
		object["previous_interaction_id"] = run.request.PreviousID
	}
	if usage != nil {
		cached := 0
		if run.cache != nil {
			cached = min(run.cache.tokens, usage.PromptTokens)
		}
		object["usage"] = gin.H{
			"total_input_tokens": usage.PromptTokens, "total_cached_tokens": cached, "total_output_tokens": usage.CompletionTokens,
			"total_thought_tokens": usage.ThoughtTokens, "total_tool_use_tokens": 0,
			"total_tokens": usage.PromptTokens + usage.CompletionTokens + usage.ThoughtTokens,
		}
	}
	return object
}

// interactionStatus 按 Gemini 响应给出交互状态：有函数调用为 requires_action，达到输出上限为 incomplete
func interactionStatus(response gin.H) string {
	for _, part := range geminiResponseParts(response) {
		if part["functionCall"] != nil {
			return "requires_action"
		}
	}
	candidates, _ := response["candidates"].([]gin.H)
	if len(candidates) > 0 && candidates[0]["finishReason"] == "MAX_TOKENS" {
		return "incomplete"
	}
	return "completed"
}

// geminiResponseParts 返回 Gemini 响应首个候选的内容段
func geminiResponseParts(response gin.H) []gin.H {
	candidates, _ := response["candidates"].([]gin.H)
	if len(candidates) == 0 {
		return nil
	}
	content, _ := candidates[0]["content"].(gin.H)
	parts, _ := content["parts"].([]gin.H)
	return parts
}

// interactionInputSteps 把 input 整理为步骤数组：字符串与内容块归入 user_input 步骤，相邻内容块合并为一个步骤
func interactionInputSteps(raw json.RawMessage) ([]json.RawMessage, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		step, _ := json.Marshal(gin.H{"type": "user_input", "content": []gin.H{{"type": "text", "text": text}}})
		return []json.RawMessage{step}, nil
	}
	items := []json.RawMessage{raw}
	if strings.HasPrefix(value, "[") {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("input: %w", err)
		}
	}
	steps := make([]json.RawMessage, 0, len(items))
	var content []json.RawMessage
	flush := func() {
		if len(content) > 0 {
			step, _ := json.Marshal(gin.H{"type": "user_input", "content": content})
			steps = append(steps, step)
			content = nil
		}
	}
	for _, item := range items {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &head); err != nil {
			return nil, fmt.Errorf("input: %w", err)
		}
		if slices.Contains(interactionContentTypes, head.Type) {
			content = append(content, item)
			continue
		}
		flush()
		steps = append(steps, item)
	}
	flush()
	return steps, nil
}

// interactionChain 返回 previous_interaction_id 链上各交互依次的输入步骤与输出步骤
func interactionChain(ctx context.Context, store *requestdb.Store, id string) ([]json.RawMessage, error) {
	var records [][]json.RawMessage
	for id != "" {
		stored, err := store.Response(ctx, id)
		if err != nil || !strings.HasPrefix(stored.ID, interactionIDPrefix) {
			return nil, fmt.Errorf("previous_interaction_id %q was not found", id)
		}
		var input []json.RawMessage
		var object struct {
			Steps []json.RawMessage `json:"steps"`
		}
		_ = json.Unmarshal(stored.Input, &input)
		_ = json.Unmarshal(stored.Response, &object)
		records = append(records, append(input, object.Steps...))
		id = stored.PreviousID
	}
	var steps []json.RawMessage
	for index := len(records) - 1; index >= 0; index-- {
		steps = append(steps, records[index]...)
	}
	return steps, nil
}

// interactionContents 把步骤转为 Gemini contents，相邻同角色的内容合并，返回 Gemini Web 不使用的字段
func interactionContents(steps []json.RawMessage) ([]GeminiContent, []string, error) {
	var contents []GeminiContent
	var unsupported []string
	add := func(role string, parts ...GeminiPart) {
		if len(parts) == 0 {
			return
		}
		if len(contents) > 0 && contents[len(contents)-1].Role == role {
			contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
			return
		}
		contents = append(contents, GeminiContent{Role: role, Parts: parts})
	}
	for _, raw := range steps {
		var step interactionContent
		if err := json.Unmarshal(raw, &step); err != nil {
			return nil, nil, fmt.Errorf("input: %w", err)
		}
		switch step.Type {
		case "user_input", "model_output":
			role := "user"
			if step.Type == "model_output" {
				role = "model"
			}
			for _, content := range step.Content {
				part, fields, err := content.part()
				if err != nil {
					return nil, nil, err
				}
				unsupported = append(unsupported, fields...)
				add(role, part)
			}
		case "thought":
		case "function_call":
			if step.ID == "" || step.Name == "" {
				return nil, nil, fmt.Errorf("function_call requires id, name and arguments")
			}
			var arguments map[string]any
			if len(step.Arguments) > 0 && string(step.Arguments) != "null" {
				if err := json.Unmarshal(step.Arguments, &arguments); err != nil {
					return nil, nil, fmt.Errorf("function_call arguments must be an object: %w", err)
				}
			}
			add("model", GeminiPart{FunctionCall: &GeminiFunctionCall{ID: step.ID, Name: step.Name, Args: arguments}})
		case "function_result":
			if step.CallID == "" || len(step.Result) == 0 {
				return nil, nil, fmt.Errorf("function_result requires call_id and result")
			}
			response, parts, err := interactionFunctionResult(step.Result, step.IsError)
			if err != nil {
				return nil, nil, err
			}
			add("user", GeminiPart{FunctionResponse: &GeminiFunctionResponse{ID: step.CallID, Name: step.Name, Response: response, Parts: parts}})
		case "code_execution_call":
			var arguments struct {
				Code     string `json:"code"`
				Language string `json:"language"`
			}
			_ = json.Unmarshal(step.Arguments, &arguments)
			add("model", GeminiPart{ExecutableCode: &GeminiExecutableCode{ID: step.ID, Language: strings.ToUpper(arguments.Language), Code: arguments.Code}})
		case "code_execution_result":
			var output string
			if json.Unmarshal(step.Result, &output) != nil {
				output = string(step.Result)
			}
			outcome := "OUTCOME_OK"
			if step.IsError {
				outcome = "OUTCOME_FAILED"
			}
			add("model", GeminiPart{CodeResult: &GeminiCodeExecutionResult{ID: step.CallID, Outcome: outcome, Output: output}})
		default:
			if slices.Contains(interactionServerSteps, step.Type) {
				continue
			}
			return nil, nil, fmt.Errorf("unsupported input step type %q", step.Type)
		}
	}
	return contents, unsupported, nil
}

// part 把内容块转为 Gemini 内容段：文本为 text，带 data 的媒体为 inlineData，带 uri 的媒体为 fileData，并返回 Gemini Web 不使用的字段
func (content interactionContent) part() (GeminiPart, []string, error) {
	var unsupported []string
	if len(content.Resolution) > 0 {
		unsupported = append(unsupported, "input.resolution")
	}
	if len(content.Processing) > 0 {
		unsupported = append(unsupported, "input.processing")
	}
	switch content.Type {
	case "text":
		if content.Text == nil {
			return GeminiPart{}, nil, fmt.Errorf("text content requires text")
		}
		return GeminiPart{Text: *content.Text}, unsupported, nil
	case "image", "audio", "video", "document":
		if (content.Data == "") == (content.URI == "") {
			return GeminiPart{}, nil, fmt.Errorf("%s content requires exactly one of data or uri", content.Type)
		}
		if content.URI != "" {
			return GeminiPart{FileData: &GeminiFileData{MimeType: content.MIMEType, FileURI: content.URI}}, unsupported, nil
		}
		mimeType := content.MIMEType
		if mimeType == "" {
			head := content.Data[:min(len(content.Data), 1024)]
			head = head[:len(head)/4*4]
			decoded, err := base64.StdEncoding.DecodeString(head)
			if err != nil {
				return GeminiPart{}, nil, fmt.Errorf("%s data is not valid base64: %w", content.Type, err)
			}
			mimeType = http.DetectContentType(decoded)
		}
		return GeminiPart{InlineData: &GeminiInlineData{MimeType: mimeType, Data: content.Data}}, unsupported, nil
	}
	return GeminiPart{}, nil, fmt.Errorf("unsupported content type %q", content.Type)
}

// interactionFunctionResult 把 function_result 的 result 转为 Gemini functionResponse：字符串为 output，对象原样使用，内容块数组的文本合并为 output、图片随结果发送，is_error 时写为 error
func interactionFunctionResult(raw json.RawMessage, failed bool) (json.RawMessage, []GeminiPart, error) {
	key := "output"
	if failed {
		key = "error"
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		response, _ := json.Marshal(gin.H{key: text})
		return response, nil, nil
	}
	var items []interactionContent
	if json.Unmarshal(raw, &items) == nil {
		var texts []string
		var parts []GeminiPart
		for _, item := range items {
			if item.Type == "text" && item.Text != nil {
				texts = append(texts, *item.Text)
				continue
			}
			part, _, err := item.part()
			if err != nil {
				return nil, nil, fmt.Errorf("function_result: %w", err)
			}
			parts = append(parts, part)
		}
		response, _ := json.Marshal(gin.H{key: strings.Join(texts, "\n")})
		return response, parts, nil
	}
	if failed {
		response, _ := json.Marshal(gin.H{key: raw})
		return response, nil, nil
	}
	return raw, nil, nil
}

// geminiRequest 把交互请求转为 Gemini 生成请求，返回 Gemini Web 不使用的字段
func (request interactionRequest) geminiRequest(contents []GeminiContent) (GeminiGenerateContentRequest, []string, error) {
	generate := GeminiGenerateContentRequest{Contents: contents}
	if strings.TrimSpace(request.SystemInstruction) != "" {
		generate.SystemInstruction = &GeminiContent{Parts: []GeminiPart{{Text: request.SystemInstruction}}}
	}
	var unsupported []string
	for field, value := range map[string]json.RawMessage{
		"safety_settings": request.SafetySettings, "agent_config": request.AgentConfig, "service_tier": request.ServiceTier,
		"webhook_config": request.WebhookConfig, "environment": request.Environment,
		"generation_config.temperature": request.Generation.Temperature, "generation_config.top_p": request.Generation.TopP,
		"generation_config.top_k": request.Generation.TopK, "generation_config.seed": request.Generation.Seed,
		"generation_config.speech_config": request.Generation.SpeechConfig, "generation_config.transcription_config": request.Generation.TranscriptionConfig,
		"generation_config.video_config": request.Generation.VideoConfig,
	} {
		if len(value) > 0 && string(value) != "null" {
			unsupported = append(unsupported, field)
		}
	}
	for _, modality := range request.ResponseModalities {
		if !strings.EqualFold(modality, "text") && !strings.EqualFold(modality, "image") {
			unsupported = append(unsupported, "response_modalities")
		}
	}
	config := gin.H{}
	g := request.Generation
	if g.MaxOutputTokens != nil {
		config["maxOutputTokens"] = *g.MaxOutputTokens
	}
	if len(g.StopSequences) > 0 {
		config["stopSequences"] = g.StopSequences
	}
	switch g.ThinkingSummaries {
	case "", "auto", "none":
	default:
		return generate, nil, fmt.Errorf("thinking_summaries must be auto or none")
	}
	thinking := gin.H{"includeThoughts": g.ThinkingSummaries != "none"}
	if g.ThinkingLevel != "" {
		thinking["thinkingLevel"] = g.ThinkingLevel
	}
	config["thinkingConfig"] = thinking
	if image := g.ImageConfig; image != nil {
		if image.AspectRatio != "" {
			config["imageConfig"] = gin.H{"aspectRatio": image.AspectRatio}
		}
		if image.ImageSize != "" {
			unsupported = append(unsupported, "generation_config.image_config.image_size")
		}
	}
	formats := []json.RawMessage{request.ResponseFormat}
	if value := strings.TrimSpace(string(request.ResponseFormat)); value == "" || value == "null" {
		formats = nil
	} else if strings.HasPrefix(value, "[") {
		if err := json.Unmarshal(request.ResponseFormat, &formats); err != nil {
			return generate, nil, fmt.Errorf("response_format: %w", err)
		}
	}
	for _, raw := range formats {
		var format struct {
			Type        string          `json:"type"`
			MIMEType    string          `json:"mime_type"`
			Schema      json.RawMessage `json:"schema"`
			AspectRatio string          `json:"aspect_ratio"`
			ImageSize   string          `json:"image_size"`
		}
		if err := json.Unmarshal(raw, &format); err != nil {
			return generate, nil, fmt.Errorf("response_format: %w", err)
		}
		switch format.Type {
		case "text":
			if format.MIMEType != "" {
				config["responseMimeType"] = format.MIMEType
			}
			if len(format.Schema) > 0 {
				config["responseMimeType"], config["responseJsonSchema"] = "application/json", format.Schema
			}
		case "image":
			if format.AspectRatio != "" {
				config["imageConfig"] = gin.H{"aspectRatio": format.AspectRatio}
			}
			if format.ImageSize != "" {
				unsupported = append(unsupported, "response_format.image_size")
			}
		case "audio", "video":
			unsupported = append(unsupported, "response_format."+format.Type)
		default:
			config["responseMimeType"], config["responseJsonSchema"] = "application/json", raw
		}
	}
	if request.ResponseMIMEType != "" {
		config["responseMimeType"] = request.ResponseMIMEType
	}
	generate.GenerationConfig, _ = json.Marshal(config)

	var declarations []gin.H
	var tools []gin.H
	for _, tool := range request.Tools {
		switch tool.Type {
		case "function":
			if tool.Name == "" {
				return generate, nil, fmt.Errorf("function tool requires name")
			}
			declaration := gin.H{"name": tool.Name, "description": tool.Description}
			if len(tool.Parameters) > 0 && string(tool.Parameters) != "null" {
				declaration["parametersJsonSchema"] = tool.Parameters
			}
			declarations = append(declarations, declaration)
		case "google_search":
			tools = append(tools, gin.H{"googleSearch": gin.H{}})
		case "url_context":
			tools = append(tools, gin.H{"urlContext": gin.H{}})
		case "code_execution":
			tools = append(tools, gin.H{"codeExecution": gin.H{}})
		default:
			return generate, nil, fmt.Errorf("unsupported interaction tool %q: %s", tool.Type, unavailableCapability("the "+tool.Type+" tool"))
		}
	}
	if len(declarations) > 0 {
		tools = append([]gin.H{{"functionDeclarations": declarations}}, tools...)
	}
	if len(tools) > 0 {
		generate.Tools, _ = json.Marshal(tools)
	}
	if len(g.ToolChoice) > 0 && string(g.ToolChoice) != "null" {
		var mode string
		var names []string
		if json.Unmarshal(g.ToolChoice, &mode) != nil {
			var choice struct {
				AllowedTools struct {
					Mode  string   `json:"mode"`
					Tools []string `json:"tools"`
				} `json:"allowed_tools"`
			}
			if err := json.Unmarshal(g.ToolChoice, &choice); err != nil {
				return generate, nil, fmt.Errorf("generation_config.tool_choice: %w", err)
			}
			mode, names = choice.AllowedTools.Mode, choice.AllowedTools.Tools
		}
		calling := gin.H{"mode": strings.ToUpper(mode)}
		if len(names) > 0 {
			calling["allowedFunctionNames"] = names
		}
		generate.ToolConfig, _ = json.Marshal(gin.H{"functionCallingConfig": calling})
	}
	return generate, unsupported, nil
}

// interactionSteps 把 Gemini 响应的内容段转为交互步骤：思考为带签名的 thought，相邻的正文与媒体合并为 model_output，代码、代码结果与函数调用各为一个步骤，搜索来源写入正文的 annotations；id 为交互 ID，用于代码步骤的 ID
func interactionSteps(parts []gin.H, citations []gemini.Citation, id string) []gin.H {
	steps := make([]gin.H, 0, len(parts))
	offset := 0
	codes := codeStepIDs{prefix: id}
	var thought, output gin.H
	signature := ""
	for _, part := range parts {
		if value, ok := part["thoughtSignature"].(string); ok && signature == "" {
			signature = value
		}
		text, hasText := part["text"].(string)
		if isThought, _ := part["thought"].(bool); isThought {
			output = nil
			if text == "" {
				continue
			}
			thought = gin.H{"type": "thought", "summary": []gin.H{{"type": "text", "text": text}}}
			steps = append(steps, thought)
			continue
		}
		content := interactionContentBlock(part)
		if hasText && text == "" {
			continue
		}
		if content != nil {
			if text != "" {
				if annotations := interactionAnnotations(text, offset, citations); len(annotations) > 0 {
					content["annotations"] = annotations
				}
				offset += len([]rune(text))
			}
			if output == nil {
				output = gin.H{"type": "model_output", "content": []gin.H{}}
				steps = append(steps, output)
			}
			output["content"] = append(output["content"].([]gin.H), content)
			continue
		}
		output = nil
		if step := codes.step(part); step != nil {
			steps = append(steps, step)
		}
	}
	if thought != nil && signature != "" {
		thought["signature"] = signature
	}
	return steps
}

// interactionContentBlock 把正文与内联媒体内容段转为内容块，其他内容段返回 nil
func interactionContentBlock(part gin.H) gin.H {
	if text, ok := part["text"].(string); ok {
		return gin.H{"type": "text", "text": text}
	}
	if data, ok := part["inlineData"].(gin.H); ok {
		mimeType, _ := data["mimeType"].(string)
		return gin.H{"type": interactionMediaType(mimeType), "mime_type": mimeType, "data": data["data"]}
	}
	return nil
}

// codeStepIDs 为代码与代码结果步骤编号，代码结果的 call_id 为其前最近一次代码的 ID
type codeStepIDs struct {
	prefix string
	count  int
	last   string
}

// step 把代码、代码结果与函数调用内容段转为独立步骤，其他内容段返回 nil
func (ids *codeStepIDs) step(part gin.H) gin.H {
	switch {
	case part["functionCall"] != nil:
		call, _ := part["functionCall"].(gin.H)
		arguments := call["args"]
		if arguments == nil {
			arguments = gin.H{}
		}
		return gin.H{"type": "function_call", "id": call["id"], "name": call["name"], "arguments": arguments}
	case part["executableCode"] != nil:
		code, _ := part["executableCode"].(gin.H)
		language, _ := code["language"].(string)
		ids.count++
		ids.last = fmt.Sprintf("%s_code_%d", ids.prefix, ids.count)
		return gin.H{"type": "code_execution_call", "id": ids.last, "arguments": gin.H{"code": code["code"], "language": strings.ToLower(language)}}
	case part["codeExecutionResult"] != nil:
		result, _ := part["codeExecutionResult"].(gin.H)
		return gin.H{"type": "code_execution_result", "call_id": ids.last, "result": result["output"], "is_error": result["outcome"] != "OUTCOME_OK"}
	}
	return nil
}

// interactionMediaType 返回 MIME 类型对应的内容块类型
func interactionMediaType(mimeType string) string {
	kind, _, _ := strings.Cut(mimeType, "/")
	if kind == "image" || kind == "audio" || kind == "video" {
		return kind
	}
	return "document"
}

// interactionAnnotations 返回落在一段正文内的搜索来源，offset 为该段正文在回答正文中的起始字符位置
func interactionAnnotations(text string, offset int, citations []gemini.Citation) []gin.H {
	length := len([]rune(text))
	var annotations []gin.H
	for _, citation := range citations {
		start, end := citation.Start-offset, citation.End-offset
		if start < 0 || end > length || end < start {
			continue
		}
		annotations = append(annotations, gin.H{"type": "url_citation", "url": citation.URL, "title": citation.Title, "start_index": start, "end_index": end})
	}
	return annotations
}

// saveInteraction 在 store 不为 false 时保存交互对象与输入步骤，后台交互另记 background
func saveInteraction(ctx context.Context, store *requestdb.Store, run interactionRun, object gin.H) {
	if store == nil || run.request.Store != nil && !*run.request.Store {
		return
	}
	encoded, err := json.Marshal(fieldorder.Official("interactions", object))
	if err != nil {
		return
	}
	input, _ := json.Marshal(run.input)
	created, _ := time.Parse(time.RFC3339, run.created)
	status, _ := object["status"].(string)
	if err := store.SaveResponse(context.WithoutCancel(ctx), requestdb.StoredResponse{
		ID: run.id, CreatedAt: created, PreviousID: run.request.PreviousID, Background: run.request.Background,
		Status: status, Response: encoded, Input: input,
	}); err != nil {
		logging.Warn(logging.SourceRequest, "保存响应失败", "响应", run.id, "错误", err)
	}
}

// failedInteraction 返回以错误结束的交互对象
func failedInteraction(c *gin.Context, run interactionRun, err error) gin.H {
	object := run.object("failed", nil)
	object["errors"] = []gin.H{{"code": geminiUpstreamStatus(err), "message": publicMessage(c, err.Error())}}
	return object
}
