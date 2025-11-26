package main

import (
	"log"
	"os"

	"gemini-web2api/internal/adapter"
	"gemini-web2api/internal/browser"
	"gemini-web2api/internal/gemini"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {

	_ = godotenv.Load()

	log.Println("Loading cookies...")
	cookies, err := browser.LoadCookies()
	if err != nil {
		log.Printf("FATAL: Cookie load failed: %v.", err)
		log.Println("Please ensure .env has correct __Secure-1PSID and __Secure-1PSIDTS.")
		log.Println("Or ensure you are logged into Google in Firefox.")
	} else {
		log.Println("Cookies loaded successfully.")
	}

	if cookies == nil {
		cookies = make(map[string]string)
	}

	client, err := gemini.NewClient(cookies)
	if err != nil {
		log.Fatalf("Failed to create Gemini Client: %v", err)
	}

	log.Println("Initializing Gemini session (fetching SNlM0e)...")
	if err := client.Init(); err != nil {
		log.Printf("Error initializing session: %v", err)
		log.Println("HINT: Your cookies might be invalid or expired. Please refresh them in .env")
	} else {
		log.Println("Gemini session initialized successfully! SNlM0e fetched.")
	}

	// gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.Use(adapter.CORSMiddleware())
	r.Use(adapter.AuthMiddleware())

	r.POST("/v1/chat/completions", adapter.ChatCompletionHandler(client))
	r.GET("/v1/models", adapter.ListModelsHandler)
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "Gemini-Web2API (Go) is running",
			"docs":   "POST /v1/chat/completions",
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8007"
	}

	log.Printf("Server starting on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
