package adapter

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"gemini-web2api/internal/balancer"
	"gemini-web2api/internal/gemini"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ChatRequest struct {
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Model    string        `json:"model"`
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requiredKey := os.Getenv("PROXY_API_KEY")

		if requiredKey == "" {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is missing"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header format"})
			return
		}

		if parts[1] != requiredKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key"})
			return
		}

		c.Next()
	}
}

func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		accountID, exists := c.Get("account_id")
		if exists {
			displayID, ok := accountID.(string)
			if !ok || displayID == "" {
				displayID = "default"
			}
			log.Printf("[Account '%s'] %s %s - %d - %v",
				displayID,
				c.Request.Method,
				c.Request.URL.Path,
				c.Writer.Status(),
				time.Since(start),
			)
		}
	}
}

func ListModelsHandler(c *gin.Context) {
	type ModelCard struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	models := []ModelCard{
		{ID: "gemini-2.5-flash", Object: "model", Created: time.Now().Unix(), OwnedBy: "Google"},
		{ID: "gemini-3-pro-preview", Object: "model", Created: time.Now().Unix(), OwnedBy: "Google"},
		{ID: "gemini-3-flash-preview", Object: "model", Created: time.Now().Unix(), OwnedBy: "Google"},
		{ID: "gemini-3-flash-preview-no-thinking", Object: "model", Created: time.Now().Unix(), OwnedBy: "Google"},
		{ID: "gemini-2.5-flash-image", Object: "model", Created: time.Now().Unix(), OwnedBy: "Google"},
		{ID: "gemini-3-pro-image-preview", Object: "model", Created: time.Now().Unix(), OwnedBy: "Google"},
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   models,
	})
}

func isImageModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "image")
}

func ChatCompletionHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		client, accountID := pool.Next()
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available accounts"})
			return
		}

		c.Set("account_id", accountID)

		var req ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Check if this is an image model request
		if isImageModel(req.Model) {
			handleImageChatRequest(c, client, req)
			return
		}

		var promptBuilder strings.Builder
		var files []gemini.FileData

		for _, msg := range req.Messages {
			role := "User"
			if strings.EqualFold(msg.Role, "model") || strings.EqualFold(msg.Role, "assistant") {
				role = "Model"
			} else if strings.EqualFold(msg.Role, "system") {
				role = "System"
			}

			promptBuilder.WriteString(fmt.Sprintf("**%s**: ", role))

			switch v := msg.Content.(type) {
			case string:
				promptBuilder.WriteString(v)
			case []interface{}:
				for _, part := range v {
					p, ok := part.(map[string]interface{})
					if !ok {
						continue
					}

					typeStr, _ := p["type"].(string)

					if typeStr == "text" {
						if text, ok := p["text"].(string); ok {
							promptBuilder.WriteString(text)
						}
					} else if typeStr == "image_url" {
						if imgMap, ok := p["image_url"].(map[string]interface{}); ok {
							if urlStr, ok := imgMap["url"].(string); ok {
								if strings.HasPrefix(urlStr, "data:") {
									parts := strings.Split(urlStr, ",")
									if len(parts) == 2 {
										data, err := base64.StdEncoding.DecodeString(parts[1])
										if err == nil {
											fname := fmt.Sprintf("image_%d.png", time.Now().UnixNano())
											fid, err := client.UploadFile(data, fname)
											if err == nil {
												files = append(files, gemini.FileData{
													URL:      fid,
													FileName: fname,
												})
												promptBuilder.WriteString("[Image]")
											} else {
												log.Printf("Failed to upload image: %v", err)
											}
										}
									}
								} else {
									promptBuilder.WriteString(fmt.Sprintf("[Image URL: %s]", urlStr))
								}
							}
						}
					}
				}
			}
			promptBuilder.WriteString("\n\n")
		}

		finalPrompt := promptBuilder.String()
		if finalPrompt == "" {
			finalPrompt = "Hello"
		}

		gemini.RandomDelay()

		respBody, err := client.StreamGenerateContent(finalPrompt, req.Model, files, nil)
		if err != nil {
			log.Printf("Gemini request failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to communicate with Gemini: " + err.Error()})
			return
		}
		defer respBody.Close()

		id := fmt.Sprintf("chatcmpl-%d", time.Now().Unix())
		created := time.Now().Unix()

		// Handle non-streaming request (stream: false)
		if !req.Stream {
			var fullText strings.Builder
			var fullThinking strings.Builder

			parseGeminiResponse(respBody, func(text, thought string) {
				fullText.WriteString(text)
				fullThinking.WriteString(thought)
			})

			resp := map[string]interface{}{
				"id":      id,
				"object":  "chat.completion",
				"created": created,
				"model":   req.Model,
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]interface{}{
							"role":              "assistant",
							"content":           fullText.String(),
							"reasoning_content": fullThinking.String(),
						},
						"finish_reason": "stop",
					},
				},
			}
			c.JSON(http.StatusOK, resp)
			return
		}

		// Handle streaming request (stream: true)
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Transfer-Encoding", "chunked")

		// Send initial Role packet (Required by Cline and others)
		sendSSERole(c.Writer, id, created, req.Model)

		c.Stream(func(w io.Writer) bool {
			parseGeminiResponse(respBody, func(text, thought string) {
				if thought != "" {
					sendSSEThinking(w, id, created, req.Model, thought)
				}
				if text != "" {
					sendSSE(w, id, created, req.Model, text)
				}
			})
			return false
		})

		w := c.Writer
		fmt.Fprintf(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}
}

