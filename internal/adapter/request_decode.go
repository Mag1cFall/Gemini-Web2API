package adapter

import (
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"
)

func decodeRequest(c *gin.Context, target interface{}) error {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// decodeGeminiRequest 读取 Gemini 原生请求，字段名按 proto3 JSON 规则接受下划线与驼峰两种写法
func decodeGeminiRequest(c *gin.Context, target interface{}) error {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	if body, _, err = geminiCamelKeys(body); err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func setUnsupportedParameters(c *gin.Context, parameters []string) {
	if len(parameters) == 0 {
		return
	}
	c.Header("X-Gemini-Web2API-Unsupported-Parameters", joinParameters(parameters))
}

func joinParameters(parameters []string) string {
	result := ""
	for index, parameter := range parameters {
		if index > 0 {
			result += ","
		}
		result += parameter
	}
	return result
}
