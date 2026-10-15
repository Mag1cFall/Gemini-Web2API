package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/Mag1cFall/Gemini-Web2API/internal/streamio"
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

// GeminiExecutableCode 表示 Gemini 已执行代码
type GeminiExecutableCode struct {
	ID       string `json:"id,omitempty"`
	Language string `json:"language"`
	Code     string `json:"code"`
}

// GeminiCodeExecutionResult 表示 Gemini 代码执行结果
type GeminiCodeExecutionResult struct {
	ID      string `json:"id,omitempty"`
	Outcome string `json:"outcome"`
	Output  string `json:"output"`
}

// GeminiPart 表示 Gemini 内容段
type GeminiPart struct {
	Text             string                     `json:"text,omitempty"`
	Thought          bool                       `json:"thought,omitempty"`
	ThoughtSignature string                     `json:"thoughtSignature,omitempty"`
	InlineData       *GeminiInlineData          `json:"inlineData,omitempty"`
	FileData         *GeminiFileData            `json:"fileData,omitempty"`
	FunctionCall     *GeminiFunctionCall        `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse    `json:"functionResponse,omitempty"`
	ExecutableCode   *GeminiExecutableCode      `json:"executableCode,omitempty"`
	CodeResult       *GeminiCodeExecutionResult `json:"codeExecutionResult,omitempty"`
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
	// GenerateContentRequest 为 countTokens 请求包装的完整生成请求
	GenerateContentRequest *GeminiGenerateContentRequest `json:"generateContentRequest,omitempty"`
}

// GeminiRouterHandler 处理 Gemini 原生 models/{model}:{method}，生成、流式生成、计数与视频长时操作之外的方法返回 404
func GeminiRouterHandler(pool *balancer.AccountPool, store *requestdb.Store) gin.HandlerFunc {
	useStore(store)
	return func(c *gin.Context) {
		action := strings.TrimPrefix(c.Param("action"), "/")
		separator := strings.LastIndex(action, ":")
		if separator < 0 {
			writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("Method not found: POST %s", c.Request.URL.Path))
			return
		}
		model, method := strings.TrimPrefix(action[:separator], "models/"), action[separator+1:]
		bindScope(c, pool, model)
		switch method {
		case "generateContent", "streamGenerateContent", "countTokens":
		case "predictLongRunning":
			if videoModel(pool, model) {
				geminiVideoOperation(c, pool, store, model)
				return
			}
			writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", geminiUnsupportedMethod(model, method, "video generation for this model"))
			return
		default:
			writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", geminiUnsupportedMethod(model, method, geminiMethodCapability(method)))
			return
		}

		var request GeminiGenerateContentRequest
		err := decodeRequest(c, &request)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		if method == "countTokens" && len(request.Contents) == 0 && request.GenerateContentRequest != nil {
			request = *request.GenerateContentRequest
		}
		if method != "countTokens" {
			logging.FromContext(c.Request.Context()).Begin("gemini", model, method == "streamGenerateContent", len(request.Contents), geminiToolCount(request.Tools))
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
			countGeminiTokens(c, pool, model, request)
			return
		}
		if !readyModel {
			writeGeminiError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "No account is ready for the requested model")
			return
		}
		thinkingLevel, includeThoughts, err := geminiRequestThinkingSettings(request)
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
			writer := newGeminiStreamWriter(c, c.Query("alt") == "sse")
			defer writer.close()
			defer startKeepalive(c, geminiKeepalive)()
			writer.w = c.Writer
			projection := newStreamProjection(bridge, includeThoughts)
			streamModel := config.MapModel(model)
			result, accountID, err := runGeneration(
				c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", model, responseID, generationMedia{aspectRatio: bridge.AspectRatio}, thinkingLevel, bridge,
				func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
					return buildGeminiPrompt(c, client, request, continuation, bridge)
				}, func(event gemini.Event) error {
					return projection.project(event, func(event gemini.Event) error {
						return writer.delta(responseID, streamModel, event)
					}, func(err error) error {
						return writer.fail(http.StatusBadGateway, "INTERNAL", err)
					})
				},
				projection.hasVisibleOutput,
			)
			c.Set("account_id", accountID)
			if err != nil {
				if !projection.errorSent {
					_ = writer.fail(upstreamStatus(err), geminiUpstreamStatus(err), streamFailure(c, err))
				}
				return
			}
			if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
				_ = writer.fail(http.StatusBadGateway, "INTERNAL", streamFailure(c, err))
				return
			}
			response, err := buildGeminiResponse(result, bridge, includeThoughts)
			if err != nil {
				_ = writer.fail(http.StatusBadGateway, "INTERNAL", streamFailure(c, err))
				return
			}
			_ = writer.write(projectGeminiStreamTail(response, projection))
			return
		}

		result, accountID, err := runGeneration(
			c.Request.Context(), pool, sessionKey, request.PreviousResponseID == "", model, responseID, generationMedia{aspectRatio: bridge.AspectRatio}, thinkingLevel, bridge,
			func(client *gemini.Client, continuation bool) (string, []gemini.FileData, error) {
				return buildGeminiPrompt(c, client, request, continuation, bridge)
			}, nil, nil,
		)
		c.Set("account_id", accountID)
		if err != nil {
			writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
			return
		}

		if err := inlineResultMedia(c.Request.Context(), &result); err != nil {
			writeGeminiError(c, http.StatusBadGateway, "INTERNAL", err.Error())
			return
		}
		response, err := buildGeminiResponse(result, bridge, includeThoughts)
		if err != nil {
			writeGeminiError(c, http.StatusBadGateway, "INTERNAL", err.Error())
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

// geminiUnsupportedMethod 返回与官方相同的方法不可用说明，并写明 Gemini Web 不提供的能力
func geminiUnsupportedMethod(model string, method string, capability string) string {
	return fmt.Sprintf("models/%s is not found for API version v1beta, or is not supported for %s. Call ListModels to see the list of available models and their supported methods. %s.", model, method, unavailableCapability(capability))
}

// geminiMethodCapability 返回 Gemini 方法对应的能力名称
func geminiMethodCapability(method string) string {
	switch method {
	case "embedContent", "batchEmbedContents", "asyncBatchEmbedContent", "embedText", "batchEmbedText":
		return "embeddings"
	case "predict":
		return "Imagen predictions"
	case "bidiGenerateContent":
		return "the Live API"
	case "batchGenerateContent":
		return "batch generation"
	case "generateAnswer":
		return "attributed question answering"
	default:
		return "the " + method + " method"
	}
}

// countGeminiTokens 以生成时的提示词编码计算文本 token，附件按官方计数规则估算并按模态列出
func countGeminiTokens(c *gin.Context, pool *balancer.AccountPool, model string, request GeminiGenerateContentRequest) {
	bridge, unsupported, err := geminiToolBridge(request)
	if err != nil {
		writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	setUnsupportedParameters(c, unsupported)
	prompt, files, err := buildGeminiPrompt(c, countClient(pool, model), request, false, bridge)
	if err != nil {
		status := http.StatusBadRequest
		var protocolErr *gemini.ProtocolError
		if errors.As(err, &protocolErr) {
			status = protocolErr.HTTPStatus
		}
		writeGeminiError(c, status, geminiStatusName(status), err.Error())
		return
	}
	counts := mediaTokenCount(files)
	counts[modalityText] += tokencount.Content(prompt)
	total := 0
	details := make([]gin.H, 0, len(counts))
	for _, modality := range []mediaModality{modalityText, modalityImage, modalityAudio, modalityVideo, modalityDocument} {
		if tokens, ok := counts[modality]; ok {
			total += tokens
			details = append(details, gin.H{"modality": string(modality), "tokenCount": tokens})
		}
	}
	c.JSON(http.StatusOK, gin.H{"totalTokens": total, "promptTokensDetails": details})
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
	transcript, err := renderTranscript(records)
	if err != nil {
		return "", nil, err
	}
	suffix, err := bridge.PromptSuffix()
	if err != nil {
		return "", nil, err
	}
	return transcript + suffix, files, nil
}

func decodeGeminiParts(c *gin.Context, client *gemini.Client, parts []GeminiPart) (string, []gemini.FileData, error) {
	var builder strings.Builder
	var files []gemini.FileData
	for _, part := range parts {
		if part.Thought {
			appendTranscriptObject(&builder, map[string]interface{}{"type": "thinking", "thinking": part.Text, "signature": part.ThoughtSignature})
		} else {
			builder.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			arguments, _ := json.Marshal(part.FunctionCall.Args)
			appendTranscriptObject(&builder, map[string]interface{}{"type": "tool_use", "id": part.FunctionCall.ID, "name": part.FunctionCall.Name, "signature": part.ThoughtSignature, "arguments": json.RawMessage(arguments)})
		}
		if part.FunctionResponse != nil {
			response, _ := json.Marshal(part.FunctionResponse.Response)
			appendTranscriptObject(&builder, map[string]interface{}{"type": "tool_result", "id": part.FunctionResponse.ID, "name": part.FunctionResponse.Name, "response": json.RawMessage(response)})
		}
		if part.ExecutableCode != nil {
			appendTranscriptObject(&builder, map[string]interface{}{
				"type": "code_execution", "id": part.ExecutableCode.ID, "language": part.ExecutableCode.Language, "code": part.ExecutableCode.Code,
			})
		}
		if part.CodeResult != nil {
			appendTranscriptObject(&builder, map[string]interface{}{
				"type": "code_execution_result", "id": part.CodeResult.ID, "outcome": part.CodeResult.Outcome, "output": part.CodeResult.Output,
			})
		}
		if part.InlineData != nil {
			partType := "document"
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part.InlineData.MimeType)), "image/") {
				partType = "image"
			}
			raw, _ := json.Marshal([]gin.H{{"type": partType, "source": gin.H{"type": "base64", "media_type": part.InlineData.MimeType, "data": part.InlineData.Data}}})
			label, attachments, err := decodeMessageContent(c.Request.Context(), raw, client)
			if err != nil {
				return "", nil, err
			}
			builder.WriteString(label)
			files = append(files, attachments...)
		}
		if part.FileData != nil && part.FileData.FileURI != "" {
			uri := part.FileData.FileURI
			if isYouTubeURL(uri) {
				builder.WriteString("\n" + uri + "\n")
				continue
			}
			if id, ok := storedFileID(uri); ok && (!strings.Contains(uri, "://") || strings.Contains(uri, "/v1beta/files/")) {
				file, err := storedFileData(c.Request.Context(), id)
				if errors.As(err, new(missingFileError)) {
					return "", nil, geminiFileNotFound(id)
				}
				if err != nil {
					return "", nil, err
				}
				if part.FileData.MimeType != "" {
					file.MimeType = part.FileData.MimeType
				}
				files = append(files, *file)
				builder.WriteString("[File]")
				continue
			}
			data, err := client.FetchMedia(c.Request.Context(), uri)
			if err != nil {
				return "", nil, err
			}
			filename := fmt.Sprintf("file_%d%s", time.Now().UnixNano(), mimeTypeToExt(part.FileData.MimeType))
			files = append(files, gemini.FileData{Data: data, FileName: filename, MimeType: part.FileData.MimeType})
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
			if field == "responseMimeType" || field == "responseJsonSchema" || field == "responseSchema" || field == "thinkingConfig" || field == "imageConfig" {
				continue
			}
			unsupported = append(unsupported, "generationConfig."+field)
		}
		var imageConfig map[string]json.RawMessage
		_ = json.Unmarshal(config["imageConfig"], &imageConfig)
		for field := range imageConfig {
			if field != "aspectRatio" {
				unsupported = append(unsupported, "generationConfig.imageConfig."+field)
			}
		}
		_ = json.Unmarshal(imageConfig["aspectRatio"], &bridge.AspectRatio)
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
				case "functionDeclarations", "googleSearch", "googleSearchRetrieval", "codeExecution":
				default:
					return bridge, nil, fmt.Errorf("不支持的 Gemini 工具组 %q", field)
				}
			}
			if len(group["googleSearch"]) > 0 || len(group["googleSearchRetrieval"]) > 0 {
				bridge.WebSearch = true
			}
			if len(group["codeExecution"]) > 0 {
				bridge.CodeExecution = true
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

func buildGeminiResponse(result generationResult, bridge ToolBridge, includeThoughts bool) (gin.H, error) {
	primary := result.Accumulator.Primary()
	if err := validateHostedOutput(primary, bridge); err != nil {
		return nil, err
	}
	content, calls, err := bridge.Parse(primary.Text)
	if err != nil {
		return nil, err
	}
	calls = assignToolCallIDs(calls, result.ResponseID)
	parts := make([]gin.H, 0)
	if includeThoughts && primary.Thought != "" {
		parts = append(parts, gin.H{"text": primary.Thought, "thought": true})
	}
	parts = append(parts, geminiStructuredParts(content, primary)...)
	for index, call := range calls {
		var arguments map[string]interface{}
		_ = json.Unmarshal(call.Arguments, &arguments)
		part := gin.H{"functionCall": gin.H{"id": call.ID, "name": call.Name, "args": arguments}}
		if index == 0 {
			part["thoughtSignature"] = thinkingSignature(result.ResponseID, 0)
		}
		parts = append(parts, part)
	}
	if len(calls) == 0 && len(parts) > 0 {
		parts[len(parts)-1]["thoughtSignature"] = thinkingSignature(result.ResponseID, 0)
	}
	finish := "STOP"
	candidate := gin.H{"content": gin.H{"role": "model", "parts": parts}, "finishReason": finish, "index": 0}
	if len(primary.Citations) > 0 {
		candidate["groundingMetadata"] = geminiGroundingMetadata(content, primary.Citations)
	}
	response := gin.H{
		"candidates":   []gin.H{candidate},
		"modelVersion": result.Model, "responseId": result.ResponseID, "conversationId": result.ConversationID,
	}
	if result.Accumulator.Usage != nil {
		response["usageMetadata"] = geminiUsage(result.Accumulator.Usage)
	}
	return response, nil
}

func geminiStructuredParts(text string, output CandidateOutput) []gin.H {
	if hasGeneratedImage(output) && strings.TrimSpace(text) == "" {
		text = ""
	}
	runes := []rune(text)
	parts := make([]gin.H, 0, len(output.Codes)+len(output.Media)+1)
	cursor := 0
	emittedResults := make(map[int]struct{})
	for _, insert := range candidateInserts(output) {
		offset := insert.offset
		if offset < 0 || offset > len(runes) {
			offset = len(runes)
		}
		if offset > cursor {
			parts = append(parts, gin.H{"text": string(runes[cursor:offset])})
			cursor = offset
		}
		if insert.code != nil {
			if insert.code.Type == gemini.CodeReference {
				parts = append(parts, geminiCodePart(*insert.code))
			} else if _, ok := emittedResults[insert.code.Index]; !ok {
				emittedResults[insert.code.Index] = struct{}{}
				parts = append(parts, geminiCodeResultPart(output.Codes, insert.code.Index))
			}
		} else if insert.media != nil {
			mimeType, data, ok := splitDataURL(insert.media.URL)
			if ok {
				parts = append(parts, gin.H{"inlineData": gin.H{"mimeType": mimeType, "data": data}})
			}
		}
	}
	if cursor < len(runes) {
		parts = append(parts, gin.H{"text": string(runes[cursor:])})
	}
	return parts
}

func geminiCodeResultPart(codes []CodeOutput, index int) gin.H {
	outcome := "OUTCOME_OK"
	var output strings.Builder
	for _, code := range codes {
		if code.Index != index || code.Type == gemini.CodeReference || code.Content == "" {
			continue
		}
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		if code.Type == gemini.CodeStderr {
			outcome = "OUTCOME_FAILED"
			output.WriteString("stderr:\n")
		}
		output.WriteString(code.Content)
	}
	return gin.H{"codeExecutionResult": gin.H{"outcome": outcome, "output": output.String()}}
}

func geminiCodePart(code CodeOutput) gin.H {
	language := strings.ToUpper(strings.TrimSpace(code.Language))
	if language != "PYTHON" {
		language = "LANGUAGE_UNSPECIFIED"
	}
	return gin.H{"executableCode": gin.H{"language": language, "code": code.Content}}
}

// geminiStreamWriter 按 alt=sse 输出 SSE 事件，未指定时与官方一样输出逐个对象的 JSON 数组
type geminiStreamWriter struct {
	w       http.ResponseWriter
	sse     bool
	written bool
	closed  bool
}

// newGeminiStreamWriter 写入流式响应头
func newGeminiStreamWriter(c *gin.Context, sse bool) *geminiStreamWriter {
	if sse {
		setSSEHeaders(c)
	} else {
		c.Header("Content-Type", "application/json; charset=UTF-8")
		c.Header("Cache-Control", "no-cache")
	}
	return &geminiStreamWriter{w: c.Writer, sse: sse}
}

// write 输出一个 GenerateContentResponse
func (w *geminiStreamWriter) write(payload interface{}) error {
	if w.sse {
		return writeSSEJSON(w.w, payload)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	prefix := "["
	if w.written {
		prefix = "\r\n,\r\n"
	}
	w.written = true
	if _, err := io.WriteString(w.w, prefix+string(data)); err != nil {
		return err
	}
	return streamio.Flush(w.w)
}

// close 结束 JSON 数组
func (w *geminiStreamWriter) close() {
	if w.sse || w.closed {
		return
	}
	w.closed = true
	tail := "\r\n]"
	if !w.written {
		tail = "[]"
	}
	if _, err := io.WriteString(w.w, tail); err == nil {
		_ = streamio.Flush(w.w)
	}
}

// delta 输出一段正文或思考增量
func (w *geminiStreamWriter) delta(responseID string, model string, event gemini.Event) error {
	part := gin.H{"text": event.Delta}
	if event.Kind == gemini.EventThought {
		part["thought"] = true
	}
	return w.write(gin.H{
		"candidates": []gin.H{{"content": gin.H{"role": "model", "parts": []gin.H{part}}, "index": 0}},
		"responseId": responseID, "modelVersion": model,
	})
}

// fail 输出 Google 错误对象
func (w *geminiStreamWriter) fail(status int, statusName string, err error) error {
	return w.write(gin.H{"error": gin.H{"code": status, "message": err.Error(), "status": statusName}})
}

func projectGeminiStreamTail(response gin.H, projection *streamProjection) gin.H {
	candidates, _ := response["candidates"].([]gin.H)
	for _, candidate := range candidates {
		content, _ := candidate["content"].(gin.H)
		parts, _ := content["parts"].([]gin.H)
		filtered := make([]gin.H, 0, len(parts))
		remainingTextRunes := projection.textRunes
		for _, part := range parts {
			_, hasText := part["text"]
			thought, _ := part["thought"].(bool)
			if hasText && thought && !projection.bufferThought {
				if _, signed := part["thoughtSignature"]; !signed {
					continue
				}
				part["text"] = ""
			}
			if hasText && !thought {
				if !projection.bufferText {
					if _, signed := part["thoughtSignature"]; !signed {
						continue
					}
					part["text"] = ""
					filtered = append(filtered, part)
					continue
				}
				text, _ := part["text"].(string)
				runes := []rune(text)
				if remainingTextRunes >= len(runes) {
					remainingTextRunes -= len(runes)
					continue
				}
				text = string(runes[remainingTextRunes:])
				remainingTextRunes = 0
				if text == "" {
					continue
				}
				part["text"] = text
			}
			filtered = append(filtered, part)
		}
		content["parts"] = filtered
	}
	return response
}

func geminiUsage(usage *gemini.Usage) gin.H {
	return gin.H{
		"promptTokenCount": usage.PromptTokens, "candidatesTokenCount": usage.CompletionTokens,
		"thoughtsTokenCount": usage.ThoughtTokens,
		"totalTokenCount":    usage.PromptTokens + usage.CompletionTokens + usage.ThoughtTokens,
	}
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
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	default:
		return ".bin"
	}
}

func splitDataURL(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	metadata, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return "", "", false
	}
	mimeType := strings.TrimSuffix(metadata, ";base64")
	if mimeType == metadata {
		return "", "", false
	}
	return mimeType, data, true
}
