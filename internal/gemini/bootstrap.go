package gemini

import (
	"fmt"
	"regexp"
)

// Bootstrap 表示首页和模型目录共同产生的运行参数
type Bootstrap struct {
	SNlM0e  string
	BL      string
	FSID    string
	Catalog ModelCatalog
}

var (
	snlM0ePattern = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	blPattern     = regexp.MustCompile(`"bl":"([^"]+)"`)
	dataBLPattern = regexp.MustCompile(`data-bl="([^"]+)"`)
	buildPattern  = regexp.MustCompile(`(boq_assistant-bard-web-server_[a-zA-Z0-9._-]+)`)
	fdrFJePattern = regexp.MustCompile(`"FdrFJe":"([^"]+)"`)
	fsidPattern   = regexp.MustCompile(`"f\.sid":"([^"]+)"`)
)

// parseBootstrap 从当前首页提取所有必需动态参数
func parseBootstrap(html string) (Bootstrap, error) {
	bootstrap := Bootstrap{
		SNlM0e: firstMatch(html, snlM0ePattern),
		BL:     firstMatch(html, blPattern, dataBLPattern, buildPattern),
		FSID:   firstMatch(html, fdrFJePattern, fsidPattern),
	}
	if bootstrap.SNlM0e == "" {
		return Bootstrap{}, fmt.Errorf("bootstrap is missing SNlM0e")
	}
	if bootstrap.BL == "" {
		return Bootstrap{}, fmt.Errorf("bootstrap is missing bl")
	}
	if bootstrap.FSID == "" {
		return Bootstrap{}, fmt.Errorf("bootstrap is missing FdrFJe/f.sid")
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
