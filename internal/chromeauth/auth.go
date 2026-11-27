package chromeauth

import (
	"cmp"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// Account 描述本机 Chrome 中可发现的 Google 账号，同一 Profile 登录的每个账号各占一项
type Account struct {
	Profile     string `json:"profile"`
	GaiaID      string `json:"-"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Importable  bool   `json:"importable"`
	// Reason 是账号不可导入的原因，不可导入且为空时表示该账号未在 Chrome 登录
	Reason string `json:"reason,omitempty"`
	// token 是 token_service 中该账号的续签材料
	token tokenRecord
}

// tokenRecord 是 token_service 中一个账号的加密 refresh token 与 wrapped binding key
type tokenRecord struct {
	encryptedToken []byte
	bindingKey     []byte
}

const (
	// ReasonReadFailed 表示读取 Profile 的 Web Data 失败
	ReasonReadFailed = "read_failed"
	// ReasonNeedsReauth 表示 refresh token 不是 App-Bound 加密，需要在 Chrome 重新登录该账号
	ReasonNeedsReauth = "needs_reauth"
)

// ImportOptions 保存批量导入参数
type ImportOptions struct {
	Output string
	// Proxy 是写入账号的固定代理，RequestProxy 是没有固定代理时导入请求使用的代理
	Proxy        string
	RequestProxy string
	Profiles     []string
	Emails       []string
}

// pendingImport 是通过验证后写入 path 的认证状态与验证请求使用的代理
type pendingImport struct {
	state        auth.StorageState
	path         string
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

// Chrome 是一次读取的本机 Chrome 账号，多次导入共用账号发现结果与 App-Bound 主密钥
type Chrome struct {
	root     string
	accounts []Account
	// masterKey 在首次导入时读取 App-Bound v20 主密钥
	masterKey func() ([]byte, error)
}

// OpenChrome 发现 chromeRoot 中的 Google 账号
func OpenChrome(chromeRoot string) (*Chrome, error) {
	accounts, err := Discover(chromeRoot)
	if err != nil {
		return nil, err
	}
	return &Chrome{
		root: chromeRoot, accounts: accounts,
		masterKey: sync.OnceValues(func() ([]byte, error) { return retrieveV20Key(chromeRoot) }),
	}, nil
}

// Accounts 返回发现的账号
func (c *Chrome) Accounts() []Account {
	return c.accounts
}

// Refresh 使用已保存的 OAuth 续签材料，以账号绑定的 Chrome 主版本 chrome 与 Accept-Language language 重新签发 Google Cookie，并返回响应登记的 DBSC 会话
func Refresh(ctx context.Context, material auth.OAuthMaterial, chrome int, language string, proxy string) ([]gemini.Cookie, *auth.BoundSession, error) {
	cookies, bound, err := fetchGoogleCookies(ctx, browserIdentity{chrome: chrome, language: language}, material.GaiaID, material.RefreshToken, material.WrappedBindingKey, proxy)
	if err != nil {
		return nil, nil, err
	}
	return auth.GeminiCookies(toStorageCookies(cookies)), bound, nil
}

// ImportCookies 从 Cookie 文本生成账号状态，返回状态文件路径与模型数
func ImportCookies(ctx context.Context, options CookieImportOptions) (string, int, error) {
	id := AccountID(options.ID)
	if id == "" {
		return "", 0, fmt.Errorf("id 必须包含英文字母或数字")
	}
	if options.AuthUser < 0 {
		return "", 0, fmt.Errorf("authuser 不能为负数")
	}
	cookies, err := parseCookies(options.Cookies)
	if err != nil {
		return "", 0, err
	}
	path := filepath.Join(options.Output, id, storageFileName)
	metadata, requestProxy, err := importIdentity(path, auth.Metadata{
		ID: id, Proxy: options.Proxy, Source: auth.ImportSource{Browser: "cookie-header"},
		Language: defaultImportLanguage, AuthUser: options.AuthUser,
	}, options.RequestProxy)
	if err != nil {
		return "", 0, err
	}
	pending := pendingImport{state: auth.StorageState{Cookies: cookies, Metadata: metadata}, path: path, requestProxy: requestProxy}
	paths, modelCount, err := validateAndWriteImports(ctx, []pendingImport{pending})
	if err != nil {
		return "", 0, err
	}
	return paths[0], modelCount, nil
}

// Import 用 OAuth 续签材料为选中的账号生成协议账号状态，返回各状态文件的路径与模型数
func (c *Chrome) Import(ctx context.Context, options ImportOptions) ([]string, int, error) {
	selected, err := selectAccounts(c.accounts, options.Profiles, options.Emails)
	if err != nil {
		return nil, 0, err
	}
	if len(selected) == 0 {
		return nil, 0, fmt.Errorf("没有选择要导入的账号")
	}
	pending := make([]pendingImport, 0, len(selected))
	for _, account := range selected {
		item, err := c.importAccount(ctx, account, options)
		if err != nil {
			return nil, 0, fmt.Errorf("导入 %s: %w", account.Email, err)
		}
		pending = append(pending, item)
	}
	return validateAndWriteImports(ctx, pending)
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
			return nil, fmt.Errorf("%s %s 缺少可导入的 OAuth 认证材料", account.Profile, account.Email)
		}
		foundProfiles[profile] = struct{}{}
		if _, exists := foundEmails[email]; exists {
			continue
		}
		selected = append(selected, account)
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

// importIdentity 返回写入 path 的账号身份与导入请求使用的代理：账号已导入过时沿用它的停用标记、Chrome 版本与未重新填写的固定代理，新账号按版本池占比分配 Chrome 版本
func importIdentity(path string, metadata auth.Metadata, fallback string) (auth.Metadata, string, error) {
	metadata.Proxy = strings.TrimSpace(metadata.Proxy)
	metadata = auth.Inherit(path, metadata)
	if !gemini.IsChromeVersion(metadata.Chrome) {
		metadata.Chrome = gemini.PickChromeVersion()
	}
	fixed, requestProxy, err := importProxies(metadata.Proxy, fallback)
	metadata.Proxy = fixed
	return metadata, requestProxy, err
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

// withImportProxyHint 在导入遇到网络连接或超时失败时为容器内的环回代理附加排障说明
func withImportProxyHint(proxyURL string, err error) error {
	if errors.Is(err, gemini.ErrTransport) || errors.Is(err, context.DeadlineExceeded) {
		return proxypool.WithLoopbackHint(proxyURL, err)
	}
	return err
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
	return string(plaintext), nil
}

// importAccount 为一个账号签发 Cookie 并组成待验证的认证状态
func (c *Chrome) importAccount(ctx context.Context, account Account, options ImportOptions) (pendingImport, error) {
	id := AccountID(account.Email)
	path := filepath.Join(options.Output, id, storageFileName)
	metadata, requestProxy, err := importIdentity(path, auth.Metadata{
		ID: id, Proxy: options.Proxy, Source: auth.ImportSource{Browser: "chrome", Profile: account.Profile},
	}, options.RequestProxy)
	if err != nil {
		return pendingImport{}, err
	}
	masterKey, err := c.masterKey()
	if err != nil {
		return pendingImport{}, err
	}
	token, err := decryptV20Token(masterKey, account.token.encryptedToken)
	if err != nil {
		return pendingImport{}, err
	}
	if metadata.Language, err = readProfileLanguage(c.root, account.Profile); err != nil {
		return pendingImport{}, err
	}
	cookies, bound, err := fetchGoogleCookies(ctx, browserIdentity{chrome: metadata.Chrome, language: metadata.Language}, account.GaiaID, token, account.token.bindingKey, requestProxy)
	if err != nil {
		return pendingImport{}, withImportProxyHint(requestProxy, err)
	}
	metadata.OAuth = &auth.OAuthMaterial{GaiaID: account.GaiaID, RefreshToken: token, WrappedBindingKey: account.token.bindingKey}
	metadata.BoundSession = bound
	state := auth.StorageState{Cookies: toStorageCookies(cookies), Metadata: metadata}
	return pendingImport{state: state, path: path, requestProxy: requestProxy}, nil
}

// validateAndWriteImports 以首页与模型目录验证每个账号，全部通过后写入认证状态，返回各状态文件的路径与模型数。用量只做尽力读取：企业/Workspace 账号官网不提供用量窗口，读取失败（或返回“未登录”假象）一律跳过、不拦导入——登录状态已由 Init 的首页 bootstrap 校验。
func validateAndWriteImports(ctx context.Context, pending []pendingImport) ([]string, int, error) {
	models := make(map[string]struct{})
	for index := range pending {
		item := &pending[index]
		source := gemini.AccountSource{
			ID: item.state.Metadata.ID, Cookies: auth.GeminiCookies(item.state.Cookies),
			Language: item.state.Metadata.Language, AuthUser: item.state.Metadata.AuthUser, Chrome: item.state.Metadata.Chrome,
			Save: func(cookies []gemini.Cookie) error {
				item.state.Cookies = auth.StateCookies(cookies)
				return nil
			},
		}
		client, err := gemini.NewClient(source, item.requestProxy, false)
		if err != nil {
			return nil, 0, fmt.Errorf("验证账号 %s: %w", item.state.Metadata.ID, err)
		}
		accountContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = client.Init(accountContext)
		if err == nil {
			// 用量为尽力读取：失败直接跳过，不作为导入校验条件
			_, _ = client.FetchUsage(accountContext)
		}
		cancel()
		client.CloseIdleConnections()
		if err != nil {
			return nil, 0, fmt.Errorf("验证账号 %s: %w", item.state.Metadata.ID, withImportProxyHint(item.requestProxy, err))
		}
		for _, model := range client.Models() {
			models[model.ID] = struct{}{}
		}
	}
	paths := make([]string, len(pending))
	for index, item := range pending {
		file, err := auth.New(item.path, item.state)
		if err != nil {
			return nil, 0, err
		}
		if err := file.Write(); err != nil {
			return nil, 0, err
		}
		paths[index] = file.Path()
	}
	return paths, len(models), nil
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
		result = append(result, auth.StateCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
			Expires:  float64(now.Add(lifetime).UnixNano()) / 1e9,
			HTTPOnly: cookie.IsHTTPOnly, Secure: cookie.IsSecure, SameSite: sameSiteName(cookie.SameSite),
		})
	}
	return result
}
