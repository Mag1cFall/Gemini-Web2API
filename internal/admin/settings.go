package admin

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// Settings 保存管理端可修改的配置，保存时写回 .env 并即时应用可热更新的项
type Settings struct {
	mu      sync.RWMutex
	envPath string
	active  config.Config
	current config.Config
	apply   func(config.Config)
	changed func()
}

// NewSettings 以启动时生效的配置创建设置，apply 应用可热更新的项
func NewSettings(envPath string, cfg config.Config, apply func(config.Config)) *Settings {
	return &Settings{envPath: envPath, active: cfg, current: cfg, apply: apply}
}

// Snapshot 返回当前配置
func (s *Settings) Snapshot() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// APIKey 返回当前生效的公开 API key，多个 key 以逗号分隔
func (s *Settings) APIKey() string {
	return s.Snapshot().ProxyAPIKey
}

// ServiceConfig 是管理端设置页的配置视图
type ServiceConfig struct {
	ProxyAPIKey      string   `json:"proxy_api_key"`
	Proxy            string   `json:"proxy"`
	SaveHistory      bool     `json:"save_history"`
	ModelMapping     string   `json:"model_mapping"`
	AccountCooldown  string   `json:"account_cooldown"`
	SessionTTL       string   `json:"session_ttl"`
	InitTimeout      string   `json:"init_timeout"`
	LogLevel         string   `json:"log_level"`
	RequestBodyLog   bool     `json:"request_body_log"`
	LogFile          bool     `json:"log_file"`
	LogMaxTotalMB    int      `json:"log_max_total_mb"`
	Concurrency      int      `json:"per_account_concurrency"`
	RoutingStrategy  string   `json:"routing_strategy"`
	AccountFallback  bool     `json:"account_fallback"`
	AdminAuthEnabled bool     `json:"admin_auth_enabled"`
	AdminUsername    string   `json:"admin_username"`
	AdminPasswordSet bool     `json:"admin_password_set"`
	ListenAddr       string   `json:"listen_addr"`
	AuthStates       string   `json:"auth_states"`
	LogFormat        string   `json:"log_format"`
	ActiveListenAddr string   `json:"active_listen_addr"`
	RestartRequired  bool     `json:"restart_required"`
	RestartFields    []string `json:"restart_fields"`
}

// ConfigUpdate 是设置页提交的修改，nil 表示不修改
type ConfigUpdate struct {
	ProxyAPIKey      *string `json:"proxy_api_key"`
	Proxy            *string `json:"proxy"`
	SaveHistory      *bool   `json:"save_history"`
	ModelMapping     *string `json:"model_mapping"`
	AccountCooldown  *string `json:"account_cooldown"`
	SessionTTL       *string `json:"session_ttl"`
	InitTimeout      *string `json:"init_timeout"`
	LogLevel         *string `json:"log_level"`
	RequestBodyLog   *bool   `json:"request_body_log"`
	LogFile          *bool   `json:"log_file"`
	LogMaxTotalMB    *int    `json:"log_max_total_mb"`
	Concurrency      *int    `json:"per_account_concurrency"`
	RoutingStrategy  *string `json:"routing_strategy"`
	AccountFallback  *bool   `json:"account_fallback"`
	AdminAuthEnabled *bool   `json:"admin_auth_enabled"`
	AdminUsername    *string `json:"admin_username"`
	AdminPassword    *string `json:"admin_password"`
	ListenAddr       *string `json:"listen_addr"`
	AuthStates       *string `json:"auth_states"`
	LogFormat        *string `json:"log_format"`
}

