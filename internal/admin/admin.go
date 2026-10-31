// Package admin 提供管理端接口：状态与事件流、账号、模型、请求、日志、用量与设置
package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/accounts"
	"github.com/Mag1cFall/Gemini-Web2API/internal/adapter"
	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/gin-gonic/gin"
)

// Deps 是管理端使用的运行期组件与进程信息
type Deps struct {
	Pool       *balancer.AccountPool
	Accounts   *accounts.Manager
	Store      *requestdb.Store
	Settings   *Settings
	Version    string
	StartedAt  time.Time
	ListenAddr string
	Desktop    bool
	DataDir    string
	Updates    UpdateService
	// Stopping 在服务开始停机时关闭，已打开的事件流随之结束
	Stopping <-chan struct{}
}

type handler struct {
	Deps
	settings *Settings
	sessions sessionStore
	attempts loginAttempts
	pairing  pairingTokens
}

// Mount 在 /api 下注册管理端接口
func Mount(router *gin.Engine, deps Deps) {
	h := &handler{Deps: deps, settings: deps.Settings}
	deps.Settings.changed = h.sessions.clear
	router.POST("/api/pairing/accounts", h.pairingAccount)
	api := router.Group("/api", h.guard)
	api.GET("/auth/session", h.session)
	api.POST("/auth/login", h.login)
	api.POST("/auth/logout", h.logout)
	api.GET("/status", func(c *gin.Context) { c.JSON(http.StatusOK, h.status()) })
	api.GET("/events", h.events)
	api.GET("/accounts", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"accounts": h.accountViews()}) })
	api.PUT("/accounts/:id", h.updateAccount)
	api.DELETE("/accounts/:id", h.deleteAccount)
	api.POST("/accounts/:id/verify", h.verifyAccount)
	api.GET("/accounts/import/chrome", h.chromeProfiles)
	api.POST("/accounts/import/chrome", h.importChrome)
	api.GET("/accounts/import/browser", h.browserProfiles)
	api.POST("/accounts/import/browser", h.importBrowser)
	api.POST("/accounts/import/cookie", h.importCookie)
	api.POST("/pairing", h.createPairing)
	api.GET("/accounts/usage", func(c *gin.Context) {
		refresh, _ := strconv.ParseBool(c.Query("refresh"))
		c.JSON(http.StatusOK, h.Pool.Usage(c.Request.Context(), refresh))
	})
	api.GET("/models", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"models": h.models()}) })
	api.GET("/cooldowns", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"cooldowns": h.cooldowns()}) })
	api.DELETE("/accounts/:id/cooldowns/:scope", h.clearCooldown)
	api.GET("/requests", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"requests": logging.ActiveRequests(), "recent": logging.RecentRequests()})
	})
	api.POST("/requests/:id/cancel", h.cancelRequest)
	api.GET("/requests/:id/body", requestdb.BodyHandler(deps.Store))
	api.GET("/logs", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"logs": logging.History()}) })
	api.DELETE("/logs", func(c *gin.Context) {
		logging.Clear()
		c.Status(http.StatusNoContent)
	})
	api.GET("/usage", requestdb.UsageHandler(deps.Store))
	api.GET("/usage/records", requestdb.RecordsHandler(deps.Store))
	api.GET("/usage/records.csv", requestdb.ExportHandler(deps.Store))
	api.GET("/config", func(c *gin.Context) { c.JSON(http.StatusOK, h.settings.View()) })
	api.PUT("/config", h.updateConfig)
	api.GET("/proxies", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"proxies": h.Accounts.ProxyStatus()}) })
	api.POST("/proxies/check", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"proxies": h.Accounts.CheckProxies(c.Request.Context())})
	})
	api.POST("/proxies/test", h.testProxy)
	api.GET("/update", h.updateStatus)
	api.POST("/update", h.applyUpdate)
}

// NotFound 以管理端错误体返回未知的 /api 路径
func NotFound(c *gin.Context) {
	writeError(c, http.StatusNotFound, "not_found", "未知的管理端接口 "+c.Request.Method+" "+c.Request.URL.Path)
}

// accountCounts 是各状态的账号数
type accountCounts struct {
	Total        int `json:"total"`
	Available    int `json:"available"`
	CoolingDown  int `json:"cooling_down"`
	Initializing int `json:"initializing"`
	AuthRequired int `json:"auth_required"`
	Unavailable  int `json:"unavailable"`
	Disabled     int `json:"disabled"`
}

