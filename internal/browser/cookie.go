package browser

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/firefox"
)

func LoadCookies() (map[string]string, error) {
	cookies := make(map[string]string)

	p1 := os.Getenv("__Secure-1PSID")
	p2 := os.Getenv("__Secure-1PSIDTS")

	if p1 != "" {
		cookies["__Secure-1PSID"] = p1
		if p2 != "" {
			cookies["__Secure-1PSIDTS"] = p2
		}
		return cookies, nil
	}

	fmt.Println("Attempting to read cookies from Firefox...")

	foundCookies, err := kooky.ReadCookies(context.Background(), kooky.Domain("google.com"))
	if err != nil {
		fmt.Printf("Warning: Firefox lookup had issues: %v\n", err)
		if os.PathSeparator == '\\' {
			fmt.Println("Tip: Ensure Firefox is installed and you have visited google.com recently.")
		}
	}

	for _, c := range foundCookies {
		if c.Name == "__Secure-1PSID" || c.Name == "__Secure-1PSIDTS" {
			cookies[c.Name] = c.Value
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
