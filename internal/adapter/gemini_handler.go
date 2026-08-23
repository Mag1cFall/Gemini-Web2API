package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/tokencount"
	"github.com/gin-gonic/gin"
)

// GeminiInlineData 表示 Gemini 内联文件
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GeminiFileData 表示 Gemini 远程文件
type GeminiFileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

// GeminiFunctionCall 表示 Gemini 函数调用
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	ID   string                 `json:"id,omitempty"`
	Args map[string]interface{} `json:"args"`
}

// GeminiFunctionResponse 表示 Gemini 函数结果
type GeminiFunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// GeminiPart 表示 Gemini 内容段
type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	InlineData       *GeminiInlineData       `json:"inlineData,omitempty"`
	FileData         *GeminiFileData         `json:"fileData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
}

// GeminiContent 表示 Gemini 消息
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiGenerateContentRequest 表示 Gemini 生成请求
type GeminiGenerateContentRequest struct {
	Contents           []GeminiContent `json:"contents"`
	SystemInstruction  *GeminiContent  `json:"systemInstruction,omitempty"`
	GenerationConfig   json.RawMessage `json:"generationConfig,omitempty"`
	Tools              json.RawMessage `json:"tools,omitempty"`
	ToolConfig         json.RawMessage `json:"toolConfig,omitempty"`
	ConversationID     string          `json:"conversation_id,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
}

// GeminiRouterHandler 处理 Gemini 原生生成接口
func GeminiRouterHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		action := strings.TrimPrefix(c.Param("action"), "/")
		separator := strings.LastIndex(action, ":")
		if separator < 0 {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "expected models/{model}:{method}")
			return
		}
		model, method := action[:separator], action[separator+1:]
		if method != "generateContent" && method != "streamGenerateContent" && method != "countTokens" {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "unknown method: "+method)
			return
		}

		var request GeminiGenerateContentRequest
		err := decodeRequest(c, &request)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		if len(request.Contents) == 0 {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "contents is required")
			return
		}
		knownModel, readyModel := modelStatus(pool, model)
		if !knownModel {
			writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("model %q is unavailable", model))
			return
		}
		if method == "countTokens" {
			countGeminiTokens(c, request)
			return
		}
		if !readyModel {
			writeGeminiError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "No account is ready for the requested model")
			return
		}
		thinkingMode, err := geminiRequestThinkingMode(request)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}

		bridge, unsupported, err := geminiToolBridge(request)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		setUnsupportedParameters(c, unsupported)
		sessionKey := conversationKey(request.PreviousResponseID, request.ConversationID, c.GetHeader("X-Conversation-ID"))
		responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
		streaming := method == "streamGenerateContent"

		if streaming {
			setSSEHeaders(c)
			projection := newStreamProjection(bridge)
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", model, responseID, false, thinkingMode,
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildGeminiPrompt(c, client, request, continuation, bridge)
				}, func(event gemini.Event) error {
					return projection.project(event, func(event gemini.Event) error {
						return writeGeminiDelta(c.Writer, responseID, event)
					}, func(err error) error {
						return writeGeminiStreamError(c.Writer, http.StatusBadGateway, "INTERNAL", err)
					})
				},
			)
			c.Set("account_id", accountID)
			if err != nil {
				if !projection.errorSent {
					_ = writeGeminiStreamError(c.Writer, upstreamStatus(err), geminiUpstreamStatus(err), err)
				}
				return
			}
			response, err := buildGeminiResponse(model, result, bridge)
			if err != nil {
				_ = writeGeminiStreamError(c.Writer, http.StatusBadGateway, "INTERNAL", err)
				return
			}
			_ = writeSSEJSON(c.Writer, projectGeminiStreamTail(response, projection))
			return
		}

		result, accountID, err := runGeneration(
			c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", model, responseID, false, thinkingMode,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildGeminiPrompt(c, client, request, continuation, bridge)
			}, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
			return
		}

		response, err := buildGeminiResponse(model, result, bridge)
		if err != nil {
			writeGeminiError(c, http.StatusBadGateway, "INTERNAL", err.Error())
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

func countGeminiTokens(c *gin.Context, request GeminiGenerateContentRequest) {
	contents := request.Contents
	if request.SystemInstruction != nil {
		contents = append(contents, *request.SystemInstruction)
	}
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.InlineData != nil || part.FileData != nil {
				writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "本地 countTokens 当前只支持文本与工具内容")
				return
			}
		}
	}
	bridge, unsupported, err := geminiToolBridge(request)
	if err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	setUnsupportedParameters(c, unsupported)
	prompt, _, err := buildGeminiPrompt(c, nil, request, false, bridge)
	if err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"totalTokens": tokencount.Content(prompt)})
}