// Extract common parsing logic
func parseGeminiResponse(reader io.Reader, onChunk func(text, thought string)) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var lastText, lastThoughts string

	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), ")]}'")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		outer := gjson.Parse(line)
		if !outer.IsArray() {
			continue
		}

		outer.ForEach(func(key, value gjson.Result) bool {
			dataStr := value.Get("2").String()
			if dataStr == "" {
				return true
			}

			inner := gjson.Parse(dataStr)

			candidates := inner.Get("4")
			if candidates.IsArray() {
				candidates.ForEach(func(_, candidate gjson.Result) bool {
					rawText := candidate.Get("1.0").String()
					rawThoughts := candidate.Get("37.0.0").String()

					deltaText := ""
					deltaThoughts := ""

					rawRunes := []rune(rawText)
					lastRunes := []rune(lastText)
					if len(rawRunes) > len(lastRunes) {
						deltaText = string(rawRunes[len(lastRunes):])
						lastText = rawText
					} else if len(lastRunes) == 0 && len(rawRunes) > 0 {
						deltaText = rawText
						lastText = rawText
					}

					rawThoughtRunes := []rune(rawThoughts)
					lastThoughtRunes := []rune(lastThoughts)
					if len(rawThoughtRunes) > len(lastThoughtRunes) {
						deltaThoughts = string(rawThoughtRunes[len(lastThoughtRunes):])
						lastThoughts = rawThoughts
					} else if len(lastThoughtRunes) == 0 && len(rawThoughtRunes) > 0 {
						deltaThoughts = rawThoughts
						lastThoughts = rawThoughts
					}

					if deltaText == "" && deltaThoughts == "" {
						return true
					}

					deltaText = strings.ReplaceAll(deltaText, `\<`, `<`)
					deltaText = strings.ReplaceAll(deltaText, `\>`, `>`)
					deltaText = strings.ReplaceAll(deltaText, `\_`, `_`)
					deltaText = strings.ReplaceAll(deltaText, `\[`, `[`)
					deltaText = strings.ReplaceAll(deltaText, `\]`, `]`)
					deltaText = filterImagePlaceholders(deltaText)

					if deltaText != "" || deltaThoughts != "" {
						onChunk(deltaText, deltaThoughts)
					}
					return true
				})
			}
			return true
		})
	}
}

func sendSSERole(w io.Writer, id string, created int64, model string) {
	resp := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]string{
					"role": "assistant",
				},
				"finish_reason": nil,
			},
		},
	}
	bytes, _ := json.Marshal(resp)
	fmt.Fprintf(w, "data: %s\n\n", bytes)
	w.(http.Flusher).Flush()
}

