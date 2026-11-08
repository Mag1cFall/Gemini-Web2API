package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/gin-gonic/gin"
)

// mcpRouter 注册 /mcp，api 为工具调用的公开 API
func mcpRouter(api http.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := Handler(api, balancer.NewAccountPool(time.Minute, time.Minute), "test")
	router.POST("/mcp", handler)
	router.GET("/mcp", handler)
	router.DELETE("/mcp", handler)
	return router
}

// mcpPost 发出一条 /mcp 请求并返回响应与解码后的正文
func mcpPost(t *testing.T, router http.Handler, method string, body string) (*httptest.ResponseRecorder, any) {
	t.Helper()
	request := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var decoded any
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: %v", method, response.Body.String(), err)
		}
	}
	return response, decoded
}

// rpcErrorCode 返回 JSON-RPC 响应的错误码，没有错误时返回 0
func rpcErrorCode(reply any) float64 {
	object, _ := reply.(map[string]any)
	failure, _ := object["error"].(map[string]any)
	code, _ := failure["code"].(float64)
	return code
}

// TestMCPInitialize 验证 initialize 按 MCP 生命周期规范协商版本：支持的版本原样回显，不支持的版本回服务端最新版本，结果声明 tools 能力与 serverInfo，响应带同一 id 与 application/json
func TestMCPInitialize(t *testing.T) {
	router := mcpRouter(http.NotFoundHandler())
	for requested, want := range map[string]string{"2025-06-18": "2025-06-18", "2025-03-26": "2025-03-26", "2024-11-05": "2024-11-05", "2099-01-01": "2025-06-18"} {
		response, reply := mcpPost(t, router, http.MethodPost, `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"`+requested+`","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
		object := reply.(map[string]any)
		result, _ := object["result"].(map[string]any)
		capabilities, _ := result["capabilities"].(map[string]any)
		info, _ := result["serverInfo"].(map[string]any)
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") || object["jsonrpc"] != "2.0" || object["id"] != "init" ||
			result["protocolVersion"] != want || capabilities["tools"] == nil || info["name"] == nil || info["version"] == nil {
			t.Fatalf("initialize %s = %d %v", requested, response.Code, reply)
		}
	}
}

// TestMCPStreamableHTTP 验证 Streamable HTTP 传输：只有通知或客户端响应时返回无正文的 202，不提供 SSE 流与会话时 GET 与 DELETE 返回带 Allow 的 405，2025-03-26 的批量消息逐条应答请求、不应答通知
func TestMCPStreamableHTTP(t *testing.T) {
	router := mcpRouter(http.NotFoundHandler())
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":7,"result":{}}`,
		`[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"x"}}]`,
	} {
		if response, _ := mcpPost(t, router, http.MethodPost, body); response.Code != http.StatusAccepted || response.Body.Len() != 0 {
			t.Fatalf("%s = %d %q", body, response.Code, response.Body.String())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if response, _ := mcpPost(t, router, method, ""); response.Code != http.StatusMethodNotAllowed || !strings.Contains(response.Header().Get("Allow"), http.MethodPost) {
			t.Fatalf("%s = %d Allow %q", method, response.Code, response.Header().Get("Allow"))
		}
	}
	response, reply := mcpPost(t, router, http.MethodPost, `[{"jsonrpc":"2.0","id":"a","method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"x"}},{"jsonrpc":"2.0","id":"b","method":"ping"}]`)
	batch, _ := reply.([]any)
	if response.Code != http.StatusOK || len(batch) != 2 {
		t.Fatalf("批量 = %d %v", response.Code, reply)
	}
	for index, id := range []string{"a", "b"} {
		if item := batch[index].(map[string]any); item["id"] != id || item["result"] == nil {
			t.Fatalf("批量第 %d 项 = %v", index, item)
		}
	}
}

// TestMCPErrors 验证 JSON-RPC 与 MCP 规范的错误码：无法解析为 -32700，未知方法为 -32601，未知工具与无效的 tools/call 参数为 -32602
func TestMCPErrors(t *testing.T) {
	router := mcpRouter(http.NotFoundHandler())
	for body, want := range map[string]float64{
		`{"jsonrpc":`: -32700,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`:                                     -32601,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope","arguments":{}}}`: -32602,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":"x"}`:                            -32602,
	} {
		if _, reply := mcpPost(t, router, http.MethodPost, body); rpcErrorCode(reply) != want {
			t.Fatalf("%s = %v", body, reply)
		}
	}
}

// TestMCPToolsList 验证 tools/list 按 MCP 工具规范列出每个工具的名称、说明与类型为 object 的 inputSchema，必填参数都在 properties 中，工具为 README 所列的七个
func TestMCPToolsList(t *testing.T) {
	_, reply := mcpPost(t, mcpRouter(http.NotFoundHandler()), http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := reply.(map[string]any)["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, item := range tools {
		tool := item.(map[string]any)
		schema, _ := tool["inputSchema"].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		if schema["type"] != "object" || tool["description"] == "" {
			t.Fatalf("工具定义 = %v", tool)
		}
		required, _ := schema["required"].([]any)
		for _, name := range required {
			if properties[name.(string)] == nil {
				t.Fatalf("%s 的必填参数 %v 不在 properties 中", tool["name"], name)
			}
		}
		names = append(names, tool["name"].(string))
	}
	if strings.Join(names, ",") != "web_search,generate_image,generate_music,create_video_task,get_video_status,start_deep_research,get_deep_research" {
		t.Fatalf("工具 = %v", names)
	}
}
