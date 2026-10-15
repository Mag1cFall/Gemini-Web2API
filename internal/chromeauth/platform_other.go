//go:build !windows

package chromeauth

import "fmt"

// DefaultChromeRoot 返回本机 Chrome User Data 目录
func DefaultChromeRoot() (string, error) {
	return "", fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func ensurePlatformImport() error {
	return fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func discoverPlatform(string) ([]Account, error) {
	return nil, fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func readTokenService(string, string, string) (string, []byte, []byte, error) {
	return "", nil, nil, fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}

func readProfileLanguage(string, string) (string, error) {
	return "", fmt.Errorf("Chrome OAuth 导入仅支持 Windows")
}
