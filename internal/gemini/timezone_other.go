//go:build !windows

package gemini

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// localTimezone 返回本机的 IANA 时区名，依次取 TZ、/etc/localtime 的链接目标与 /etc/timezone
func localTimezone() string {
	if zone := ianaTimezone(os.Getenv("TZ")); zone != "" {
		return zone
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, name, found := strings.Cut(filepath.ToSlash(target), "/zoneinfo/"); found {
			if zone := ianaTimezone(name); zone != "" {
				return zone
			}
		}
	}
	if value, err := os.ReadFile("/etc/timezone"); err == nil {
		if zone := ianaTimezone(string(value)); zone != "" {
			return zone
		}
	}
	return fallbackTimezone
}

// ianaTimezone 返回 value 中可加载的 IANA 时区名，不是时区名时返回空
func ianaTimezone(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, ":"))
	if value == "" {
		return ""
	}
	if _, err := time.LoadLocation(value); err != nil {
		return ""
	}
	return value
}
