package config

import (
	"reflect"
	"testing"
	"time"
)

// TestLoad 验证配置解析和模型映射安装
func TestLoad(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "127.0.0.1:9000")
	t.Setenv("GEMINI_AUTH_STATES", "one.json, two.json")
	t.Setenv("ACCOUNT_COOLDOWN", "45s")
	t.Setenv("SESSION_TTL", "10m")
	t.Setenv("INIT_TIMEOUT", "8s")
	t.Setenv("MODEL_MAPPING", "fast:gemini-flash,smart:gemini-pro")
	t.Setenv("GEMINI_SAVE_HISTORY", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddress != "127.0.0.1:9000" {
		t.Fatalf("ListenAddress = %q", cfg.ListenAddress)
	}
	if !reflect.DeepEqual(cfg.AuthStatePaths, []string{"one.json", "two.json"}) {
		t.Fatalf("AuthStatePaths = %#v", cfg.AuthStatePaths)
	}
	if cfg.AccountCooldown != 45*time.Second || cfg.SessionTTL != 10*time.Minute || cfg.InitTimeout != 8*time.Second {
		t.Fatalf("durations = %v %v %v", cfg.AccountCooldown, cfg.SessionTTL, cfg.InitTimeout)
	}
	if !cfg.SaveHistory {
		t.Fatal("SaveHistory = false")
	}
	if MapModel("fast") != "gemini-flash" || MapModel("missing") != "missing" {
		t.Fatalf("model mapping was not installed")
	}
}

// TestLoadRejectsInvalidDuration 验证非法时长会阻止启动
func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("ACCOUNT_COOLDOWN", "soon")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected an error")
	}
}

// TestLoadRejectsInvalidMapping 验证非法模型映射会阻止启动
func TestLoadRejectsInvalidMapping(t *testing.T) {
	t.Setenv("MODEL_MAPPING", "invalid")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected an error")
	}
}
