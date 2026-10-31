//go:build windows

package chromeauth

import (
	"cmp"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unsafe"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"golang.org/x/sys/windows"
	_ "modernc.org/sqlite"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	crypt32            = windows.NewLazySystemDLL("crypt32.dll")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

// DefaultChromeRoot 返回本机 Chrome User Data 目录
func DefaultChromeRoot() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("环境变量 LOCALAPPDATA 为空")
	}
	return filepath.Join(localAppData, "Google", "Chrome", "User Data"), nil
}

// DefaultBraveRoot 返回本机 Brave User Data 目录
func DefaultBraveRoot() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("环境变量 LOCALAPPDATA 为空")
	}
	return filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "User Data"), nil
}

// DefaultEdgeRoot 返回本机 Edge User Data 目录
func DefaultEdgeRoot() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("环境变量 LOCALAPPDATA 为空")
	}
	return filepath.Join(localAppData, "Microsoft", "Edge", "User Data"), nil
}

// DefaultFirefoxRoot 返回本机 Firefox 目录
func DefaultFirefoxRoot() (string, error) {
	appData := strings.TrimSpace(os.Getenv("APPDATA"))
	if appData == "" {
		return "", fmt.Errorf("环境变量 APPDATA 为空")
	}
	return filepath.Join(appData, "Mozilla", "Firefox"), nil
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
	return nil
}

func isSharingViolation(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == 32 || errno == windows.ERROR_SHARING_VIOLATION || errno == windows.ERROR_LOCK_VIOLATION) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "used by another process") || strings.Contains(msg, "sharing violation") || strings.Contains(msg, "lock violation")
}

func decryptDPAPIMasterKey(root string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("读取 Local State: %w", err)
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析 Local State: %w", err)
	}
	raw := strings.TrimSpace(state.OSCrypt.EncryptedKey)
	if raw == "" {
		return nil, fmt.Errorf("Local State 缺少 encrypted_key")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 encrypted_key: %w", err)
	}
	const dpapiPrefix = "DPAPI"
	if len(decoded) <= len(dpapiPrefix) || string(decoded[:len(dpapiPrefix)]) != dpapiPrefix {
		return nil, fmt.Errorf("encrypted_key 缺少 DPAPI 前缀")
	}
	cipher := decoded[len(dpapiPrefix):]
	var inBlob, outBlob dataBlob
	inBlob.cbData = uint32(len(cipher))
	inBlob.pbData = &cipher[0]
	r, _, callErr := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))
	key := make([]byte, outBlob.cbData)
	copy(key, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	if len(key) != 32 {
		return nil, fmt.Errorf("DPAPI 密钥长度异常 (%d)", len(key))
	}
	return key, nil
}

func retrieveMasterKey(browser string, root string) ([]byte, error) {
	// 优先尝试 v20 App-Bound 密钥（Chrome 127+ 及现代 Chromium 核心默认启用）
	v20Key, v20Err := retrieveV20KeyForBrowser(browser, root)
	if v20Err == nil && len(v20Key) == 32 {
		return v20Key, nil
	}
	// 其次尝试 DPAPI 密钥（Brave 或未启用 ABE 的 Chromium 核心）
	dpapiMasterKey, dpapiErr := decryptDPAPIMasterKey(root)
	if dpapiErr == nil && len(dpapiMasterKey) == 32 {
		return dpapiMasterKey, nil
	}
	if v20Err != nil {
		return nil, fmt.Errorf("解密 %s 主密钥失败 (ABE: %v, DPAPI: %v)", browser, v20Err, dpapiErr)
	}
	return nil, fmt.Errorf("解密 %s 主密钥失败: %w", browser, dpapiErr)
}