// GeminiListModelsHandler 返回 Gemini 原生模型目录
func GeminiListModelsHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		models := availableModels(pool)
		data := make([]gin.H, 0, len(models))
		for _, model := range models {
			data = append(data, gin.H{
				"name": "models/" + model.ID, "displayName": model.DisplayName, "description": model.Description,
				"supportedGenerationMethods": model.Capabilities, "default": model.Default,
			})
		}
		c.JSON(http.StatusOK, gin.H{"models": data})
	}
}

func buildGeminiPrompt(c *gin.Context, client *gemini.Client, request GeminiGenerateContentRequest, continuation bool, bridge ToolBridge) (string, []gemini.FileData, error) {
	contents := request.Contents
	if continuation {
		for index := len(contents) - 1; index >= 0; index-- {
			if contents[index].Role == "model" {
				contents = contents[index+1:]
				break
			}
		}
	}
	records := make([]map[string]interface{}, 0, len(contents)+1)
	var files []gemini.FileData
	if request.SystemInstruction != nil && !continuation {
		text, attachments, err := decodeGeminiParts(c, client, request.SystemInstruction.Parts)
		if err != nil {
			return "", nil, err
		}
		records = append(records, map[string]interface{}{"role": "system", "content": text})
		files = append(files, attachments...)
	}
	for _, content := range contents {
		text, attachments, err := decodeGeminiParts(c, client, content.Parts)
		if err != nil {
			return "", nil, err
		}
		records = append(records, map[string]interface{}{"role": transcriptRole(content.Role), "content": text})
		files = append(files, attachments...)
	}
	transcript, err := json.Marshal(map[string]interface{}{"messages": records})
	if err != nil {
		return "", nil, err
	}
	suffix, err := bridge.PromptSuffix()
	if err != nil {
		return "", nil, err
	}
	return string(transcript) + suffix, files, nil
}

func decodeGeminiParts(c *gin.Context, client *gemini.Client, parts []GeminiPart) (string, []gemini.FileData, error) {
	var builder strings.Builder
	var files []gemini.FileData
	for _, part := range parts {
		builder.WriteString(part.Text)
		if part.FunctionCall != nil {
			arguments, _ := json.Marshal(part.FunctionCall.Args)
			appendTranscriptObject(&builder, map[string]interface{}{"type": "tool_use", "id": part.FunctionCall.ID, "name": part.FunctionCall.Name, "signature": part.ThoughtSignature, "arguments": json.RawMessage(arguments)})
		}
		if part.FunctionResponse != nil {
			response, _ := json.Marshal(part.FunctionResponse.Response)
			appendTranscriptObject(&builder, map[string]interface{}{"type": "tool_result", "id": part.FunctionResponse.ID, "name": part.FunctionResponse.Name, "response": json.RawMessage(response)})
		}
		if part.InlineData != nil {
			raw, _ := json.Marshal([]gin.H{{"type": "image", "source": gin.H{"type": "base64", "media_type": part.InlineData.MimeType, "data": part.InlineData.Data}}})
			label, attachments, err := decodeMessageContent(c.Request.Context(), raw, client)
			if err != nil {
				return "", nil, err
			}
			builder.WriteString(label)
			files = append(files, attachments...)
		}
		if part.FileData != nil && part.FileData.FileURI != "" {
			data, err := client.FetchMedia(c.Request.Context(), part.FileData.FileURI)
			if err != nil {
				return "", nil, err
			}
			filename := fmt.Sprintf("file_%d%s", time.Now().UnixNano(), mimeTypeToExt(part.FileData.MimeType))
			fileID, err := client.UploadFile(c.Request.Context(), data, filename)
			if err != nil {
				return "", nil, err
			}
			files = append(files, gemini.FileData{URL: fileID, FileName: filename})
			builder.WriteString("[File]")
		}
	}
	return builder.String(), files, nil
}

