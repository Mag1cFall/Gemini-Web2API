package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/adapter"
	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/chromeauth"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"golang.org/x/term"
)

type bootstrapResult struct {
	index     int
	accountID string
	client    *gemini.Client
	err       error
}

// main 执行单二进制命令入口
func main() {
	if err := runCommand(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
	}
}

// runCommand 分派首次配置与默认服务
func runCommand(args []string) error {
	_ = godotenv.Load()
	if len(args) != 0 && args[0] == "setup" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return chromeauth.RunSetup(ctx, args[1:], os.Stdin, os.Stdout, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())))
	}
	return run(args)
}

// run 管理配置、协议初始化和服务生命周期
func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := parseFlags(args, &cfg); err != nil {
		return err
	}
	if len(cfg.AuthStatePaths) == 0 {
		return fmt.Errorf("请通过 --auth 或 GEMINI_AUTH_STATES 提供认证状态文件")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := bootstrapAccounts(ctx, cfg)
	if err != nil {
		return err
	}
	status := pool.Status()
	log.Printf(
		"账号初始化完成: total=%d ready=%d unavailable=%d models=%d",
		status.Total, status.Available, status.Unavailable, modelCount(pool.Clients()),
	)
	adapter.ConfigureSessionTTL(cfg.SessionTTL)

	server := newHTTPServer(ctx, cfg.ListenAddress, newRouter(pool, cfg.ProxyAPIKey))
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		close(shutdownDone)
	}()

	log.Printf("Gemini-Web2API listening on http://%s", cfg.ListenAddress)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if ctx.Err() != nil {
		<-shutdownDone
	}
	return nil
}

// newHTTPServer 将活动请求绑定到服务生命周期
func newHTTPServer(ctx context.Context, address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
}

// parseFlags 用命令行参数覆盖最常用的启动配置
func parseFlags(args []string, cfg *config.Config) error {
	flags := flag.NewFlagSet("gemini-web2api", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "首次配置: gemini-web2api setup")
		fmt.Fprintln(flags.Output(), "日常启动: gemini-web2api [参数]")
		flags.PrintDefaults()
	}
	authDefault := strings.Join(cfg.AuthStatePaths, ",")
	if authDefault == "" {
		authDefault = "auth"
	}
	authPaths := flags.String("auth", authDefault, "认证文件、目录或逗号分隔的多个路径")
	listenAddress := flags.String("listen", cfg.ListenAddress, "服务监听地址")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("未知参数 %q", flags.Arg(0))
	}

	cfg.AuthStatePaths = splitFlagList(*authPaths)
	cfg.ListenAddress = strings.TrimSpace(*listenAddress)
	if cfg.ListenAddress == "" {
		return fmt.Errorf("监听地址不能为空")
	}
	return nil
}

// bootstrapAccounts 并发初始化账号并保持配置顺序
func bootstrapAccounts(parent context.Context, cfg config.Config) (*balancer.AccountPool, error) {
	accounts, err := auth.LoadFiles(cfg.AuthStatePaths, cfg.ProxyURL)
	if err != nil {
		return nil, err
	}
	pool := balancer.NewAccountPool(cfg.AccountCooldown, cfg.SessionTTL)
	results := make(chan bootstrapResult, len(accounts))

	for index, account := range accounts {
		go func(index int, account auth.LoadedAccount) {
			ctx, cancel := context.WithTimeout(parent, cfg.InitTimeout)
			defer cancel()
			if account.OAuth != nil {
				material := *account.OAuth
				account.Source.Refresh = func(refreshContext context.Context) ([]gemini.Cookie, error) {
					return chromeauth.Refresh(refreshContext, material, account.ProxyURL)
				}
			}

			client, err := gemini.NewClient(account.Source, account.ProxyURL, cfg.SaveHistory)
			if err == nil {
				err = client.Init(ctx)
			}
			if err == nil {
				_, err = client.FetchUsage(ctx)
			}
			results <- bootstrapResult{
				index: index, accountID: account.ID,
				client: client, err: err,
			}
		}(index, account)
	}

	ordered := make([]bootstrapResult, len(accounts))
	for range accounts {
		result := <-results
		ordered[result.index] = result
	}
	for _, result := range ordered {
		if result.err != nil {
			pool.AddUnavailable(result.accountID)
			log.Printf("账号 %q 初始化失败: %v", result.accountID, result.err)
			continue
		}
		pool.Add(result.client, result.accountID)
	}
	if pool.Size() == 0 {
		return nil, fmt.Errorf("没有账号完成 Gemini Web 协议初始化")
	}
	return pool, nil
}

// newRouter 注册公开健康检查和全部兼容 API
func newRouter(pool *balancer.AccountPool, proxyAPIKey string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(adapter.CORSMiddleware())

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		status := pool.Status()
		if status.Available == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unavailable", "accounts": status.Total, "available": status.Available,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "ready", "accounts": status.Total, "available": status.Available,
		})
	})
	router.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"name": "Gemini-Web2API",
			"endpoints": []string{
				"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/messages/count_tokens",
				"/v1/images/generations", "/v1/images/edits",
				"/v1beta/models/{model}:generateContent", "/v1/models", "/v1/accounts/usage",
			},
		})
	})

	api := router.Group("")
	api.Use(adapter.AuthMiddleware(proxyAPIKey))
	api.Use(adapter.LoggerMiddleware())
	api.GET("/v1/accounts/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, pool.Status())
	})
	api.GET("/v1/accounts/usage", func(c *gin.Context) {
		c.JSON(http.StatusOK, pool.Usage(c.Request.Context()))
	})
	api.POST("/v1/chat/completions", adapter.ChatCompletionHandler(pool))
	api.POST("/v1/responses", adapter.ResponsesHandler(pool))
	api.POST("/v1/images/generations", adapter.ImageGenerationHandler(pool))
	api.POST("/v1/images/edits", adapter.ImageEditHandler(pool))
	api.GET("/v1/models", adapter.ListModelsHandler(pool))
	api.POST("/v1/messages", adapter.ClaudeMessagesHandler(pool))
	api.POST("/v1/messages/count_tokens", adapter.ClaudeCountTokensHandler(pool))
	api.POST("/v1beta/models/*action", adapter.GeminiRouterHandler(pool))
	api.GET("/v1beta/models", adapter.GeminiListModelsHandler(pool))
	return router
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

// modelCount 统计所有健康账号可见的模型并集
func modelCount(clients []*gemini.Client) int {
	models := make(map[string]struct{})
	for _, client := range clients {
		for _, model := range client.Models() {
			models[model.ID] = struct{}{}
		}
	}
	return len(models)
}
