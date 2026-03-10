package adapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"gemini-web2api/internal/balancer"
	"gemini-web2api/internal/config"
	"gemini-web2api/internal/gemini"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type GeminiInlineData struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

type GeminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *GeminiInlineData `json:"inlineData,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts,omitempty"`
}

type GeminiSystemInstruction struct {
	Parts []GeminiPart `json:"parts,omitempty"`
}

type GeminiGenerationConfig struct {
	Temperature        *float64        `json:"temperature,omitempty"`
	MaxOutputTokens    *int            `json:"maxOutputTokens,omitempty"`
	TopP               *float64        `json:"topP,omitempty"`
	TopK               *int            `json:"topK,omitempty"`
	StopSequences      []string        `json:"stopSequences,omitempty"`
	ResponseMimeType   string          `json:"responseMimeType,omitempty"`
	ResponseJsonSchema json.RawMessage `json:"responseJsonSchema,omitempty"`
}

type GeminiGenerateContentRequest struct {
	Contents          []GeminiContent          `json:"contents"`
	SystemInstruction *GeminiSystemInstruction `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type GeminiGenerateContentResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates,omitempty"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

func GeminiRouterHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		action := strings.TrimPrefix(c.Param("action"), "/")

		colonIdx := strings.LastIndex(action, ":")
		if colonIdx < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid path, expected format: models/{model}:{method}"})
			return
		}

		model := action[:colonIdx]
		method := action[colonIdx+1:]

		c.Set("gemini_model", model)

		switch method {
		case "generateContent":
			geminiGenerateContent(c, pool, model)
		case "streamGenerateContent":
			geminiStreamGenerateContent(c, pool, model)
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Unknown method: %s", method)})
		}
	}
}

func geminiGenerateContent(c *gin.Context, pool *balancer.AccountPool, model string) {
	client, accountID := pool.Next()
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available accounts"})
		return
	}

	c.Set("account_id", accountID)

	var req GeminiGenerateContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if len(req.Contents) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contents is required and cannot be empty"})
		return
	}

	mappedModel := config.MapModel(model)
	if mappedModel != model {
		log.Printf("[Gemini] 模型映射: %s -> %s", model, mappedModel)
	}

	prompt, files := buildGeminiPrompt(&req, client)
	if strings.TrimSpace(prompt) == "" {
		prompt = "Hello"
	}

	log.Printf("[Gemini] 请求 | 模型: %s | 流式: false | 内容段: %d | 文件: %d", model, len(req.Contents), len(files))

	gemini.RandomDelay()
	respBody, err := client.StreamGenerateContent(prompt, mappedModel, files, nil)
	if err != nil {
		log.Printf("[Gemini] 请求失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer respBody.Close()

	var fullText strings.Builder
	parseGeminiResponse(respBody, func(text, thought string) {
		if text != "" {
			fullText.WriteString(text)
		}
	})

	resp := GeminiGenerateContentResponse{
		Candidates: []GeminiCandidate{
			{
				Content: GeminiContent{
					Role:  "model",
					Parts: []GeminiPart{{Text: fullText.String()}},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: &GeminiUsageMetadata{
			PromptTokenCount:     0,
			CandidatesTokenCount: 0,
			TotalTokenCount:      0,
		},
		ModelVersion: mappedModel,
	}

	c.JSON(http.StatusOK, resp)
}

func geminiStreamGenerateContent(c *gin.Context, pool *balancer.AccountPool, model string) {
	client, accountID := pool.Next()
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available accounts"})
		return
	}

	c.Set("account_id", accountID)

	var req GeminiGenerateContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if len(req.Contents) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contents is required and cannot be empty"})
		return
	}

	mappedModel := config.MapModel(model)
	if mappedModel != model {
		log.Printf("[Gemini] 模型映射: %s -> %s", model, mappedModel)
	}

	prompt, files := buildGeminiPrompt(&req, client)
	if strings.TrimSpace(prompt) == "" {
		prompt = "Hello"
	}

	log.Printf("[Gemini] 请求 | 模型: %s | 流式: true | 内容段: %d | 文件: %d", model, len(req.Contents), len(files))

	gemini.RandomDelay()
	respBody, err := client.StreamGenerateContent(prompt, mappedModel, files, nil)
	if err != nil {
		log.Printf("[Gemini] 请求失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer respBody.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	c.Stream(func(w io.Writer) bool {
		parseGeminiResponse(respBody, func(text, thought string) {
			if text == "" {
				return
			}

			chunk := GeminiGenerateContentResponse{
				Candidates: []GeminiCandidate{
					{
						Content: GeminiContent{
							Role:  "model",
							Parts: []GeminiPart{{Text: text}},
						},
					},
				},
				ModelVersion: mappedModel,
			}

			bytes, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", bytes)
			w.(http.Flusher).Flush()
		})
		return false
	})

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	c.Writer.(http.Flusher).Flush()
}

func GeminiListModelsHandler(c *gin.Context) {
	models := []gin.H{
		{
			"name":                       "models/gemini-2.5-flash",
			"displayName":                "Gemini 2.5 Flash",
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		},
		{
			"name":                       "models/gemini-3.1-pro-preview",
			"displayName":                "Gemini 3.1 Pro Preview",
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		},
		{
			"name":                       "models/gemini-3-flash-preview",
			"displayName":                "Gemini 3 Flash Preview",
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		},
		{
			"name":                       "models/gemini-3-flash-preview-no-thinking",
			"displayName":                "Gemini 3 Flash Preview (No Thinking)",
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		},
		{
			"name":                       "models/gemini-2.5-flash-image",
			"displayName":                "Gemini 2.5 Flash Image",
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		},
		{
			"name":                       "models/gemini-3-pro-image-preview",
			"displayName":                "Gemini 3 Pro Image Preview",
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		},
	}

	c.JSON(http.StatusOK, gin.H{"models": models})
}

func buildGeminiPrompt(req *GeminiGenerateContentRequest, client *gemini.Client) (string, []gemini.FileData) {
	var builder strings.Builder
	var files []gemini.FileData

	if req.SystemInstruction != nil {
		builder.WriteString("**System**: ")
		appendGeminiParts(&builder, client, &files, req.SystemInstruction.Parts)
		builder.WriteString("\n\n")
	}

	for _, content := range req.Contents {
		roleLabel := roleToPromptLabel(content.Role)
		builder.WriteString(fmt.Sprintf("**%s**: ", roleLabel))
		appendGeminiParts(&builder, client, &files, content.Parts)
		builder.WriteString("\n\n")
	}

	return builder.String(), files
}

func roleToPromptLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "model", "assistant":
		return "Model"
	case "system":
		return "System"
	default:
		return "User"
	}
}

func appendGeminiParts(builder *strings.Builder, client *gemini.Client, files *[]gemini.FileData, parts []GeminiPart) {
	for _, part := range parts {
		if part.Text != "" {
			builder.WriteString(part.Text)
			continue
		}

		if part.InlineData == nil {
			continue
		}

		dataStr := strings.TrimSpace(part.InlineData.Data)
		if dataStr == "" {
			continue
		}
		if strings.HasPrefix(dataStr, "data:") {
			if idx := strings.Index(dataStr, ","); idx >= 0 {
				dataStr = strings.TrimSpace(dataStr[idx+1:])
			}
		}

		data, err := decodeBase64Flex(dataStr)
		if err != nil {
			log.Printf("[Gemini] 解析 inlineData 失败: %v", err)
			continue
		}

		mimeType := strings.TrimSpace(part.InlineData.MimeType)
		ext := mimeTypeToExt(mimeType)
		filename := fmt.Sprintf("inline_%d%s", time.Now().UnixNano(), ext)

		fid, err := client.UploadFile(data, filename)
		if err != nil {
			log.Printf("[Gemini] 上传图片失败: %v", err)
			continue
		}

		*files = append(*files, gemini.FileData{URL: fid, FileName: filename})
		builder.WriteString("[Image]")
	}
}

func mimeTypeToExt(mimeType string) string {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if idx := strings.Index(mt, ";"); idx >= 0 {
		mt = strings.TrimSpace(mt[:idx])
	}
	switch mt {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	if strings.HasPrefix(mt, "image/") {
		sub := strings.TrimPrefix(mt, "image/")
		if idx := strings.Index(sub, "+"); idx >= 0 {
			sub = sub[:idx]
		}
		sub = strings.TrimSpace(sub)
		if sub != "" {
			return "." + sub
		}
	}
	return ".bin"
}

func decodeBase64Flex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("空的 base64 数据")
	}

	if data, err := base64.StdEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	if data, err := base64.URLEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	if data, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return data, nil
	}

	return nil, fmt.Errorf("base64 解码失败")
}