func openChromiumCookies(browser string, root string, profile string) (*sql.DB, func(), error) {
	tempDir, err := os.MkdirTemp("", "gemini-web2api-cookies-*")
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Cookie 临时目录: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()
	cookiePath := filepath.Join(root, profile, "Network", "Cookies")
	if _, statErr := os.Stat(cookiePath); statErr != nil {
		cookiePath = filepath.Join(root, profile, "Cookies")
		if _, statErr2 := os.Stat(cookiePath); statErr2 != nil {
			return nil, nil, fmt.Errorf("未找到 Cookie 数据库: %w", statErr2)
		}
	}
	copyPath := filepath.Join(tempDir, "Cookies")
	exeName := browserExeName(browser)
	if err = copyLockedFile(cookiePath, copyPath, exeName); err != nil {
		if isSharingViolation(err) {
			b := browser
			if b == "" {
				b = "浏览器"
			}
			return nil, nil, fmt.Errorf("%s 正在运行中并锁定了 Cookie 数据库，请先关闭 %s 浏览器后再重试导入", b, b)
		}
		return nil, nil, fmt.Errorf("复制 %s Cookies: %w", profile, err)
	}
	_ = copyLockedFile(cookiePath+"-wal", copyPath+"-wal", exeName)
	uri := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(copyPath), RawQuery: "mode=ro"}
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, nil, fmt.Errorf("打开 %s Cookies: %w", profile, err)
	}
	database.SetMaxOpenConns(1)
	return database, func() {
		database.Close()
		os.RemoveAll(tempDir)
	}, nil
}

func checkChromiumCookies(browser string, root string, profile string) (hasCookies bool, isLocked bool, err error) {
	database, closeDatabase, err := openChromiumCookies(browser, root, profile)
	if err != nil {
		if errors.Is(err, errBrowserRunning) || isSharingViolation(err) {
			return false, true, nil
		}
		return false, false, err
	}
	defer closeDatabase()

	rows, err := database.Query("SELECT name FROM cookies WHERE host_key LIKE '%google.com'")
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			names[name] = true
		}
	}
	return names["SAPISID"] && names["__Secure-1PSID"], false, nil
}

func readChromiumCookies(browser string, root string, profile string, masterKey []byte) ([]auth.StateCookie, error) {
	dpapiKey, _ := decryptDPAPIMasterKey(root)

	database, closeDatabase, err := openChromiumCookies(browser, root, profile)
	if err != nil {
		if errors.Is(err, errBrowserRunning) || isSharingViolation(err) {
			b := browser
			if b == "" {
				b = "浏览器"
			}
			return nil, fmt.Errorf("%s 正在运行中并锁定了 Cookie 数据库，请先关闭 %s 浏览器后再重试导入", b, b)
		}
		return nil, err
	}
	defer closeDatabase()

	rows, err := database.Query("SELECT name, encrypted_value, host_key, path, expires_utc, is_secure, is_httponly, samesite FROM cookies WHERE host_key LIKE '%google.com'")
	if err != nil {
		return nil, fmt.Errorf("读取 %s cookies: %w", profile, err)
	}
	defer rows.Close()

	var cookies []auth.StateCookie
	for rows.Next() {
		var name, hostKey, path string
		var encrypted []byte
		var expiresUtc int64
		var isSecure, isHttpOnly, samesite int
		if err := rows.Scan(&name, &encrypted, &hostKey, &path, &expiresUtc, &isSecure, &isHttpOnly, &samesite); err != nil {
			return nil, fmt.Errorf("解析 %s cookies: %w", profile, err)
		}
		if name == "" || len(encrypted) == 0 {
			continue
		}

		val := ""
		if len(encrypted) >= 3+12+16 {
			ver := string(encrypted[:3])
			var key []byte
			if ver == "v20" {
				key = masterKey
			} else if ver == "v10" {
				if len(dpapiKey) == 32 {
					key = dpapiKey
				} else {
					key = masterKey
				}
			}
			if len(key) == 32 {
				block, err := aes.NewCipher(key)
				if err == nil {
					gcm, err := cipher.NewGCM(block)
					if err == nil {
						plain, err := gcm.Open(nil, encrypted[3:15], encrypted[15:], nil)
						if err == nil {
							if ver == "v20" && len(plain) > 32 && !isPrintableASCII(plain[:32]) {
								plain = plain[32:]
							}
							val = string(plain)
						}
					}
				}
			}
		}
		if val == "" {
			continue
		}

		expiresUnix := float64(0)
		if expiresUtc > 11644473600000000 {
			expiresUnix = float64((expiresUtc - 11644473600000000) / 1000000)
		}
		sameSiteStr := ""
		switch samesite {
		case 1:
			sameSiteStr = "Lax"
		case 2:
			sameSiteStr = "Strict"
		}
		cookies = append(cookies, auth.StateCookie{
			Name:     name,
			Value:    val,
			Domain:   hostKey,
			Path:     path,
			Expires:  expiresUnix,
			Secure:   isSecure != 0,
			HTTPOnly: isHttpOnly != 0,
			SameSite: sameSiteStr,
		})
	}
	names := make(map[string]bool)
	for _, c := range cookies {
		names[c.Name] = true
	}
	if !names["SAPISID"] || !names["__Secure-1PSID"] {
		return nil, fmt.Errorf("%s 缺少核心 Google Cookie (SAPISID/__Secure-1PSID)", profile)
	}
	return cookies, nil
}

