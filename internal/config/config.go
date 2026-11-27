package config

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MinLogTotalMB 是日志目录总量上限的最小值，容纳当前文件与一个轮转文件
const MinLogTotalMB = 20

// PredefinedGemsSelector 在 HIDDEN_MODELS 中表示官网 CNgdBe 类型 3 的全部 Google 预置 Gem
const PredefinedGemsSelector = "predefined-gems"

// Defaults 是 .env 全部配置项的默认值写法，与默认值相同的项不写入 .env
var Defaults = map[string]string{
	"LISTEN_ADDR": "127.0.0.1:8007", "GEMINI_AUTH_STATES": "auth", "PROXY_API_KEY": "", "PROXY": "",
	"ACCOUNT_COOLDOWN": "2m", "SESSION_TTL": "30m", "INIT_TIMEOUT": "20s", "MODEL_MAPPING": "", "HIDDEN_MODELS": "gemini-canvas,gemini-guided-learning," + PredefinedGemsSelector,
	"GEMINI_SAVE_HISTORY": "false", "GEMINI_CONTINUE_SESSION": "false",
	"LOG_LEVEL": "info", "LOG_FORMAT": "text", "REQUEST_BODY_LOG": "false", "LOG_FILE": "true", "LOG_MAX_TOTAL_MB": "500",
	"PER_ACCOUNT_CONCURRENCY": "1", "ROUTING_STRATEGY": "round-robin", "ADMIN_AUTH_ENABLED": "false", "ADMIN_USERNAME": "admin",
	"ADMIN_PASSWORD": "", "API_KEY_ACCOUNTS": "", "FIRST_EVENT_HEDGE": "auto", "FIRST_EVENT_HEDGE_MODELS": "gemini-4-carbon",
	"NONSTREAM_KEEPALIVE": "true", "FAKE_STREAM": "false",
	"ACCOUNT_ORDER": "", "MODEL_ACCOUNTS": "",
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
	// HiddenModels 是在公开 API 中关闭的模型 ID 或 predefined-gems 列表
	HiddenModels []string
	SaveHistory  bool
	// ContinueSession 为真时后续请求续接已登记的网页会话，为假时每个请求新建网页会话
	ContinueSession bool
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
	// AccountOrder 是 fill-first 依次使用的账号 ID
	AccountOrder []string
	// ModelAccounts 是各模型可用的账号 ID，未列出的模型可用全部账号
	ModelAccounts map[string][]string
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

	hiddenMu        sync.RWMutex
	hiddenModelList []string
	hiddenModelSet  map[string]bool
	hidePredefined  bool
)

func init() {
	SetHiddenModels(NormalizeHiddenModels(Defaults["HIDDEN_MODELS"]))
}

