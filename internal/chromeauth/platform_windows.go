//go:build windows

package chromeauth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

// DefaultChromeRoot 返回本机 Chrome User Data 目录
func DefaultChromeRoot() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("环境变量 LOCALAPPDATA 为空")
	}
	return filepath.Join(localAppData, "Google", "Chrome", "User Data"), nil
}

func ensurePlatformImport() error {
	return nil
}

func discoverPlatform(chromeRoot string) ([]Account, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("读取 Chrome Local State: %w", err)
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
		return nil, fmt.Errorf("解析 Chrome Local State: %w", err)
	}
	if state.Profile.InfoCache == nil {
		return nil, fmt.Errorf("Chrome Local State 缺少 profile.info_cache")
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
		identities, err := profileIdentities(chromeRoot, profile)
		if err != nil || len(identities) == 0 {
			accounts = append(accounts, Account{Profile: profile, DisplayName: info.Name, Email: info.UserName})
			continue
		}
		seen := make(map[string]bool, len(identities))
		for _, identity := range identities {
			if identity.GaiaID == "" || seen[identity.GaiaID] {
				continue
			}
			seen[identity.GaiaID] = true
			_, encrypted, bindingKey, tokenErr := readTokenService(chromeRoot, profile, identity.GaiaID)
			accounts = append(accounts, Account{
				Profile: profile, GaiaID: identity.GaiaID, DisplayName: info.Name, Email: identity.Email,
				Importable: tokenErr == nil && strings.HasPrefix(string(encrypted), "v20") && len(bindingKey) != 0,
			})
		}
	}
	return accounts, nil
}

// profileIdentity 是 Preferences.account_info 中的一个 Google 账号
type profileIdentity struct {
	GaiaID string `json:"gaia"`
	Email  string `json:"email"`
}

// profileIdentities 读取 Profile 中登录的全部 Google 账号
func profileIdentities(chromeRoot string, profile string) ([]profileIdentity, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, profile, "Preferences"))
	if err != nil {
		return nil, fmt.Errorf("读取 %s Preferences: %w", profile, err)
	}
	var preferences struct {
		Accounts []profileIdentity `json:"account_info"`
	}
	if err := json.Unmarshal(data, &preferences); err != nil {
		return nil, fmt.Errorf("解析 %s Preferences: %w", profile, err)
	}
	return preferences.Accounts, nil
}

// readTokenService 读取 Profile 中指定 Gaia ID 账号的 refresh token 与设备绑定密钥
func readTokenService(chromeRoot string, profile string, gaiaID string) (string, []byte, []byte, error) {
	databasePath := filepath.Join(chromeRoot, profile, "Web Data")
	uri := "file:" + filepath.ToSlash(databasePath) + "?mode=ro&immutable=1"
	database, err := sql.Open("sqlite", uri)
	if err != nil {
		return "", nil, nil, fmt.Errorf("打开 %s Web Data: %w", profile, err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)

	rows, err := database.Query("SELECT service, encrypted_token, binding_key FROM token_service WHERE service = ?", "AccountId-"+gaiaID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", nil, nil, fmt.Errorf("读取 %s token_service: %w", profile, err)
		}
		return "", nil, nil, fmt.Errorf("%s 的 token_service 没有账号 %s", profile, gaiaID)
	}
	var service string
	var encryptedToken []byte
	var bindingKey []byte
	if err := rows.Scan(&service, &encryptedToken, &bindingKey); err != nil {
		return "", nil, nil, fmt.Errorf("解析 %s token_service: %w", profile, err)
	}
	if len(encryptedToken) == 0 || len(bindingKey) == 0 {
		return "", nil, nil, fmt.Errorf("%s 的 token_service 缺少认证材料", profile)
	}
	return strings.TrimPrefix(service, "AccountId-"), encryptedToken, bindingKey, nil
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
