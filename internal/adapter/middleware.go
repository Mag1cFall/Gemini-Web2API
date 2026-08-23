package adapter

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

// CORSMiddleware 设置公开 API 的跨域响应头
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, x-goog-api-key, X-Conversation-ID")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "X-Gemini-Web2API-Unsupported-Parameters, X-Conversation-ID, X-Response-ID")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// AuthMiddleware 校验服务公开 API key
func AuthMiddleware(requiredKey string) gin.HandlerFunc {
	requiredKey = strings.TrimSpace(requiredKey)
	return func(c *gin.Context) {
		if requiredKey == "" {
			c.Next()
			return
		}

		provided := strings.TrimSpace(c.Query("key"))
		if provided == "" {
			provided = strings.TrimSpace(c.GetHeader("x-goog-api-key"))
		}
		if provided == "" {
			provided = strings.TrimSpace(c.GetHeader("x-api-key"))
		}
		if provided == "" {
			authorization := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(authorization, "Bearer ") {
				provided = strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
			}
		}
		if provided != requiredKey {
			writeAuthError(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// LoggerMiddleware 记录请求耗时和脱敏账号标识
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		accountID, _ := c.Get("account_id")
		log.Printf("account=%v method=%s path=%s status=%d duration=%s", accountID, c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start))
	}
}

// ListModelsHandler 返回 OpenAI 模型目录
func ListModelsHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		models := availableModels(pool)
		if c.GetHeader("anthropic-version") != "" {
			writeClaudeModelList(c, models)
			return
		}
		data := make([]gin.H, 0, len(models))
		for _, model := range models {
			data = append(data, gin.H{
				"id": model.ID, "object": "model", "created": 0, "owned_by": "google", "default": model.Default,
			})
		}
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
	}
}

func writeAuthError(c *gin.Context) {
	switch {
	case strings.HasPrefix(c.Request.URL.Path, "/v1beta/"):
		writeGeminiError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Invalid API Key")
	case strings.HasPrefix(c.Request.URL.Path, "/v1/messages") || c.GetHeader("anthropic-version") != "":
		writeClaudeError(c, http.StatusUnauthorized, "authentication_error", "Invalid API Key")
	default:
		writeOpenAIError(c, http.StatusUnauthorized, "invalid_api_key", "Invalid API Key")
	}
}

func availableModels(pool *balancer.AccountPool) []gemini.Model {
	byID := make(map[string]gemini.Model)
	defaultVotes := make(map[string]int)
	accountCounts := make(map[string]int)
	for _, client := range pool.Clients() {
		for _, model := range client.Models() {
			accountCounts[model.ID]++
			if model.Default {
				defaultVotes[model.ID]++
			}
			if _, ok := byID[model.ID]; !ok {
				byID[model.ID] = model
			}
		}
	}
	globalDefault := ""
	for id := range byID {
		if globalDefault == "" || defaultVotes[id] > defaultVotes[globalDefault] ||
			defaultVotes[id] == defaultVotes[globalDefault] && accountCounts[id] > accountCounts[globalDefault] ||
			defaultVotes[id] == defaultVotes[globalDefault] && accountCounts[id] == accountCounts[globalDefault] && id < globalDefault {
			globalDefault = id
		}
	}
	models := make([]gemini.Model, 0, len(byID))
	for id, model := range byID {
		model.Default = id == globalDefault
		models = append(models, model)
	}
	sort.Slice(models, func(left int, right int) bool {
		if models[left].Default != models[right].Default {
			return models[left].Default
		}
		return models[left].ID < models[right].ID
	})
	return models
}