func isPrintableASCII(data []byte) bool {
	for _, b := range data {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}

func discoverPlatformBrowser(browser string, root string) ([]Account, error) {
	browser = strings.ToLower(strings.TrimSpace(browser))
	if browser == "" {
		browser = BrowserChrome
	}
	if root == "" {
		var err error
		root, err = DefaultBrowserRoot(browser)
		if err != nil {
			return nil, err
		}
	}
	if browser == BrowserFirefox {
		return discoverFirefox(root)
	}
	return discoverChromium(browser, root)
}

func discoverPlatform(chromeRoot string) ([]Account, error) {
	return discoverChromium(BrowserChrome, chromeRoot)
}

func discoverChromium(browser string, root string) ([]Account, error) {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("读取 %s Local State: %w", browser, err)
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name     string `json:"name"`
				UserName string `json:"user_name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析 %s Local State: %w", browser, err)
	}
	if state.Profile.InfoCache == nil {
		return nil, fmt.Errorf("%s Local State 缺少 profile.info_cache", browser)
	}

	profiles := make([]string, 0, len(state.Profile.InfoCache))
	for profile := range state.Profile.InfoCache {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i int, j int) bool {
		return naturalLess(profiles[i], profiles[j])
	})
	accounts := make([]Account, 0, len(profiles))
	for _, profile := range profiles {
		info := state.Profile.InfoCache[profile]
		identities, err := profileIdentities(root, profile)
		if err == nil && len(identities) > 0 {
			records, recordsErr := readTokenServices(root, profile)
			hasCoreCookies, isLocked, cookieErr := checkChromiumCookies(browser, root, profile)
			seen := make(map[string]bool, len(identities))
			for _, identity := range identities {
				if identity.GaiaID == "" || seen[identity.GaiaID] {
					continue
				}
				seen[identity.GaiaID] = true
				record, found := records[identity.GaiaID]
				dispName := identity.DisplayName
				if dispName == "" {
					dispName = info.Name
				}
				account := Account{
					Browser:     browser,
					Profile:     profile,
					GaiaID:      identity.GaiaID,
					DisplayName: dispName,
					Email:       identity.Email,
					AuthUser:    identity.AuthUser,
				}
				switch {
				case found:
					account.SourceType = "oauth"
					if recordsErr != nil {
						account.Reason = ReasonReadFailed
					} else if !strings.HasPrefix(string(record.encryptedToken), "v20") && !strings.HasPrefix(string(record.encryptedToken), "v10") {
						account.Reason = ReasonNeedsReauth
					} else {
						account.Importable = true
					}
				case isLocked:
					account.SourceType = "cookie"
					account.Reason = ReasonBrowserRunning
				case hasCoreCookies:
					account.SourceType = "cookie"
					account.Importable = true
				case cookieErr != nil:
					account.SourceType = "cookie"
					account.Reason = ReasonReadFailed
				default:
					account.Reason = "not_signed_in"
				}
				accounts = append(accounts, account)
			}
		}

		if len(identities) == 0 {
			hasCoreCookies, isLocked, cookieErr := checkChromiumCookies(browser, root, profile)
			acc := Account{
				Browser:     browser,
				Profile:     profile,
				DisplayName: info.Name,
				Email:       cmp.Or(info.UserName, profile),
			}
			switch {
			case isLocked:
				acc.Reason = ReasonBrowserRunning
			case hasCoreCookies:
				acc.Importable = true
				acc.SourceType = "cookie"
			case cookieErr != nil:
				acc.Reason = ReasonReadFailed
			default:
				acc.Reason = "not_signed_in"
			}
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

// profileIdentity 是 Preferences 中的一个 Google 账号
type profileIdentity struct {
	GaiaID      string `json:"gaia"`
	Email       string `json:"email"`
	DisplayName string `json:"name,omitempty"`
	AuthUser    int    `json:"authuser,omitempty"`
}

// profileIdentities 读取 Profile 中登录的全部 Google 账号
func profileIdentities(chromeRoot string, profile string) ([]profileIdentity, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, profile, "Preferences"))
	if err != nil {
		return nil, fmt.Errorf("读取 %s Preferences: %w", profile, err)
	}
	var preferences struct {
		Accounts   []profileIdentity `json:"account_info"`
		GaiaCookie struct {
			LastListAccountsBinary string `json:"last_list_accounts_binary_data"`
		} `json:"gaia_cookie"`
	}
	if err := json.Unmarshal(data, &preferences); err != nil {
		return nil, fmt.Errorf("解析 %s Preferences: %w", profile, err)
	}
	if len(preferences.Accounts) > 0 {
		return preferences.Accounts, nil
	}
	if preferences.GaiaCookie.LastListAccountsBinary != "" {
		if raw, err := base64.StdEncoding.DecodeString(preferences.GaiaCookie.LastListAccountsBinary); err == nil {
			parsed := parseGaiaAccounts(raw)
			if len(parsed) > 0 {
				identities := make([]profileIdentity, len(parsed))
				for i, p := range parsed {
					identities[i] = profileIdentity{
						GaiaID:      p.GaiaID,
						Email:       p.Email,
						DisplayName: p.DisplayName,
						AuthUser:    p.AuthUser,
					}
				}
				return identities, nil
			}
		}
	}
	return nil, nil
}

// openWebData 把 Profile 的 Web Data 与其 WAL 日志复制到临时目录后只读打开，绕过运行中 Chrome 的排他锁并包含尚未检查点的写入
func openWebData(chromeRoot string, profile string) (database *sql.DB, closeDatabase func(), err error) {
	tempDir, err := os.MkdirTemp("", "gemini-web2api-webdata-*")
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Web Data 临时目录: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()
	databasePath := filepath.Join(chromeRoot, profile, "Web Data")
	copyPath := filepath.Join(tempDir, "Web Data")
	if err = copyFile(databasePath, copyPath); err != nil {
		return nil, nil, fmt.Errorf("复制 %s Web Data: %w", profile, err)
	}
	if err = copyFile(databasePath+"-wal", copyPath+"-wal"); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("复制 %s Web Data-wal: %w", profile, err)
	}
	uri := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(copyPath), RawQuery: "mode=ro"}
	if database, err = sql.Open("sqlite", uri.String()); err != nil {
		return nil, nil, fmt.Errorf("打开 %s Web Data: %w", profile, err)
	}
	database.SetMaxOpenConns(1)
	return database, func() {
		database.Close()
		os.RemoveAll(tempDir)
	}, nil
}

// tokenRecord 是 token_service 中一个账号的加密 refresh token 与 wrapped binding key
type tokenRecord struct {
	encryptedToken []byte
	bindingKey     []byte
}

// readTokenServices 读取 Profile 中全部 Google 账号的 token_service 记录，键为 Gaia ID
func readTokenServices(chromeRoot string, profile string) (map[string]tokenRecord, error) {
	database, closeDatabase, err := openWebData(chromeRoot, profile)
	if err != nil {
		return nil, err
	}
	defer closeDatabase()
	rows, err := database.Query("SELECT service, encrypted_token, binding_key FROM token_service WHERE service LIKE 'AccountId-%'")
	if err != nil {
		return nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
	}
	defer rows.Close()
	records := map[string]tokenRecord{}
	for rows.Next() {
		var service string
		var record tokenRecord
		if err := rows.Scan(&service, &record.encryptedToken, &record.bindingKey); err != nil {
			return nil, fmt.Errorf("解析 %s token_service: %w", profile, err)
		}
		records[strings.TrimPrefix(service, "AccountId-")] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
	}
	return records, nil
}

// readTokenService 返回一个账号的 Gaia ID、加密 refresh token 与 wrapped binding key
func readTokenService(chromeRoot string, profile string, gaiaID string) (string, []byte, []byte, error) {
	records, err := readTokenServices(chromeRoot, profile)
	if err != nil {
		return "", nil, nil, err
	}
	record, found := records[gaiaID]
	if !found {
		return "", nil, nil, fmt.Errorf("%s 的 token_service 没有账号 %s", profile, gaiaID)
	}
	if len(record.encryptedToken) == 0 {
		return "", nil, nil, fmt.Errorf("%s 的 token_service 缺少认证材料", profile)
	}
	return gaiaID, record.encryptedToken, record.bindingKey, nil
}

func readProfileLanguage(chromeRoot string, profile string) (string, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, profile, "Preferences"))
	if err != nil {
		return "", fmt.Errorf("读取 %s Preferences: %w", profile, err)
	}
	var preferences struct {
		Intl struct {
			AcceptLanguages   string `json:"accept_languages"`
			SelectedLanguages string `json:"selected_languages"`
		} `json:"intl"`
	}
	if err := json.Unmarshal(data, &preferences); err != nil {
		return "", fmt.Errorf("解析 %s Preferences: %w", profile, err)
	}
	languages := strings.TrimSpace(preferences.Intl.AcceptLanguages)
	if languages == "" {
		languages = strings.TrimSpace(preferences.Intl.SelectedLanguages)
	}
	if languages == "" {
		return "", fmt.Errorf("%s Preferences 缺少语言偏好", profile)
	}
	return chromeAcceptLanguage(languages), nil
}

// chromeAcceptLanguage 按 Chrome 规则在地区语言后补充基础语言，并按 0.1 递减写入 q 值
func chromeAcceptLanguage(preference string) string {
	parts := strings.Split(preference, ",")
	languages := make([]string, 0, len(parts)*2)
	seen := make(map[string]bool, len(parts)*2)
	add := func(language string) {
		if language != "" && !seen[language] {
			seen[language] = true
			languages = append(languages, language)
		}
	}
	for index, part := range parts {
		language := strings.TrimSpace(part)
		add(language)
		base, _, regional := strings.Cut(language, "-")
		if !regional {
			continue
		}
		if index+1 < len(parts) {
			if next, _, _ := strings.Cut(strings.TrimSpace(parts[index+1]), "-"); next == base {
				continue
			}
		}
		add(base)
	}
	header := make([]string, 0, len(languages))
	quality := 10
	for index, language := range languages {
		if index == 0 {
			header = append(header, language)
		} else {
			header = append(header, fmt.Sprintf("%s;q=0.%d", language, quality))
		}
		if quality > 1 {
			quality--
		}
	}
	return strings.Join(header, ",")
}

func naturalLess(left string, right string) bool {
	leftParts := splitNatural(left)
	rightParts := splitNatural(right)
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		leftNumber, leftErr := strconv.Atoi(leftParts[index])
		rightNumber, rightErr := strconv.Atoi(rightParts[index])
		if leftErr == nil && rightErr == nil {
			if leftNumber != rightNumber {
				return leftNumber < rightNumber
			}
			continue
		}
		comparison := strings.Compare(strings.ToLower(leftParts[index]), strings.ToLower(rightParts[index]))
		if comparison != 0 {
			return comparison < 0
		}
	}
	return len(leftParts) < len(rightParts)
}

func splitNatural(value string) []string {
	if value == "" {
		return nil
	}
	parts := make([]string, 0)
	start := 0
	lastDigit := unicode.IsDigit(rune(value[0]))
	for index, character := range value {
		isDigit := unicode.IsDigit(character)
		if index != 0 && isDigit != lastDigit {
			parts = append(parts, value[start:index])
			start = index
		}
		lastDigit = isDigit
	}
	return append(parts, value[start:])
}