func sendSSE(w io.Writer, id string, created int64, model, content string) {
	resp := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]string{
					"content": content,
				},
				"finish_reason": nil,
			},
		},
	}
	bytes, _ := json.Marshal(resp)
	fmt.Fprintf(w, "data: %s\n\n", bytes)
	w.(http.Flusher).Flush()
}

func sendSSEThinking(w io.Writer, id string, created int64, model, thinking string) {
	resp := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]string{
					"reasoning_content": thinking,
					"content":           "",
				},
				"finish_reason": nil,
			},
		},
	}
	bytes, _ := json.Marshal(resp)
	fmt.Fprintf(w, "data: %s\n\n", bytes)
	w.(http.Flusher).Flush()
}

var imagePlaceholderRegex = regexp.MustCompile(`\s*https?://googleusercontent\.com/image_generation_content/\d+\s*`)

func filterImagePlaceholders(text string) string {
	return imagePlaceholderRegex.ReplaceAllString(text, "")
}

func parseGeminiResponseFromBytes(content []byte, onChunk func(text, thought string, imgURL string)) {
	var allParts []gjson.Result

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimPrefix(line, ")]}'")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		outer := gjson.Parse(line)
		if !outer.IsArray() {
			continue
		}

		outer.ForEach(func(_, part gjson.Result) bool {
			allParts = append(allParts, part)
			return true
		})
	}

	if len(allParts) == 0 {
		return
	}

	bodyIndex := -1
	var body gjson.Result

	for i, part := range allParts {
		dataStr := part.Get("2").String()
		if dataStr == "" {
			continue
		}
		inner := gjson.Parse(dataStr)
		if inner.Get("4").Exists() {
			bodyIndex = i
			body = inner
			break
		}
	}

	if bodyIndex < 0 || !body.Exists() {
		return
	}

	candidateArr := body.Get("4").Array()
	for candIdx, candidate := range candidateArr {
		text := candidate.Get("1.0").String()
		thoughts := candidate.Get("37.0.0").String()

		text = strings.ReplaceAll(text, `\<`, `<`)
		text = strings.ReplaceAll(text, `\>`, `>`)
		text = strings.ReplaceAll(text, `\_`, `_`)
		text = strings.ReplaceAll(text, `\[`, `[`)
		text = strings.ReplaceAll(text, `\]`, `]`)
		text = filterImagePlaceholders(text)

		var imgURL string

		if candidate.Get("12.7.0").Exists() {
			for i := bodyIndex; i < len(allParts); i++ {
				imgDataStr := allParts[i].Get("2").String()
				if imgDataStr == "" {
					continue
				}
				imgInner := gjson.Parse(imgDataStr)
				imgCandidate := imgInner.Get(fmt.Sprintf("4.%d", candIdx))
				if !imgCandidate.Get("12.7.0").Exists() {
					continue
				}

				if finishedText := imgCandidate.Get("1.0").String(); finishedText != "" {
					text = filterImagePlaceholders(finishedText)
					text = strings.ReplaceAll(text, `\<`, `<`)
					text = strings.ReplaceAll(text, `\>`, `>`)
					text = strings.ReplaceAll(text, `\_`, `_`)
					text = strings.ReplaceAll(text, `\[`, `[`)
					text = strings.ReplaceAll(text, `\]`, `]`)
				}

				imgCandidate.Get("12.7.0").ForEach(func(_, genImg gjson.Result) bool {
					url := genImg.Get("0.3.3").String()
					if url != "" && !strings.HasPrefix(url, "http://googleusercontent.com/image_generation_content") {
						imgURL = url
					}
					return true
				})

				if imgURL != "" {
					break
				}
			}
		}

		onChunk(text, thoughts, imgURL)
	}
}

