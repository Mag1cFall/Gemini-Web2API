package config

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// envKeyPattern 匹配 .env 中未注释的赋值行并取出键名
var envKeyPattern = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// WriteEnv 把键值写入 .env：已有键原位替换，新键追加到末尾，与默认值相同的键删去，其余行与注释保持不变
func WriteEnv(path string, values map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	written := make(map[string]bool, len(values))
	kept := lines[:0]
	for _, line := range lines {
		match := envKeyPattern.FindStringSubmatch(line)
		if match != nil {
			if value, ok := values[match[1]]; ok {
				if written[match[1]] || isDefault(match[1], value) {
					continue
				}
				line = match[1] + "=" + quoteEnv(value)
				written[match[1]] = true
			}
		}
		kept = append(kept, line)
	}
	lines = kept
	for _, key := range sortedKeys(values) {
		if !written[key] && !isDefault(key, values[key]) {
			lines = append(lines, key+"="+quoteEnv(values[key]))
		}
	}
	output := strings.Join(lines, newline) + newline
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(output), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// isDefault 报告取值是否与该配置项的默认值相同
func isDefault(key string, value string) bool {
	fallback, ok := Defaults[key]
	return ok && fallback == value
}

// quoteEnv 在值含空白、引号、反斜杠、# 或 $ 时加双引号转义，$ 转义后读回时不展开为变量
func quoteEnv(value string) string {
	if !strings.ContainsAny(value, " \t#\"'\\\n$") {
		return value
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, `$`, `\$`)
	return `"` + replacer.Replace(value) + `"`
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
