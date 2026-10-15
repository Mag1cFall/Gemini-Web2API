package adapter

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

// CatalogModel 是公开目录中的一个模型及其在账号间的上下文窗口范围
type CatalogModel struct {
	gemini.Model
	MinInputTokenLimit int
	MaxInputTokenLimit int
	AccountCount       int
	// Accounts 为提供该模型的已就绪账号
	Accounts []string
	// AliasOf 为 MODEL_MAPPING 别名指向的模型 ID
	AliasOf string
}

// Catalog 返回公开模型目录
func Catalog(pool *balancer.AccountPool) []CatalogModel {
	return availableModels(pool)
}

// availableModels 汇总已就绪账号的模型目录，并追加 MODEL_MAPPING 中指向已有模型的别名
func availableModels(pool *balancer.AccountPool) []CatalogModel {
	byID := make(map[string]CatalogModel)
	defaultVotes := make(map[string]int)
	for _, account := range pool.ReadyAccounts() {
		client := account.Client
		for _, model := range client.Models() {
			if model.Default {
				defaultVotes[model.ID]++
			}
			availability := byID[model.ID]
			availability.Model = model
			availability.AccountCount++
			availability.Accounts = append(availability.Accounts, account.ID)
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
			defaultVotes[id] == defaultVotes[globalDefault] && byID[id].AccountCount > byID[globalDefault].AccountCount ||
			defaultVotes[id] == defaultVotes[globalDefault] && byID[id].AccountCount == byID[globalDefault].AccountCount && id < globalDefault {
			globalDefault = id
		}
	}
	models := make([]CatalogModel, 0, len(byID))
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
	aliases := config.ModelAliases()
	names := make([]string, 0, len(aliases))
	for alias := range aliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	for _, alias := range names {
		target, ok := byID[aliases[alias]]
		if _, exists := byID[alias]; !ok || exists {
			continue
		}
		target.ID, target.AliasOf, target.Default = alias, aliases[alias], false
		models = append(models, target)
	}
	return models
}

// findModel 在公开目录中查找模型 ID 或别名
func findModel(pool *balancer.AccountPool, id string) (CatalogModel, bool) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
	for _, model := range availableModels(pool) {
		if strings.EqualFold(model.ID, id) {
			return model, true
		}
	}
	return CatalogModel{}, false
}

// modelNotFoundMessage 返回模型不存在的说明并列出可用模型
func modelNotFoundMessage(pool *balancer.AccountPool, id string) string {
	models := availableModels(pool)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	if len(ids) == 0 {
		return fmt.Sprintf("model %q is unavailable: no account is ready", id)
	}
	return fmt.Sprintf("model %q is unavailable; available models: %s", id, strings.Join(ids, ", "))
}

func openAIModelObject(model CatalogModel) gin.H {
	owner := "google"
	if model.Feature != 0 || model.AliasOf != "" {
		owner = "gemini-web2api"
	}
	object := gin.H{
		"id": model.ID, "object": "model", "created": 0, "owned_by": owner, "default": model.Default,
		"display_name": model.DisplayName, "description": model.Description,
		"context_window": model.MaxInputTokenLimit, "min_context_window": model.MinInputTokenLimit,
		"max_context_window": model.MaxInputTokenLimit, "available_account_count": model.AccountCount,
		"thinking_levels": thinkingLevelNames(model.ThinkingLevels),
	}
	if model.AliasOf != "" {
		object["alias_of"] = model.AliasOf
	}
	return object
}

func claudeModelObject(model CatalogModel) gin.H {
	return gin.H{
		"id": model.ID, "type": "model", "display_name": model.DisplayName,
		"created_at": "1970-01-01T00:00:00Z", "default": model.Default,
		"context_window": model.MaxInputTokenLimit, "min_context_window": model.MinInputTokenLimit,
		"available_account_count": model.AccountCount, "thinking_levels": thinkingLevelNames(model.ThinkingLevels),
	}
}

func geminiModelObject(model CatalogModel) gin.H {
	return gin.H{
		"name": "models/" + model.ID, "displayName": model.DisplayName, "description": model.Description,
		"supportedGenerationMethods": model.Capabilities, "default": model.Default,
		"inputTokenLimit": model.MaxInputTokenLimit, "minInputTokenLimit": model.MinInputTokenLimit,
		"availableAccountCount": model.AccountCount, "thinking": len(model.ThinkingLevels) > 0,
		"thinkingLevels": thinkingLevelNames(model.ThinkingLevels),
	}
}

// ListModelsHandler 返回 OpenAI 模型目录，带 anthropic-version 头时返回 Anthropic 格式
func ListModelsHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		models := availableModels(pool)
		if c.GetHeader("anthropic-version") != "" {
			data := make([]gin.H, 0, len(models))
			for _, model := range models {
				data = append(data, claudeModelObject(model))
			}
			firstID, lastID := "", ""
			if len(models) > 0 {
				firstID, lastID = models[0].ID, models[len(models)-1].ID
			}
			c.JSON(http.StatusOK, gin.H{"data": data, "has_more": false, "first_id": firstID, "last_id": lastID})
			return
		}
		data := make([]gin.H, 0, len(models))
		for _, model := range models {
			data = append(data, openAIModelObject(model))
		}
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
	}
}

// RetrieveModelHandler 返回单个模型，带 anthropic-version 头时返回 Anthropic 格式
func RetrieveModelHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("model")
		model, ok := findModel(pool, id)
		anthropic := c.GetHeader("anthropic-version") != ""
		switch {
		case !ok && anthropic:
			writeClaudeError(c, http.StatusNotFound, "not_found_error", modelNotFoundMessage(pool, id))
		case !ok:
			writeOpenAIError(c, http.StatusNotFound, "model_not_found", modelNotFoundMessage(pool, id))
		case anthropic:
			c.JSON(http.StatusOK, claudeModelObject(model))
		default:
			c.JSON(http.StatusOK, openAIModelObject(model))
		}
	}
}

// GeminiListModelsHandler 返回 Gemini 模型目录
func GeminiListModelsHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		models := availableModels(pool)
		data := make([]gin.H, 0, len(models))
		for _, model := range models {
			data = append(data, geminiModelObject(model))
		}
		c.JSON(http.StatusOK, gin.H{"models": data})
	}
}

// GeminiGetModelHandler 返回单个 Gemini 模型
func GeminiGetModelHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("model")
		model, ok := findModel(pool, id)
		if !ok {
			writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", modelNotFoundMessage(pool, id))
			return
		}
		c.JSON(http.StatusOK, geminiModelObject(model))
	}
}

// WriteOpenAINotFound 以 OpenAI 错误体返回未知路由
func WriteOpenAINotFound(c *gin.Context) {
	writeOpenAIError(c, http.StatusNotFound, "unknown_url", fmt.Sprintf("Unknown request URL: %s %s", c.Request.Method, c.Request.URL.Path))
}

// WriteGeminiNotFound 以 Google 错误体返回未知路由
func WriteGeminiNotFound(c *gin.Context) {
	writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("Unknown request URL: %s %s", c.Request.Method, c.Request.URL.Path))
}
