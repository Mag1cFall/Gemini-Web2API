package chromeauth

import (
	"cmp"
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
	"github.com/Mag1cFall/Gemini-Web2API/internal/proxypool"
)

const (
	defaultImportLanguage = "en-US,en;q=0.9"
	maxCookieAge          = 400 * 24 * time.Hour
	storageFileName       = "storage-state.json"
)

const (
	BrowserChrome  = "chrome"
	BrowserBrave   = "brave"
	BrowserEdge    = "edge"
	BrowserFirefox = "firefox"
)

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// Account 描述本机浏览器中可发现的 Google 账号，同一 Profile 登录的每个账号各占一项
type Account struct {
	Browser     string `json:"browser,omitempty"`
	Profile     string `json:"profile"`
	GaiaID      string `json:"-"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	AuthUser    int    `json:"authUser,omitempty"`
	Importable  bool   `json:"importable"`
	// Reason 是账号不可导入的原因，不可导入且为空时表示该账号未在浏览器登录
	Reason string `json:"reason,omitempty"`
	// SourceType 表示认证来源方式，oauth 为 OAuth Refresh Token，cookie 为浏览器 Cookie
	SourceType string `json:"sourceType,omitempty"`
}

const (
	// ReasonReadFailed 表示读取 Profile 的数据失败
	ReasonReadFailed = "read_failed"
	// ReasonNeedsReauth 表示 refresh token 不是 App-Bound 加密，需要在浏览器重新登录该账号
	ReasonNeedsReauth = "needs_reauth"
	// ReasonBrowserRunning 表示浏览器正在运行并锁定了 Cookie 数据库
	ReasonBrowserRunning = "browser_running"
)

// ImportOptions 保存批量导入参数
type ImportOptions struct {
	ChromeRoot string
	Output     string
	// Proxy 是写入账号的固定代理，RequestProxy 是没有固定代理时导入请求使用的代理
	Proxy        string
	RequestProxy string
	Profiles     []string
	Emails       []string
}

// BrowserImportOptions 保存按浏览器批量导入参数
type BrowserImportOptions struct {
	Browser      string
	BrowserRoot  string
	Output       string
	// Proxy 是写入账号的固定代理，RequestProxy 是没有固定代理时导入请求使用的代理
	Proxy        string
	RequestProxy string
	Profiles     []string
	Emails       []string
	Targets      []string
}

// ImportFailure 描述单个账号导入失败的原因
type ImportFailure struct {
	Target string `json:"target"`
	Error  string `json:"error"`
}

// ImportBrowserResult 包含批量导入的结果与失败详情
type ImportBrowserResult struct {
	Results  []ImportResult  `json:"results"`
	Failures []ImportFailure `json:"failures,omitempty"`
	Models   int             `json:"models"`
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

type pendingImport struct {
	state  auth.StorageState
	result ImportResult
	// requestProxy 是验证请求使用的代理
	requestProxy string
}

// CookieImportOptions 保存 Cookie 导入参数，Cookies 为 Cookie Header、浏览器导出的 JSON 或 Netscape cookies.txt
type CookieImportOptions struct {
	Cookies string
	ID      string
	Output  string
	// Proxy 是写入账号的固定代理，RequestProxy 是没有固定代理时验证请求使用的代理
	Proxy        string
	RequestProxy string
	// AuthUser 是目标账号在浏览器多账号登录中的序号，即官网地址 /u/N 中的 N
	AuthUser int
}

// Discover 只读列出本机 Chrome Google 账号
func Discover(chromeRoot string) ([]Account, error) {
	return DiscoverBrowser(BrowserChrome, chromeRoot)
}

// DiscoverBrowser 只读列出指定浏览器中可导入的 Google 账号
func DiscoverBrowser(browser string, root string) ([]Account, error) {
	if browser == "" {
		browser = BrowserChrome
	}
	return discoverPlatformBrowser(browser, root)
}

// Refresh 使用已保存的 OAuth 续签材料重新签发 Google Cookie
func Refresh(ctx context.Context, material auth.OAuthMaterial, proxy string) ([]gemini.Cookie, error) {
	if err := ensurePlatformImport(); err != nil {
		return nil, err
	}
	proxyURL, err := ValidateProxy(proxy)
	if err != nil {
		return nil, err
	}
	cookies, err := fetchGoogleCookies(ctx, material.GaiaID, material.RefreshToken, material.WrappedBindingKey, proxyURL)
	if err != nil {
		return nil, err
	}
	return toGeminiCookies(cookies), nil
}

// ImportCookies 从 Cookie 文本生成短期账号状态
func ImportCookies(ctx context.Context, options CookieImportOptions) (ImportResult, int, error) {
	proxyURL, requestProxy, err := importProxies(options.Proxy, options.RequestProxy)
	if err != nil {
		return ImportResult{}, 0, err
	}
	id := AccountID(options.ID)
	if id == "" {
		return ImportResult{}, 0, fmt.Errorf("id 必须包含英文字母或数字")
	}
	if options.AuthUser < 0 {
		return ImportResult{}, 0, fmt.Errorf("authuser 不能为负数")
	}
	cookies, err := parseCookies(options.Cookies)
	if err != nil {
		return ImportResult{}, 0, err
	}
	path := filepath.Join(options.Output, id, storageFileName)
	state := auth.StorageState{
		Cookies: cookies,
		Origins: []json.RawMessage{},
		Metadata: auth.Metadata{
			ID: id, Proxy: proxyURL,
			Source:   auth.ImportSource{Browser: "cookie-header"},
			Language: defaultImportLanguage,
			AuthUser: options.AuthUser,
		},
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return ImportResult{}, 0, fmt.Errorf("解析认证状态路径: %w", err)
	}
	pending := pendingImport{
		state: state,
		result: ImportResult{
			Profile: "cookie-header", Imported: true, CookieCount: len(cookies), Path: absPath,
		},
		requestProxy: requestProxy,
	}
	results, _, modelCount, err := validateAndWriteImports(ctx, []pendingImport{pending}, nil)
	if err != nil {
		return ImportResult{}, 0, err
	}
	return results[0], modelCount, nil
}

// Import 用本机 Chrome 的 OAuth 续签材料生成协议账号状态
func Import(ctx context.Context, options ImportOptions) ([]ImportResult, int, error) {
	return ImportBrowser(ctx, BrowserImportOptions{
		Browser:      BrowserChrome,
		BrowserRoot:  options.ChromeRoot,
		Output:       options.Output,
		Proxy:        options.Proxy,
		RequestProxy: options.RequestProxy,
		Profiles:     options.Profiles,
		Emails:       options.Emails,
	})
}

// ReadBrowserCookies 读取指定浏览器 Profile 中的 Google Cookie
func ReadBrowserCookies(browser string, profile string) ([]auth.StateCookie, error) {
	if browser == "" {
		browser = BrowserChrome
	}
	root, err := DefaultBrowserRoot(browser)
	if err != nil {
		return nil, err
	}
	if browser == BrowserFirefox {
		return readFirefoxCookies(root, profile)
	}
	masterKey, err := retrieveMasterKey(browser, root)
	if err != nil {
		return nil, err
	}
	return readChromiumCookies(browser, root, profile, masterKey)
}

// ImportBrowser 用本机指定浏览器的认证材料生成协议账号状态
func ImportBrowser(ctx context.Context, options BrowserImportOptions) ([]ImportResult, int, error) {
	res, err := ImportBrowserWithDetails(ctx, options)
	return res.Results, res.Models, err
}

// ImportBrowserWithDetails 用本机指定浏览器的认证材料生成协议账号状态，返回成功与失败详情
func ImportBrowserWithDetails(ctx context.Context, options BrowserImportOptions) (ImportBrowserResult, error) {
	if options.Browser == "" {
		options.Browser = BrowserChrome
	}
	if options.Browser == BrowserFirefox {
		return importFirefox(ctx, options)
	}
	return importChromium(ctx, options)
}

func importFirefox(ctx context.Context, options BrowserImportOptions) (ImportBrowserResult, error) {
	proxyURL, requestProxy, err := importProxies(options.Proxy, options.RequestProxy)
	if err != nil {
		return ImportBrowserResult{}, err
	}
	root := options.BrowserRoot
	if root == "" {
		var rootErr error
		root, rootErr = DefaultFirefoxRoot()
		if rootErr != nil {
			return ImportBrowserResult{}, rootErr
		}
		options.BrowserRoot = root
	}
	accounts, err := discoverFirefox(root)
	if err != nil {
		return ImportBrowserResult{}, err
	}
	selected, err := selectAccounts(accounts, options.Profiles, options.Emails, options.Targets)
	if err != nil {
		return ImportBrowserResult{}, err
	}
	pending := make([]pendingImport, 0, len(selected))
	var initialFailures []ImportFailure
	for _, account := range selected {
		targetLabel := account.Email
		if targetLabel == "" {
			targetLabel = account.Profile
		}
		cookies, err := readFirefoxCookies(root, account.Profile)
		if err != nil {
			initialFailures = append(initialFailures, ImportFailure{Target: targetLabel, Error: err.Error()})
			continue
		}
		id := AccountID(account.Email)
		if id == "" {
			id = AccountID(account.Profile)
		}
		path := filepath.Join(options.Output, id, storageFileName)
		absPath, err := filepath.Abs(path)
		if err != nil {
			initialFailures = append(initialFailures, ImportFailure{Target: targetLabel, Error: fmt.Sprintf("解析认证状态路径: %v", err)})
			continue
		}
		state := auth.StorageState{
			Cookies: cookies,
			Origins: []json.RawMessage{},
			Metadata: auth.Metadata{
				ID:       id,
				Proxy:    proxyURL,
				Source:   auth.ImportSource{Browser: BrowserFirefox, Profile: account.Profile},
				Language: defaultImportLanguage,
			},
		}
		pending = append(pending, pendingImport{
			state: state,
			result: ImportResult{
				Profile: account.Profile, Email: account.Email, Imported: true,
				CookieCount: len(cookies), Path: absPath,
			},
			requestProxy: requestProxy,
		})
	}
	succeeded, failures, models, err := validateAndWriteImports(ctx, pending, initialFailures)
	return ImportBrowserResult{Results: succeeded, Failures: failures, Models: models}, err
}

func importChromium(ctx context.Context, options BrowserImportOptions) (ImportBrowserResult, error) {
	if err := ensurePlatformImport(); err != nil {
		return ImportBrowserResult{}, err
	}
	proxyURL, requestProxy, err := importProxies(options.Proxy, options.RequestProxy)
	if err != nil {
		return ImportBrowserResult{}, err
	}
	root := options.BrowserRoot
	if root == "" {
		var rootErr error
		root, rootErr = DefaultBrowserRoot(options.Browser)
		if rootErr != nil {
			return ImportBrowserResult{}, rootErr
		}
		options.BrowserRoot = root
	}
	accounts, err := DiscoverBrowser(options.Browser, root)
	if err != nil {
		return ImportBrowserResult{}, err
	}
	selected, err := selectAccounts(accounts, options.Profiles, options.Emails, options.Targets)
	if err != nil {
		return ImportBrowserResult{}, err
	}

	masterKey, err := retrieveMasterKey(options.Browser, root)
	if err != nil {
		return ImportBrowserResult{}, err
	}
	if len(masterKey) != 32 {
		return ImportBrowserResult{}, fmt.Errorf("%s 主密钥长度异常", options.Browser)
	}

	pending := make([]pendingImport, 0, len(selected))
	var initialFailures []ImportFailure
	for _, account := range selected {
		targetLabel := account.Email
		if targetLabel == "" {
			targetLabel = account.Profile
		}
		if account.SourceType == "cookie" {
			cookies, err := readChromiumCookies(options.Browser, root, account.Profile, masterKey)
			if err != nil {
				initialFailures = append(initialFailures, ImportFailure{Target: targetLabel, Error: err.Error()})
				continue
			}
			id := AccountID(account.Email)
			if id == "" {
				id = AccountID(account.Profile)
			}
			path := filepath.Join(options.Output, id, storageFileName)
			absPath, err := filepath.Abs(path)
			if err != nil {
				initialFailures = append(initialFailures, ImportFailure{Target: targetLabel, Error: fmt.Sprintf("解析认证状态路径: %v", err)})
				continue
			}
			language, _ := readProfileLanguage(root, account.Profile)
			if language == "" {
				language = defaultImportLanguage
			}
			state := auth.StorageState{
				Cookies: cookies,
				Origins: []json.RawMessage{},
				Metadata: auth.Metadata{
					ID:       id,
					Proxy:    proxyURL,
					Source:   auth.ImportSource{Browser: options.Browser, Profile: account.Profile},
					Language: language,
					AuthUser: account.AuthUser,
				},
			}
			pending = append(pending, pendingImport{
				state: state,
				result: ImportResult{
					Profile: account.Profile, Email: account.Email, Imported: true,
					CookieCount: len(cookies), Path: absPath,
				},
				requestProxy: requestProxy,
			})
			continue
		}

		result, err := importChromiumAccount(ctx, account, options, proxyURL, requestProxy, masterKey)
		if err != nil {
			initialFailures = append(initialFailures, ImportFailure{Target: targetLabel, Error: err.Error()})
			continue
		}
		pending = append(pending, result)
	}
	succeeded, failures, models, err := validateAndWriteImports(ctx, pending, initialFailures)
	return ImportBrowserResult{Results: succeeded, Failures: failures, Models: models}, err
}

func selectAccounts(accounts []Account, profiles []string, emails []string, targets []string) ([]Account, error) {
	requestedProfiles := normalizedSet(profiles)
	requestedEmails := normalizedSet(emails)
	requestedTargets := normalizedSet(targets)
	selected := make([]Account, 0, len(accounts))
	foundProfiles := make(map[string]struct{})
	foundEmails := make(map[string]struct{})
	foundTargets := make(map[string]struct{})

	for _, account := range accounts {
		profile := strings.ToLower(strings.TrimSpace(account.Profile))
		email := strings.ToLower(strings.TrimSpace(account.Email))
		combo := profile + "/" + email
		_, profileMatch := requestedProfiles[profile]
		_, emailMatch := requestedEmails[email]
		_, targetMatchProfile := requestedTargets[profile]
		_, targetMatchEmail := requestedTargets[email]
		_, targetMatchCombo := requestedTargets[combo]

		matched := false
		if len(requestedTargets) > 0 {
			if targetMatchCombo || targetMatchProfile || targetMatchEmail {
				matched = true
				if targetMatchCombo {
					foundTargets[combo] = struct{}{}
				}
				if targetMatchProfile {
					foundTargets[profile] = struct{}{}
				}
				if targetMatchEmail {
					foundTargets[email] = struct{}{}
				}
			}
		} else if profileMatch || emailMatch {
			matched = true
			if profileMatch {
				foundProfiles[profile] = struct{}{}
			}
			if emailMatch {
				foundEmails[email] = struct{}{}
			}
		}

		if !matched {
			continue
		}
		if !account.Importable {
			if account.Reason == ReasonBrowserRunning {
				b := account.Browser
				if b == "" {
					b = "浏览器"
				}
				return nil, fmt.Errorf("%s 正在运行中并锁定了 Cookie 数据库，请先关闭 %s 浏览器后再重试导入", b, b)
			}
			return nil, fmt.Errorf("%s %s 缺少可导入的认证材料", account.Profile, account.Email)
		}
		if account.SourceType != "cookie" && !strings.Contains(account.Email, "@") {
			return nil, fmt.Errorf("%s 缺少账号邮箱", account.Profile)
		}
		selected = append(selected, account)
	}

	if len(requestedTargets) > 0 {
		if missing := missingValues(requestedTargets, foundTargets); len(missing) != 0 {
			return nil, fmt.Errorf("找不到目标: %s", strings.Join(missing, ", "))
		}
		return selected, nil
	}

	if missing := missingValues(requestedProfiles, foundProfiles); len(missing) != 0 {
		return nil, fmt.Errorf("找不到 Profile: %s", strings.Join(missing, ", "))
	}
	if missing := missingValues(requestedEmails, foundEmails); len(missing) != 0 {
		return nil, fmt.Errorf("找不到账号: %s", strings.Join(missing, ", "))
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

// importProxies 校验导入时填写的账号代理，返回写入账号的固定代理与导入请求使用的代理：direct 为直连，未填写时使用 fallback
func importProxies(proxy string, fallback string) (string, string, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == proxypool.Direct {
		return proxy, "", nil
	}
	fixed, err := ValidateProxy(proxy)
	if err != nil {
		return "", "", err
	}
	return fixed, cmp.Or(fixed, fallback), nil
}

// ValidateProxy 校验 HTTP、HTTPS、SOCKS4 或 SOCKS5 代理 URL 并返回去掉空白的值
func ValidateProxy(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("proxy 必须是 http、https、socks4、socks4a、socks5 或 socks5h URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks4", "socks4a", "socks5", "socks5h":
		return value, nil
	default:
		return "", fmt.Errorf("proxy 必须是 http、https、socks4、socks4a、socks5 或 socks5h URL")
	}
}

func decryptToken(root string, masterKey []byte, encrypted []byte) (string, error) {
	if len(encrypted) < 3+12+16 {
		return "", fmt.Errorf("refresh token 密文长度不足")
	}
	ver := string(encrypted[:3])
	key := masterKey
	if ver == "v10" && root != "" {
		if dpapi, err := decryptDPAPIMasterKey(root); err == nil && len(dpapi) == 32 {
			key = dpapi
		}
	} else if ver != "v20" && ver != "v10" {
		return "", fmt.Errorf("不支持的 refresh token 密文版本 %s", ver)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("缺少 %s 解密密钥", ver)
	}
	block, err := aes.NewCipher(key)
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

func decryptV20Token(masterKey []byte, encrypted []byte) (string, error) {
	return decryptToken("", masterKey, encrypted)
}

func importChromiumAccount(ctx context.Context, account Account, options BrowserImportOptions, proxyURL string, requestProxy string, masterKey []byte) (pendingImport, error) {
	gaiaID, encryptedToken, wrappedKey, err := readTokenService(options.BrowserRoot, account.Profile, account.GaiaID)
	if err != nil {
		return pendingImport{}, err
	}
	token, err := decryptToken(options.BrowserRoot, masterKey, encryptedToken)
	if err != nil {
		return pendingImport{}, err
	}
	cookies, err := fetchGoogleCookies(ctx, gaiaID, token, wrappedKey, requestProxy)
	if err != nil {
		return pendingImport{}, err
	}
	language, err := readProfileLanguage(options.BrowserRoot, account.Profile)
	if err != nil {
		return pendingImport{}, err
	}

	email := strings.ToLower(strings.TrimSpace(account.Email))
	path := filepath.Join(options.Output, AccountID(email), storageFileName)
	state := auth.StorageState{
		Cookies: toStorageCookies(cookies),
		Origins: []json.RawMessage{},
		Metadata: auth.Metadata{
			ID:     AccountID(email),
			Proxy:  proxyURL,
			Source: auth.ImportSource{Browser: options.Browser, Profile: account.Profile},
			OAuth: &auth.OAuthMaterial{
				GaiaID: gaiaID, RefreshToken: token, WrappedBindingKey: wrappedKey,
			},
			Language: language,
		},
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return pendingImport{}, fmt.Errorf("解析认证状态路径: %w", err)
	}
	return pendingImport{
		state: state,
		result: ImportResult{
			Profile: account.Profile, Email: email, Imported: true,
			CookieCount: len(cookies), Path: absPath,
		},
		requestProxy: requestProxy,
	}, nil
}

func validateAndWriteImports(ctx context.Context, pending []pendingImport, initialFailures []ImportFailure) ([]ImportResult, []ImportFailure, int, error) {
	models := make(map[string]struct{})
	failures := append([]ImportFailure(nil), initialFailures...)
	var succeeded []ImportResult
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)

	for index := range pending {
		item := &pending[index]
		targetLabel := item.result.Email
		if targetLabel == "" {
			targetLabel = item.result.Profile
		}
		source := gemini.AccountSource{
			ID: item.state.Metadata.ID, Cookies: stateCookiesToGemini(item.state.Cookies),
			Language: item.state.Metadata.Language, AuthUser: item.state.Metadata.AuthUser,
		}
		source.Save = func(cookies []gemini.Cookie) error {
			item.state.Cookies = geminiCookiesToState(cookies)
			return nil
		}
		client, err := gemini.NewClient(source, item.requestProxy, false)
		if err != nil {
			failures = append(failures, ImportFailure{Target: targetLabel, Error: err.Error()})
			continue
		}
		accountContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = client.Init(accountContext)
		if err == nil {
			_, err = client.FetchUsage(accountContext)
		}
		if email := client.Email(); email != "" {
			newID := AccountID(email)
			if newID != "" && item.state.Metadata.ID != newID && !strings.Contains(item.state.Metadata.ID, "@") {
				item.state.Metadata.ID = newID
				item.result.Email = email
				item.result.Path = filepath.Join(filepath.Dir(filepath.Dir(item.result.Path)), newID, storageFileName)
			}
		}
		cancel()
		client.CloseIdleConnections()
		if err != nil {
			failures = append(failures, ImportFailure{Target: targetLabel, Error: err.Error()})
			continue
		}
		for _, model := range client.Models() {
			models[model.ID] = struct{}{}
		}

		file, err := auth.New(item.result.Path, item.state)
		if err != nil {
			failures = append(failures, ImportFailure{Target: targetLabel, Error: err.Error()})
			continue
		}
		if err := file.Write(); err != nil {
			failures = append(failures, ImportFailure{Target: targetLabel, Error: err.Error()})
			continue
		}
		item.result.CompletedAt = completedAt
		succeeded = append(succeeded, item.result)
	}

	if len(succeeded) == 0 && len(failures) > 0 {
		return nil, failures, 0, fmt.Errorf("所选账号均验证失败: %s", failures[0].Error)
	}
	return succeeded, failures, len(models), nil
}

// AccountID 返回邮箱对应的账号 ID
func AccountID(email string) string {
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

func toGeminiCookies(cookies []multiloginCookie) []gemini.Cookie {
	return stateCookiesToGemini(toStorageCookies(cookies))
}

func stateCookiesToGemini(cookies []auth.StateCookie) []gemini.Cookie {
	result := make([]gemini.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		result = append(result, gemini.Cookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path,
			Expires: int64(cookie.Expires), HTTPOnly: cookie.HTTPOnly, Secure: cookie.Secure, SameSite: cookie.SameSite,
		})
	}
	return result
}

func geminiCookiesToState(cookies []gemini.Cookie) []auth.StateCookie {
	result := make([]auth.StateCookie, 0, len(cookies))
	for _, cookie := range cookies {
		result = append(result, auth.StateCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path,
			Expires: float64(cookie.Expires), HTTPOnly: cookie.HTTPOnly, Secure: cookie.Secure, SameSite: cookie.SameSite,
		})
	}
	return result
}

func parseCookieHeader(header string) ([]auth.StateCookie, error) {
	header = strings.TrimSpace(header)
	if len(header) >= len("Cookie:") && strings.EqualFold(header[:len("Cookie:")], "Cookie:") {
		header = strings.TrimSpace(header[len("Cookie:"):])
	}
	if header == "" || strings.ContainsAny(header, "\r\n") {
		return nil, fmt.Errorf("cookie 必须是一行 Cookie Header")
	}
	cookies := make([]auth.StateCookie, 0)
	names := make(map[string]struct{})
	for _, item := range strings.Split(header, ";") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("Cookie Header 项 %q 格式无效", strings.TrimSpace(item))
		}
		name := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if value == "" {
			return nil, fmt.Errorf("Cookie %s 的值为空", name)
		}
		if _, exists := names[name]; exists {
			continue
		}
		names[name] = struct{}{}
		domain := ".google.com"
		if strings.HasPrefix(name, "__Host-") {
			domain = "gemini.google.com"
		}
		cookies = append(cookies, auth.StateCookie{
			Name: name, Value: value, Domain: domain, Path: "/", Secure: true,
		})
	}
	return cookies, nil
}
