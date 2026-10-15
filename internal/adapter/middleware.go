package adapter

import (
	"bytes"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"
	"github.com/gin-gonic/gin"
)

// CORSMiddleware 允许任意网页跨域调用公开 API，预检回显请求的请求头并暴露全部响应头，管理端 /api 不允许跨域
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		header := c.Writer.Header()
		header.Set("Access-Control-Allow-Origin", "*")
		header.Set("Access-Control-Expose-Headers", "*")
		header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if requested := c.GetHeader("Access-Control-Request-Headers"); requested != "" {
			header.Set("Access-Control-Allow-Headers", requested)
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// authFailedKey 是标记请求鉴权失败的 gin 上下文键
const authFailedKey = "gw.auth_failed"

// AuthMiddleware 校验服务公开 API key，apiKey 返回当前生效的密钥，多个 key 以逗号分隔，任一匹配即通过；未设密钥时拒绝 null 来源与非本机网页发起的请求，以 IP 地址打开的本服务页面照常访问，断点续传的后续请求凭 upload_id 授权
func AuthMiddleware(apiKey func() string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/upload/v1beta/files" && geminiUploadExists(c.Query("upload_id")) {
			c.Next()
			return
		}
		keys := config.SplitList(apiKey())
		if len(keys) == 0 {
			if foreignWebOrigin(c.GetHeader("Origin"), c.Request.Host) {
				writeAuthError(c, false)
				c.Abort()
				return
			}
			c.Next()
			return
		}
		provided, matched := apiKeyMatches(c, keys)
		if !matched {
			writeAuthError(c, provided)
			c.Abort()
			return
		}
		c.Next()
	}
}

// apiKeyMatches 报告请求是否带了 API key，以及查询参数或请求头中是否有任一 key 与配置的某个 key 相同
func apiKeyMatches(c *gin.Context, keys []string) (bool, bool) {
	bearer := ""
	if scheme, value, ok := strings.Cut(strings.TrimSpace(c.GetHeader("Authorization")), " "); ok && strings.EqualFold(scheme, "Bearer") {
		bearer = value
	}
	provided := false
	for _, candidate := range []string{c.Query("key"), c.GetHeader("x-goog-api-key"), c.GetHeader("x-api-key"), bearer} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		provided = true
		for _, key := range keys {
			if subtle.ConstantTimeCompare([]byte(candidate), []byte(key)) == 1 {
				return true, true
			}
		}
	}
	return provided, false
}

// foreignWebOrigin 报告 Origin 为 null，或是非本机且不是以 IP 地址打开的本服务页面的 http、https 网页，没有 Origin 的客户端与其他 scheme 不算
func foreignWebOrigin(value string, host string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	origin, err := url.Parse(value)
	if err != nil || value == "null" {
		return true
	}
	return (origin.Scheme == "http" || origin.Scheme == "https") && !LoopbackHost(origin.Host) && !sameAddressHost(origin.Host, host)
}

// sameAddressHost 报告 Origin 主机与请求 Host 相同且是 IP 地址，域名同源可能来自 DNS 重绑定
func sameAddressHost(origin string, host string) bool {
	if !strings.EqualFold(origin, host) {
		return false
	}
	if hostname, _, err := net.SplitHostPort(origin); err == nil {
		origin = hostname
	}
	return net.ParseIP(strings.Trim(origin, "[]")) != nil
}

// LoopbackHost 报告 Host 或 Origin 主机名为 localhost 或回环地址
func LoopbackHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RequestMiddleware 为公开 API 请求分配 ID、跟踪生成请求的阶段，结束时输出结果并把生成请求写入账本
func RequestMiddleware(store *requestdb.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		request, ctx := logging.NewRequest(c.Request.Context(), c.Request.Method, c.Request.URL.Path, c.ClientIP())
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Request-ID", request.ID())
		var capture *bodyCapture
		if store.BodyCapture() {
			capture = captureBodies(c)
		}
		c.Next()
		status := c.Writer.Status()
		info := request.Finish(status, ctx.Err() != nil)
		if request.Begun() {
			store.Record(info, capture.body())
			return
		}
		target := c.Request.Method + " " + c.Request.URL.Path
		switch {
		case c.GetBool(authFailedKey):
			logging.Warn(logging.SourceRequest, "鉴权失败", "", target, "IP", c.ClientIP())
		case status == http.StatusNotFound && c.FullPath() == "":
			logging.Warn(logging.SourceRequest, "未知路由", "", target, "IP", c.ClientIP())
		case status >= 400:
			logging.Warn(logging.SourceRequest, "请求被拒绝", "", target, "", status, "", info.Error)
		default:
			logging.Debug(logging.SourceRequest, "访问", "", target, "", status, "", time.Since(started))
		}
	}
}

