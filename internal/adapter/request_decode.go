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
