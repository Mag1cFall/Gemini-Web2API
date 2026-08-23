package chromeauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

const (
	userAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	maxCookieAge    = 400 * 24 * time.Hour
	storageFileName = "storage-state.json"
)

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// Account 描述本机 Chrome 中可发现的 Google 账号
type Account struct {
	Profile     string `json:"profile"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Importable  bool   `json:"importable"`
}

// ImportOptions 保存批量导入参数
type ImportOptions struct {
	ChromeRoot string
	Output     string
	Proxy      string
	Profiles   []string
	Emails     []string
}

// ImportResult 描述单个账号导入结果
type ImportResult struct {
	Profile     string `json:"profile"`
	Email       string `json:"email"`
	Imported    bool   `json:"imported"`
	CookieCount int    `json:"cookieCount"`
	Path        string `json:"path"`
	CompletedAt string `json:"completedAt"`
}

// Discover 只读列出本机 Chrome Google 账号
func Discover(chromeRoot string) ([]Account, error) {
	return discoverPlatform(chromeRoot)
}

// Import 通过设备绑定 OAuth 认证材料生成协议账号状态
func Import(ctx context.Context, options ImportOptions) ([]ImportResult, error) {
	if err := ensurePlatformImport(); err != nil {
		return nil, err
	}
	proxyURL, err := validateProxy(options.Proxy)
	if err != nil {
		return nil, err
	}
	accounts, err := Discover(options.ChromeRoot)
	if err != nil {
		return nil, err
	}
	selected, err := selectAccounts(accounts, options.Profiles, options.Emails)
	if err != nil {
		return nil, err
	}
	masterKey, err := retrieveV20Key(options.ChromeRoot)
	if err != nil {
		return nil, err
	}
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("Chrome v20 主密钥长度异常")
	}

	results := make([]ImportResult, 0, len(selected))
	for _, account := range selected {
		result, err := importAccount(ctx, account, options, proxyURL, masterKey)
		if err != nil {
			return nil, fmt.Errorf("导入 %s: %w", account.Profile, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func verifyImported(ctx context.Context, results []ImportResult) (int, error) {
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, result.Path)
	}
	accounts, err := auth.LoadFiles(paths, "")
	if err != nil {
		return 0, fmt.Errorf("加载新认证状态: %w", err)
	}
	models := make(map[string]struct{})
	for _, account := range accounts {
		client, err := gemini.NewClient(account.Source, account.ProxyURL, false)
		if err != nil {
			return 0, fmt.Errorf("验证账号 %s: %w", account.ID, err)
		}
		accountContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = client.Init(accountContext)
		cancel()
		if err != nil {
			return 0, fmt.Errorf("验证账号 %s: %w", account.ID, err)
		}
		for _, model := range client.Models() {
			models[model.ID] = struct{}{}
		}
	}
	return len(models), nil
}

func selectAccounts(accounts []Account, profiles []string, emails []string) ([]Account, error) {
	requestedProfiles := normalizedSet(profiles)
	requestedEmails := normalizedSet(emails)
	selected := make([]Account, 0, len(accounts))
	foundProfiles := make(map[string]struct{})
	foundEmails := make(map[string]struct{})
	for _, account := range accounts {
		profile := strings.ToLower(strings.TrimSpace(account.Profile))
		email := strings.ToLower(strings.TrimSpace(account.Email))
		_, profileMatch := requestedProfiles[profile]
		_, emailMatch := requestedEmails[email]
		if !profileMatch && !emailMatch {
			continue
		}
		if !account.Importable {
			return nil, fmt.Errorf("%s 缺少可导入的 OAuth 认证材料", account.Profile)
		}
		if !strings.Contains(account.Email, "@") {
			return nil, fmt.Errorf("%s 缺少账号邮箱", account.Profile)
		}
		selected = append(selected, account)
		foundProfiles[profile] = struct{}{}
		foundEmails[email] = struct{}{}
	}
	if missing := missingValues(requestedProfiles, foundProfiles); len(missing) != 0 {
		return nil, fmt.Errorf("找不到 Chrome Profile: %s", strings.Join(missing, ", "))
	}
	if missing := missingValues(requestedEmails, foundEmails); len(missing) != 0 {
		return nil, fmt.Errorf("找不到 Chrome 账号: %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func normalizedSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func missingValues(requested map[string]struct{}, found map[string]struct{}) []string {
	missing := make([]string, 0)
	for value := range requested {
		if _, ok := found[value]; !ok {
			missing = append(missing, value)
		}
	}
	sort.Strings(missing)
	return missing
}

func validateProxy(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("proxy 必须是 http、https 或 socks5 URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
		return value, nil
	default:
		return "", fmt.Errorf("proxy 必须是 http、https 或 socks5 URL")
	}
}

func decryptV20Token(masterKey []byte, encrypted []byte) (string, error) {
	if len(encrypted) < 3+12+16 || string(encrypted[:3]) != "v20" {
		return "", fmt.Errorf("refresh token 密文版本不是 v20")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", fmt.Errorf("创建 AES 解密器: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 解密器: %w", err)
	}
	plaintext, err := gcm.Open(nil, encrypted[3:15], encrypted[15:], nil)
	if err != nil {
		return "", fmt.Errorf("refresh token 解密失败")
	}
	token := string(plaintext)
	if len(token) != 103 || !strings.HasPrefix(token, "1//0") {
		return "", fmt.Errorf("refresh token 解密结果格式异常")
	}
	return token, nil
}

func importAccount(ctx context.Context, account Account, options ImportOptions, proxyURL string, masterKey []byte) (ImportResult, error) {
	gaiaID, encryptedToken, wrappedKey, err := readTokenService(options.ChromeRoot, account.Profile)
	if err != nil {
		return ImportResult{}, err
	}
	token, err := decryptV20Token(masterKey, encryptedToken)
	if err != nil {
		return ImportResult{}, err
	}
	cookies, err := fetchGoogleCookies(ctx, gaiaID, token, wrappedKey, proxyURL)
	if err != nil {
		return ImportResult{}, err
	}
	language, err := readProfileLanguage(options.ChromeRoot, account.Profile)
	if err != nil {
		return ImportResult{}, err
	}

	email := strings.ToLower(strings.TrimSpace(account.Email))
	path := filepath.Join(options.Output, accountSlug(email), storageFileName)
	state := auth.StorageState{
		Cookies: toStorageCookies(cookies),
		Origins: []json.RawMessage{},
		Metadata: auth.Metadata{
			Version: 1,
			ID:      accountSlug(email),
			Proxy:   proxyURL,
			Source:  auth.ImportSource{Browser: "chrome", Profile: account.Profile},
			Fingerprint: gemini.Fingerprint{
				Browser: "Chrome", Version: "146", Platform: "Windows",
				UserAgent: userAgent, Language: language, TLSProfile: "chrome_146",
			},
		},
	}
	file, err := auth.New(path, state)
	if err != nil {
		return ImportResult{}, err
	}
	if err := file.Write(); err != nil {
		return ImportResult{}, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("解析认证状态路径: %w", err)
	}
	return ImportResult{
		Profile: account.Profile, Email: email, Imported: true,
		CookieCount: len(cookies), Path: absPath, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func accountSlug(email string) string {
	return strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(email)), "-"), "-")
}

func toStorageCookies(cookies []multiloginCookie) []auth.StateCookie {
	now := time.Now()
	result := make([]auth.StateCookie, 0, len(cookies))
	for _, cookie := range cookies {
		domain := cookie.Domain
		if domain == "" && cookie.Host != "" && !strings.HasPrefix(cookie.Host, ".") {
			domain = cookie.Host
		}
		lifetime := time.Duration(0)
		if cookie.MaxAge != nil {
			lifetime = time.Duration(*cookie.MaxAge * float64(time.Second))
			if lifetime > maxCookieAge {
				lifetime = maxCookieAge
			}
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		sameSite := strings.ToLower(cookie.SameSite)
		if sameSite != "none" && sameSite != "lax" && sameSite != "strict" {
			sameSite = ""
		} else {
			sameSite = strings.ToUpper(sameSite[:1]) + sameSite[1:]
		}
		result = append(result, auth.StateCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
			Expires:  float64(now.Add(lifetime).UnixNano()) / 1e9,
			HTTPOnly: cookie.IsHTTPOnly, Secure: cookie.IsSecure, SameSite: sameSite,
		})
	}
	return result
}
