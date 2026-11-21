//go:build !windows

package chromeauth

import "fmt"

// DefaultChromeRoot 返回本机 Chrome User Data 目录
func DefaultChromeRoot() (string, error) {
	return "", fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

// Discover 只读列出本机 Chrome Google 账号
func Discover(string) ([]Account, error) {
	return nil, fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func readProfileLanguage(string, string) (string, error) {
	return "", fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}
