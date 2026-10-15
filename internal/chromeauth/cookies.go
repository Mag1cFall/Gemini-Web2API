package chromeauth

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
)

// parseCookies 解析 Cookie Header、浏览器扩展导出的 JSON 或 Netscape cookies.txt，只保留 google.com 及其子域的 Cookie
func parseCookies(input string) ([]auth.StateCookie, error) {
	text := strings.TrimSpace(strings.TrimPrefix(input, string(rune(0xFEFF))))
	var cookies []auth.StateCookie
	var err error
	switch {
	case strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{"):
		cookies, err = parseCookieJSON(text)
	case strings.Contains(text, "\t"):
		cookies, err = parseNetscapeCookies(text)
	default:
		cookies, err = parseCookieHeader(text)
	}
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(cookies))
	for _, cookie := range cookies {
		names[cookie.Name] = true
	}
	for _, required := range []string{"SAPISID", "__Secure-1PSID"} {
		if !names[required] {
			return nil, fmt.Errorf("Cookie 缺少核心 Cookie: %s", required)
		}
	}
	return cookies, nil
}

// googleDomain 报告 Cookie 域是否为 google.com 或其子域
func googleDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(domain, "."))
	return domain == "google.com" || strings.HasSuffix(domain, ".google.com")
}

// exportedCookie 是 Cookie-Editor 等扩展与 Playwright storage state 导出的 Cookie
type exportedCookie struct {
	Name           string  `json:"name"`
	Value          string  `json:"value"`
	Domain         string  `json:"domain"`
	Path           string  `json:"path"`
	Expires        float64 `json:"expires"`
	ExpirationDate float64 `json:"expirationDate"`
	HTTPOnly       bool    `json:"httpOnly"`
	Secure         bool    `json:"secure"`
	SameSite       string  `json:"sameSite"`
}

// parseCookieJSON 解析 Cookie 数组或带 cookies 字段的 storage state
func parseCookieJSON(text string) ([]auth.StateCookie, error) {
	var exported []exportedCookie
	if strings.HasPrefix(text, "{") {
		var state struct {
			Cookies []exportedCookie `json:"cookies"`
		}
		if err := json.Unmarshal([]byte(text), &state); err != nil {
			return nil, fmt.Errorf("Cookie JSON 无效: %w", err)
		}
		exported = state.Cookies
	} else if err := json.Unmarshal([]byte(text), &exported); err != nil {
		return nil, fmt.Errorf("Cookie JSON 无效: %w", err)
	}
	cookies := make([]auth.StateCookie, 0, len(exported))
	for _, item := range exported {
		if item.Name == "" || item.Value == "" || !googleDomain(item.Domain) {
			continue
		}
		expires := item.Expires
		if expires == 0 {
			expires = item.ExpirationDate
		}
		path := item.Path
		if path == "" {
			path = "/"
		}
		cookies = append(cookies, auth.StateCookie{
			Name: item.Name, Value: item.Value, Domain: item.Domain, Path: path, Expires: expires,
			HTTPOnly: item.HTTPOnly, Secure: item.Secure, SameSite: sameSiteName(item.SameSite),
		})
	}
	return cookies, nil
}

// sameSiteName 把扩展导出的 SameSite 取值转为 storage state 写法
func sameSiteName(value string) string {
	switch strings.ToLower(value) {
	case "no_restriction", "none":
		return "None"
	case "lax":
		return "Lax"
	case "strict":
		return "Strict"
	}
	return ""
}

// parseNetscapeCookies 解析 Netscape cookies.txt，#HttpOnly_ 前缀表示 HttpOnly
func parseNetscapeCookies(text string) ([]auth.StateCookie, error) {
	var cookies []auth.StateCookie
	for number, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		httpOnly := strings.HasPrefix(line, "#HttpOnly_")
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			return nil, fmt.Errorf("cookies.txt 第 %d 行不是 7 个制表符分隔的字段", number+1)
		}
		if !googleDomain(fields[0]) || fields[6] == "" {
			continue
		}
		expires, _ := strconv.ParseFloat(fields[4], 64)
		cookies = append(cookies, auth.StateCookie{
			Name: fields[5], Value: fields[6], Domain: fields[0], Path: fields[2], Expires: expires,
			HTTPOnly: httpOnly, Secure: strings.EqualFold(fields[3], "TRUE"),
		})
	}
	return cookies, nil
}
