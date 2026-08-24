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
			owner := "google"
			if model.ID == flashImageModelID {
				owner = "gemini-web2api"
			}
			data = append(data, gin.H{
				"id": model.ID, "object": "model", "created": 0, "owned_by": owner, "default": model.Default,
				"display_name": model.DisplayName, "description": model.Description,
				"context_window": model.MaxInputTokenLimit, "min_context_window": model.MinInputTokenLimit,
				"max_context_window": model.MaxInputTokenLimit, "available_account_count": model.AccountCount,
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

type availableModel struct {
	gemini.Model
	MinInputTokenLimit int
	MaxInputTokenLimit int
	AccountCount       int
}

const flashImageModelID = "gemini-3.1-flash-image"

func availableModels(pool *balancer.AccountPool) []availableModel {
	models := discoveredModels(pool)
	if provider, ok := imageProviderModel(models, nil); ok {
		imageModel := provider
		imageModel.ID = flashImageModelID
		imageModel.DisplayName = "Nano Banana 2"
		imageModel.Description = "Gemini Web image generation and editing"
		imageModel.Default = false
		imageModel.Capabilities = []string{"generateContent", "streamGenerateContent"}
		models = append(models, imageModel)
	}
	sortAvailableModels(models)
	return models
}

func discoveredModels(pool *balancer.AccountPool) []availableModel {
	byID := make(map[string]availableModel)
	defaultVotes := make(map[string]int)
	accountCounts := make(map[string]int)
	for _, client := range pool.Clients() {
		for _, model := range client.Models() {
			accountCounts[model.ID]++
			if model.Default {
				defaultVotes[model.ID]++
			}
			availability := byID[model.ID]
			availability.Model = model
			availability.AccountCount++
			contextWindow := client.ContextWindow()
			if availability.MinInputTokenLimit == 0 || contextWindow < availability.MinInputTokenLimit {
				availability.MinInputTokenLimit = contextWindow
			}
			if contextWindow > availability.MaxInputTokenLimit {
				availability.MaxInputTokenLimit = contextWindow
			}
			byID[model.ID] = availability
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
	models := make([]availableModel, 0, len(byID))
	for id, model := range byID {
		model.Default = id == globalDefault
		models = append(models, model)
	}
	sortAvailableModels(models)
	return models
}

func sortAvailableModels(models []availableModel) {
	sort.Slice(models, func(left int, right int) bool {
		if models[left].Default != models[right].Default {
			return models[left].Default
		}
		return models[left].ID < models[right].ID
	})
}

func imageProviderModel(models []availableModel, ready func(string) bool) (availableModel, bool) {
	var selected availableModel
	selectedRank := 0
	found := false
	for _, model := range models {
		id := strings.ToLower(model.ID)
		if !strings.Contains(id, "flash") || ready != nil && !ready(model.ID) {
			continue
		}
		rank := 1
		if !strings.Contains(id, "flash-lite") {
			rank = 2
		}
		if model.Default && !strings.Contains(id, "flash-lite") {
			rank = 3
		}
		if !found || rank > selectedRank || rank == selectedRank && model.AccountCount > selected.AccountCount ||
			rank == selectedRank && model.AccountCount == selected.AccountCount && model.ID > selected.ID {
			selected = model
			selectedRank = rank
			found = true
		}
	}
	return selected, found
}

func resolveImageProvider(pool *balancer.AccountPool, modelID string) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(modelID), flashImageModelID) {
		return "", false
	}
	models := discoveredModels(pool)
	if _, ok := imageProviderModel(models, nil); !ok {
		return "", true
	}
	provider, ok := imageProviderModel(models, pool.HasReadyModel)
	if !ok {
		return "", true
	}
	return provider.ID, true
}
