package browser

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/browserutils/kooky"
	"github.com/browserutils/kooky/browser/firefox"
)

func findFirefoxCookiesDB() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	profilesDir := filepath.Join(appData, "Mozilla", "Firefox", "Profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			cookiesPath := filepath.Join(profilesDir, entry.Name(), "cookies.sqlite")
			if _, err := os.Stat(cookiesPath); err == nil {
				return cookiesPath
			}
		}
	}
	return ""
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	_, err = io.Copy(dstFile, srcFile)
	return err
}

func loadCookiesFromBrowser() (map[string]string, error) {
	cookies := make(map[string]string)

	fmt.Println("Attempting to read cookies from Firefox...")

	var foundCookies []*kooky.Cookie
	var err error

	cookiesDB := findFirefoxCookiesDB()
	if cookiesDB != "" {
		fmt.Printf("Found Firefox cookies at: %s\n", cookiesDB)
		tmpFile := filepath.Join(os.TempDir(), "gemini_cookies_tmp.sqlite")
		if copyErr := copyFile(cookiesDB, tmpFile); copyErr != nil {
			fmt.Printf("Warning: Could not copy cookies file: %v\n", copyErr)
			foundCookies, err = firefox.ReadCookies(context.Background(), cookiesDB)
		} else {
			defer os.Remove(tmpFile)
			foundCookies, err = firefox.ReadCookies(context.Background(), tmpFile)
		}
	} else {
		fmt.Println("Firefox profile not found, trying all browsers...")
		foundCookies, err = kooky.ReadCookies(context.Background())
	}

	if err != nil {
		fmt.Printf("Warning: Browser cookie lookup had issues: %v\n", err)
		if os.PathSeparator == '\\' {
			fmt.Println("Tip: Ensure Firefox/Chrome/Edge is installed and you have visited google.com recently.")
			fmt.Println("Tip: Make sure the browser is closed when running this program.")
		}
	}

	for _, c := range foundCookies {
		if c.Name == "__Secure-1PSID" || c.Name == "__Secure-1PSIDTS" {
			if strings.Contains(c.Domain, "google.com") {
				cookies[c.Name] = c.Value
			}
		}
	}

	if val, ok := cookies["__Secure-1PSID"]; !ok || val == "" {
		return nil, fmt.Errorf("cookie '__Secure-1PSID' not found in browser. Please ensure you are logged into Google in your browser")
	}

	return cookies, nil
}

func LoadMultiCookies(accountIDs []string) ([]map[string]string, []string, error) {
	var results []map[string]string
	var usedIDs []string

	envMap := make(map[string]string)

	content, err := os.ReadFile(".env")
	if err != nil {
		fmt.Println(".env file not found, attempting to auto-detect cookies from browser...")
		cookies, browserErr := loadCookiesFromBrowser()
		if browserErr != nil {
			createEnvTemplate()
			return nil, nil, fmt.Errorf("failed to auto-detect cookies: %v. A template .env file has been created", browserErr)
		}
		saveToEnv(cookies)
		results = append(results, cookies)
		usedIDs = append(usedIDs, "")
		fmt.Println("Auto-detected cookies from browser and saved to .env")
		return results, usedIDs, nil
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			envMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	if len(accountIDs) == 0 || (len(accountIDs) == 1 && accountIDs[0] == "") {
		accountIDs = []string{}
		if envMap["__Secure-1PSID"] != "" {
			accountIDs = append(accountIDs, "")
		}
		for key := range envMap {
			if strings.HasPrefix(key, "__Secure-1PSID_") && key != "__Secure-1PSID" {
				suffix := strings.TrimPrefix(key, "__Secure-1PSID_")
				accountIDs = append(accountIDs, suffix)
			}
		}
	}

	if len(accountIDs) == 0 {
		fmt.Println("No accounts configured in .env, attempting to auto-detect cookies from browser...")
		cookies, browserErr := loadCookiesFromBrowser()
		if browserErr != nil {
			return nil, nil, fmt.Errorf("no accounts in .env and failed to auto-detect: %v", browserErr)
		}
		saveToEnv(cookies)
		results = append(results, cookies)
		usedIDs = append(usedIDs, "")
		fmt.Println("Auto-detected cookies from browser and saved to .env")
		return results, usedIDs, nil
	}

	fmt.Printf("Auto-detected accounts: %v\n", accountIDs)

	for _, id := range accountIDs {
		var psidKey, psidtsKey string
		if id == "" {
			psidKey = "__Secure-1PSID"
			psidtsKey = "__Secure-1PSIDTS"
		} else {
			psidKey = fmt.Sprintf("__Secure-1PSID_%s", id)
			psidtsKey = fmt.Sprintf("__Secure-1PSIDTS_%s", id)
		}

		psid := envMap[psidKey]
		psidts := envMap[psidtsKey]

		if psid == "" {
			displayID := id
			if displayID == "" {
				displayID = "default"
			}
			fmt.Printf("Warning: Account '%s' missing %s, skipped\n", displayID, psidKey)
			continue
		}

		cookies := map[string]string{
			"__Secure-1PSID":   psid,
			"__Secure-1PSIDTS": psidts,
		}
		results = append(results, cookies)
		usedIDs = append(usedIDs, id)
		displayID := id
		if displayID == "" {
			displayID = "default"
		}
		fmt.Printf("Loaded account '%s' cookies\n", displayID)
	}

	if len(results) == 0 {
		return nil, nil, fmt.Errorf("no valid accounts found in .env")
	}

	return results, usedIDs, nil
}

func createEnvTemplate() {
	template := "__Secure-1PSID=\n__Secure-1PSIDTS=\nACCOUNTS=\nPROXY_API_KEY=\nPORT=8007\n"
	err := os.WriteFile(".env", []byte(template), 0644)
	if err != nil {
		fmt.Printf("Warning: Failed to create .env template: %v\n", err)
	} else {
		fmt.Println("Created .env template file.")
	}
}

func ParseAccountIDs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{""}
	}

	s = strings.Trim(s, "[]{}")
	parts := strings.Split(s, ",")

	var ids []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			ids = append(ids, p)
		}
	}

	if len(ids) == 0 {
		return []string{""}
	}
	return ids
}

func saveToEnv(cookies map[string]string) {
	content, err := os.ReadFile(".env")
	envMap := make(map[string]string)
	lines := []string{}

	if err == nil {
		lines = strings.Split(string(content), "\n")
		for _, line := range lines {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				envMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	for k, v := range cookies {
		envMap[k] = v
	}

	var newLines []string
	processedKeys := make(map[string]bool)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			newLines = append(newLines, line)
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			if val, ok := envMap[key]; ok {
				newLines = append(newLines, fmt.Sprintf("%s=%s", key, val))
				processedKeys[key] = true
			} else {
				newLines = append(newLines, line)
			}
		} else {
			newLines = append(newLines, line)
		}
	}

	for k, v := range envMap {
		if !processedKeys[k] {
			newLines = append(newLines, fmt.Sprintf("%s=%s", k, v))
		}
	}

	finalContent := strings.Join(newLines, "\n")
	if !strings.HasSuffix(finalContent, "\n") {
		finalContent += "\n"
	}

	_ = os.WriteFile(".env", []byte(finalContent), 0644)
	fmt.Println("Cookies saved to .env file.")
}
