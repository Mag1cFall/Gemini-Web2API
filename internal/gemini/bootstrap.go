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
	// UploadURL、UploadPushID、UploadClientContext 为首页 h1eoVe、qKIAYe、Ylro7b 给出的附件上传端点与请求头
	UploadURL           string
	UploadPushID        string
	UploadClientContext string
	// UploadTermsWindow 为首页实验 45725082 给出的上传条款确认有效期
	UploadTermsWindow time.Duration
	// TermsToken 为首页 thykhd 给出的服务条款同意令牌
	TermsToken  string
	Catalog     ModelCatalog
	Preferences Preferences
}

var (
	snlM0ePattern = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	blPattern     = regexp.MustCompile(`"cfb2h":"([^"]+)"`)
	fdrFJePattern = regexp.MustCompile(`"FdrFJe":"([^"]+)"`)
	fsidPattern   = regexp.MustCompile(`"f\.sid":"([^"]+)"`)
	localePattern = regexp.MustCompile(`"TuX5cc":"([^"]+)"`)
	uploadPattern = regexp.MustCompile(`"h1eoVe":"([^"]+)"`)
	pushIDPattern = regexp.MustCompile(`"qKIAYe":"([^"]+)"`)
	pctxPattern   = regexp.MustCompile(`"Ylro7b":"([^"]+)"`)
	thykhdPattern = regexp.MustCompile(`"thykhd":"([^"]+)"`)
	termsPattern  = regexp.MustCompile(`"45725082\\?":(\d+)`)
)

// ErrSignedOut 表示账号会话已失效且未能续签，需要重新导入
var ErrSignedOut = errors.New("Gemini Web 会话已失效")

// errBootstrapSignedOut 表示首页缺少只签发给登录会话的 SNlM0e
var errBootstrapSignedOut = errors.New("bootstrap is missing SNlM0e")

// parseBootstrap 从当前首页提取所有必需动态参数，访客首页没有 SNlM0e
func parseBootstrap(html string) (Bootstrap, error) {
	bootstrap := Bootstrap{
		SNlM0e:   firstMatch(html, snlM0ePattern),
		BL:       firstMatch(html, blPattern),
		FSID:     firstMatch(html, fdrFJePattern, fsidPattern),
		Language: firstMatch(html, localePattern),

		UploadURL:           firstMatch(html, uploadPattern),
		UploadPushID:        firstMatch(html, pushIDPattern),
		UploadClientContext: firstMatch(html, pctxPattern),
		TermsToken:          firstMatch(html, thykhdPattern),
	}
	if seconds, err := strconv.ParseInt(firstMatch(html, termsPattern), 10, 64); err == nil {
		bootstrap.UploadTermsWindow = time.Duration(seconds) * time.Second
	}
	if bootstrap.BL == "" {
		return Bootstrap{}, fmt.Errorf("bootstrap is missing cfb2h")
	}
	if bootstrap.FSID == "" {
		return Bootstrap{}, fmt.Errorf("bootstrap is missing FdrFJe/f.sid")
	}
	if bootstrap.Language == "" {
		return Bootstrap{}, fmt.Errorf("bootstrap is missing TuX5cc")
	}
	return bootstrap, nil
}

func firstMatch(value string, patterns ...*regexp.Regexp) string {
	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(value)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}
