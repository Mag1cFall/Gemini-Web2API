package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// endpointRotateCookies 是轮换页脚本调用的 Cookie 轮换接口
	endpointRotateCookies = "https://accounts.google.com/RotateCookies"
	// endpointRotateCookiesPage 是官网首页嵌入的 Cookie 轮换页
	endpointRotateCookiesPage = "https://accounts.google.com/RotateCookiesPage?og_pid=658&rot=3&origin=https%3A%2F%2Fgemini.google.com&exp_id=0"
	// defaultRotationInterval 是取得轮换页参数前使用的轮换间隔
	defaultRotationInterval = 600 * time.Second
	// minRotationInterval 是轮换页接受的最短轮换间隔
	minRotationInterval = 60 * time.Second
	// endpointListAccounts 是 Chrome 浏览器进程读取 Cookie 中账号登录态的接口
	endpointListAccounts = "https://accounts.google.com/ListAccounts?gpsia=1&source=ChromiumBrowser&json=standard&laf=b64bin"
)

// rotationInitPattern 匹配轮换页脚本的 init(会话值, og_pid, 字段 3, 字段 4, 间隔秒数)
var rotationInitPattern = regexp.MustCompile(`init\('(-?\d+)',\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*\)`)

// rotationPageHeaderOrder 是 Chrome 稳定版加载轮换页 iframe 的请求头顺序
var rotationPageHeaderOrder = []string{
	"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-full-version", "sec-ch-ua-arch",
	"sec-ch-ua-platform", "sec-ch-ua-platform-version", "sec-ch-ua-model", "sec-ch-ua-bitness",
	"sec-ch-ua-wow64", "sec-ch-ua-full-version-list", "sec-ch-ua-form-factors",
	"upgrade-insecure-requests", "user-agent", "accept",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// rotateHeaderOrder 是 Chrome 稳定版轮换页 RotateCookies 请求的请求头顺序
var rotateHeaderOrder = []string{
	"content-length", "pragma", "cache-control", "sec-ch-ua-full-version-list", "sec-ch-ua-platform",
	"sec-ch-ua", "sec-ch-ua-bitness", "sec-ch-ua-model", "sec-ch-ua-mobile", "sec-ch-ua-form-factors",
	"sec-ch-ua-wow64", "sec-ch-ua-arch", "sec-ch-ua-full-version", "user-agent", "content-type",
	"sec-ch-ua-platform-version", "accept", "origin",
	"x-browser-channel", "x-browser-year", "x-browser-validation", "x-browser-copyright",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority",
}

// listAccountsHeaderOrder 是 Chrome 稳定版浏览器进程 ListAccounts 请求的请求头顺序
var listAccountsHeaderOrder = []string{
	"content-length", "origin", "content-type", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest",
	"user-agent", "accept-encoding", "accept-language", "cookie", "priority",
}

// cookieRotation 是轮换页 init 给出的 RotateCookies 正文与当前轮换间隔
type cookieRotation struct {
	body     string
	interval time.Duration
}

// CookieRotator 以官网轮换页的方式为一个账号轮换 Cookie，同一时间只由一个调用方使用
type CookieRotator struct {
	client   *Client
	rotation *cookieRotation
	// rotated 表示已轮换过，第一轮紧接启动或恢复时的初始化，会话有效时不重读账号状态
	rotated bool
}

// NewCookieRotator 创建账号的 Cookie 轮换器
func NewCookieRotator(client *Client) *CookieRotator {
	return &CookieRotator{client: client}
}

// Rotate 轮换 __Secure-1PSIDTS 并经 ListAccounts 确认会话有效，释放请求槽后重读账号状态，返回下次轮换前的等待时间；会话失效且账号带续签材料时在请求槽内续签并重读
func (r *CookieRotator) Rotate(ctx context.Context) (time.Duration, error) {
	reload, err := r.rotateSession(ctx)
	if err == nil && reload {
		err = r.client.reload(ctx)
	}
	return r.interval(), err
}

// rotateSession 在账号请求槽内轮换 Cookie 并确认会话，会话失效时续签并重读，返回会话有效时是否还需重读账号状态
func (r *CookieRotator) rotateSession(ctx context.Context) (bool, error) {
	c := r.client
	release, err := c.AcquireRequest(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	first := !r.rotated
	r.rotated = true
	err = r.rotate(ctx)
	if err == nil {
		listed, signedIn, err := c.listAccountsSignedIn(ctx)
		if err != nil {
			return false, err
		}
		if signedIn {
			return !first, nil
		}
		if !listed {
			// 没有 accounts.google.com Cookie 的纯 Cookie 导入账号，不主动触发 reload 避免引爆 302
			if c.refresh == nil {
				return false, nil
			}
			return !first, nil
		}
		if c.refresh == nil {
			return false, retryableProtocolError(ErrSignedOut.Error(), ErrSignedOut)
		}
	} else if !isAuthenticationError(err) || c.refresh == nil {
		return false, err
	}
	if err := c.refreshCookies(ctx); err != nil {
		return false, err
	}
	r.rotation = nil
	return false, c.reload(ctx)
}

// reload 重读首页、模型目录、偏好与用量，让目录、偏好与上下文窗口跟上账号变化，失败时保留原有 bootstrap
func (c *Client) reload(ctx context.Context) error {
	if err := c.init(ctx, false); err != nil {
		return err
	}
	_, err := c.FetchUsage(ctx)
	return err
}

// interval 返回当前轮换间隔，尚未取得轮换页参数时为默认间隔
func (r *CookieRotator) interval() time.Duration {
	if r.rotation == nil {
		return defaultRotationInterval
	}
	return r.rotation.interval
}

// rotate 在首次轮换或续签后先加载轮换页，再发送 RotateCookies 并按响应更新间隔
func (r *CookieRotator) rotate(ctx context.Context) error {
	c := r.client
	if r.rotation == nil {
		rotation, err := c.loadRotationPage(ctx)
		if err != nil {
			return err
		}
		r.rotation = &rotation
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointRotateCookies, strings.NewReader(r.rotation.body))
	if err != nil {
		return err
	}
	applyChromeHeaders(req, c.language, true)
	req.Header.Set("pragma", "no-cache")
	req.Header.Set("cache-control", "no-cache")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "*/*")
	req.Header.Set("origin", "https://accounts.google.com")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("sec-fetch-mode", "same-origin")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("referer", endpointRotateCookiesPage)
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("priority", "u=1, i")
	req.Header[http.HeaderOrderKey] = rotateHeaderOrder
	req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder
	body, err := c.doRotationRequest(req, "rotate cookies")
	if err != nil {
		return err
	}
	if interval, ok := rotationInterval(body); ok {
		r.rotation.interval = interval
	}
	return nil
}

// loadRotationPage 以 iframe 导航加载轮换页，读取 init 参数
func (c *Client) loadRotationPage(ctx context.Context) (cookieRotation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointRotateCookiesPage, nil)
	if err != nil {
		return cookieRotation{}, err
	}
	applyChromeHeaders(req, c.language, true)
	req.Header.Set("upgrade-insecure-requests", "1")
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("sec-fetch-site", "same-site")
	req.Header.Set("sec-fetch-mode", "navigate")
	req.Header.Set("sec-fetch-dest", "iframe")
	req.Header.Set("referer", "https://gemini.google.com/")
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("priority", "u=0, i")
	req.Header[http.HeaderOrderKey] = rotationPageHeaderOrder
	req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder
	body, err := c.doRotationRequest(req, "rotate cookies page")
	if err != nil {
		return cookieRotation{}, err
	}
	match := rotationInitPattern.FindSubmatch(body)
	if match == nil {
		return cookieRotation{}, retryableProtocolError("rotate cookies page has no init parameters", nil)
	}
	fields := make([]any, 4)
	fields[1] = string(match[1])
	for index, slot := range []int{0, 2, 3} {
		value, _ := strconv.ParseFloat(string(match[index+2]), 64)
		if value != 0 {
			fields[slot] = int64(value)
		}
	}
	for len(fields) > 0 && fields[len(fields)-1] == nil {
		fields = fields[:len(fields)-1]
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return cookieRotation{}, err
	}
	seconds, _ := strconv.ParseFloat(string(match[5]), 64)
	return cookieRotation{body: string(encoded), interval: max(time.Duration(seconds)*time.Second, minRotationInterval)}, nil
}

