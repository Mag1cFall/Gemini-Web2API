package admin

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/adapter"
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
	ContinueSession  bool     `json:"continue_session"`
	Keepalive        bool     `json:"nonstream_keepalive"`
	FakeStream       bool     `json:"fake_stream"`
	ModelMapping     string   `json:"model_mapping"`
	HiddenModels     string   `json:"hidden_models"`
	AccountCooldown  string   `json:"account_cooldown"`
	SessionTTL       string   `json:"session_ttl"`
	InitTimeout      string   `json:"init_timeout"`
	LogLevel         string   `json:"log_level"`
	RequestBodyLog   bool     `json:"request_body_log"`
	LogFile          bool     `json:"log_file"`
	LogMaxTotalMB    int      `json:"log_max_total_mb"`
	Concurrency      int      `json:"per_account_concurrency"`
	RoutingStrategy  string   `json:"routing_strategy"`
	AdminAuthEnabled bool     `json:"admin_auth_enabled"`
	AdminUsername    string   `json:"admin_username"`
	AdminPasswordSet bool     `json:"admin_password_set"`
	ListenAddr       string   `json:"listen_addr"`
	AuthStates       string   `json:"auth_states"`
	LogFormat        string   `json:"log_format"`
	ActiveListenAddr string   `json:"active_listen_addr"`
	RestartRequired  bool     `json:"restart_required"`
	RestartFields    []string `json:"restart_fields"`

	APIKeyAccounts        string `json:"api_key_accounts"`
	AccountOrder          string `json:"account_order"`
	ModelAccounts         string `json:"model_accounts"`
	FirstEventHedge       string `json:"first_event_hedge"`
	FirstEventHedgeModels string `json:"first_event_hedge_models"`
	// Invalid 是启动时取值无效、按默认值运行的 .env 项
	Invalid []config.InvalidValue `json:"invalid"`
	// Locked 是由进程环境变量或启动参数决定的配置项，键为 .env 名，值为 env 或 flag
	Locked map[string]string `json:"locked"`
}

// ConfigUpdate 是设置页提交的修改，nil 表示不修改
type ConfigUpdate struct {
	ProxyAPIKey      *string `json:"proxy_api_key"`
	Proxy            *string `json:"proxy"`
	SaveHistory      *bool   `json:"save_history"`
	ContinueSession  *bool   `json:"continue_session"`
	Keepalive        *bool   `json:"nonstream_keepalive"`
	FakeStream       *bool   `json:"fake_stream"`
	ModelMapping     *string `json:"model_mapping"`
	HiddenModels     *string `json:"hidden_models"`
	AccountCooldown  *string `json:"account_cooldown"`
	SessionTTL       *string `json:"session_ttl"`
	InitTimeout      *string `json:"init_timeout"`
	LogLevel         *string `json:"log_level"`
	RequestBodyLog   *bool   `json:"request_body_log"`
	LogFile          *bool   `json:"log_file"`
	LogMaxTotalMB    *int    `json:"log_max_total_mb"`
	Concurrency      *int    `json:"per_account_concurrency"`
	RoutingStrategy  *string `json:"routing_strategy"`
	AdminAuthEnabled *bool   `json:"admin_auth_enabled"`
	AdminUsername    *string `json:"admin_username"`
	AdminPassword    *string `json:"admin_password"`
	ListenAddr       *string `json:"listen_addr"`
	AuthStates       *string `json:"auth_states"`
	LogFormat        *string `json:"log_format"`
	APIKeyAccounts   *string `json:"api_key_accounts"`
	AccountOrder     *string `json:"account_order"`
	ModelAccounts    *string `json:"model_accounts"`
	FirstEventHedge  *string `json:"first_event_hedge"`
	HedgeModels      *string `json:"first_event_hedge_models"`
}