// serviceStatus 是服务运行状态
type serviceStatus struct {
	Version        string        `json:"version"`
	Ready          bool          `json:"ready"`
	StartedAt      time.Time     `json:"started_at"`
	ListenAddr     string        `json:"listen_addr"`
	BaseURL        string        `json:"base_url"`
	APIKeyRequired bool          `json:"api_key_required"`
	ActiveRequests int           `json:"active_requests"`
	Accounts       accountCounts `json:"accounts"`
	// Guest 是访客会话的状态
	Guest   *balancer.AccountStatus `json:"guest,omitempty"`
	Desktop bool                    `json:"desktop"`
	DataDir string                  `json:"data_dir"`
}

func (h *handler) status() serviceStatus {
	pool := h.Pool.Status()
	return serviceStatus{
		Version: h.Version, Ready: pool.Ready(), StartedAt: h.StartedAt, ListenAddr: h.ListenAddr,
		BaseURL: baseURL(h.ListenAddr), APIKeyRequired: h.settings.APIKey() != "", ActiveRequests: logging.ActiveCount(),
		Accounts: accountCounts{
			Total: pool.Total, Available: pool.Available, CoolingDown: pool.CoolingDown, Initializing: pool.Initializing,
			AuthRequired: pool.AuthRequired, Unavailable: pool.Unavailable, Disabled: pool.Disabled,
		},
		Guest:   pool.Guest,
		Desktop: h.Desktop, DataDir: h.DataDir,
	}
}

// baseURL 返回客户端接入地址，监听全部地址时用本机回环地址
func baseURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// modelInfo 是管理端模型页的模型
type modelInfo struct {
	ID               string   `json:"id"`
	DisplayName      string   `json:"display_name"`
	Description      string   `json:"description"`
	Default          bool     `json:"default"`
	AliasOf          string   `json:"alias_of,omitempty"`
	Tool             string   `json:"tool,omitempty"`
	Capabilities     []string `json:"capabilities"`
	ThinkingLevels   []string `json:"thinking_levels"`
	MinContextWindow int      `json:"min_context_window"`
	MaxContextWindow int      `json:"max_context_window"`
	Accounts         []string `json:"accounts"`
	CoolingAccounts  []string `json:"cooling_accounts"`
}

// modelTools 是官网工具条目在模型页的工具名
var modelTools = map[gemini.Feature]string{
	gemini.FeatureImage: "image", gemini.FeatureVideo: "video", gemini.FeatureMusic: "music", gemini.FeatureCanvas: "canvas",
	gemini.FeatureDeepResearch: "deep_research", gemini.FeatureLearning: "guided_learning", gemini.FeatureTask: "task",
}

func (h *handler) models() []modelInfo {
	cooling := make(map[string][]string)
	now := time.Now()
	for _, account := range h.Pool.Status().Accounts {
		for _, cooldown := range account.ModelCooldowns {
			if now.Before(cooldown.Until) {
				cooling[cooldown.Model] = append(cooling[cooldown.Model], account.AccountID)
			}
		}
	}
	catalog := adapter.Catalog(h.Pool)
	models := make([]modelInfo, 0, len(catalog))
	for _, model := range catalog {
		levels := make([]string, 0, len(model.ThinkingLevels))
		for _, level := range model.ThinkingLevels {
			levels = append(levels, level.String())
		}
		target := model.ID
		if model.AliasOf != "" {
			target = model.AliasOf
		}
		models = append(models, modelInfo{
			ID: model.ID, DisplayName: model.DisplayName, Description: model.Description, Default: model.Default,
			AliasOf: model.AliasOf, Tool: modelTools[model.Feature], Capabilities: nonNil(model.Capabilities), ThinkingLevels: levels,
			MinContextWindow: model.MinInputTokenLimit, MaxContextWindow: model.MaxInputTokenLimit,
			Accounts: nonNil(model.Accounts), CoolingAccounts: nonNil(cooling[target]),
		})
	}
	return models
}

// cooldown 是一个账号或账号内一个模型的冷却
type cooldown struct {
	AccountID string    `json:"account_id"`
	Model     string    `json:"model"`
	Until     time.Time `json:"until"`
	Reason    string    `json:"reason"`
}