// doRotationRequest 发送轮换或 ListAccounts 请求、吸收响应 Cookie，返回 HTTP 200 的正文
func (c *Client) doRotationRequest(req *http.Request, operation string) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportProtocolError(operation, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, transportProtocolError("read "+operation+" response", err)
	}
	if err := c.absorbResponseCookies(req.URL, resp); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, responseStatusError(resp, operation)
	}
	return body, nil
}

// listAccountsSignedIn 以 Chrome 浏览器进程的方式请求 ListAccounts，返回是否列出账号与 Cookie 中目标账号的会话是否有效
func (c *Client) listAccountsSignedIn(ctx context.Context) (bool, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointListAccounts, strings.NewReader(" "))
	if err != nil {
		return false, false, err
	}
	req.Header.Set("origin", "https://www.google.com")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("sec-fetch-site", "none")
	req.Header.Set("sec-fetch-mode", "no-cors")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("user-agent", ChromeUserAgent)
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("accept-language", c.language)
	req.Header.Set("priority", "u=4, i")
	req.Header[http.HeaderOrderKey] = listAccountsHeaderOrder
	req.Header[http.PHeaderOrderKey] = pseudoHeaderOrder
	body, err := c.doRotationRequest(req, "list accounts")
	if err != nil {
		return false, false, err
	}
	return parseListAccounts(body, c.authUser)
}

