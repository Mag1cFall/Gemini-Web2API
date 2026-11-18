package adapter

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/gin-gonic/gin"
)

// geminiCacheTTL 是未指定 ttl 与 expireTime 时显式缓存的存活时间，与 Gemini API 默认的 1 小时一致
const geminiCacheTTL = time.Hour

// geminiCache 是一个显式缓存：缓存的系统指令、内容与工具，以及资源元数据
type geminiCache struct {
	name        string
	model       string
	displayName string
	created     time.Time
	updated     time.Time
	expires     time.Time
	tokens      int
	request     GeminiGenerateContentRequest
}

// geminiCaches 按资源名保存显式缓存，过期缓存在访问与创建缓存时删除
var geminiCaches = struct {
	sync.Mutex
	items map[string]*geminiCache
}{items: make(map[string]*geminiCache)}

// geminiCacheRequest 是 cachedContents 创建与更新请求
type geminiCacheRequest struct {
	Model             string          `json:"model"`
	DisplayName       string          `json:"displayName"`
	Contents          []GeminiContent `json:"contents"`
	SystemInstruction *GeminiContent  `json:"systemInstruction"`
	Tools             json.RawMessage `json:"tools"`
	ToolConfig        json.RawMessage `json:"toolConfig"`
	TTL               string          `json:"ttl"`
	ExpireTime        string          `json:"expireTime"`
}

// expiration 返回 expireTime 或 now 加 ttl，两者都未设置时 ok 为 false，同时设置时返回错误
func (request geminiCacheRequest) expiration(now time.Time) (time.Time, bool, error) {
	if request.ExpireTime != "" && request.TTL != "" {
		return time.Time{}, false, fmt.Errorf("Invalid value at 'cached_content' (oneof), oneof field 'expiration' is already set. Cannot set both 'ttl' and 'expireTime'")
	}
	if request.ExpireTime != "" {
		value, err := time.Parse(time.RFC3339Nano, request.ExpireTime)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("expireTime: %w", err)
		}
		return value, true, nil
	}
	if request.TTL != "" {
		ttl, err := time.ParseDuration(request.TTL)
		if err != nil || ttl <= 0 {
			return time.Time{}, false, fmt.Errorf("ttl %q must be a positive duration such as \"300s\"", request.TTL)
		}
		return now.Add(ttl), true, nil
	}
	return time.Time{}, false, nil
}

// geminiCacheName 返回缓存资源名，接受 cachedContents/{id} 与 {id}
func geminiCacheName(value string) string {
	return "cachedContents/" + strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "/"), "cachedContents/")
}

// lookupGeminiCache 返回未过期缓存的副本，已过期的缓存同时删除
func lookupGeminiCache(name string) (*geminiCache, bool) {
	geminiCaches.Lock()
	defer geminiCaches.Unlock()
	cache, ok := geminiCaches.items[geminiCacheName(name)]
	if !ok {
		return nil, false
	}
	if !time.Now().Before(cache.expires) {
		delete(geminiCaches.items, cache.name)
		return nil, false
	}
	snapshot := *cache
	return &snapshot, true
}