func (h *handler) cooldowns() []cooldown {
	result := []cooldown{}
	for _, account := range h.Pool.Status().Accounts {
		if account.CooldownUntil != nil {
			result = append(result, cooldown{AccountID: account.AccountID, Until: *account.CooldownUntil, Reason: account.CooldownReason})
		}
		for _, model := range account.ModelCooldowns {
			result = append(result, cooldown{AccountID: account.AccountID, Model: model.Model, Until: model.Until, Reason: model.Reason})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Until.Before(result[j].Until) })
	return result
}

// clearCooldown 清除账号的一项冷却，scope 为模型 ID，* 表示账号级冷却
func (h *handler) clearCooldown(c *gin.Context) {
	accountID, model := c.Param("id"), c.Param("scope")
	if model == "*" {
		model = ""
	}
	if err := h.Pool.ClearCooldown(accountID, model); err != nil {
		writeError(c, http.StatusNotFound, "account_not_found", err.Error())
		return
	}
	logging.Info(accountID, "冷却已手动清除", "范围", c.Param("scope"))
	c.Status(http.StatusNoContent)
}

func (h *handler) cancelRequest(c *gin.Context) {
	if !logging.CancelRequest(c.Param("id")) {
		writeError(c, http.StatusNotFound, "request_not_found", "请求不存在或已结束")
		return
	}
	c.Status(http.StatusNoContent)
}

// eventPing 是事件流空闲时的保活间隔
const eventPing = 15 * time.Second

// events 以 SSE 先用一条 logs 事件回放历史日志再推送状态快照，之后推送快照的变化与逐条的新日志
func (h *handler) events(c *gin.Context) {
	history, logs, cancel := logging.Subscribe(2000)
	defer cancel()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	lastSent := time.Now()
	send := func(kind string, data any) bool {
		payload, err := json.Marshal(struct {
			Type string `json:"type"`
			Data any    `json:"data"`
		}{kind, data})
		if err != nil {
			return true
		}
		if _, err := c.Writer.Write([]byte("data: " + string(payload) + "\n\n")); err != nil {
			return false
		}
		lastSent = time.Now()
		return true
	}
	snapshots := map[string][]byte{}
	push := func() bool {
		for _, item := range []struct {
			kind string
			data any
		}{
			{"status", h.status()}, {"accounts", h.accountViews()}, {"models", h.models()},
			{"cooldowns", h.cooldowns()}, {"requests", logging.ActiveRequests()}, {"recent", logging.RecentRequests()},
		} {
			encoded, _ := json.Marshal(item.data)
			if bytes.Equal(encoded, snapshots[item.kind]) {
				continue
			}
			snapshots[item.kind] = encoded
			if !send(item.kind, json.RawMessage(encoded)) {
				return false
			}
		}
		return true
	}
	if !send("logs", nonNil(history)) {
		return
	}
	if !push() {
		return
	}
	c.Writer.Flush()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-h.Stopping:
			return
		case event, ok := <-logs:
			if !ok || !send("log", event) {
				return
			}
		case <-ticker.C:
			if !push() {
				return
			}
			if time.Since(lastSent) >= eventPing {
				if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
					return
				}
				lastSent = time.Now()
			}
		}
		c.Writer.Flush()
	}
}

func (h *handler) updateConfig(c *gin.Context) {
	var update ConfigUpdate
	if !bindJSON(c, &update) {
		return
	}
	if err := h.settings.Update(update); err != nil {
		var invalid *validationError
		if errors.As(err, &invalid) {
			writeError(c, http.StatusBadRequest, "invalid_config", invalid.Error())
			return
		}
		writeError(c, http.StatusInternalServerError, "config_write_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, h.settings.View())
}

func (h *handler) testProxy(c *gin.Context) {
	var input struct {
		Proxy string `json:"proxy"`
	}
	if !bindJSON(c, &input) {
		return
	}
	res := h.Accounts.TestProxy(c.Request.Context(), input.Proxy)
	c.JSON(http.StatusOK, res)
}

// writeError 以管理端错误体结束请求
func writeError(c *gin.Context, status int, code string, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// bindJSON 解析 JSON 请求体，失败时返回 400
func bindJSON(c *gin.Context, target any) bool {
	if err := json.NewDecoder(c.Request.Body).Decode(target); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "请求体不是有效的 JSON: "+err.Error())
		return false
	}
	return true
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func sortStrings(values []string) []string {
	sort.Strings(values)
	return values
}

// trimmed 返回去掉首尾空白的字符串
func trimmed(value string) string {
	return strings.TrimSpace(value)
}
