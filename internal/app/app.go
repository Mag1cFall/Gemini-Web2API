// Package app 装配配置、日志、账号、请求账本与 HTTP 服务并管理其生命周期
package app

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/accounts"
	"github.com/Mag1cFall/Gemini-Web2API/internal/adapter"
	"github.com/Mag1cFall/Gemini-Web2API/internal/admin"
	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/proxypool"
	"github.com/Mag1cFall/Gemini-Web2API/internal/requestdb"

	"github.com/gofrs/flock"
	"github.com/joho/godotenv"
	"golang.org/x/term"
)

// shutdownGrace 是停机时等待活动请求结束的最长时间
const shutdownGrace = 10 * time.Second

// dataDir 是请求账本与实例锁所在目录
const dataDir = "data"

// envPath 是管理端写回配置的文件
const envPath = ".env"

// logFilePath 是开启日志文件时写入的轮转日志
var logFilePath = filepath.Join(dataDir, "logs", "gemini-web2api.log")

// dotenvKeys 是启动时从 .env 写入进程环境的变量名，启动前已有的环境变量不在其中
var dotenvKeys []string

// Main 分派首次配置、版本查询与默认服务
func Main(args []string) error {
	if err := enterDataDir(); err != nil {
		return err
	}
	loadDotenv()
	if len(args) != 0 {
		switch args[0] {
		case "setup":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			attachConsole(cancel)
			return chromeauth.RunSetup(ctx, args[1:], os.Stdin, os.Stdout, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())))
		case "version", "--version", "-version":
			attachConsole(nil)
			fmt.Println("Gemini-Web2API " + Version())
			return nil
		}
	}
	if slices.ContainsFunc(args, func(arg string) bool { return arg == "-h" || arg == "-help" || arg == "--help" }) {
		attachConsole(nil)
	}
	return runDefault(args)
}

// loadDotenv 把 .env 中进程环境没有的变量写入进程环境并记入 dotenvKeys
func loadDotenv() {
	values, err := godotenv.Read()
	if err != nil {
		return
	}
	for key, value := range values {
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, value)
			dotenvKeys = append(dotenvKeys, key)
		}
	}
}

// unloadDotenv 移除启动时从 .env 写入的变量，重启后的新进程按当前 .env 读取配置
func unloadDotenv() {
	for _, key := range dotenvKeys {
		_ = os.Unsetenv(key)
	}
}

// version 是发行构建经 -ldflags -X 写入的版本号
var version string

// Version 返回发行构建写入的版本号，未写入时返回模块版本与提交
func Version() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}
	version := info.Main.Version
	if version == "" {
		version = "(devel)"
	}
	revision, modified := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if version == "(devel)" && revision != "" {
		if len(revision) > 7 {
			revision = revision[:7]
		}
		version += " " + revision
		if modified {
			version += "-dirty"
		}
	}
	return version
}

// LoadConfig 读取 .env 与启动参数，同时返回启动后是否打开管理页
func LoadConfig(args []string) (config.Config, bool, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, false, err
	}
	openUI, err := ParseFlags(args, &cfg)
	if err != nil {
		return config.Config{}, false, err
	}
	if len(cfg.AuthStatePaths) == 0 {
		return config.Config{}, false, fmt.Errorf("请通过 --auth 或 GEMINI_AUTH_STATES 提供认证状态文件")
	}
	return cfg, openUI, nil
}

// Instance 是已取得实例锁并占用端口、尚未开始服务的进程
type Instance struct {
	cfg      config.Config
	lock     *flock.Flock
	listener net.Listener
	started  time.Time
	desktop  bool
	// openUI 表示服务开始接受请求后用系统浏览器打开管理页
	openUI bool
	// executable 是在线更新替换的可执行文件
	executable string
	// restart 在在线更新安装完成后重启到新版本
	restart func()
}

