package gemini

import (
	"context"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

// Cookie 表示可无损写回认证状态的 Cookie
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires"`
	HTTPOnly bool   `json:"http_only"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"same_site"`
}

// AccountSource 表示账号固定身份和 Cookie 持久化入口
type AccountSource struct {
	ID       string
	Cookies  []Cookie
	Language string
	// AuthUser 是目标账号在多账号 Cookie 中的序号，对应官网地址 /u/N，0 为默认账号
	AuthUser int
	// Chrome 是账号绑定的 Chrome 主版本，0 为版本池中最新的版本
	Chrome  int
	Save    func([]Cookie) error
	Refresh func(context.Context) ([]Cookie, error)
	// RotateBound 在设备绑定 Cookie 将到期时刷新设备绑定会话并返回距下次刷新的等待时间，cookies 返回账号当前的 Cookie，send 以账号的客户端发出请求并写回响应 Cookie
	RotateBound func(ctx context.Context, cookies func() []Cookie, send func(*http.Request) (int, http.Header, error)) (time.Duration, error)
	// Concurrency 是账号同时进行的上游请求数，零按 1 处理
	Concurrency int
}
