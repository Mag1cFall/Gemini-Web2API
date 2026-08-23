package adapter

import (
	"errors"
	"net/http"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
	"github.com/gin-gonic/gin"
)

func upstreamStatus(err error) int {
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

func openAIUpstreamCode(err error) string {
	if upstreamStatus(err) == http.StatusNotFound {
		return "model_not_found"
	}
	return "upstream_error"
}

func claudeUpstreamType(err error) string {
	if upstreamStatus(err) == http.StatusNotFound {
		return "not_found_error"
	}
	if upstreamStatus(err) == http.StatusServiceUnavailable {
		return "overloaded_error"
	}
	return "api_error"
}

func geminiUpstreamStatus(err error) string {
	switch upstreamStatus(err) {
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	default:
		return "INTERNAL"
	}
}

func writeOpenAIError(c *gin.Context, status int, code string, message string) {
	c.JSON(status, gin.H{"error": gin.H{
		"message": message,
		"type":    "api_error",
		"code":    code,
	}})
}

func writeClaudeError(c *gin.Context, status int, errorType string, message string) {
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errorType, "message": message},
	})
}

func writeGeminiError(c *gin.Context, status int, statusName string, message string) {
	c.JSON(status, gin.H{"error": gin.H{
		"code": status, "message": message, "status": statusName,
	}})
}
