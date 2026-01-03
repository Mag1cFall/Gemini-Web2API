package browser

import (
	"fmt"
	"os"
	"strings"
)

func saveToEnvWithOrder(cookieKeys []string, cookies map[string]string) {
	content, err := os.ReadFile(".env")
	lines := []string{}

	if err == nil {
		lines = strings.Split(string(content), "\n")
	}

	var newLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			newLines = append(newLines, line)
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			if !strings.HasPrefix(key, "__Secure-1PSID") {
				newLines = append(newLines, line)
			}
		} else {
			newLines = append(newLines, line)
		}
	}

	for _, key := range cookieKeys {
		if val, ok := cookies[key]; ok {
			newLines = append(newLines, fmt.Sprintf("%s=%s", key, val))
		}
	}

	finalContent := strings.Join(newLines, "\n")
	if !strings.HasSuffix(finalContent, "\n") {
		finalContent += "\n"
	}

	_ = os.WriteFile(".env", []byte(finalContent), 0644)
	fmt.Println("Cookies saved to .env file.")
}