func geminiToolBridge(request GeminiGenerateContentRequest) (ToolBridge, []string, error) {
	bridge := ToolBridge{Choice: "auto"}
	var unsupported []string
	if len(request.GenerationConfig) > 0 && string(request.GenerationConfig) != "null" {
		var config map[string]json.RawMessage
		if err := json.Unmarshal(request.GenerationConfig, &config); err != nil {
			return bridge, nil, err
		}
		for field := range config {
			if field == "responseMimeType" || field == "responseJsonSchema" || field == "responseSchema" || field == "thinkingConfig" {
				continue
			}
			unsupported = append(unsupported, "generationConfig."+field)
		}
		var mime string
		_ = json.Unmarshal(config["responseMimeType"], &mime)
		bridge.JSONSchema = config["responseJsonSchema"]
		if len(bridge.JSONSchema) == 0 {
			bridge.JSONSchema = config["responseSchema"]
		}
		if mime == "application/json" || len(bridge.JSONSchema) > 0 {
			if len(bridge.JSONSchema) == 0 {
				bridge.JSONSchema = json.RawMessage(`{"type":"object"}`)
			}
		}
	}
	if len(request.Tools) > 0 && string(request.Tools) != "null" {
		var groups []map[string]json.RawMessage
		if err := json.Unmarshal(request.Tools, &groups); err != nil {
			return bridge, nil, err
		}
		for _, group := range groups {
			for field := range group {
				switch field {
				case "functionDeclarations", "googleSearch", "googleSearchRetrieval":
				default:
					return bridge, nil, fmt.Errorf("不支持的 Gemini 工具组 %q", field)
				}
			}
			if len(group["googleSearch"]) > 0 || len(group["googleSearchRetrieval"]) > 0 {
				return bridge, nil, fmt.Errorf("Gemini Web search 工具尚未映射")
			}
			var declarations []struct {
				Name                       string          `json:"name"`
				Description                string          `json:"description"`
				Parameters                 json.RawMessage `json:"parameters"`
				ParametersJSONSchema       json.RawMessage `json:"parametersJsonSchema"`
				ParametersJSONSchemaPython json.RawMessage `json:"parameters_json_schema"`
			}
			if err := json.Unmarshal(group["functionDeclarations"], &declarations); err != nil && len(group["functionDeclarations"]) > 0 {
				return bridge, nil, err
			}
			for _, function := range declarations {
				parameters := function.Parameters
				if len(function.ParametersJSONSchema) > 0 {
					parameters = function.ParametersJSONSchema
				}
				if len(function.ParametersJSONSchemaPython) > 0 {
					parameters = function.ParametersJSONSchemaPython
				}
				bridge.Definitions = append(bridge.Definitions, ToolDefinition{Name: function.Name, Description: function.Description, Parameters: parameters})
			}
		}
	}
	if len(request.ToolConfig) > 0 && string(request.ToolConfig) != "null" {
		var rawConfig map[string]json.RawMessage
		if err := json.Unmarshal(request.ToolConfig, &rawConfig); err != nil {
			return bridge, nil, err
		}
		for field := range rawConfig {
			if field != "functionCallingConfig" {
				return bridge, nil, fmt.Errorf("不支持的 toolConfig 字段 %q", field)
			}
		}
		var functionConfig map[string]json.RawMessage
		if err := json.Unmarshal(rawConfig["functionCallingConfig"], &functionConfig); err != nil {
			return bridge, nil, err
		}
		for field := range functionConfig {
			if field != "mode" && field != "allowedFunctionNames" {
				return bridge, nil, fmt.Errorf("不支持的 functionCallingConfig 字段 %q", field)
			}
		}
		var mode string
		var allowedNames []string
		_ = json.Unmarshal(functionConfig["mode"], &mode)
		_ = json.Unmarshal(functionConfig["allowedFunctionNames"], &allowedNames)
		switch strings.ToUpper(mode) {
		case "NONE":
			bridge.Choice = "none"
		case "ANY":
			bridge.Choice = "required"
			if len(allowedNames) == 1 {
				bridge.Choice = allowedNames[0]
			}
		case "", "AUTO":
		default:
			return bridge, nil, fmt.Errorf("不支持的 functionCallingConfig.mode %q", mode)
		}
		if len(allowedNames) > 0 {
			allowed := make(map[string]struct{}, len(allowedNames))
			for _, name := range allowedNames {
				allowed[name] = struct{}{}
			}
			filtered := make([]ToolDefinition, 0, len(allowed))
			for _, definition := range bridge.Definitions {
				if _, ok := allowed[definition.Name]; ok {
					filtered = append(filtered, definition)
					delete(allowed, definition.Name)
				}
			}
			if len(allowed) != 0 {
				return bridge, nil, fmt.Errorf("allowedFunctionNames 包含未声明的工具")
			}
			bridge.Definitions = filtered
		}
	}
	sort.Strings(unsupported)
	return bridge, unsupported, nil
}

