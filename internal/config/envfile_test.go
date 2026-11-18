package config

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

// TestWriteEnvRoundTrip 验证写回的值经 godotenv 读回不变，已有行原位替换、保留换行风格，新键追加在末尾
func TestWriteEnvRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original := "# 注释\r\nPROXY=\r\nexport LOG_LEVEL=info\r\nOTHER=1\r\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"PROXY": "socks5://user:p#ss@127.0.0.1:1080", "LOG_LEVEL": "debug", "PROXY_API_KEY": `a "quoted" \ key`, "EMPTY": "",
	}
	if err := WriteEnv(path, values); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSuffix(string(data), "\r\n"), "\r\n")
	if strings.Count(string(data), "\n") != strings.Count(string(data), "\r\n") || len(lines) != 6 || lines[0] != "# 注释" ||
		!strings.HasPrefix(lines[1], "PROXY=") || !strings.Contains(lines[2], "LOG_LEVEL=") || lines[3] != "OTHER=1" {
		t.Fatalf("file = %q", data)
	}
	parsed, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range values {
		if parsed[key] != value {
			t.Fatalf("%s = %q, want %q", key, parsed[key], value)
		}
	}
	if parsed["OTHER"] != "1" {
		t.Fatalf("OTHER = %q", parsed["OTHER"])
	}
}

// TestWriteEnvKeepsDollarSigns 验证含 $ 的密码、key 与代理写回后经 godotenv 读回原值，不被展开为环境变量
func TestWriteEnvKeepsDollarSigns(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	values := map[string]string{
		"ADMIN_PASSWORD": "Secret$2026", "PROXY_API_KEY": "sk-$ABC_def", "PROXY": "http://user:p$1@127.0.0.1:7890",
		"ADMIN_USERNAME": "${HOME}", "MODEL_MAPPING": `a\$B`, "LOG_LEVEL": "tail$",
	}
	if err := WriteEnv(path, values); err != nil {
		t.Fatal(err)
	}
	parsed, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if parsed[key] != values[key] {
			t.Errorf("%s = %q, want %q", key, parsed[key], values[key])
		}
	}
}
