package chromeauth

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
)

// parseCookies 解析 Cookie Header、浏览器扩展导出的 JSON、Netscape cookies.txt 或开发者工具 Cookie 表格，只保留 google.com 及其子域的 Cookie
func parseCookies(input string) ([]auth.StateCookie, error) {
	text := strings.TrimSpace(strings.TrimPrefix(input, string(rune(0xFEFF))))
	var cookies []auth.StateCookie
	var err error
	switch {
	case strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{"):
		cookies, err = parseCookieJSON(text)
	case strings.Contains(text, "\t"):
		cookies, err = parseCookieTable(text)
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

// parseCookieHeader 解析 Cookie Header，分号与换行都分隔 Cookie，跳过空项与空值项；OSID、__Secure-OSID 与 __Host- 前缀的 Cookie 归 gemini.google.com，其余归 .google.com
func parseCookieHeader(header string) ([]auth.StateCookie, error) {
	header = strings.TrimSpace(strings.Trim(strings.TrimSpace(header), `"'`))
	if len(header) >= len("Cookie:") && strings.EqualFold(header[:len("Cookie:")], "Cookie:") {
		header = strings.TrimSpace(header[len("Cookie:"):])
	}
	cookies := make([]auth.StateCookie, 0)
	names := make(map[string]struct{})
	for _, item := range strings.FieldsFunc(header, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("Cookie Header 项 %q 格式无效", item)
		}
		name := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if value == "" {
			continue
		}
		if _, exists := names[name]; exists {
			continue
		}
		names[name] = struct{}{}
		domain := ".google.com"
		if strings.HasPrefix(name, "__Host-") || name == "OSID" || name == "__Secure-OSID" {
			domain = "gemini.google.com"
		}
		cookies = append(cookies, auth.StateCookie{
			Name: name, Value: value, Domain: domain, Path: "/", Secure: true,
		})
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

// parseCookieTable 解析制表符分隔的 Netscape cookies.txt 与从开发者工具 Cookie 表格复制的行，#HttpOnly_ 前缀表示 HttpOnly
func parseCookieTable(text string) ([]auth.StateCookie, error) {
	var cookies []auth.StateCookie
	for number, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		httpOnly := strings.HasPrefix(line, "#HttpOnly_")
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		var cookie auth.StateCookie
		switch {
		case len(fields) == 7 && (fields[1] == "TRUE" || fields[1] == "FALSE"):
			expires, _ := strconv.ParseFloat(fields[4], 64)
			cookie = auth.StateCookie{
				Name: fields[5], Value: fields[6], Domain: fields[0], Path: fields[2], Expires: expires,
				HTTPOnly: httpOnly, Secure: strings.EqualFold(fields[3], "TRUE"),
			}
		case len(fields) >= 4:
			// 开发者工具的列依次为名称、值、域、路径、到期时间、大小、HttpOnly、Secure，勾选显示为 ✓
			cookie = auth.StateCookie{Name: fields[0], Value: fields[1], Domain: fields[2], Path: fields[3]}
			if len(fields) > 4 {
				if expires, err := time.Parse(time.RFC3339, fields[4]); err == nil {
					cookie.Expires = float64(expires.Unix())
				}
			}
			cookie.HTTPOnly = len(fields) > 6 && strings.TrimSpace(fields[6]) == "✓"
			cookie.Secure = len(fields) > 7 && strings.TrimSpace(fields[7]) == "✓"
		default:
			return nil, fmt.Errorf("Cookie 表格第 %d 行无法识别，应为 cookies.txt 的 7 列或开发者工具 Cookie 表格的行", number+1)
		}
		if !googleDomain(cookie.Domain) || cookie.Value == "" {
			continue
		}
		cookies = append(cookies, cookie)
	}
	return cookies, nil
}