func buildGeminiResponse(model string, result generationResult, bridge ToolBridge) (gin.H, error) {
	primary := result.Accumulator.Primary()
	content, calls, err := bridge.Parse(primary.Text)
	if err != nil {
		return nil, err
	}
	calls = assignToolCallIDs(calls, result.ResponseID)
	parts := make([]gin.H, 0)
	if primary.Thought != "" {
		parts = append(parts, gin.H{"text": primary.Thought, "thought": true})
	}
	if content != "" {
		parts = append(parts, gin.H{"text": content})
	}
	for index, call := range calls {
		var arguments map[string]interface{}
		_ = json.Unmarshal(call.Arguments, &arguments)
		parts = append(parts, gin.H{
			"functionCall":     gin.H{"id": call.ID, "name": call.Name, "args": arguments},
			"thoughtSignature": thinkingSignature(result.ResponseID, index+1),
		})
	}
	for _, image := range primary.Images {
		parts = append(parts, gin.H{"fileData": gin.H{"fileUri": image.URL}})
	}
	finish := "STOP"
	response := gin.H{
		"candidates":   []gin.H{{"content": gin.H{"role": "model", "parts": parts}, "finishReason": finish, "index": 0}},
		"modelVersion": result.ProviderModel, "responseId": result.ResponseID, "conversationId": result.ConversationID,
	}
	if result.Accumulator.Usage != nil {
		response["usageMetadata"] = geminiUsage(result.Accumulator.Usage)
	}
	return response, nil
}

func writeGeminiDelta(w http.ResponseWriter, responseID string, event gemini.Event) error {
	part := gin.H{"text": event.Delta}
	if event.Kind == gemini.EventThought {
		part["thought"] = true
	}
	return writeSSEJSON(w, gin.H{
		"candidates": []gin.H{{"content": gin.H{"role": "model", "parts": []gin.H{part}}, "index": 0}},
		"responseId": responseID,
	})
}

func writeGeminiStreamError(w http.ResponseWriter, status int, statusName string, err error) error {
	return writeSSEJSON(w, gin.H{"error": gin.H{"code": status, "message": err.Error(), "status": statusName}})
}

func projectGeminiStreamTail(response gin.H, projection *streamProjection) gin.H {
	candidates, _ := response["candidates"].([]gin.H)
	for _, candidate := range candidates {
		content, _ := candidate["content"].(gin.H)
		parts, _ := content["parts"].([]gin.H)
		filtered := make([]gin.H, 0, len(parts))
		for _, part := range parts {
			_, hasText := part["text"]
			thought, _ := part["thought"].(bool)
			if hasText && thought && !projection.bufferThought {
				continue
			}
			if hasText && !thought && !projection.bufferText {
				continue
			}
			filtered = append(filtered, part)
		}
		content["parts"] = filtered
	}
	return response
}

func geminiUsage(usage *gemini.Usage) gin.H {
	result := gin.H{"promptTokenCount": usage.PromptTokens, "candidatesTokenCount": usage.CompletionTokens, "totalTokenCount": usage.TotalTokens}
	if usage.ThoughtTokens > 0 {
		result["thoughtsTokenCount"] = usage.ThoughtTokens
	}
	return result
}

func mimeTypeToExt(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "application/pdf":
		return ".pdf"
	default:
		return ".bin"
	}
}
