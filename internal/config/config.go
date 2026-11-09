package config

import (
	"cmp"
	"fmt"
	"maps"
	"net"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MinLogTotalMB 是日志目录总量上限的最小值，容纳当前文件与一个轮转文件
const MinLogTotalMB = 20

// Defaults 是 .env 全部配置项的默认值写法，与默认值相同的项不写入 .env
var Defaults = map[string]string{
	"LISTEN_ADDR": "127.0.0.1:8007", "GEMINI_AUTH_STATES": "auth", "PROXY_API_KEY": "", "PROXY": "",
	"ACCOUNT_COOLDOWN": "2m", "SESSION_TTL": "30m", "INIT_TIMEOUT": "20s", "MODEL_MAPPING": "", "GEMINI_SAVE_HISTORY": "false",
	"LOG_LEVEL": "info", "LOG_FORMAT": "text", "REQUEST_BODY_LOG": "false", "LOG_FILE": "true", "LOG_MAX_TOTAL_MB": "500",
	"PER_ACCOUNT_CONCURRENCY": "1", "ROUTING_STRATEGY": "round-robin", "ADMIN_AUTH_ENABLED": "false", "ADMIN_USERNAME": "admin",
	"ADMIN_PASSWORD": "", "API_KEY_ACCOUNTS": "", "FIRST_EVENT_HEDGE": "auto", "FIRST_EVENT_HEDGE_MODELS": "gemini-4-argon",
	"NONSTREAM_KEEPALIVE": "true", "FAKE_STREAM": "false",
}

// InvalidValue 是加载时取值无效、改用默认值的配置项，含 API key 的 API_KEY_ACCOUNTS 不记 Value
type InvalidValue struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

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
	// NonstreamKeepalive 为真时非流式请求等待结果期间每 10 秒写出一个换行
	NonstreamKeepalive bool
	// FakeStream 为真时流式请求收齐完整结果后再按流式输出
	FakeStream     bool
	LogLevel       string
	LogFormat      string
	RequestBodyLog bool
	// LogFile 为 true 时日志同时写入数据目录的轮转文件，LogMaxTotalMB 是日志目录总量上限
	LogFile       bool
	LogMaxTotalMB int
	// PerAccountConcurrency 是每个账号同时进行的上游请求数
	PerAccountConcurrency int
	// RoutingStrategy 为 round-robin 或 fill-first
	RoutingStrategy string
	// AdminAuthEnabled 为 true 时管理端要求登录并接受远程访问
	AdminAuthEnabled bool
	AdminUsername    string
	AdminPassword    string
	// APIKeyAccounts 是各 API key 可用的账号 ID，未列出的 key 可用全部账号
	APIKeyAccounts map[string][]string
	// FirstEventHedge 是首事件备用请求方式：off、auto 或等待秒数
	FirstEventHedge string
	// FirstEventHedgeModels 是发起首事件备用请求的模型 ID
	FirstEventHedgeModels []string
	// Invalid 是加载时取值无效、改用默认值启动的配置项
	Invalid []InvalidValue
	// Locked 是由进程环境变量（env）或启动参数（flag）决定、设置页不能修改的配置项
	Locked map[string]string
}

var (
	modelMapping = map[string]string{}
	mappingMu    sync.RWMutex
)

