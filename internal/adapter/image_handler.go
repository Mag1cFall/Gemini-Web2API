package adapter

import (
	"encoding/base64"
	"fmt"
	"gemini-web2api/internal/balancer"
	"gemini-web2api/internal/gemini"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type ImageGenerationRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	ResponseFormat string `json:"response_format"`
	Quality        string `json:"quality"`
	Style          string `json:"style"`
}

var aspectRatioMap = map[string]string{
	"1024x1024": "1:1",
	"512x512":   "1:1",
	"256x256":   "1:1",
	"1024x768":  "4:3",
	"1280x960":  "4:3",
	"768x1024":  "3:4",
	"960x1280":  "3:4",
	"1792x1024": "16:9",
	"1920x1080": "16:9",
	"1024x1792": "9:16",
	"1080x1920": "9:16",
	"1792x768":  "21:9",
	"2560x1080": "21:9",
}

func sizeToAspectRatio(size string) string {
	if ratio, ok := aspectRatioMap[size]; ok {
		return ratio
	}
	return "1:1"
}

func ImageGenerationHandler(pool *balancer.AccountPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		client, accountID := pool.Next()
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available accounts"})
			return
		}

		c.Set("account_id", accountID)

		var req ImageGenerationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if req.Prompt == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'prompt' field"})
			return
		}

		if req.N <= 0 {
			req.N = 1
		}
		if req.N > 4 {
			req.N = 4
		}
		if req.Model == "" {
			req.Model = "gemini-2.5-flash-image"
		}
		if req.Size == "" {
			req.Size = "1024x1024"
		}
		if req.ResponseFormat == "" {
			req.ResponseFormat = "b64_json"
		}

		log.Printf("[Images] Request | Model: %s | Prompt: %.50s... | N: %d | Size: %s",
			req.Model, req.Prompt, req.N, req.Size)

		finalPrompt := fmt.Sprintf("Generate an image of %s", req.Prompt)
		if req.Quality == "hd" {
			finalPrompt += " (high quality, highly detailed, 4k resolution, hdr)"
		}
		if req.Style == "vivid" {
			finalPrompt += " (vivid colors, dramatic lighting, rich details)"
		} else if req.Style == "natural" {
			finalPrompt += " (natural lighting, realistic, photorealistic)"
		}

		gemini.RandomDelay()

		var images []gin.H
		var errors []string

		for i := 0; i < req.N; i++ {
			respBody, err := client.StreamGenerateContent(finalPrompt, req.Model, nil, nil)
			if err != nil {
				log.Printf("[Images] Request %d failed: %v", i, err)
				errors = append(errors, err.Error())
				continue
			}

			extracted := extractImagesFromResponse(respBody, req.ResponseFormat, client.Cookies)
			respBody.Close()

			if len(extracted) > 0 {
				images = append(images, extracted...)
				log.Printf("[Images] Request %d succeeded, got %d images", i, len(extracted))
			} else {
				errors = append(errors, "No images generated")
			}
		}

		if len(images) == 0 {
			errMsg := "Failed to generate images"
			if len(errors) > 0 {
				errMsg = strings.Join(errors, "; ")
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"message": errMsg,
					"type":    "server_error",
				},
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"created": time.Now().Unix(),
			"data":    images,
		})
	}
}

func extractImagesFromResponse(reader io.Reader, format string, cookies map[string]string) []gin.H {
	var images []gin.H

	content, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("[Images] Failed to read response: %v", err)
		return images
	}

	var allParts []gjson.Result

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimPrefix(line, ")]}'")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		outer := gjson.Parse(line)
		if !outer.IsArray() {
			continue
		}

		outer.ForEach(func(_, part gjson.Result) bool {
			allParts = append(allParts, part)
			return true
		})
	}

	if len(allParts) == 0 {
		log.Printf("[Images] No parts found in response")
		return images
	}

	bodyIndex := -1
	var body gjson.Result

	for i, part := range allParts {
		dataStr := part.Get("2").String()
		if dataStr == "" {
			continue
		}
		inner := gjson.Parse(dataStr)
		if inner.Get("4").Exists() {
			bodyIndex = i
			body = inner
			break
		}
	}

	if bodyIndex < 0 || !body.Exists() {
		log.Printf("[Images] No body found in response")
		return images
	}

	for i := bodyIndex; i < len(allParts); i++ {
		imgDataStr := allParts[i].Get("2").String()
		if imgDataStr == "" {
			continue
		}
		imgInner := gjson.Parse(imgDataStr)
		imgCandidate := imgInner.Get("4.0")
		if !imgCandidate.Get("12.7.0").Exists() {
			continue
		}

		imgCandidate.Get("12.7.0").ForEach(func(idx gjson.Result, genImg gjson.Result) bool {
			url := genImg.Get("0.3.3").String()
			if url == "" {
				return true
			}

			if strings.HasPrefix(url, "http://googleusercontent.com/image_generation_content") {
				return true
			}

			fullSizeURL := url
			if !strings.Contains(url, "=s") {
				fullSizeURL = url + "=s2048"
			}

			log.Printf("[Images] Found image %d URL: %s...", idx.Int(), fullSizeURL[:minInt(len(fullSizeURL), 60)])

			if format == "url" {
				images = append(images, gin.H{"url": fullSizeURL})
			} else {
				data := fetchImageWithCookies(fullSizeURL, cookies)
				if data != "" {
					images = append(images, gin.H{"b64_json": data})
				}
			}
			return true
		})

		if len(images) > 0 {
			break
		}
	}

	return images
}

func getNestedValue(data interface{}, path []int) interface{} {
	current := data
	for _, idx := range path {
		arr, ok := current.([]interface{})
		if !ok || idx >= len(arr) {
			return nil
		}
		current = arr[idx]
	}
	return current
}

func getNestedValueStr(data interface{}, path []int) string {
	val := getNestedValue(data, path)
	if val == nil {
		return ""
	}
	str, ok := val.(string)
	if !ok {
		return ""
	}
	return str
}

func fetchImageWithCookies(url string, cookies map[string]string) string {
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("[Images] Failed to create request: %v", err)
		return ""
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")

	var cookieParts []string
	for k, v := range cookies {
		cookieParts = append(cookieParts, k+"="+v)
	}
	if len(cookieParts) > 0 {
		req.Header.Set("Cookie", strings.Join(cookieParts, "; "))
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Images] Failed to fetch image: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("[Images] Image fetch returned status %d for URL: %s", resp.StatusCode, url[:minInt(len(url), 80)])
		return ""
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Images] Failed to read image data: %v", err)
		return ""
	}

	if len(data) == 0 {
		return ""
	}

	return base64.StdEncoding.EncodeToString(data)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
