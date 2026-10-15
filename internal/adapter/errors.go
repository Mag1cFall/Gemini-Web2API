package adapter

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/gin-gonic/gin"
)

// requestScopeKey 是 gin 上下文中保存账号池与请求模型的键
const requestScopeKey = "gemini_web2api.scope"

// requestScope 是错误响应计算 Retry-After 与隐去账号 ID 所需的账号池与请求模型
type requestScope struct {
	pool  *balancer.AccountPool
	model string
}

// bindScope 记录请求使用的账号池与模型，之后写出的 429、503 错误带 Retry-After，错误正文隐去账号 ID
func bindScope(c *gin.Context, pool *balancer.AccountPool, model string) {
	c.Set(requestScopeKey, requestScope{pool: pool, model: model})
}

// scopeOf 返回请求绑定的账号池与模型
func scopeOf(c *gin.Context) (requestScope, bool) {
	value, ok := c.Get(requestScopeKey)
	scope, _ := value.(requestScope)
	return scope, ok && scope.pool != nil
}

// upstreamStatus 返回上游错误对应的 HTTP 状态，账号上下文窗口不足按请求过长返回 400
func upstreamStatus(err error) int {
	if isAccountContextError(err) {
		return http.StatusBadRequest
	}
	var protocolErr *gemini.ProtocolError
	if errors.As(err, &protocolErr) {
		if protocolErr.HTTPStatus >= 400 && protocolErr.HTTPStatus <= 599 {
			return protocolErr.HTTPStatus
		}
		if protocolErr.Retryable {
			return http.StatusServiceUnavailable
		}
	}
	return http.StatusBadGateway
}

// openAIUpstreamCode 返回上游错误的 OpenAI 错误码
func openAIUpstreamCode(err error) string {
	if isAccountContextError(err) {
		return "context_length_exceeded"
	}
	switch upstreamStatus(err) {
	case http.StatusNotFound:
		return "model_not_found"
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusBadRequest:
		return "invalid_request"
	case logging.StatusClientClosed:
		return "request_cancelled"
	}
	return "upstream_error"
}

// claudeUpstreamType 返回上游错误的 Anthropic 错误类型
func claudeUpstreamType(err error) string {
	return claudeErrorType(upstreamStatus(err))
}

// claudeErrorType 返回 HTTP 状态对应的 Anthropic 错误类型
func claudeErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable, 529:
		return "overloaded_error"
	case logging.StatusClientClosed:
		return "api_error"
	default:
		if status >= 400 && status < 500 {
			return "invalid_request_error"
		}
		return "api_error"
	}
}

// geminiUpstreamStatus 返回上游错误的 Google RPC 状态名
func geminiUpstreamStatus(err error) string {
	return geminiStatusName(upstreamStatus(err))
}

// geminiStatusName 返回 HTTP 状态对应的 Google RPC 状态名
func geminiStatusName(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "ABORTED"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case 499:
		return "CANCELLED"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		if status >= 400 && status < 500 {
			return "FAILED_PRECONDITION"
		}
		return "INTERNAL"
	}
}

// openAIErrorType 返回 HTTP 状态对应的 OpenAI 错误类型：请求错误为 invalid_request_error，频率限制为 requests，服务端错误与管理端取消为 server_error
func openAIErrorType(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "requests"
	case status >= 500 || status == logging.StatusClientClosed:
		return "server_error"
	default:
		return "invalid_request_error"
	}
}

// writeOpenAIError 按 OpenAI 错误对象写入 message、type、param、code
func writeOpenAIError(c *gin.Context, status int, code string, message string) {
	message = publicMessage(c, message)
	logging.FromContext(c.Request.Context()).Fail(status, message)
	setRetryAfter(c, status)
	c.JSON(status, gin.H{"error": openAIErrorObject(status, code, message)})
}

// openAIErrorObject 返回 OpenAI 错误对象
func openAIErrorObject(status int, code string, message string) gin.H {
	return gin.H{"message": message, "type": openAIErrorType(status), "param": nil, "code": code}
}

func writeClaudeError(c *gin.Context, status int, errorType string, message string) {
	message = publicMessage(c, message)
	logging.FromContext(c.Request.Context()).Fail(status, message)
	setRetryAfter(c, status)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errorType, "message": message},
	})
}

func writeGeminiError(c *gin.Context, status int, statusName string, message string) {
	message = publicMessage(c, message)
	logging.FromContext(c.Request.Context()).Fail(status, message)
	setRetryAfter(c, status)
	c.JSON(status, gin.H{"error": gin.H{
		"code": status, "message": message, "status": statusName,
	}})
}

// setRetryAfter 在 429、503 响应中写入请求模型最早结束冷却的秒数，没有冷却中的账号时不写
func setRetryAfter(c *gin.Context, status int) {
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		return
	}
	scope, ok := scopeOf(c)
	if !ok {
		return
	}
	until := scope.pool.RetryAfter(config.MapModel(scope.model))
	if until.IsZero() {
		return
	}
	seconds := int(math.Ceil(time.Until(until).Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
}

// upstreamURLPattern 匹配 Go HTTP 客户端错误中的请求方法与 URL 前缀
var upstreamURLPattern = regexp.MustCompile(`(?:Get|Post|Put|Delete|Head) "https?://[^"]*": `)

// upstreamQueryPattern 匹配 URL 的查询串，其中含 f.sid、_reqid 与签名参数
var upstreamQueryPattern = regexp.MustCompile(`(https?://[^\s"?]+)\?[^\s"]*`)

// publicMessage 去掉错误文本中的上游请求 URL、查询串与账号 ID
func publicMessage(c *gin.Context, message string) string {
	message = upstreamURLPattern.ReplaceAllString(message, "")
	message = upstreamQueryPattern.ReplaceAllString(message, "$1")
	if scope, ok := scopeOf(c); ok {
		for _, account := range scope.pool.Status().Accounts {
			message = replaceWord(message, account.AccountID, "account")
		}
	}
	return message
}

// replaceWord 把 text 中作为完整词出现的 word 替换为 replacement
func replaceWord(text string, word string, replacement string) string {
	if strings.TrimSpace(word) == "" || !strings.Contains(text, word) {
		return text
	}
	pattern := regexp.MustCompile(`(^|[^\w.-])` + regexp.QuoteMeta(word) + `($|[^\w.-])`)
	for {
		replaced := pattern.ReplaceAllString(text, "${1}"+replacement+"${2}")
		if replaced == text {
			return replaced
		}
		text = replaced
	}
}

// publicError 返回去掉上游 URL、查询串与账号 ID 的错误
func publicError(c *gin.Context, err error) error {
	if err == nil {
		return nil
	}
	return errors.New(publicMessage(c, err.Error()))
}

// streamFailure 记录流式响应头发出后的失败并返回去掉内部标识的错误
func streamFailure(c *gin.Context, err error) error {
	if err != nil {
		logging.FromContext(c.Request.Context()).Fail(upstreamStatus(err), err.Error())
	}
	return publicError(c, err)
}

// unavailableCapability 返回 Gemini Web 不提供某项能力的说明
func unavailableCapability(capability string) string {
	return fmt.Sprintf("Gemini Web does not provide %s; this service only exposes capabilities that the Gemini web app can perform", capability)
}
