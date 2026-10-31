package chromeauth

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	_ "modernc.org/sqlite"
)

type firefoxProfile struct {
	Name string
	Path string
}

// readFirefoxProfiles 解析 profiles.ini 获取所有 Profile
func readFirefoxProfiles(firefoxRoot string) ([]firefoxProfile, error) {
	data, err := os.ReadFile(filepath.Join(firefoxRoot, "profiles.ini"))
	if err != nil {
		return nil, fmt.Errorf("读取 Firefox profiles.ini: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	var profiles []firefoxProfile
	var curName, curPath string
	var isRel bool

	save := func() {
		if curPath != "" {
			p := curPath
			if isRel {
				p = filepath.Join(firefoxRoot, filepath.FromSlash(curPath))
			}
			name := curName
			if name == "" {
				name = filepath.Base(p)
			}
			profiles = append(profiles, firefoxProfile{Name: name, Path: p})
		}
		curName, curPath, isRel = "", "", false
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			save()
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Name":
			curName = strings.TrimSpace(v)
		case "Path":
			curPath = strings.TrimSpace(v)
		case "IsRelative":
			isRel = strings.TrimSpace(v) == "1"
		}
	}
	save()
	return profiles, nil
}

// openFirefoxCookies 复制并只读打开 Firefox cookies.sqlite
func openFirefoxCookies(profileDir string) (*sql.DB, func(), error) {
	tempDir, err := os.MkdirTemp("", "gemini-web2api-ffcookies-*")
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Firefox Cookie 临时目录: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()
	src := filepath.Join(profileDir, "cookies.sqlite")
	dst := filepath.Join(tempDir, "cookies.sqlite")
	if err = copyLockedFile(src, dst, "firefox.exe"); err != nil {
		if isSharingViolation(err) {
			return nil, nil, fmt.Errorf("%w: %s", errBrowserRunning, err.Error())
		}
		return nil, nil, fmt.Errorf("复制 Firefox cookies.sqlite: %w", err)
	}
	_ = copyLockedFile(src+"-wal", dst+"-wal", "firefox.exe")
	uri := "file:" + filepath.ToSlash(dst) + "?mode=ro"
	database, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, nil, fmt.Errorf("打开 Firefox cookies.sqlite: %w", err)
	}
	database.SetMaxOpenConns(1)
	return database, func() {
		database.Close()
		os.RemoveAll(tempDir)
	}, nil
}

// discoverFirefox 扫描 Firefox profiles 并检查 Google 登录态
func discoverFirefox(firefoxRoot string) ([]Account, error) {
	profiles, err := readFirefoxProfiles(firefoxRoot)
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(profiles))
	for _, p := range profiles {
		cookieDB := filepath.Join(p.Path, "cookies.sqlite")
		if _, err := os.Stat(cookieDB); err != nil {
			continue
		}
		db, closeDB, err := openFirefoxCookies(p.Path)
		if err != nil {
			reason := ReasonReadFailed
			if errors.Is(err, errBrowserRunning) || isSharingViolation(err) {
				reason = ReasonBrowserRunning
			}
			accounts = append(accounts, Account{
				Browser:     BrowserFirefox,
				Profile:     p.Name,
				DisplayName: p.Name,
				Email:       p.Name,
				Reason:      reason,
			})
			continue
		}
		rows, err := db.Query("SELECT name FROM moz_cookies WHERE host LIKE '%google.com'")
		if err != nil {
			closeDB()
			accounts = append(accounts, Account{
				Browser:     BrowserFirefox,
				Profile:     p.Name,
				DisplayName: p.Name,
				Email:       p.Name,
				Reason:      ReasonReadFailed,
			})
			continue
		}
		names := make(map[string]bool)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				names[name] = true
			}
		}
		rows.Close()
		closeDB()

		importable := names["SAPISID"] && names["__Secure-1PSID"]
		acc := Account{
			Browser:     BrowserFirefox,
			Profile:     p.Name,
			DisplayName: p.Name,
			Email:       p.Name,
			Importable:  importable,
		}
		if importable {
			acc.SourceType = "cookie"
		} else {
			acc.Reason = "not_signed_in"
		}
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

// readFirefoxCookies 读取特定 Profile 的 Google Cookie
func readFirefoxCookies(firefoxRoot string, profileName string) ([]auth.StateCookie, error) {
	profiles, err := readFirefoxProfiles(firefoxRoot)
	if err != nil {
		return nil, err
	}
	var targetPath string
	for _, p := range profiles {
		if strings.EqualFold(p.Name, profileName) || strings.EqualFold(filepath.Base(p.Path), profileName) {
			targetPath = p.Path
			break
		}
	}
	if targetPath == "" {
		return nil, fmt.Errorf("找不到 Firefox Profile: %s", profileName)
	}

	db, closeDB, err := openFirefoxCookies(targetPath)
	if err != nil {
		return nil, err
	}
	defer closeDB()

	rows, err := db.Query("SELECT name, value, host, path, expiry, isSecure, isHttpOnly, sameSite FROM moz_cookies WHERE host LIKE '%google.com'")
	if err != nil {
		return nil, fmt.Errorf("读取 Firefox moz_cookies: %w", err)
	}
	defer rows.Close()

	var cookies []auth.StateCookie
	for rows.Next() {
		var name, value, host, path string
		var expiry int64
		var isSecure, isHttpOnly int
		var sameSite int
		if err := rows.Scan(&name, &value, &host, &path, &expiry, &isSecure, &isHttpOnly, &sameSite); err != nil {
			return nil, fmt.Errorf("解析 Firefox moz_cookies: %w", err)
		}
		if name == "" || value == "" {
			continue
		}
		sameSiteStr := ""
		switch sameSite {
		case 1:
			sameSiteStr = "Lax"
		case 2:
			sameSiteStr = "Strict"
		}
		cookies = append(cookies, auth.StateCookie{
			Name:     name,
			Value:    value,
			Domain:   host,
			Path:     path,
			Expires:  float64(expiry),
			Secure:   isSecure != 0,
			HTTPOnly: isHttpOnly != 0,
			SameSite: sameSiteStr,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 Firefox moz_cookies: %w", err)
	}
	names := make(map[string]bool)
	for _, c := range cookies {
		names[c.Name] = true
	}
	if !names["SAPISID"] || !names["__Secure-1PSID"] {
		return nil, fmt.Errorf("Firefox %s 缺少核心 Google Cookie (SAPISID/__Secure-1PSID)", profileName)
	}
	return cookies, nil
}
