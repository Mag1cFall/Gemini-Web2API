package gemini

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Bootstrap 表示首页和模型目录共同产生的运行参数
type Bootstrap struct {
	// SNlM0e 为登录会话的表单令牌，访客会话为空
	SNlM0e string
	BL     string
	FSID   string
	// Language 为首页 TuX5cc 给出的账号界面语言，用于 hl 与生成载荷语言槽
	Language string
	// Email 为首页 oPEP7c 给出的当前登录邮箱
	Email string
	// UploadURL、UploadPushID、UploadClientContext 为首页 h1eoVe、qKIAYe、Ylro7b 给出的附件上传端点与请求头
	UploadURL           string
	UploadPushID        string
	UploadClientContext string
	// UploadTermsWindow 为首页实验 45725082 给出的上传条款确认有效期
	UploadTermsWindow time.Duration
	// TermsToken 为首页 thykhd 给出的服务条款同意令牌
	TermsToken string
	// APIKey、ServerToken 为首页 d2zJAe、PI9WOb 给出的生成会话 API key 与服务端令牌
	APIKey      string
	ServerToken string
	Catalog     ModelCatalog
	Preferences Preferences
}

var (
	snlM0ePattern = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	blPattern     = regexp.MustCompile(`"cfb2h":"([^"]+)"`)
	fdrFJePattern = regexp.MustCompile(`"FdrFJe":"([^"]+)"`)
	fsidPattern   = regexp.MustCompile(`"f\.sid":"([^"]+)"`)
	localePattern = regexp.MustCompile(`"TuX5cc":"([^"]+)"`)
	emailPattern  = regexp.MustCompile(`"oPEP7c":"([^"]+)"`)
	uploadPattern = regexp.MustCompile(`"h1eoVe":"([^"]+)"`)
	pushIDPattern = regexp.MustCompile(`"qKIAYe":"([^"]+)"`)
	pctxPattern   = regexp.MustCompile(`"Ylro7b":"([^"]+)"`)
	thykhdPattern = regexp.MustCompile(`"thykhd":"([^"]+)"`)
	termsPattern  = regexp.MustCompile(`"45725082\\?":(\d+)`)
	apiKeyPattern = regexp.MustCompile(`"d2zJAe":"([^"]+)"`)
	tokenPattern  = regexp.MustCompile(`"PI9WOb":"([^"]+)"`)
)

// ErrSignedOut 表示账号会话已失效且未能续签，需要重新导入
var ErrSignedOut = errors.New("Gemini Web 会话已失效")

// errBootstrapSignedOut 表示首页缺少只签发给登录会话的 SNlM0e
var errBootstrapSignedOut = errors.New("Gemini 页面显示未登录")

// parseBootstrap 从当前首页提取所有必需动态参数，访客首页没有 SNlM0e
func parseBootstrap(html string) (Bootstrap, error) {
	bootstrap := Bootstrap{
		SNlM0e:   firstMatch(html, snlM0ePattern),
		BL:       firstMatch(html, blPattern),
		FSID:     firstMatch(html, fdrFJePattern, fsidPattern),
		Language: firstMatch(html, localePattern),
		Email:    firstMatch(html, emailPattern),

		UploadURL:           firstMatch(html, uploadPattern),
		UploadPushID:        firstMatch(html, pushIDPattern),
		UploadClientContext: firstMatch(html, pctxPattern),
		TermsToken:          firstMatch(html, thykhdPattern),
		APIKey:              firstMatch(html, apiKeyPattern),
		ServerToken:         firstMatch(html, tokenPattern),
	}
	if seconds, err := strconv.ParseInt(firstMatch(html, termsPattern), 10, 64); err == nil {
		bootstrap.UploadTermsWindow = time.Duration(seconds) * time.Second
	}
	if bootstrap.BL == "" {
		return Bootstrap{}, fmt.Errorf("Gemini 页面缺少 cfb2h，官网页面可能已更新")
	}
	if bootstrap.FSID == "" {
		return Bootstrap{}, fmt.Errorf("Gemini 页面缺少 FdrFJe，官网页面可能已更新")
	}
	if bootstrap.Language == "" {
		return Bootstrap{}, fmt.Errorf("Gemini 页面缺少 TuX5cc，官网页面可能已更新")
	}
	if bootstrap.APIKey == "" {
		return Bootstrap{}, fmt.Errorf("Gemini 页面缺少 d2zJAe，官网页面可能已更新")
	}
	if bootstrap.ServerToken == "" {
		return Bootstrap{}, fmt.Errorf("Gemini 页面缺少 PI9WOb，官网页面可能已更新")
	}
	return bootstrap, nil
}

// jsEscapePattern 匹配首页脚本字符串字面量中的 \uXXXX 转义
var jsEscapePattern = regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)

// firstMatch 返回首个匹配到的首页脚本字符串，\uXXXX 转义还原为字符，与页面脚本读到的值相同
func firstMatch(value string, patterns ...*regexp.Regexp) string {
	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(value)
		if len(matches) > 1 {
			return jsEscapePattern.ReplaceAllStringFunc(matches[1], func(escape string) string {
				code, _ := strconv.ParseUint(escape[2:], 16, 16)
				return string(rune(code))
			})
		}
	}
	return ""
}
