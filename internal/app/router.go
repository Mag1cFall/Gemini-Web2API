package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/accounts"
	"github.com/Mag1cFall/Gemini-Web2API/internal/adapter"
	"github.com/Mag1cFall/Gemini-Web2API/internal/admin"
	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/Mag1cFall/Gemini-Web2API/internal/webui"

	"github.com/gin-gonic/gin"
)

// Deps 是路由使用的运行期组件
type Deps struct {
	Pool     *balancer.AccountPool
	Store    *requestdb.Store
	Accounts *accounts.Manager
	// APIKey 返回当前生效的公开 API key，多个 key 以逗号分隔
	APIKey func() string
	// Admin 为管理端组件，nil 时不注册 /api
	Admin *admin.Deps
}

// NewRouter 注册健康检查与全部兼容 API
func NewRouter(deps Deps) *gin.Engine {
	pool := deps.Pool
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), securityHeaders)

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		status := pool.Status()
		body := gin.H{"status": "ready", "accounts": status.Total, "available": status.Available, "version": Version()}
		if !status.Ready() {
			body["status"] = "unavailable"
			c.JSON(http.StatusServiceUnavailable, body)
			return
		}
		c.JSON(http.StatusOK, body)
	})

	api := router.Group("")
	api.Use(adapter.CORSMiddleware())
	api.Use(adapter.RequestMiddleware(deps.Store))
	api.Use(adapter.AuthMiddleware(deps.APIKey))
	api.GET("/v1/accounts/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, pool.Status())
	})
	api.GET("/v1/accounts/usage", func(c *gin.Context) {
		refresh, _ := strconv.ParseBool(c.Query("refresh"))
		c.JSON(http.StatusOK, pool.Usage(c.Request.Context(), refresh))
	})
	api.POST("/v1/chat/completions", adapter.ChatCompletionHandler(pool))
	api.POST("/v1/responses", adapter.ResponsesHandler(pool, deps.Store))
	api.POST("/v1/images/generations", adapter.ImageGenerationHandler(pool))
	api.POST("/v1/images/edits", adapter.ImageEditHandler(pool))
	api.GET("/v1/models", adapter.ListModelsHandler(pool))
	api.GET("/v1/models/:model", adapter.RetrieveModelHandler(pool))
	api.POST("/v1/messages", adapter.ClaudeMessagesHandler(pool))
	api.POST("/v1/messages/count_tokens", adapter.ClaudeCountTokensHandler(pool))
	api.POST("/v1beta/models/*action", adapter.GeminiRouterHandler(pool, deps.Store))
	api.GET("/v1beta/models", adapter.GeminiListModelsHandler(pool))
	api.GET("/v1beta/models/:model", adapter.GeminiGetModelHandler(pool))
	api.GET("/v1/responses/:response", adapter.ResponseRetrieveHandler(deps.Store))
	api.DELETE("/v1/responses/:response", adapter.ResponseDeleteHandler(deps.Store))
	api.GET("/v1/responses/:response/input_items", adapter.ResponseInputItemsHandler(deps.Store))
	api.POST("/v1/responses/:response/cancel", adapter.ResponseCancelHandler(deps.Store))
	api.POST("/v1/responses/input_tokens", adapter.ResponsesInputTokensHandler(pool))
	api.POST("/v1/responses/compact", adapter.ResponsesCompactHandler(pool, deps.Store))
	api.POST("/v1/completions", adapter.CompletionHandler(pool))
	api.POST("/v1/files", adapter.FileUploadHandler(deps.Store))
	api.GET("/v1/files", adapter.FileListHandler(deps.Store))
	api.GET("/v1/files/:file", adapter.FileRetrieveHandler(deps.Store))
	api.DELETE("/v1/files/:file", adapter.FileDeleteHandler(deps.Store))
	api.GET("/v1/files/:file/content", adapter.FileContentHandler(deps.Store))
	api.POST("/upload/v1beta/files", adapter.GeminiFileUploadHandler(deps.Store))
	api.PUT("/upload/v1beta/files", adapter.GeminiFileUploadHandler(deps.Store))
	api.GET("/v1beta/files", adapter.GeminiFileListHandler(deps.Store))
	api.GET("/v1beta/files/:name", adapter.GeminiFileGetHandler(deps.Store))
	api.DELETE("/v1beta/files/:name", adapter.GeminiFileDeleteHandler(deps.Store))
	api.POST("/v1/videos", adapter.VideoCreateHandler(pool, deps.Store))
	api.GET("/v1/videos", adapter.VideoListHandler(deps.Store))
	api.GET("/v1/videos/:video", adapter.VideoRetrieveHandler(deps.Store))
	api.DELETE("/v1/videos/:video", adapter.VideoDeleteHandler(deps.Store))
	api.GET("/v1/videos/:video/content", adapter.VideoContentHandler(deps.Store))
	api.GET("/v1beta/models/:model/operations/:operation", adapter.GeminiOperationHandler(deps.Store))
	api.GET("/v1beta/operations/:operation", adapter.GeminiOperationHandler(deps.Store))
	api.POST("/v1/audio/speech", adapter.SpeechHandler(pool))
	api.POST("/v1/audio/transcriptions", adapter.TranscriptionHandler(pool))
	api.POST("/v1/audio/translations", adapter.TranslationHandler(pool))
	api.POST("/v1/embeddings", adapter.UnavailableOpenAIHandler("embeddings", "embeddings"))
	api.POST("/v1/moderations", adapter.UnavailableOpenAIHandler("moderations", "moderation"))
	api.GET("/v1/realtime", adapter.UnavailableOpenAIHandler("realtime", "the Realtime API"))
	api.POST("/v1/realtime/sessions", adapter.UnavailableOpenAIHandler("realtime", "the Realtime API"))
	api.POST("/v1/realtime/client_secrets", adapter.UnavailableOpenAIHandler("realtime", "the Realtime API"))
	api.GET("/ws/*method", adapter.GeminiLiveHandler)

	if deps.Admin != nil {
		admin.Mount(router, *deps.Admin)
	}

	webuiHandler := webui.Handler()
	router.NoRoute(adapter.CORSMiddleware(), adapter.RequestMiddleware(deps.Store), func(c *gin.Context) {
		path := c.Request.URL.Path
		switch {
		case strings.HasPrefix(path, "/v1beta/"), strings.HasPrefix(path, "/upload/"):
			adapter.WriteGeminiNotFound(c)
		case strings.HasPrefix(path, "/v1/"):
			adapter.WriteOpenAINotFound(c)
		case strings.HasPrefix(path, "/api/"):
			admin.NotFound(c)
		default:
			webuiHandler.ServeHTTP(c.Writer, c.Request)
		}
	})
	return router
}

// securityHeaders 禁止页面被其他站点嵌入并关闭 MIME 嗅探与 Referer
func securityHeaders(c *gin.Context) {
	header := c.Writer.Header()
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", "frame-ancestors 'none'")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	c.Next()
}