// Start 配置日志与日志文件，取得数据目录的实例锁并监听端口，logWriter 为 nil 时写标准错误
func Start(cfg config.Config, logWriter io.Writer, desktop bool) (*Instance, error) {
	started := time.Now()
	if err := logging.Setup(logging.Options{Level: cfg.LogLevel, Format: cfg.LogFormat, Writer: logWriter}); err != nil {
		return nil, err
	}
	if err := logging.SetFile(logFilePath, cfg.LogFile, cfg.LogMaxTotalMB); err != nil {
		return nil, fmt.Errorf("打开日志文件: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	lock := flock.New(filepath.Join(dataDir, "gemini-web2api.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("获取实例锁: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("同一目录已有 Gemini-Web2API 实例在运行（%s）", filepath.Join(dataDir, "gemini-web2api.lock"))
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("监听 %s: %w", cfg.ListenAddress, err)
	}
	return &Instance{cfg: cfg, lock: lock, listener: listener, started: started, desktop: desktop}, nil
}

// Serve 运行服务直到 ctx 结束，停机后释放端口与实例锁
func (instance *Instance) Serve(ctx context.Context) error {
	defer instance.lock.Unlock()
	defer instance.listener.Close()
	cfg, listener, started := instance.cfg, instance.listener, instance.started
	go logging.Watch(ctx)

	store, err := requestdb.Open(filepath.Join(dataDir, "requests.db"))
	if err != nil {
		return fmt.Errorf("打开请求账本: %w", err)
	}
	defer store.Close()
	store.SetBodyCapture(cfg.RequestBodyLog)

	authDirectory := authDir(cfg.AuthStatePaths)
	if err := os.MkdirAll(authDirectory, 0o700); err != nil {
		return fmt.Errorf("创建认证目录: %w", err)
	}
	loaded, err := auth.LoadFiles(cfg.AuthStatePaths)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(loaded))
	for _, account := range loaded {
		ids = append(ids, account.ID)
	}
	logging.SetSourceWidth(ids)
	logStartup(cfg, len(loaded))

	adapter.ConfigureSessionTTL(cfg.SessionTTL)
	adapter.ConfigureAccountFallback(cfg.AccountFallback)
	pool := balancer.NewAccountPool(cfg.AccountCooldown, cfg.SessionTTL)
	manager := accounts.New(ctx, pool, accounts.Settings{
		InitTimeout: cfg.InitTimeout, RetryInterval: cfg.AccountCooldown, Proxy: cfg.ProxyURL,
		SaveHistory: cfg.SaveHistory, AuthDir: authDirectory, Concurrency: cfg.PerAccountConcurrency,
	})
	pool.SetStrategy(cfg.RoutingStrategy)
	settings := admin.NewSettings(envPath, cfg, func(next config.Config) {
		config.SetModelMapping(next.ModelMapping)
		pool.SetTimings(next.AccountCooldown, next.SessionTTL)
		adapter.ConfigureSessionTTL(next.SessionTTL)
		adapter.ConfigureAccountFallback(next.AccountFallback)
		if level, err := logging.ParseLevel(next.LogLevel); err == nil {
			logging.SetLevel(level)
		}
		store.SetBodyCapture(next.RequestBodyLog)
		if err := logging.SetFile(logFilePath, next.LogFile, next.LogMaxTotalMB); err != nil {
			logging.Error(logging.SourceService, "日志文件设置失败", "错误", err)
		}
		pool.SetStrategy(next.RoutingStrategy)
		manager.SetSettings(accounts.Settings{
			InitTimeout: next.InitTimeout, RetryInterval: next.AccountCooldown, Proxy: next.ProxyURL,
			SaveHistory: next.SaveHistory, AuthDir: authDirectory, Concurrency: next.PerAccountConcurrency,
		})
	})
	workDir, _ := os.Getwd()
	shuttingDown := make(chan struct{})
	updater := newSelfUpdater(Version(), instance.executable, func() string { return updateProxy(settings.Snapshot().ProxyURL) }, instance.restart)
	server := newHTTPServer(NewRouter(Deps{
		Pool: pool, Store: store, Accounts: manager, APIKey: settings.APIKey,
		Admin: &admin.Deps{
			Pool: pool, Accounts: manager, Store: store, Settings: settings, Version: Version(), StartedAt: started,
			ListenAddr: listener.Addr().String(), DataDir: workDir, Desktop: instance.desktop, Updates: updater, Stopping: shuttingDown,
		},
	}))
	server.RegisterOnShutdown(func() { close(shuttingDown) })
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	adminURL := AdminURL(listener.Addr().String())
	logging.Info(logging.SourceService, "服务已启动", "地址", "http://"+listener.Addr().String(), "管理端", adminURL)
	if instance.openUI {
		if err := openBrowser(adminURL); err != nil {
			logging.Warn(logging.SourceService, "管理页打开失败", "错误", err)
		}
	}

	initStarted := time.Now()
	manager.Start(loaded)
	status := pool.Status()
	guest := balancer.StateUnavailable
	if status.Guest != nil {
		guest = status.Guest.State
	}
	logging.Info(logging.SourceService, "账号初始化完成",
		"就绪", status.Available, "待认证", status.AuthRequired, "不可用", status.Unavailable,
		"停用", status.Disabled, "访客会话", guest, "模型", len(pool.ModelAccounts()), "耗时", time.Since(initStarted), "启动耗时", time.Since(started))
	unavailable := "接口返回 503"
	if status.Ready() {
		unavailable = "只提供访客会话可用的模型"
	}
	switch {
	case status.Total == 0:
		logging.Warn(logging.SourceService, "没有账号", "", unavailable+"；在管理端导入账号，或运行 gemini-web2api setup")
	case status.Available == 0:
		logging.Warn(logging.SourceService, "没有可用账号", "", unavailable+"，失败的账号按 ACCOUNT_COOLDOWN 间隔继续初始化")
	}

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	stopping := time.Now()
	logging.Info(logging.SourceService, "服务停止", "活动请求", logging.ActiveCount())
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logging.Warn(logging.SourceService, "活动请求未在等待时间内结束，强制关闭", "等待", shutdownGrace, "活动请求", logging.ActiveCount())
		_ = server.Close()
	}
	logging.Info(logging.SourceService, "服务已停止", "耗时", time.Since(stopping))
	return nil
}

