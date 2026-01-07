package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"gemini-web2api/internal/adapter"
	"gemini-web2api/internal/balancer"
	"gemini-web2api/internal/browser"
	"gemini-web2api/internal/config"
	"gemini-web2api/internal/gemini"

	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

var (
	pool           *balancer.AccountPool
	accountCookies map[string]string
	cookiesMu      sync.RWMutex
)

func main() {

	if len(os.Args) > 1 && os.Args[1] == "--fetch-cookies" {
		if err := browser.RunFetchCookies(); err != nil {
			log.Fatalf("Error: %v", err)
		}
		return
	}

	_ = godotenv.Load()

	config.LoadModelMapping()

	pool = balancer.NewAccountPool()
	accountCookies = make(map[string]string)

	go loadAccountsAsync()

	go watchEnvFile()

	r := gin.Default()

	r.Use(adapter.CORSMiddleware())
	r.Use(adapter.AuthMiddleware())
	r.Use(adapter.LoggerMiddleware())

	// OpenAI Protocol
	r.POST("/v1/chat/completions", adapter.ChatCompletionHandler(pool))
	r.POST("/v1/images/generations", adapter.ImageGenerationHandler(pool))
	r.GET("/v1/models", adapter.ListModelsHandler)

	// Claude Protocol
	r.POST("/v1/messages", adapter.ClaudeMessagesHandler(pool))
	r.POST("/v1/messages/count_tokens", adapter.ClaudeCountTokensHandler(pool))
	r.GET("/v1/models/claude", adapter.ClaudeListModelsHandler)

	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":   "Gemini-Web2API (Go) is running",
			"docs":     "POST /v1/chat/completions (OpenAI) | POST /v1/messages (Claude)",
			"accounts": pool.Size(),
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8007"
	}

	log.Printf("Server starting on port %s (accounts loading in background...)", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func cookieHash(cookies map[string]string) string {
	return cookies["__Secure-1PSID"] + "|" + cookies["__Secure-1PSIDTS"]
}

func loadAccountsAsync() {
	log.Println("Loading accounts in background...")

	allCookies, accountIDs, err := browser.LoadMultiCookies(browser.ParseAccountIDs(os.Getenv("ACCOUNTS")))
	if err != nil {
		log.Printf("Failed to load cookies: %v", err)
		return
	}

	cookiesMu.RLock()
	oldCookies := make(map[string]string)
	for k, v := range accountCookies {
		oldCookies[k] = v
	}
	cookiesMu.RUnlock()

	newCookies := make(map[string]string)
	for i, cookies := range allCookies {
		newCookies[accountIDs[i]] = cookieHash(cookies)
	}

	var toInit []int
	var toKeep []string
	for i, accountID := range accountIDs {
		oldHash, existed := oldCookies[accountID]
		newHash := newCookies[accountID]
		if !existed || oldHash != newHash {
			toInit = append(toInit, i)
		} else {
			toKeep = append(toKeep, accountID)
		}
	}

	if len(toInit) == 0 {
		log.Println("No cookie changes detected, skipping reload")
		return
	}

	log.Printf("Detected %d account(s) with cookie changes, %d unchanged", len(toInit), len(toKeep))

	type accountResult struct {
		client    *gemini.Client
		accountID string
	}
	results := make(chan accountResult, len(toInit))

	var wg sync.WaitGroup

	for _, idx := range toInit {
		wg.Add(1)
		go func(i int, c map[string]string) {
			defer wg.Done()

			displayID := accountIDs[i]
			if displayID == "" {
				displayID = "default"
			}

			const maxRetries = 3
			for attempt := 1; attempt <= maxRetries; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

				done := make(chan error, 1)
				var client *gemini.Client

				go func() {
					var err error
					client, err = gemini.NewClient(c)
					if err != nil {
						done <- err
						return
					}
					done <- client.Init()
				}()

				select {
				case err := <-done:
					cancel()
					if err == nil {
						results <- accountResult{client: client, accountID: accountIDs[i]}
						log.Printf("Account '%s': ready", displayID)
						return
					}
					if attempt < maxRetries {
						log.Printf("Account '%s': init failed (attempt %d/%d): %v, retrying in 2s...", displayID, attempt, maxRetries, err)
						time.Sleep(2 * time.Second)
					} else {
						log.Printf("Account '%s': init failed after %d attempts: %v", displayID, maxRetries, err)
					}
				case <-ctx.Done():
					cancel()
					if attempt < maxRetries {
						log.Printf("Account '%s': init timeout (attempt %d/%d), retrying in 2s...", displayID, attempt, maxRetries)
						time.Sleep(2 * time.Second)
					} else {
						log.Printf("Account '%s': init timeout after %d attempts, skipped", displayID, maxRetries)
					}
				}
			}
		}(idx, allCookies[idx])
	}

	wg.Wait()
	close(results)

	changedAccounts := make(map[string]*gemini.Client)
	for result := range results {
		changedAccounts[result.accountID] = result.client
	}

	pool.ReplaceAccounts(accountIDs, changedAccounts)

	cookiesMu.Lock()
	accountCookies = newCookies
	cookiesMu.Unlock()

	log.Printf("Total %d account(s) available for load balancing", pool.Size())
}

func watchEnvFile() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("Failed to create file watcher: %v", err)
		return
	}
	defer watcher.Close()

	err = watcher.Add(".env")
	if err != nil {
		log.Printf("Failed to watch .env file: %v", err)
		return
	}

	log.Println("Watching .env for changes...")

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Println(".env changed, reloading accounts...")
				time.Sleep(200 * time.Millisecond)
				_ = godotenv.Load()
				loadAccountsAsync()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}
