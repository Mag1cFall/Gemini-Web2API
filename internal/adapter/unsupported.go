package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/gin-gonic/gin"
)

// UnavailableOpenAIHandler 处理 Gemini Web 没有对应能力的 OpenAI 端点，按官方模型不可用错误返回 404 model_not_found
func UnavailableOpenAIHandler(protocol string, capability string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			Model string `json:"model"`
		}
		if body, err := io.ReadAll(c.Request.Body); err == nil {
			_ = json.Unmarshal(body, &request)
		}
		if request.Model == "" {
			request.Model = c.Query("model")
		}
		logging.FromContext(c.Request.Context()).Begin(protocol, request.Model, false, 0, 0)
		writeOpenAIError(c, http.StatusNotFound, "model_not_found", fmt.Sprintf(
			"The model `%s` does not exist or you do not have access to it. %s.", request.Model, unavailableCapability(capability)))
	}
}

// GeminiLiveHandler 处理 Gemini Live API 的 BidiGenerateContent 连接，Gemini Web 没有该能力时返回 404 NOT_FOUND
func GeminiLiveHandler(c *gin.Context) {
	logging.FromContext(c.Request.Context()).Begin("live", "", true, 0, 0)
	writeGeminiError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf(
		"Method not found: %s. %s.", c.Request.URL.Path, unavailableCapability("the Live API")))
}