// View 返回设置页的配置视图
func (s *Settings) View() ServiceConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.current
	view := ServiceConfig{
		ProxyAPIKey: cfg.ProxyAPIKey, Proxy: cfg.ProxyURL, SaveHistory: cfg.SaveHistory,
		ModelMapping: config.FormatModelMapping(cfg.ModelMapping), AccountCooldown: shortDuration(cfg.AccountCooldown),
		SessionTTL: shortDuration(cfg.SessionTTL), InitTimeout: shortDuration(cfg.InitTimeout), LogLevel: cfg.LogLevel,
		RequestBodyLog: cfg.RequestBodyLog, LogFile: cfg.LogFile, LogMaxTotalMB: cfg.LogMaxTotalMB, Concurrency: cfg.PerAccountConcurrency, RoutingStrategy: cfg.RoutingStrategy,
		AccountFallback: cfg.AccountFallback, AdminAuthEnabled: cfg.AdminAuthEnabled, AdminUsername: cfg.AdminUsername,
		AdminPasswordSet: cfg.AdminPassword != "", ListenAddr: cfg.ListenAddress,
		AuthStates: strings.Join(cfg.AuthStatePaths, ","), LogFormat: cfg.LogFormat,
		ActiveListenAddr: s.active.ListenAddress, RestartFields: []string{},
	}
	if cfg.ListenAddress != s.active.ListenAddress {
		view.RestartFields = append(view.RestartFields, "listen_addr")
	}
	if strings.Join(cfg.AuthStatePaths, ",") != strings.Join(s.active.AuthStatePaths, ",") {
		view.RestartFields = append(view.RestartFields, "auth_states")
	}
	if cfg.LogFormat != s.active.LogFormat {
		view.RestartFields = append(view.RestartFields, "log_format")
	}
	view.RestartRequired = len(view.RestartFields) > 0
	return view
}

