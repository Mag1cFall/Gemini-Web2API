package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// TestParseFlags 验证常用启动参数可以覆盖环境配置
func TestParseFlags(t *testing.T) {
	cfg := config.Config{ListenAddress: "127.0.0.1:8007"}
	if err := parseFlags([]string{"--auth", "one.json,two.json", "--listen", "127.0.0.1:9000"}, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:9000" || len(cfg.AuthStatePaths) != 2 {
		t.Fatalf("config = %#v", cfg)
	}
}

// TestParseFlagsDefaultAuth 验证服务默认读取 auth 目录
func TestParseFlagsDefaultAuth(t *testing.T) {
	cfg := config.Config{ListenAddress: "127.0.0.1:8007"}
	if err := parseFlags(nil, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.AuthStatePaths) != 1 || cfg.AuthStatePaths[0] != "auth" {
		t.Fatalf("auth paths = %#v", cfg.AuthStatePaths)
	}
}

// TestHealthEndpoints 验证存活与就绪状态反映账号池
func TestHealthEndpoints(t *testing.T) {
	pool := balancer.NewAccountPool(time.Minute, time.Hour)
	router := newRouter(pool, "")

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}

	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty readiness status = %d", ready.Code)
	}

	pool.Add(&gemini.Client{}, "ready")
	ready = httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status = %d", ready.Code)
	}
}

// TestProtectedEndpointRequiresKey 验证业务接口使用启动时注入的密钥
func TestProtectedEndpointRequiresKey(t *testing.T) {
	pool := balancer.NewAccountPool(time.Minute, time.Hour)
	router := newRouter(pool, "secret")

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/accounts/health", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	authorized := httptest.NewRecorder()
	router.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
}