// View 返回设置页的配置视图
func (s *Settings) View() ServiceConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.current
	view := ServiceConfig{
		ProxyAPIKey: cfg.ProxyAPIKey, Proxy: cfg.ProxyURL, SaveHistory: cfg.SaveHistory, ContinueSession: cfg.ContinueSession,
		Keepalive: cfg.NonstreamKeepalive, FakeStream: cfg.FakeStream,
		ModelMapping: config.FormatModelMapping(cfg.ModelMapping), HiddenModels: config.FormatHiddenModels(cfg.HiddenModels), AccountCooldown: shortDuration(cfg.AccountCooldown),
		SessionTTL: shortDuration(cfg.SessionTTL), InitTimeout: shortDuration(cfg.InitTimeout), LogLevel: cfg.LogLevel,
		RequestBodyLog: cfg.RequestBodyLog, LogFile: cfg.LogFile, LogMaxTotalMB: cfg.LogMaxTotalMB, Concurrency: cfg.PerAccountConcurrency, RoutingStrategy: cfg.RoutingStrategy,
		AdminAuthEnabled: cfg.AdminAuthEnabled, AdminUsername: cfg.AdminUsername,
		AdminPasswordSet: cfg.AdminPassword != "", ListenAddr: cfg.ListenAddress,
		AuthStates: strings.Join(cfg.AuthStatePaths, ","), LogFormat: cfg.LogFormat,
		ActiveListenAddr: s.active.ListenAddress, RestartFields: []string{},
		Invalid: nonNil(cfg.Invalid), Locked: cfg.Locked,
		APIKeyAccounts: config.FormatAccountBindings(cfg.APIKeyAccounts), FirstEventHedge: cfg.FirstEventHedge,
		AccountOrder: strings.Join(cfg.AccountOrder, ","), ModelAccounts: config.FormatAccountBindings(cfg.ModelAccounts),
		FirstEventHedgeModels: strings.Join(cfg.FirstEventHedgeModels, ","),
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

// hedgeStat 是一个发起首事件备用请求的模型当前的首事件等待 p95
type hedgeStat struct {
	Model   string `json:"model"`
	P95MS   int64  `json:"p95_ms"`
	Samples int    `json:"samples"`
	Ready   bool   `json:"ready"`
}

// hedgeStats 返回设置中各备用请求模型近 24 小时原请求首事件等待的 p95
func (h *handler) hedgeStats() []hedgeStat {
	models := config.SplitList(h.Settings.View().FirstEventHedgeModels)
	stats := make([]hedgeStat, 0, len(models))
	for _, model := range models {
		p95, samples, ready := adapter.FirstEventP95(model)
		stats = append(stats, hedgeStat{Model: model, P95MS: p95.Milliseconds(), Samples: samples, Ready: ready})
	}
	return stats
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
	validate := func(key string, value string) string {
		normalized, err := config.Validate(key, value)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", key, err))
		}
		return normalized
	}
	setDuration := func(value *string, key string, target *time.Duration) {
		if value == nil {
			return
		}
		*target, _ = time.ParseDuration(validate(key, *value))
		env[key] = shortDuration(*target)
	}
	setChecked := func(value *string, key string, target *string) {
		if value != nil {
			*target = validate(key, *value)
			env[key] = *target
		}
	}
	setNumber := func(value *int, key string, target *int) {
		if value != nil {
			validate(key, strconv.Itoa(*value))
			*target = *value
			env[key] = strconv.Itoa(*value)
		}
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
	setBool(update.ContinueSession, "GEMINI_CONTINUE_SESSION", &next.ContinueSession)
	setBool(update.Keepalive, "NONSTREAM_KEEPALIVE", &next.NonstreamKeepalive)
	setBool(update.FakeStream, "FAKE_STREAM", &next.FakeStream)
	if update.ModelMapping != nil {
		mapping, err := config.ParseModelMapping(*update.ModelMapping)
		if err != nil {
			problems = append(problems, err)
		}
		next.ModelMapping = mapping
		env["MODEL_MAPPING"] = config.FormatModelMapping(mapping)
	}
	if update.HiddenModels != nil {
		next.HiddenModels = config.NormalizeHiddenModels(*update.HiddenModels)
		env["HIDDEN_MODELS"] = config.FormatHiddenModels(next.HiddenModels)
	}
	setDuration(update.AccountCooldown, "ACCOUNT_COOLDOWN", &next.AccountCooldown)
	setDuration(update.SessionTTL, "SESSION_TTL", &next.SessionTTL)
	setDuration(update.InitTimeout, "INIT_TIMEOUT", &next.InitTimeout)
	setChecked(update.LogLevel, "LOG_LEVEL", &next.LogLevel)
	setBool(update.RequestBodyLog, "REQUEST_BODY_LOG", &next.RequestBodyLog)
	setBool(update.LogFile, "LOG_FILE", &next.LogFile)
	setNumber(update.LogMaxTotalMB, "LOG_MAX_TOTAL_MB", &next.LogMaxTotalMB)
	setNumber(update.Concurrency, "PER_ACCOUNT_CONCURRENCY", &next.PerAccountConcurrency)
	setChecked(update.RoutingStrategy, "ROUTING_STRATEGY", &next.RoutingStrategy)
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
	setChecked(update.ListenAddr, "LISTEN_ADDR", &next.ListenAddress)
	if update.AuthStates != nil {
		next.AuthStatePaths = config.SplitList(*update.AuthStates)
		if len(next.AuthStatePaths) == 0 {
			problems = append(problems, errors.New("GEMINI_AUTH_STATES 不能为空"))
		}
		env["GEMINI_AUTH_STATES"] = strings.Join(next.AuthStatePaths, ",")
	}
	setChecked(update.LogFormat, "LOG_FORMAT", &next.LogFormat)
	if update.APIKeyAccounts != nil || update.ProxyAPIKey != nil {
		raw := config.FormatAccountBindings(next.APIKeyAccounts)
		if update.APIKeyAccounts != nil {
			raw = *update.APIKeyAccounts
		}
		bindings, err := config.ParseAPIKeyAccounts(raw, next.ProxyAPIKey)
		if err != nil {
			problems = append(problems, fmt.Errorf("API_KEY_ACCOUNTS %w", err))
		}
		next.APIKeyAccounts = bindings
		if formatted := config.FormatAccountBindings(bindings); update.APIKeyAccounts != nil || formatted != config.FormatAccountBindings(s.current.APIKeyAccounts) {
			env["API_KEY_ACCOUNTS"] = formatted
		}
	}
	if update.AccountOrder != nil {
		next.AccountOrder = config.SplitList(*update.AccountOrder)
		env["ACCOUNT_ORDER"] = strings.Join(next.AccountOrder, ",")
	}
	if update.ModelAccounts != nil {
		bindings, err := config.ParseModelAccounts(*update.ModelAccounts)
		if err != nil {
			problems = append(problems, fmt.Errorf("MODEL_ACCOUNTS %w", err))
		}
		next.ModelAccounts = bindings
		env["MODEL_ACCOUNTS"] = config.FormatAccountBindings(bindings)
	}
	setChecked(update.FirstEventHedge, "FIRST_EVENT_HEDGE", &next.FirstEventHedge)
	if update.HedgeModels != nil {
		next.FirstEventHedgeModels = config.SplitList(*update.HedgeModels)
		env["FIRST_EVENT_HEDGE_MODELS"] = strings.Join(next.FirstEventHedgeModels, ",")
	}
	for key := range env {
		if source := s.current.Locked[key]; source != "" {
			problems = append(problems, fmt.Errorf("%s 由%s决定，设置页不能修改", key, map[string]string{"env": "环境变量", "flag": "启动参数"}[source]))
		}
	}
	if len(problems) > 0 {
		s.mu.Unlock()
		return &validationError{errors.Join(problems...)}
	}
	next.Invalid = slices.DeleteFunc(slices.Clone(next.Invalid), func(item config.InvalidValue) bool {
		_, saved := env[item.Key]
		return saved
	})
	if err := config.WriteEnv(s.envPath, env); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("写入 %s: %w", s.envPath, err)
	}
	previous := s.current
	s.current = next
	changed := s.changed
	s.mu.Unlock()

	s.apply(next)
	logging.Info(logging.SourceService, "设置已保存", "项", strings.Join(slices.Sorted(maps.Keys(env)), ","))
	if previous.AdminUsername != next.AdminUsername || previous.AdminPassword != next.AdminPassword || previous.AdminAuthEnabled != next.AdminAuthEnabled {
		changed()
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