// Load 从环境变量读取配置，取值无效的项改用默认值并记入 Invalid
func Load() Config {
	cfg := Config{
		AuthStatePaths:        SplitList(os.Getenv("GEMINI_AUTH_STATES")),
		ProxyAPIKey:           strings.TrimSpace(os.Getenv("PROXY_API_KEY")),
		ProxyURL:              strings.TrimSpace(os.Getenv("PROXY")),
		AdminUsername:         envOrDefault("ADMIN_USERNAME"),
		AdminPassword:         os.Getenv("ADMIN_PASSWORD"),
		AccountOrder:          SplitList(os.Getenv("ACCOUNT_ORDER")),
		FirstEventHedgeModels: SplitList(envOrDefault("FIRST_EVENT_HEDGE_MODELS")),
	}
	reject := func(key string, reason string) {
		value := strings.TrimSpace(os.Getenv(key))
		if key == "API_KEY_ACCOUNTS" {
			value = ""
		}
		cfg.Invalid = append(cfg.Invalid, InvalidValue{Key: key, Value: value, Reason: reason})
	}
	value := func(key string) string {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			return Defaults[key]
		}
		normalized, err := Validate(key, raw)
		if err != nil {
			reject(key, err.Error())
			return Defaults[key]
		}
		return normalized
	}
	cfg.ListenAddress = value("LISTEN_ADDR")
	cfg.LogLevel = value("LOG_LEVEL")
	cfg.LogFormat = value("LOG_FORMAT")
	cfg.RoutingStrategy = value("ROUTING_STRATEGY")
	cfg.FirstEventHedge = value("FIRST_EVENT_HEDGE")
	for key, target := range map[string]*bool{
		"GEMINI_SAVE_HISTORY": &cfg.SaveHistory, "GEMINI_CONTINUE_SESSION": &cfg.ContinueSession, "REQUEST_BODY_LOG": &cfg.RequestBodyLog,
		"ADMIN_AUTH_ENABLED": &cfg.AdminAuthEnabled, "LOG_FILE": &cfg.LogFile,
		"NONSTREAM_KEEPALIVE": &cfg.NonstreamKeepalive, "FAKE_STREAM": &cfg.FakeStream,
	} {
		*target, _ = strconv.ParseBool(value(key))
	}
	for key, target := range map[string]*int{"LOG_MAX_TOTAL_MB": &cfg.LogMaxTotalMB, "PER_ACCOUNT_CONCURRENCY": &cfg.PerAccountConcurrency} {
		*target, _ = strconv.Atoi(value(key))
	}
	for key, target := range map[string]*time.Duration{
		"ACCOUNT_COOLDOWN": &cfg.AccountCooldown, "SESSION_TTL": &cfg.SessionTTL, "INIT_TIMEOUT": &cfg.InitTimeout,
	} {
		*target, _ = time.ParseDuration(value(key))
	}
	if cfg.AdminAuthEnabled && cfg.AdminPassword == "" {
		reject("ADMIN_AUTH_ENABLED", "开启管理登录前先设置 ADMIN_PASSWORD")
		cfg.AdminAuthEnabled = false
	}
	bindings, err := ParseAPIKeyAccounts(os.Getenv("API_KEY_ACCOUNTS"), cfg.ProxyAPIKey)
	if err != nil {
		reject("API_KEY_ACCOUNTS", err.Error())
	}
	cfg.APIKeyAccounts = bindings
	if cfg.ModelAccounts, err = ParseModelAccounts(os.Getenv("MODEL_ACCOUNTS")); err != nil {
		reject("MODEL_ACCOUNTS", err.Error())
	}
	mapping, err := ParseModelMapping(os.Getenv("MODEL_MAPPING"))
	if err != nil {
		reject("MODEL_MAPPING", err.Error())
	}
	cfg.ModelMapping = mapping
	hiddenRaw, hiddenSet := os.LookupEnv("HIDDEN_MODELS")
	if !hiddenSet {
		hiddenRaw = Defaults["HIDDEN_MODELS"]
	}
	cfg.HiddenModels = NormalizeHiddenModels(hiddenRaw)
	slices.SortFunc(cfg.Invalid, func(a, b InvalidValue) int { return strings.Compare(a.Key, b.Key) })

	SetModelMapping(cfg.ModelMapping)
	SetHiddenModels(cfg.HiddenModels)
	return cfg
}

// NormalizeHiddenModels 解析逗号分隔的隐藏模型名单，规范为小写、去前缀与去重排序后的切片
func NormalizeHiddenModels(value string) []string {
	items := SplitList(value)
	result := make([]string, 0, len(items))
	for _, item := range items {
		normalized := normalizeHiddenEntry(item)
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// FormatHiddenModels 返回隐藏模型名单写入 .env 的规范逗号分隔字符串
func FormatHiddenModels(models []string) string {
	return strings.Join(NormalizeHiddenModels(strings.Join(models, ",")), ",")
}

// SetHiddenModels 替换当前生效的隐藏模型名单
func SetHiddenModels(models []string) {
	normalized := NormalizeHiddenModels(strings.Join(models, ","))
	set := make(map[string]bool, len(normalized))
	predefined := false
	for _, item := range normalized {
		if item == PredefinedGemsSelector {
			predefined = true
			continue
		}
		set[item] = true
	}
	hiddenMu.Lock()
	hiddenModelList = normalized
	hiddenModelSet = set
	hidePredefined = predefined
	hiddenMu.Unlock()
}

// HiddenModels 返回当前生效的隐藏模型名单副本
func HiddenModels() []string {
	hiddenMu.RLock()
	defer hiddenMu.RUnlock()
	return slices.Clone(hiddenModelList)
}

// IsModelHidden 判断指定模型 ID 或 Google 预置 Gem 是否在设置中关闭
func IsModelHidden(id string, predefinedGem bool) bool {
	key := normalizeHiddenEntry(id)
	hiddenMu.RLock()
	defer hiddenMu.RUnlock()
	if predefinedGem && hidePredefined {
		return true
	}
	return key != "" && hiddenModelSet[key]
}

// normalizeHiddenEntry 去掉首尾空白、models/ 或 google/ 前缀与 [1m] 后缀并转为小写
func normalizeHiddenEntry(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "[1m]")
	value = strings.TrimPrefix(value, "models/")
	return strings.TrimPrefix(value, "google/")
}

// SetModelMapping 替换当前模型别名映射
func SetModelMapping(mapping map[string]string) {
	mappingMu.Lock()
	defer mappingMu.Unlock()

	modelMapping = maps.Clone(mapping)
}

// ModelAliases 返回当前模型别名映射的副本
func ModelAliases() map[string]string {
	mappingMu.RLock()
	defer mappingMu.RUnlock()
	return maps.Clone(modelMapping)
}

// MapModel 去掉首尾空白、models/ 与 google/ 前缀和 Claude Code 1M 上下文模式的 [1m] 后缀后返回别名对应的真实模型标识
func MapModel(model string) string {
	model = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(model), "models/"), "google/"), "[1m]")
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

