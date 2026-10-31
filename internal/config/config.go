package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MinLogTotalMB 是日志目录总量上限的最小值，容纳当前文件与一个轮转文件
const MinLogTotalMB = 20

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
	LogLevel        string
	LogFormat       string
	RequestBodyLog  bool
	// LogFile 为 true 时日志同时写入数据目录的轮转文件，LogMaxTotalMB 是日志目录总量上限
	LogFile       bool
	LogMaxTotalMB int
	// PerAccountConcurrency 是每个账号同时进行的上游请求数
	PerAccountConcurrency int
	// RoutingStrategy 为 round-robin 或 fill-first
	RoutingStrategy string
	// AccountFallback 为 true 时遇到模型用量上限或错误自动换号重试
	AccountFallback bool
	// AdminAuthEnabled 为 true 时管理端要求登录并接受远程访问
	AdminAuthEnabled bool
	AdminUsername    string
	AdminPassword    string
}

var (
	modelMapping = map[string]string{}
	mappingMu    sync.RWMutex
)

// Load 从环境变量读取并校验配置
func Load() (Config, error) {
	cfg := Config{
		ListenAddress:   envOrDefault("LISTEN_ADDR", defaultListenAddress),
		AuthStatePaths:  SplitList(os.Getenv("GEMINI_AUTH_STATES")),
		ProxyAPIKey:     strings.TrimSpace(os.Getenv("PROXY_API_KEY")),
		ProxyURL:        strings.TrimSpace(os.Getenv("PROXY")),
		AccountCooldown: defaultCooldown,
		SessionTTL:      defaultSessionTTL,
		InitTimeout:     defaultInitTimeout,
		LogLevel:        envOrDefault("LOG_LEVEL", "info"),
		LogFormat:       envOrDefault("LOG_FORMAT", "text"),
	}
	var err error
	if cfg.SaveHistory, err = parseBool("GEMINI_SAVE_HISTORY"); err != nil {
		return Config{}, err
	}
	if cfg.RequestBodyLog, err = parseBool("REQUEST_BODY_LOG"); err != nil {
		return Config{}, err
	}
	if cfg.AdminAuthEnabled, err = parseBool("ADMIN_AUTH_ENABLED"); err != nil {
		return Config{}, err
	}
	cfg.LogFile = true
	if strings.TrimSpace(os.Getenv("LOG_FILE")) != "" {
		if cfg.LogFile, err = parseBool("LOG_FILE"); err != nil {
			return Config{}, err
		}
	}
	if cfg.LogMaxTotalMB, err = parsePositiveInt("LOG_MAX_TOTAL_MB", 500); err != nil {
		return Config{}, err
	}
	if cfg.LogMaxTotalMB < MinLogTotalMB {
		return Config{}, fmt.Errorf("LOG_MAX_TOTAL_MB 不能小于 %d", MinLogTotalMB)
	}
	if cfg.PerAccountConcurrency, err = parsePositiveInt("PER_ACCOUNT_CONCURRENCY", 1); err != nil {
		return Config{}, err
	}
	cfg.RoutingStrategy = strings.ToLower(envOrDefault("ROUTING_STRATEGY", "fill-first"))
	if cfg.RoutingStrategy != "round-robin" && cfg.RoutingStrategy != "fill-first" {
		return Config{}, fmt.Errorf("ROUTING_STRATEGY 取 round-robin 或 fill-first")
	}
	cfg.AccountFallback = true
	if strings.TrimSpace(os.Getenv("ACCOUNT_FALLBACK")) != "" {
		if cfg.AccountFallback, err = parseBool("ACCOUNT_FALLBACK"); err != nil {
			return Config{}, err
		}
	}
	cfg.AdminUsername = envOrDefault("ADMIN_USERNAME", "admin")
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	if cfg.AdminAuthEnabled && cfg.AdminPassword == "" {
		return Config{}, fmt.Errorf("ADMIN_AUTH_ENABLED=true 时必须设置 ADMIN_PASSWORD")
	}
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
	cfg.ModelMapping, err = ParseModelMapping(os.Getenv("MODEL_MAPPING"))
	if err != nil {
		return Config{}, err
	}

	SetModelMapping(cfg.ModelMapping)
	return cfg, nil
}

// parsePositiveInt 读取正整数环境变量，未设置时为 fallback
func parsePositiveInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s 必须是正整数", key)
	}
	return value, nil
}

// parseBool 读取布尔环境变量，未设置时为 false
func parseBool(key string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s 必须是 true 或 false", key)
	}
	return value, nil
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

// ModelAliases 返回当前模型别名映射的副本
func ModelAliases() map[string]string {
	mappingMu.RLock()
	defer mappingMu.RUnlock()
	aliases := make(map[string]string, len(modelMapping))
	for source, target := range modelMapping {
		aliases[source] = target
	}
	return aliases
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

// FormatModelMapping 按别名排序输出 MODEL_MAPPING 格式的映射
func FormatModelMapping(mapping map[string]string) string {
	aliases := make([]string, 0, len(mapping))
	for alias := range mapping {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	pairs := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		pairs = append(pairs, alias+":"+mapping[alias])
	}
	return strings.Join(pairs, ",")
}

// SplitList 按逗号拆分列表配置，去掉各项首尾空白与空项
func SplitList(value string) []string {
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

// ParseModelMapping 解析逗号分隔的“别名:模型 ID”映射
func ParseModelMapping(value string) (map[string]string, error) {
	mapping := make(map[string]string)
	for _, pair := range SplitList(value) {
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