// geminiCacheObject 是 CachedContent 资源的元数据，字段按 proto 字段号排列
type geminiCacheObject struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	CreateTime    string `json:"createTime"`
	UpdateTime    string `json:"updateTime"`
	ExpireTime    string `json:"expireTime"`
	DisplayName   string `json:"displayName,omitempty"`
	UsageMetadata struct {
		TotalTokenCount int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

// object 返回 CachedContent 资源的元数据
func (cache *geminiCache) object() geminiCacheObject {
	object := geminiCacheObject{
		Name: cache.name, Model: "models/" + cache.model, DisplayName: cache.displayName,
		CreateTime: cache.created.Format(time.RFC3339Nano), UpdateTime: cache.updated.Format(time.RFC3339Nano), ExpireTime: cache.expires.Format(time.RFC3339Nano),
	}
	object.UsageMetadata.TotalTokenCount = cache.tokens
	return object
}

// applyGeminiCache 把请求引用的显式缓存的系统指令、内容与工具放在请求之前，请求未设置工具调用方式时沿用缓存的设置；缓存不存在或已过期时写出 404 并返回 false
func applyGeminiCache(c *gin.Context, name string, request *GeminiGenerateContentRequest) (*geminiCache, bool) {
	if strings.TrimSpace(name) == "" {
		return nil, true
	}
	cache, ok := lookupGeminiCache(name)
	if !ok {
		writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("cachedContent %q was not found or has expired", geminiCacheName(name)))
		return nil, false
	}
	cached := cache.request
	if cached.SystemInstruction != nil {
		system := *cached.SystemInstruction
		if request.SystemInstruction != nil {
			system.Parts = append(slices.Clone(system.Parts), request.SystemInstruction.Parts...)
		}
		request.SystemInstruction = &system
	}
	request.Contents = append(slices.Clone(cached.Contents), request.Contents...)
	if tools := mergeGeminiTools(cached.Tools, request.Tools); tools != nil {
		request.Tools = tools
	}
	if len(request.ToolConfig) == 0 || string(request.ToolConfig) == "null" {
		request.ToolConfig = cached.ToolConfig
	}
	request.CachedContent = ""
	return cache, true
}

// mergeGeminiTools 返回缓存的工具组接上请求的工具组，两者都为空时返回 nil
func mergeGeminiTools(cached, requested json.RawMessage) json.RawMessage {
	var groups, more []json.RawMessage
	_ = json.Unmarshal(cached, &groups)
	_ = json.Unmarshal(requested, &more)
	if len(groups)+len(more) == 0 {
		return nil
	}
	merged, _ := json.Marshal(append(groups, more...))
	return merged
}

// cacheUsage 把缓存的 token 数写入 usageMetadata 的 cachedContentTokenCount
func (cache *geminiCache) cacheUsage(usage gin.H) gin.H {
	if cache != nil && usage != nil {
		usage["cachedContentTokenCount"] = cache.tokens
	}
	return usage
}

// GeminiCacheCreateHandler 保存缓存内容并以生成时的提示词编码统计缓存的 token 数
func GeminiCacheCreateHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request geminiCacheRequest
		if err := decodeGeminiRequest(c, &request); err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
		if model == "" {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "cachedContent model is required")
			return
		}
		if err := modelKnown(pool, model); err != nil {
			writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
			return
		}
		now := time.Now().UTC()
		expires, ok, err := request.expiration(now)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		if !ok {
			expires = now.Add(geminiCacheTTL)
		}
		cached := GeminiGenerateContentRequest{Contents: request.Contents, SystemInstruction: request.SystemInstruction, Tools: request.Tools, ToolConfig: request.ToolConfig}
		if len(cached.Contents) == 0 && (cached.SystemInstruction == nil || len(cached.SystemInstruction.Parts) == 0) {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "cachedContent requires contents or systemInstruction")
			return
		}
		tokens, err := geminiPromptTokens(c, pool, cached)
		if err != nil {
			writeGeminiError(c, upstreamStatus(err), geminiUpstreamStatus(err), err.Error())
			return
		}
		cache := &geminiCache{
			name: "cachedContents/" + strings.ToLower(rand.Text()), model: model, displayName: request.DisplayName,
			created: now, updated: now, expires: expires.UTC(), tokens: tokens, request: cached,
		}
		geminiCaches.Lock()
		maps.DeleteFunc(geminiCaches.items, func(_ string, item *geminiCache) bool { return !now.Before(item.expires) })
		geminiCaches.items[cache.name] = cache
		object := cache.object()
		geminiCaches.Unlock()
		c.JSON(http.StatusOK, object)
	}
}