// parseListAccounts 解析 b64bin 格式的 ListAccounts 响应，返回是否列出账号与第 index 个账号的会话是否有效：9 号字段为 1 且 14 号字段不为 1 时有效
func parseListAccounts(body []byte, index int) (bool, bool, error) {
	data, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(body)))
	if err != nil {
		return false, false, fmt.Errorf("decode list accounts response: %w", err)
	}
	var account []byte
	for position := 0; len(data) > 0; {
		number, kind, size := protowire.ConsumeTag(data)
		if size < 0 {
			return false, false, fmt.Errorf("decode list accounts response: %w", protowire.ParseError(size))
		}
		data = data[size:]
		size = protowire.ConsumeFieldValue(number, kind, data)
		if size < 0 {
			return false, false, fmt.Errorf("decode list accounts response: %w", protowire.ParseError(size))
		}
		if number == 1 && kind == protowire.BytesType {
			if position == index {
				account, _ = protowire.ConsumeBytes(data)
				break
			}
			position++
		}
		data = data[size:]
	}
	if account == nil {
		return false, false, nil
	}
	fields, err := protoFields(account)
	if err != nil {
		return false, false, fmt.Errorf("decode list accounts account: %w", err)
	}
	return true, fields[9] == uint64(1) && fields[14] != uint64(1), nil
}

// protoFields 返回 protobuf 消息每个字段号的首个值，变长整数为 uint64，长度分隔字段为原始字节
func protoFields(data []byte) (map[protowire.Number]any, error) {
	fields := map[protowire.Number]any{}
	for len(data) > 0 {
		number, kind, size := protowire.ConsumeTag(data)
		if size < 0 {
			return nil, protowire.ParseError(size)
		}
		data = data[size:]
		size = protowire.ConsumeFieldValue(number, kind, data)
		if size < 0 {
			return nil, protowire.ParseError(size)
		}
		if _, seen := fields[number]; !seen {
			switch kind {
			case protowire.VarintType:
				fields[number], _ = protowire.ConsumeVarint(data)
			case protowire.BytesType:
				fields[number], _ = protowire.ConsumeBytes(data)
			}
		}
		data = data[size:]
	}
	return fields, nil
}

// rotationInterval 读取 RotateCookies 响应中的 identity.hfcr 秒数
func rotationInterval(body []byte) (time.Duration, bool) {
	body = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(body), []byte(")]}'")))
	var entries [][]any
	if err := json.Unmarshal(body, &entries); err != nil {
		return 0, false
	}
	for _, entry := range entries {
		if len(entry) == 2 && entry[0] == "identity.hfcr" {
			if seconds, ok := entry[1].(float64); ok {
				return max(time.Duration(seconds)*time.Second, minRotationInterval), true
			}
		}
	}
	return 0, false
}
