// Package mcp 在 /mcp 以 MCP Streamable HTTP 提供联网搜索、生图、音乐、视频与 Deep Research 工具，工具调用转为本服务公开 API 的进程内请求
package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"

	"github.com/Mag1cFall/Gemini-Web2API/internal/adapter"
	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/gin-gonic/gin"
)

// protocolVersions 是支持的 MCP 协议版本，第一个为最新版本
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// bodyLimit 是一次 JSON-RPC 请求正文的最大字节数
const bodyLimit = 64 << 20

// request 是 JSON-RPC 2.0 请求或通知
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response 是 JSON-RPC 2.0 响应
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError 是 JSON-RPC 错误对象
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// server 处理 MCP 请求，工具经 api 调用本服务的公开 API
type server struct {
	api     http.Handler
	pool    *balancer.AccountPool
	version string
}

// Handler 返回 /mcp 的处理函数：POST 接收 JSON-RPC 消息或批量消息并以 application/json 应答，只有通知与响应时返回 202，GET 与 DELETE 返回 405（不提供服务端推送流与会话）
func Handler(api http.Handler, pool *balancer.AccountPool, version string) gin.HandlerFunc {
	s := &server{api: api, pool: pool, version: version}
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
			c.Header("Allow", http.MethodPost)
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, bodyLimit))
		if err != nil {
			c.JSON(http.StatusBadRequest, response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: err.Error()}})
			return
		}
		body = bytes.TrimSpace(body)
		if len(body) > 0 && body[0] == '[' {
			var batch []request
			if err := json.Unmarshal(body, &batch); err != nil || len(batch) == 0 {
				c.JSON(http.StatusBadRequest, response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "invalid JSON-RPC batch"}})
				return
			}
			var replies []response
			for index := range batch {
				if reply := s.dispatch(c, &batch[index]); reply != nil {
					replies = append(replies, *reply)
				}
			}
			if len(replies) == 0 {
				c.Status(http.StatusAccepted)
				return
			}
			c.JSON(http.StatusOK, replies)
			return
		}
		var message request
		if err := json.Unmarshal(body, &message); err != nil {
			c.JSON(http.StatusBadRequest, response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "invalid JSON-RPC message"}})
			return
		}
		reply := s.dispatch(c, &message)
		if reply == nil {
			c.Status(http.StatusAccepted)
			return
		}
		c.JSON(http.StatusOK, reply)
	}
}

// dispatch 处理一条消息，通知与客户端发来的响应返回 nil
func (s *server) dispatch(c *gin.Context, message *request) *response {
	if len(message.ID) == 0 || message.Method == "" {
		return nil
	}
	reply := &response{JSONRPC: "2.0", ID: message.ID}
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(message.Params, &params)
		version := protocolVersions[0]
		if slices.Contains(protocolVersions, params.ProtocolVersion) {
			version = params.ProtocolVersion
		}
		reply.Result = gin.H{
			"protocolVersion": version,
			"capabilities":    gin.H{"tools": gin.H{"listChanged": false}},
			"serverInfo":      gin.H{"name": "gemini-web2api", "title": "Gemini-Web2API", "version": s.version},
		}
	case "ping":
		reply.Result = gin.H{}
	case "tools/list":
		reply.Result = gin.H{"tools": s.tools()}
	case "tools/call":
		var params struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			reply.Error = &rpcError{Code: -32602, Message: "invalid tools/call params: " + err.Error()}
			break
		}
		tool, ok := toolByName(params.Name)
		if !ok {
			reply.Error = &rpcError{Code: -32602, Message: "unknown tool: " + params.Name}
			break
		}
		reply.Result = tool.call(s, c, arguments(params.Arguments))
	default:
		reply.Error = &rpcError{Code: -32601, Message: "method not found: " + message.Method}
	}
	return reply
}

// catalog 返回当前公开模型目录
func (s *server) catalog() []adapter.CatalogModel {
	return adapter.Catalog(s.pool)
}