// Load 从环境变量读取配置，取值无效的项改用默认值并记入 Invalid
func Load() Config {
	cfg := Config{
		ListenAddress:         envOrDefault("LISTEN_ADDR"),
		AuthStatePaths:        SplitList(os.Getenv("GEMINI_AUTH_STATES")),
		ProxyAPIKey:           strings.TrimSpace(os.Getenv("PROXY_API_KEY")),
		ProxyURL:              strings.TrimSpace(os.Getenv("PROXY")),
		LogLevel:              strings.ToLower(envOrDefault("LOG_LEVEL")),
		LogFormat:             strings.ToLower(envOrDefault("LOG_FORMAT")),
		RoutingStrategy:       strings.ToLower(envOrDefault("ROUTING_STRATEGY")),
		AdminUsername:         envOrDefault("ADMIN_USERNAME"),
		AdminPassword:         os.Getenv("ADMIN_PASSWORD"),
		FirstEventHedge:       strings.ToLower(envOrDefault("FIRST_EVENT_HEDGE")),
		FirstEventHedgeModels: SplitList(envOrDefault("FIRST_EVENT_HEDGE_MODELS")),
	}
	reject := func(key string, reason string) {
		value := strings.TrimSpace(os.Getenv(key))
		if key == "API_KEY_ACCOUNTS" {
			value = ""
		}
		cfg.Invalid = append(cfg.Invalid, InvalidValue{Key: key, Value: value, Reason: reason})
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddress); err != nil {
		reject("LISTEN_ADDR", "必须是 主机:端口")
		cfg.ListenAddress = Defaults["LISTEN_ADDR"]
	}
	for key, target := range map[string]*bool{
		"GEMINI_SAVE_HISTORY": &cfg.SaveHistory, "REQUEST_BODY_LOG": &cfg.RequestBodyLog,
		"ADMIN_AUTH_ENABLED": &cfg.AdminAuthEnabled, "LOG_FILE": &cfg.LogFile,
		"NONSTREAM_KEEPALIVE": &cfg.NonstreamKeepalive, "FAKE_STREAM": &cfg.FakeStream,
	} {
		*target, _ = strconv.ParseBool(Defaults[key])
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				reject(key, "必须是 true 或 false")
				continue
			}
			*target = value
		}
	}
	for key, target := range map[string]*int{"LOG_MAX_TOTAL_MB": &cfg.LogMaxTotalMB, "PER_ACCOUNT_CONCURRENCY": &cfg.PerAccountConcurrency} {
		*target, _ = strconv.Atoi(Defaults[key])
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				reject(key, "必须是正整数")
				continue
			}
			*target = value
		}
	}
	if cfg.LogMaxTotalMB < MinLogTotalMB {
		reject("LOG_MAX_TOTAL_MB", fmt.Sprintf("不能小于 %d", MinLogTotalMB))
		cfg.LogMaxTotalMB, _ = strconv.Atoi(Defaults["LOG_MAX_TOTAL_MB"])
	}
	for key, target := range map[string]*time.Duration{
		"ACCOUNT_COOLDOWN": &cfg.AccountCooldown, "SESSION_TTL": &cfg.SessionTTL, "INIT_TIMEOUT": &cfg.InitTimeout,
	} {
		*target, _ = time.ParseDuration(Defaults[key])
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 {
				reject(key, "必须是正数时长，例如 30s 或 2m")
				continue
			}
			*target = value
		}
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, cfg.LogLevel) {
		reject("LOG_LEVEL", "取 debug、info、warn 或 error")
		cfg.LogLevel = Defaults["LOG_LEVEL"]
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		reject("LOG_FORMAT", "取 text 或 json")
		cfg.LogFormat = Defaults["LOG_FORMAT"]
	}
	if cfg.RoutingStrategy != "round-robin" && cfg.RoutingStrategy != "fill-first" {
		reject("ROUTING_STRATEGY", "取 round-robin 或 fill-first")
		cfg.RoutingStrategy = Defaults["ROUTING_STRATEGY"]
	}
	if cfg.AdminAuthEnabled && cfg.AdminPassword == "" {
		reject("ADMIN_AUTH_ENABLED", "开启管理登录前先设置 ADMIN_PASSWORD")
		cfg.AdminAuthEnabled = false
	}
	if err := ValidateFirstEventHedge(cfg.FirstEventHedge); err != nil {
		reject("FIRST_EVENT_HEDGE", err.Error())
		cfg.FirstEventHedge = Defaults["FIRST_EVENT_HEDGE"]
	}
	bindings, err := ParseAPIKeyAccounts(os.Getenv("API_KEY_ACCOUNTS"), cfg.ProxyAPIKey)
	if err != nil {
		reject("API_KEY_ACCOUNTS", err.Error())
	}
	cfg.APIKeyAccounts = bindings
	mapping, err := ParseModelMapping(os.Getenv("MODEL_MAPPING"))
	if err != nil {
		reject("MODEL_MAPPING", err.Error())
	}
	cfg.ModelMapping = mapping
	slices.SortFunc(cfg.Invalid, func(a, b InvalidValue) int { return strings.Compare(a.Key, b.Key) })

	SetModelMapping(cfg.ModelMapping)
	return cfg
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

func envOrDefault(key string) string {
	return cmp.Or(strings.TrimSpace(os.Getenv(key)), Defaults[key])
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

// ValidateFirstEventHedge 检查首事件备用请求方式是 off、auto 或正整数秒
func ValidateFirstEventHedge(value string) error {
	if value == "off" || value == "auto" {
		return nil
	}
	if seconds, err := strconv.Atoi(value); err != nil || seconds < 1 {
		return fmt.Errorf("取 off、auto 或正整数秒")
	}
	return nil
}

// ParseAPIKeyAccounts 解析逗号分隔的“API key:账号|账号”绑定，跳过不在 apiKeys 中的 key，无效项与重复的 key 跳过并返回错误，其余绑定照常返回
func ParseAPIKeyAccounts(value string, apiKeys string) (map[string][]string, error) {
	keys := SplitList(apiKeys)
	accounts := make(map[string][]string)
	var problem error
	for _, item := range SplitList(value) {
		index := strings.LastIndexByte(item, ':')
		key := strings.TrimSpace(item[:max(index, 0)])
		var ids []string
		for id := range strings.SplitSeq(item[index+1:], "|") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if index < 0 || key == "" || len(ids) == 0 {
			problem = fmt.Errorf("每一项写成 API key:账号|账号")
			continue
		}
		if !slices.Contains(keys, key) {
			continue
		}
		if _, exists := accounts[key]; exists {
			problem = fmt.Errorf("同一 key 重复")
			continue
		}
		accounts[key] = ids
	}
	return accounts, problem
}

// FormatAPIKeyAccounts 把 API key 绑定写成 .env 的取值，按 key 排序
func FormatAPIKeyAccounts(accounts map[string][]string) string {
	items := make([]string, 0, len(accounts))
	for _, key := range slices.Sorted(maps.Keys(accounts)) {
		items = append(items, key+":"+strings.Join(accounts[key], "|"))
	}
	return strings.Join(items, ",")
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