// bodyCapture 保存请求与响应正文的前 requestdb.BodyLimit 字节与原始字节数
type bodyCapture struct {
	gin.ResponseWriter
	request  limitedBuffer
	response limitedBuffer
}

// limitedBuffer 只保留前 requestdb.BodyLimit 字节并统计总字节数
type limitedBuffer struct {
	data  bytes.Buffer
	total int64
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	buffer.total += int64(len(data))
	if room := requestdb.BodyLimit - buffer.data.Len(); room > 0 {
		buffer.data.Write(data[:min(len(data), room)])
	}
	return len(data), nil
}

// captureBodies 在请求体被读取与响应写出时同步记录正文
func captureBodies(c *gin.Context) *bodyCapture {
	capture := &bodyCapture{ResponseWriter: c.Writer}
	if c.Request.Body != nil {
		c.Request.Body = struct {
			io.Reader
			io.Closer
		}{io.TeeReader(c.Request.Body, &capture.request), c.Request.Body}
	}
	c.Writer = capture
	return capture
}

func (capture *bodyCapture) Write(data []byte) (int, error) {
	_, _ = capture.response.Write(data)
	return capture.ResponseWriter.Write(data)
}

func (capture *bodyCapture) WriteString(data string) (int, error) {
	_, _ = capture.response.Write([]byte(data))
	return capture.ResponseWriter.WriteString(data)
}

// body 返回捕获的正文，未捕获时为 nil
func (capture *bodyCapture) body() *requestdb.Body {
	if capture == nil {
		return nil
	}
	return &requestdb.Body{
		Request: capture.request.data.Bytes(), RequestSize: capture.request.total,
		Response: capture.response.data.Bytes(), ResponseSize: capture.response.total,
	}
}

// thinkingLevelNames 返回模型可选思考强度的小写名称
func thinkingLevelNames(levels []gemini.ThinkingLevel) []string {
	names := make([]string, 0, len(levels))
	for _, level := range levels {
		names = append(names, level.String())
	}
	return names
}

// writeAuthError 按请求协议写与官方同形的鉴权错误，provided 表示请求带了 API key
func writeAuthError(c *gin.Context, provided bool) {
	c.Set(authFailedKey, true)
	path := c.Request.URL.Path
	var status int
	var message string
	var body gin.H
	switch {
	case strings.HasPrefix(path, "/v1beta/") || strings.HasPrefix(path, "/upload/v1beta/") || strings.HasPrefix(path, "/ws/google.ai.generativelanguage."):
		if provided {
			status, message = http.StatusBadRequest, "API key not valid. Please pass a valid API key."
			body = gin.H{"error": gin.H{"code": status, "message": message, "status": "INVALID_ARGUMENT", "details": []gin.H{{
				"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "API_KEY_INVALID", "domain": "googleapis.com",
				"metadata": gin.H{"service": "generativelanguage.googleapis.com"},
			}}}}
		} else {
			status, message = http.StatusForbidden, "Method doesn't allow unregistered callers (callers without established identity). Please use API Key or other form of API consumer identity to call this API."
			body = gin.H{"error": gin.H{"code": status, "message": message, "status": "PERMISSION_DENIED"}}
		}
	case strings.HasPrefix(path, "/v1/messages") || c.GetHeader("anthropic-version") != "":
		status, message = http.StatusUnauthorized, "x-api-key header is required"
		if provided {
			message = "invalid x-api-key"
		}
		body = gin.H{"type": "error", "error": gin.H{"type": "authentication_error", "message": message}}
	default:
		status, message = http.StatusUnauthorized, "You didn't provide an API key. You need to provide your API key in an Authorization header using Bearer auth (i.e. Authorization: Bearer YOUR_KEY)."
		var code any
		if provided {
			message, code = "Incorrect API key provided.", "invalid_api_key"
		}
		body = gin.H{"error": gin.H{"message": message, "type": "invalid_request_error", "param": nil, "code": code}}
	}
	logging.FromContext(c.Request.Context()).Fail(status, message)
	c.JSON(status, body)
}
