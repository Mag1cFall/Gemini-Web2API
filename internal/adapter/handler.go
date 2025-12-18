package adapter

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

var (
	thinkingBlockRegex   = regexp.MustCompile(`(?s)<details>\s*<summary>\s*Thinking Process\s*</summary>\s*(.*?)\s*</details>`)
	chineseThinkingRegex = regexp.MustCompile(`(?s)<details>\s*<summary>\s*思考过程\s*</summary>\s*(.*?)\s*</details>`)
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
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   models,
	})
}

func ChatCompletionHandler(client *gemini.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, ")]}'") {
			line = line[4:]
		}
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
					text := candidate.Get("1.0").String()
					thoughts := candidate.Get("37.0.0").String()

					// Fix: Gemini Web often escapes special characters in code blocks or XML tags
					// e.g. \<read\_file\> instead of <read_file>
					// This reverses the Markdown escaping to restore raw tool calls/code.
					text = strings.ReplaceAll(text, `\<`, `<`)
					text = strings.ReplaceAll(text, `\>`, `>`)
					text = strings.ReplaceAll(text, `\_`, `_`)
					// Also fix brackets which might break JSON or array definitions in text
					text = strings.ReplaceAll(text, `\[`, `[`)
					text = strings.ReplaceAll(text, `\]`, `]`)

					if thoughts == "" && text != "" {
						matches := thinkingBlockRegex.FindStringSubmatch(text)
						if len(matches) < 2 {
							matches = chineseThinkingRegex.FindStringSubmatch(text)
						}

						if len(matches) >= 2 {
							thoughts = strings.TrimSpace(matches[1])
							text = strings.Replace(text, matches[0], "", 1)
							text = strings.TrimSpace(text)
						}
					}

					onChunk(text, thoughts)
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