// GeminiCacheListHandler 按创建时间从新到旧分页列出未过期的缓存
func GeminiCacheListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		size := 10
		if raw := c.Query("pageSize"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 {
				writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "pageSize must be a non-negative integer")
				return
			}
			if value > 0 {
				size = min(value, 1000)
			}
		}
		now := time.Now()
		geminiCaches.Lock()
		maps.DeleteFunc(geminiCaches.items, func(_ string, cache *geminiCache) bool { return !now.Before(cache.expires) })
		items := slices.Collect(maps.Values(geminiCaches.items))
		slices.SortFunc(items, func(a, b *geminiCache) int { return cmpCache(a, b) })
		start := 0
		if token := c.Query("pageToken"); token != "" {
			start = slices.IndexFunc(items, func(cache *geminiCache) bool { return cache.name == token }) + 1
			if start == 0 {
				geminiCaches.Unlock()
				writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pageToken")
				return
			}
		}
		page := items[start:min(start+size, len(items))]
		objects := make([]geminiCacheObject, len(page))
		for index, cache := range page {
			objects[index] = cache.object()
		}
		geminiCaches.Unlock()
		response := gin.H{}
		if len(objects) > 0 {
			response["cachedContents"] = objects
		}
		if start+size < len(items) {
			response["nextPageToken"] = page[len(page)-1].name
		}
		c.JSON(http.StatusOK, response)
	}
}

// cmpCache 按创建时间从新到旧、同一时刻按名称排列缓存
func cmpCache(a, b *geminiCache) int {
	if order := b.created.Compare(a.created); order != 0 {
		return order
	}
	return strings.Compare(a.name, b.name)
}

// GeminiCacheGetHandler 返回缓存资源的元数据
func GeminiCacheGetHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		cache, ok := lookupGeminiCache(c.Param("cache"))
		if !ok {
			writeGeminiCacheNotFound(c)
			return
		}
		c.JSON(http.StatusOK, cache.object())
	}
}

// GeminiCacheUpdateHandler 按 updateMask 列出的字段更新缓存的过期时间与显示名称，没有 updateMask 时更新请求中出现的这些字段
func GeminiCacheUpdateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var request geminiCacheRequest
		if err := decodeGeminiRequest(c, &request); err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		now := time.Now().UTC()
		expires, hasExpiration, err := request.expiration(now)
		if err != nil {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		expiration, displayName := hasExpiration, request.DisplayName != ""
		if mask := c.Query("updateMask"); mask != "" {
			expiration, displayName = false, false
			for _, path := range strings.Split(mask, ",") {
				switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "_", "")) {
				case "ttl", "expiretime", "expiration":
					expiration = true
				case "displayname":
					displayName = true
				default:
					writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("updateMask field %q cannot be updated; updatable fields are ttl, expireTime and displayName", path))
					return
				}
			}
			if expiration && !hasExpiration {
				writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "updateMask names the expiration but the request has no ttl or expireTime")
				return
			}
		}
		if !expiration && !displayName {
			writeGeminiError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "cachedContent update requires ttl, expireTime or displayName")
			return
		}
		if _, ok := lookupGeminiCache(c.Param("cache")); !ok {
			writeGeminiCacheNotFound(c)
			return
		}
		geminiCaches.Lock()
		cache, ok := geminiCaches.items[geminiCacheName(c.Param("cache"))]
		if ok {
			if expiration {
				cache.expires = expires.UTC()
			}
			if displayName {
				cache.displayName = request.DisplayName
			}
			cache.updated = now
		}
		var object geminiCacheObject
		if ok {
			object = cache.object()
		}
		geminiCaches.Unlock()
		if !ok {
			writeGeminiCacheNotFound(c)
			return
		}
		c.JSON(http.StatusOK, object)
	}
}

// GeminiCacheDeleteHandler 删除缓存
func GeminiCacheDeleteHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		cache, ok := lookupGeminiCache(c.Param("cache"))
		if !ok {
			writeGeminiCacheNotFound(c)
			return
		}
		geminiCaches.Lock()
		delete(geminiCaches.items, cache.name)
		geminiCaches.Unlock()
		c.JSON(http.StatusOK, gin.H{})
	}
}

// writeGeminiCacheNotFound 写出缓存资源不存在或已过期时官方的 403
func writeGeminiCacheNotFound(c *gin.Context) {
	writeGeminiError(c, http.StatusForbidden, "PERMISSION_DENIED", "CachedContent not found (or permission denied)")
}
