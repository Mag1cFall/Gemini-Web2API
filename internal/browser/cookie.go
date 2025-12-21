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

func LoadCookies() (map[string]string, error) {
	cookies := make(map[string]string)

	if content, err := os.ReadFile(".env"); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "__Secure-1PSID=") {
				cookies["__Secure-1PSID"] = strings.TrimPrefix(line, "__Secure-1PSID=")
			} else if strings.HasPrefix(line, "__Secure-1PSIDTS=") {
				cookies["__Secure-1PSIDTS"] = strings.TrimPrefix(line, "__Secure-1PSIDTS=")
			}
		}
		if val, ok := cookies["__Secure-1PSID"]; ok && val != "" {
			return cookies, nil
		}
	}

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
		foundCookies, err = kooky.ReadCookies(context.Background())
	}

	if err != nil {
		fmt.Printf("Warning: Firefox lookup had issues: %v\n", err)
		if os.PathSeparator == '\\' {
			fmt.Println("Tip: Ensure Firefox is installed and you have visited google.com recently.")
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
		return nil, fmt.Errorf("cookie '__Secure-1PSID' not found in env or Firefox. Please create a .env file")
	}

	saveToEnv(cookies)

	return cookies, nil
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