func downloadImageAsBase64(url string, cookies map[string]string) string {
	client := &http.Client{Timeout: 60 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("[Images] Failed to create request: %v", err)
		return ""
	}

	for k, v := range cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Images] Failed to download image: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("[Images] Image download returned status %d", resp.StatusCode)
		return ""
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		log.Printf("[Images] Failed to read image data: %v", err)
		return ""
	}

	log.Printf("[Images] Downloaded image: %d bytes", len(data))
	return base64.StdEncoding.EncodeToString(data)
}

func extractImageURLsFromResponse(reader io.Reader) []string {
	var urls []string
	var allParts []gjson.Result

	content, _ := io.ReadAll(reader)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimPrefix(line, ")]}'")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		outer := gjson.Parse(line)
		if !outer.IsArray() {
			continue
		}

		outer.ForEach(func(_, part gjson.Result) bool {
			allParts = append(allParts, part)
			return true
		})
	}

	if len(allParts) == 0 {
		return urls
	}

	bodyIndex := -1
	for i, part := range allParts {
		dataStr := part.Get("2").String()
		if dataStr == "" {
			continue
		}
		inner := gjson.Parse(dataStr)
		if inner.Get("4").Exists() {
			bodyIndex = i
			break
		}
	}

	if bodyIndex < 0 {
		return urls
	}

	for i := bodyIndex; i < len(allParts); i++ {
		imgDataStr := allParts[i].Get("2").String()
		if imgDataStr == "" {
			continue
		}
		imgInner := gjson.Parse(imgDataStr)
		imgCandidate := imgInner.Get("4.0")
		if !imgCandidate.Get("12.7.0").Exists() {
			continue
		}

		imgCandidate.Get("12.7.0").ForEach(func(_, genImg gjson.Result) bool {
			url := genImg.Get("0.3.3").String()
			if url != "" && !strings.HasPrefix(url, "http://googleusercontent.com/image_generation_content") {
				urls = append(urls, url)
			}
			return true
		})

		if len(urls) > 0 {
			break
		}
	}

	return urls
}

func handleImageChatRequest(c *gin.Context, client *gemini.Client, req ChatRequest) {
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	// Extract prompt from last user message
	var prompt string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if strings.EqualFold(req.Messages[i].Role, "user") {
			switch content := req.Messages[i].Content.(type) {
			case string:
				prompt = content
			case []interface{}:
				for _, part := range content {
					if m, ok := part.(map[string]interface{}); ok {
						if text, ok := m["text"].(string); ok {
							prompt = text
							break
						}
					}
				}
			}
			break
		}
	}

	if prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No prompt found in messages"})
		return
	}

	respBody, err := client.StreamGenerateContent(fmt.Sprintf("Generate an image of %s", prompt), req.Model, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer respBody.Close()

	imageURLs := extractImageURLsFromResponse(respBody)

	if len(imageURLs) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "No images generated",
			"type":    "server_error",
		}})
		return
	}

	// Download images using gemini client
	var content strings.Builder
	for i, imgURL := range imageURLs {
		fullURL := imgURL
		if !strings.Contains(fullURL, "=s") {
			fullURL = imgURL + "=s2048"
		}
		data, err := client.FetchImage(fullURL)
		if err != nil {
			log.Printf("[Images] Failed to fetch image: %v", err)
			continue
		}
		b64 := base64.StdEncoding.EncodeToString(data)
		content.WriteString(fmt.Sprintf("![Generated Image %d](data:image/png;base64,%s)\n\n", i+1, b64))
	}

	if content.Len() == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to download images",
			"type":    "server_error",
		}})
		return
	}

	if req.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")

		sendSSERole(c.Writer, id, created, req.Model)
		sendSSE(c.Writer, id, created, req.Model, content.String())
		fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
		c.Writer.(http.Flusher).Flush()
	} else {
		c.JSON(http.StatusOK, gin.H{
			"id":      id,
			"object":  "chat.completion",
			"created": created,
			"model":   req.Model,
			"choices": []gin.H{
				{
					"index": 0,
					"message": gin.H{
						"role":    "assistant",
						"content": content.String(),
					},
					"finish_reason": "stop",
				},
			},
		})
	}
}