// logStartup 输出版本、监听、账号与安全配置
func logStartup(cfg config.Config, accountCount int) {
	logging.Info(logging.SourceService, "Gemini-Web2API 启动", "版本", Version(), "Go", strings.TrimPrefix(runtime.Version(), "go"), "系统", runtime.GOOS+"/"+runtime.GOARCH)
	proxies := config.SplitList(cfg.ProxyURL)
	for index, proxy := range proxies {
		proxies[index] = maskProxy(proxy)
	}
	proxy := strings.Join(proxies, ",")
	if proxy == "" {
		proxy = cmp.Or(maskProxy(proxypool.System()), "无")
	}
	key := "未设置"
	if cfg.ProxyAPIKey != "" {
		key = "已设置"
	}
	logging.Info(logging.SourceService, "配置",
		"监听", cfg.ListenAddress, "认证", strings.Join(cfg.AuthStatePaths, ","), "账号", accountCount,
		"代理", proxy, "API key", key, "历史记录", cfg.SaveHistory, "日志", cfg.LogLevel)
	if cfg.ProxyAPIKey == "" && !loopbackAddress(cfg.ListenAddress) {
		logging.Warn(logging.SourceService, "监听地址不是本机回环且未设置 PROXY_API_KEY，能访问该地址的任何人都可以调用", "监听", cfg.ListenAddress)
	}
}

// loopbackAddress 判断监听地址是否只接受本机连接
func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// authDir 返回管理端导入账号的目录，取第一个认证路径中的目录
func authDir(paths []string) string {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(path, "storage-state.json")); err != nil {
				return path
			}
		}
	}
	return "auth"
}

// maskProxy 隐藏代理 URL 中的密码
func maskProxy(value string) string {
	if at := strings.LastIndex(value, "@"); at > 0 {
		if scheme := strings.Index(value, "://"); scheme >= 0 && scheme < at {
			if user, _, found := strings.Cut(value[scheme+3:at], ":"); found {
				return value[:scheme+3] + user + ":***" + value[at:]
			}
		}
	}
	return value
}

// newHTTPServer 创建 HTTP 服务，活动请求在停机时先等待结束
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
}

// ParseFlags 用命令行参数覆盖最常用的启动配置，返回是否打开管理页，不带参数启动时默认打开
func ParseFlags(args []string, cfg *config.Config) (bool, error) {
	flags := flag.NewFlagSet("gemini-web2api", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "首次配置: gemini-web2api setup")
		fmt.Fprintln(flags.Output(), "查看版本: gemini-web2api version")
		fmt.Fprintln(flags.Output(), "日常启动: gemini-web2api [参数]")
		flags.PrintDefaults()
	}
	authDefault := strings.Join(cfg.AuthStatePaths, ",")
	if authDefault == "" {
		authDefault = "auth"
	}
	authPaths := flags.String("auth", authDefault, "认证文件、目录或逗号分隔的多个路径")
	listenAddress := flags.String("listen", cfg.ListenAddress, "服务监听地址")
	openUI := flags.Bool("open-ui", len(args) == 0, "启动后打开管理页")
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	if flags.NArg() != 0 {
		return false, fmt.Errorf("未知参数 %q", flags.Arg(0))
	}

	cfg.AuthStatePaths = splitFlagList(*authPaths)
	cfg.ListenAddress = strings.TrimSpace(*listenAddress)
	if cfg.ListenAddress == "" {
		return false, fmt.Errorf("监听地址不能为空")
	}
	return *openUI, nil
}

func splitFlagList(value string) []string {
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
