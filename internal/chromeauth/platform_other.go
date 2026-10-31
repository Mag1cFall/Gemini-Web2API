//go:build !windows

package chromeauth

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
)

// DefaultChromeRoot 返回本机 Chrome User Data 目录
func DefaultChromeRoot() (string, error) {
	return "", fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

// DefaultBraveRoot 返回本机 Brave User Data 目录
func DefaultBraveRoot() (string, error) {
	return "", fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

// DefaultEdgeRoot 返回本机 Edge User Data 目录
func DefaultEdgeRoot() (string, error) {
	return "", fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

// DefaultFirefoxRoot 返回本机 Firefox 目录
func DefaultFirefoxRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Firefox"), nil
	}
	return filepath.Join(home, ".mozilla", "firefox"), nil
}

// DefaultBrowserRoot 根据浏览器类型返回默认根目录
func DefaultBrowserRoot(browser string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(browser)) {
	case BrowserChrome, "":
		return DefaultChromeRoot()
	case BrowserBrave:
		return DefaultBraveRoot()
	case BrowserEdge:
		return DefaultEdgeRoot()
	case BrowserFirefox:
		return DefaultFirefoxRoot()
	default:
		return "", fmt.Errorf("不支持的浏览器: %s", browser)
	}
}

func ensurePlatformImport() error {
	return fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func discoverPlatform(string) ([]Account, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func discoverPlatformBrowser(browser string, root string) ([]Account, error) {
	if strings.ToLower(strings.TrimSpace(browser)) == BrowserFirefox {
		if root == "" {
			var err error
			root, err = DefaultFirefoxRoot()
			if err != nil {
				return nil, err
			}
		}
		return discoverFirefox(root)
	}
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func retrieveMasterKey(string, string) ([]byte, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func decryptDPAPIMasterKey(string) ([]byte, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func readChromiumCookies(string, string, string, []byte) ([]auth.StateCookie, error) {
	return nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func isSharingViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "resource temporarily unavailable") || strings.Contains(msg, "busy") || strings.Contains(msg, "locked")
}

func readTokenService(string, string, string) (string, []byte, []byte, error) {
	return "", nil, nil, fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

func readProfileLanguage(string, string) (string, error) {
	return "", fmt.Errorf("Chromium 浏览器导入仅支持 Windows")
}

