package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// forwardedHeaders 是转给进程内公开 API 请求的鉴权与来源请求头
var forwardedHeaders = []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Origin"}

// recorder 收集进程内公开 API 请求的响应
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header {
	return r.header
}

func (r *recorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(data)
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *recorder) Flush() {}

// do 以 MCP 请求的鉴权头与 key 查询参数、来源地址与上下文向本服务公开 API 发出一个请求
func (s *server) do(c *gin.Context, method string, path string, body any) (int, http.Header, []byte) {
	var reader io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return http.StatusInternalServerError, nil, []byte(err.Error())
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), method, path, reader)
	if err != nil {
		return http.StatusInternalServerError, nil, []byte(err.Error())
	}
	request.Host, request.RemoteAddr = c.Request.Host, c.Request.RemoteAddr
	if key := c.Query("key"); key != "" {
		query := request.URL.Query()
		query.Set("key", key)
		request.URL.RawQuery = query.Encode()
	}
	for _, name := range forwardedHeaders {
		if value := c.GetHeader(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := &recorder{header: http.Header{}}
	s.api.ServeHTTP(response, request)
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.status, response.header, response.body.Bytes()
}

// call 发出公开 API 请求并把 200 响应解码到 target，其余状态返回写明公开 API 错误信息的错误
func (s *server) call(c *gin.Context, method string, path string, body any, target any) error {
	status, _, data := s.do(c, method, path, body)
	if status != http.StatusOK {
		return apiError(status, data)
	}
	return json.Unmarshal(data, target)
}

// apiError 返回公开 API 错误响应中的状态与信息
func apiError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
	}
	return fmt.Errorf("HTTP %d: %s", status, message)
}