// Update 校验修改，写回 .env 并应用可热更新的项
func (s *Settings) Update(update ConfigUpdate) error {
	s.mu.Lock()
	next := s.current
	env := make(map[string]string)
	var problems []error
	setString := func(value *string, key string, target *string) {
		if value != nil {
			*target = strings.TrimSpace(*value)
			env[key] = *target
		}
	}
	setBool := func(value *bool, key string, target *bool) {
		if value != nil {
			*target = *value
			env[key] = strconv.FormatBool(*value)
		}
	}
	setDuration := func(value *string, key string, target *time.Duration) {
		if value == nil {
			return
		}
		duration, err := time.ParseDuration(strings.TrimSpace(*value))
		if err != nil || duration <= 0 {
			problems = append(problems, fmt.Errorf("%s 必须是正数时长，例如 30s 或 2m", key))
			return
		}
		*target = duration
		env[key] = shortDuration(duration)
	}

	if update.ProxyAPIKey != nil {
		next.ProxyAPIKey = strings.Join(config.SplitList(*update.ProxyAPIKey), ",")
		env["PROXY_API_KEY"] = next.ProxyAPIKey
	}
	if update.Proxy != nil {
		proxies := config.SplitList(*update.Proxy)
		for _, proxy := range proxies {
			if _, err := chromeauth.ValidateProxy(proxy); err != nil {
				problems = append(problems, fmt.Errorf("PROXY: %w", err))
			}
		}
		proxy := strings.Join(proxies, ",")
		next.ProxyURL = proxy
		env["PROXY"] = proxy
	}
	setBool(update.SaveHistory, "GEMINI_SAVE_HISTORY", &next.SaveHistory)
	if update.ModelMapping != nil {
		mapping, err := config.ParseModelMapping(*update.ModelMapping)
		if err != nil {
			problems = append(problems, err)
		}
		next.ModelMapping = mapping
		env["MODEL_MAPPING"] = config.FormatModelMapping(mapping)
	}
	setDuration(update.AccountCooldown, "ACCOUNT_COOLDOWN", &next.AccountCooldown)
	setDuration(update.SessionTTL, "SESSION_TTL", &next.SessionTTL)
	setDuration(update.InitTimeout, "INIT_TIMEOUT", &next.InitTimeout)
	if update.LogLevel != nil {
		level := strings.ToLower(strings.TrimSpace(*update.LogLevel))
		if _, err := logging.ParseLevel(level); err != nil {
			problems = append(problems, fmt.Errorf("LOG_LEVEL 取 debug、info、warn 或 error"))
		}
		next.LogLevel = level
		env["LOG_LEVEL"] = level
	}
	setBool(update.RequestBodyLog, "REQUEST_BODY_LOG", &next.RequestBodyLog)
	setBool(update.LogFile, "LOG_FILE", &next.LogFile)
	if update.LogMaxTotalMB != nil {
		if *update.LogMaxTotalMB < config.MinLogTotalMB {
			problems = append(problems, fmt.Errorf("LOG_MAX_TOTAL_MB 不能小于 %d", config.MinLogTotalMB))
		}
		next.LogMaxTotalMB = *update.LogMaxTotalMB
		env["LOG_MAX_TOTAL_MB"] = strconv.Itoa(*update.LogMaxTotalMB)
	}
	if update.Concurrency != nil {
		if *update.Concurrency < 1 {
			problems = append(problems, errors.New("PER_ACCOUNT_CONCURRENCY 必须是正整数"))
		}
		next.PerAccountConcurrency = *update.Concurrency
		env["PER_ACCOUNT_CONCURRENCY"] = strconv.Itoa(*update.Concurrency)
	}
	if update.RoutingStrategy != nil {
		strategy := strings.ToLower(strings.TrimSpace(*update.RoutingStrategy))
		if strategy != "round-robin" && strategy != "fill-first" {
			problems = append(problems, errors.New("ROUTING_STRATEGY 取 round-robin 或 fill-first"))
		}
		next.RoutingStrategy = strategy
		env["ROUTING_STRATEGY"] = strategy
	}
	setBool(update.AccountFallback, "ACCOUNT_FALLBACK", &next.AccountFallback)
	setBool(update.AdminAuthEnabled, "ADMIN_AUTH_ENABLED", &next.AdminAuthEnabled)
	setString(update.AdminUsername, "ADMIN_USERNAME", &next.AdminUsername)
	if next.AdminUsername == "" {
		next.AdminUsername = "admin"
	}
	if update.AdminPassword != nil && *update.AdminPassword != "" {
		next.AdminPassword = *update.AdminPassword
		env["ADMIN_PASSWORD"] = next.AdminPassword
	}
	if next.AdminAuthEnabled && next.AdminPassword == "" {
		problems = append(problems, errors.New("开启管理登录前先设置管理密码"))
	}
	if update.ListenAddr != nil {
		address := strings.TrimSpace(*update.ListenAddr)
		if _, _, err := net.SplitHostPort(address); err != nil {
			problems = append(problems, fmt.Errorf("LISTEN_ADDR 必须是 主机:端口"))
		}
		next.ListenAddress = address
		env["LISTEN_ADDR"] = address
	}
	if update.AuthStates != nil {
		next.AuthStatePaths = nil
		for _, path := range strings.Split(*update.AuthStates, ",") {
			if path = strings.TrimSpace(path); path != "" {
				next.AuthStatePaths = append(next.AuthStatePaths, path)
			}
		}
		if len(next.AuthStatePaths) == 0 {
			problems = append(problems, errors.New("GEMINI_AUTH_STATES 不能为空"))
		}
		env["GEMINI_AUTH_STATES"] = strings.Join(next.AuthStatePaths, ",")
	}
	if update.LogFormat != nil {
		format := strings.ToLower(strings.TrimSpace(*update.LogFormat))
		if format != "text" && format != "json" {
			problems = append(problems, errors.New("LOG_FORMAT 取 text 或 json"))
		}
		next.LogFormat = format
		env["LOG_FORMAT"] = format
	}
	if len(problems) > 0 {
		s.mu.Unlock()
		return &validationError{errors.Join(problems...)}
	}
	if err := config.WriteEnv(s.envPath, env); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("写入 %s: %w", s.envPath, err)
	}
	previous := s.current
	s.current = next
	changed := s.changed
	s.mu.Unlock()

	s.apply(next)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	logging.Info(logging.SourceService, "设置已保存", "项", strings.Join(sortStrings(keys), ","))
	if previous.AdminUsername != next.AdminUsername || previous.AdminPassword != next.AdminPassword || previous.AdminAuthEnabled != next.AdminAuthEnabled {
		if changed != nil {
			changed()
		}
	}
	return nil
}

// validationError 表示提交的设置无效
type validationError struct{ error }

// shortDuration 把时长写成去掉末尾零单位的形式，例如 2m、1h30m、20s
func shortDuration(duration time.Duration) string {
	text := duration.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}