// Validate 校验配置项 key 的取值并返回规范写法，取值无效时返回不含项名的原因；取值不受限的项原样返回
func Validate(key string, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch key {
	case "LISTEN_ADDR":
		if _, _, err := net.SplitHostPort(value); err != nil {
			return "", errors.New("必须是 主机:端口")
		}
	case "GEMINI_SAVE_HISTORY", "GEMINI_CONTINUE_SESSION", "REQUEST_BODY_LOG", "ADMIN_AUTH_ENABLED", "LOG_FILE", "NONSTREAM_KEEPALIVE", "FAKE_STREAM":
		if _, err := strconv.ParseBool(value); err != nil {
			return "", errors.New("必须是 true 或 false")
		}
	case "LOG_MAX_TOTAL_MB", "PER_ACCOUNT_CONCURRENCY":
		number, err := strconv.Atoi(value)
		if err != nil || number < 1 {
			return "", errors.New("必须是正整数")
		}
		if key == "LOG_MAX_TOTAL_MB" && number < MinLogTotalMB {
			return "", fmt.Errorf("不能小于 %d", MinLogTotalMB)
		}
	case "ACCOUNT_COOLDOWN", "SESSION_TTL", "INIT_TIMEOUT":
		if duration, err := time.ParseDuration(value); err != nil || duration <= 0 {
			return "", errors.New("必须是正数时长，例如 30s 或 2m")
		}
	case "LOG_LEVEL":
		value = strings.ToLower(value)
		if !slices.Contains([]string{"debug", "info", "warn", "error"}, value) {
			return "", errors.New("取 debug、info、warn 或 error")
		}
	case "LOG_FORMAT":
		value = strings.ToLower(value)
		if value != "text" && value != "json" {
			return "", errors.New("取 text 或 json")
		}
	case "ROUTING_STRATEGY":
		value = strings.ToLower(value)
		if value != "round-robin" && value != "fill-first" {
			return "", errors.New("取 round-robin 或 fill-first")
		}
	case "FIRST_EVENT_HEDGE":
		value = strings.ToLower(value)
		if seconds, err := strconv.Atoi(value); value != "off" && value != "auto" && (err != nil || seconds < 1) {
			return "", errors.New("取 off、auto 或正整数秒")
		}
	}
	return value, nil
}

// FormatModelMapping 按别名排序输出 MODEL_MAPPING 格式的映射
func FormatModelMapping(mapping map[string]string) string {
	pairs := make([]string, 0, len(mapping))
	for _, alias := range slices.Sorted(maps.Keys(mapping)) {
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

// ParseAPIKeyAccounts 解析逗号分隔的“API key:账号|账号”绑定，跳过不在 apiKeys 中的 key，无效项与重复的 key 跳过并返回错误，其余绑定照常返回
func ParseAPIKeyAccounts(value string, apiKeys string) (map[string][]string, error) {
	keys := SplitList(apiKeys)
	accounts := make(map[string][]string)
	var problem error
	for _, item := range SplitList(value) {
		key, ids, ok := accountBinding(item)
		if !ok {
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

// ParseModelAccounts 解析逗号分隔的“模型 ID:账号|账号”绑定，无效项与重复的模型跳过并返回错误，其余绑定照常返回
func ParseModelAccounts(value string) (map[string][]string, error) {
	accounts := make(map[string][]string)
	var problem error
	for _, item := range SplitList(value) {
		model, ids, ok := accountBinding(item)
		if !ok {
			problem = fmt.Errorf("每一项写成 模型 ID:账号|账号")
			continue
		}
		if _, exists := accounts[model]; exists {
			problem = fmt.Errorf("同一模型重复")
			continue
		}
		accounts[model] = ids
	}
	return accounts, problem
}

// accountBinding 以最后一个冒号拆分“名称:账号|账号”，名称或账号为空时 ok 为假
func accountBinding(item string) (name string, ids []string, ok bool) {
	index := strings.LastIndexByte(item, ':')
	if index < 0 {
		return "", nil, false
	}
	for id := range strings.SplitSeq(item[index+1:], "|") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	name = strings.TrimSpace(item[:index])
	return name, ids, name != "" && len(ids) > 0
}

// FormatAccountBindings 把 API key 或模型的账号绑定写成 .env 的取值，按名称排序
func FormatAccountBindings(accounts map[string][]string) string {
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
