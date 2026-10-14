package gemini

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

func (c *Client) replaceCookies(cookies []Cookie) error {
	jar := newCookieJar()
	next := make(map[string]Cookie, len(cookies))
	for _, cookie := range cookies {
		cookie = normalizeCookie(cookie, "gemini.google.com")
		if cookie.Name == "" {
			return fmt.Errorf("cookie name is empty")
		}
		target, err := cookieURL(cookie)
		if err != nil {
			return err
		}
		next[cookieKey(cookie)] = cookie
		jar.SetCookies(target, []*http.Cookie{toHTTPCookie(cookie)})
	}
	c.cookieMu.Lock()
	c.cookies = next
	c.httpClient.SetCookieJar(jar)
	c.cookieMu.Unlock()
	return nil
}

func (c *Client) absorbResponseCookies(requestURL *url.URL, resp *http.Response) error {
	setCookies := resp.Cookies()
	if len(setCookies) == 0 {
		return nil
	}
	c.cookieMu.Lock()
	defer c.cookieMu.Unlock()
	now := time.Now()
	changed := false
	for _, source := range setCookies {
		cookie := Cookie{
			Name:     source.Name,
			Value:    source.Value,
			Domain:   cookieDomain(source.Domain),
			Path:     source.Path,
			HTTPOnly: source.HttpOnly,
			Secure:   source.Secure,
			SameSite: sameSiteString(source.SameSite),
		}
		if !source.Expires.IsZero() {
			cookie.Expires = source.Expires.Unix()
		} else {
			cookie.Expires = -1
		}
		cookie = normalizeCookie(cookie, requestURL.Hostname())
		key := cookieKey(cookie)
		existing, exists := c.cookies[key]
		if source.MaxAge < 0 || cookie.Expires > 0 && cookie.Expires <= now.Unix() {
			if exists {
				delete(c.cookies, key)
				changed = true
			}
			continue
		}
		if exists && existing == cookie {
			continue
		}
		c.cookies[key] = cookie
		changed = true
	}
	if !changed || c.save == nil {
		return nil
	}
	if err := c.save(c.cookieSnapshotLocked()); err != nil {
		return fmt.Errorf("persist account cookies: %w", err)
	}
	return nil
}

func (c *Client) cookieSnapshotLocked() []Cookie {
	result := make([]Cookie, 0, len(c.cookies))
	for _, cookie := range c.cookies {
		result = append(result, cookie)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Domain != result[right].Domain {
			return result[left].Domain < result[right].Domain
		}
		if result[left].Path != result[right].Path {
			return result[left].Path < result[right].Path
		}
		return result[left].Name < result[right].Name
	})
	return result
}

func normalizeCookie(cookie Cookie, requestHost string) Cookie {
	cookie.Domain = strings.ToLower(strings.TrimSpace(cookie.Domain))
	if cookie.Domain == "" {
		cookie.Domain = strings.ToLower(requestHost)
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	return cookie
}

func cookieKey(cookie Cookie) string {
	return cookie.Domain + "\x00" + cookie.Path + "\x00" + cookie.Name
}

func cookieURL(cookie Cookie) (*url.URL, error) {
	host := strings.TrimPrefix(cookie.Domain, ".")
	if host == "" {
		return nil, fmt.Errorf("cookie %q has no domain", cookie.Name)
	}
	return url.Parse("https://" + host + "/")
}

// cookieDomain 把 Set-Cookie 的 Domain 属性写成带前导点的域 Cookie，没有该属性时保持为空并按 host-only 处理
func cookieDomain(attribute string) string {
	attribute = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(attribute)), ".")
	if attribute == "" {
		return ""
	}
	return "." + attribute
}

// toHTTPCookie 转换为 jar 使用的 Cookie，只有带前导点的域 Cookie 写入 Domain 属性
func toHTTPCookie(cookie Cookie) *http.Cookie {
	domain := ""
	if strings.HasPrefix(cookie.Domain, ".") {
		domain = cookie.Domain
	}
	result := &http.Cookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Domain:   domain,
		Path:     cookie.Path,
		Secure:   cookie.Secure,
		HttpOnly: cookie.HTTPOnly,
		SameSite: parseSameSite(cookie.SameSite),
	}
	if cookie.Expires > 0 {
		result.Expires = time.Unix(cookie.Expires, 0)
	}
	return result
}

func parseSameSite(value string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteDefaultMode
	}
}

func sameSiteString(value http.SameSite) string {
	switch value {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return ""
	}
}
