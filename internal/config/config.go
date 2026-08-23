package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultListenAddress = "127.0.0.1:8007"
	defaultCooldown      = 2 * time.Minute
	defaultSessionTTL    = 30 * time.Minute
	defaultInitTimeout   = 20 * time.Second
)

// Config 保存服务启动所需的全部配置
type Config struct {
	ListenAddress   string
	AuthStatePaths  []string
	ProxyAPIKey     string
	ProxyURL        string
	AccountCooldown time.Duration
	SessionTTL      time.Duration
	InitTimeout     time.Duration
	ModelMapping    map[string]string
	SaveHistory     bool
}

var (
	modelMapping = map[string]string{}
	mappingMu    sync.RWMutex
)

// Load 从环境变量读取并校验配置
func Load() (Config, error) {
	cfg := Config{
		ListenAddress:   envOrDefault("LISTEN_ADDR", defaultListenAddress),
		AuthStatePaths:  splitList(os.Getenv("GEMINI_AUTH_STATES")),
		ProxyAPIKey:     strings.TrimSpace(os.Getenv("PROXY_API_KEY")),
		ProxyURL:        strings.TrimSpace(os.Getenv("PROXY")),
		AccountCooldown: defaultCooldown,
		SessionTTL:      defaultSessionTTL,
		InitTimeout:     defaultInitTimeout,
	}
	if raw := strings.TrimSpace(os.Getenv("GEMINI_SAVE_HISTORY")); raw != "" {
		saveHistory, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("GEMINI_SAVE_HISTORY 必须是 true 或 false")
		}
		cfg.SaveHistory = saveHistory
	}

	var err error
	cfg.AccountCooldown, err = parseDuration("ACCOUNT_COOLDOWN", cfg.AccountCooldown)
	if err != nil {
		return Config{}, err
	}
	cfg.SessionTTL, err = parseDuration("SESSION_TTL", cfg.SessionTTL)
	if err != nil {
		return Config{}, err
	}
	cfg.InitTimeout, err = parseDuration("INIT_TIMEOUT", cfg.InitTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.ModelMapping, err = parseModelMapping(os.Getenv("MODEL_MAPPING"))
	if err != nil {
		return Config{}, err
	}

	SetModelMapping(cfg.ModelMapping)
	return cfg, nil
}

// SetModelMapping 替换当前模型别名映射
func SetModelMapping(mapping map[string]string) {
	mappingMu.Lock()
	defer mappingMu.Unlock()

	modelMapping = make(map[string]string, len(mapping))
	for source, target := range mapping {
		modelMapping[source] = target
	}
}

// MapModel 返回别名对应的真实模型标识
func MapModel(model string) string {
	mappingMu.RLock()
	defer mappingMu.RUnlock()

	if mapped, ok := modelMapping[model]; ok {
		return mapped
	}
	return model
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func parseDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s 必须是正数时长，例如 30s 或 2m", key)
	}
	return duration, nil
}

func parseModelMapping(value string) (map[string]string, error) {
	mapping := make(map[string]string)
	for _, pair := range splitList(value) {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("MODEL_MAPPING 项 %q 缺少冒号", pair)
		}

		source := strings.TrimSpace(parts[0])
		target := strings.TrimSpace(parts[1])
		if source == "" || target == "" {
			return nil, fmt.Errorf("MODEL_MAPPING 项 %q 的模型名不能为空", pair)
		}
		mapping[source] = target
	}
	return mapping, nil
}
