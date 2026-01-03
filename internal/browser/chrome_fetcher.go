package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type ChromeProfile struct {
	Name        string
	DisplayName string
	Path        string
}

func getChomeUserDataDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "User Data")
	} else if runtime.GOOS == "darwin" {
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Google", "Chrome")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "google-chrome")
}

func ListChromeProfiles() ([]ChromeProfile, error) {
	userDataDir := getChomeUserDataDir()

	entries, err := os.ReadDir(userDataDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read Chrome user data dir: %v", err)
	}

	profileNames := make(map[string]string)
	localStatePath := filepath.Join(userDataDir, "Local State")
	if data, err := os.ReadFile(localStatePath); err == nil {
		var localState struct {
			Profile struct {
				InfoCache map[string]struct {
					Name string `json:"name"`
				} `json:"info_cache"`
			} `json:"profile"`
		}
		if json.Unmarshal(data, &localState) == nil {
			for k, v := range localState.Profile.InfoCache {
				profileNames[k] = v.Name
			}
		}
	}

	var profiles []ChromeProfile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "Default" || strings.HasPrefix(name, "Profile ") {
			cookiesPath := filepath.Join(userDataDir, name, "Network", "Cookies")
			if _, err := os.Stat(cookiesPath); err == nil {
				displayName := profileNames[name]
				if displayName == "" {
					displayName = name
				}
				profiles = append(profiles, ChromeProfile{
					Name:        name,
					DisplayName: displayName,
					Path:        filepath.Join(userDataDir, name),
				})
			}
		}
	}

	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Name == "Default" {
			return true
		}
		if profiles[j].Name == "Default" {
			return false
		}
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, nil
}

func findChromePath() string {
	if runtime.GOOS == "windows" {
		paths := []string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return "chrome"
}

func FetchCookiesFromProfile(profile ChromeProfile) (map[string]string, error) {
	chromePath := findChromePath()
	port := 20000 + time.Now().Nanosecond()%10000

	userDataDir := getChomeUserDataDir()

	cmd := exec.Command(chromePath,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		fmt.Sprintf("--profile-directory=%s", profile.Name),
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Chrome: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	var resp *http.Response
	var err error
	for i := 0; i < 15; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Chrome DevTools: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var targets []struct {
		WebSocketDebuggerUrl string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &targets); err != nil || len(targets) == 0 {
		return nil, fmt.Errorf("failed to get debugger URL")
	}

	wsURL := targets[0].WebSocketDebuggerUrl
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to WebSocket: %v", err)
	}
	defer conn.Close()

	conn.WriteJSON(map[string]interface{}{"id": 1, "method": "Page.enable"})
	conn.WriteJSON(map[string]interface{}{"id": 2, "method": "Network.enable"})
	conn.WriteJSON(map[string]interface{}{
		"id":     3,
		"method": "Page.navigate",
		"params": map[string]interface{}{"url": "https://gemini.google.com"},
	})

	time.Sleep(5 * time.Second)

	conn.WriteJSON(map[string]interface{}{"id": 4, "method": "Network.getAllCookies"})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resultChan := make(chan map[string]string, 1)
	errChan := make(chan error, 1)

	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			var result struct {
				ID     int `json:"id"`
				Result struct {
					Cookies []struct {
						Name   string `json:"name"`
						Value  string `json:"value"`
						Domain string `json:"domain"`
					} `json:"cookies"`
				} `json:"result"`
			}
			if err := json.Unmarshal(message, &result); err != nil {
				continue
			}
			if result.ID == 4 {
				cookies := make(map[string]string)
				for _, c := range result.Result.Cookies {
					if (c.Name == "__Secure-1PSID" || c.Name == "__Secure-1PSIDTS") && strings.Contains(c.Domain, "google.com") {
						cookies[c.Name] = c.Value
					}
				}
				resultChan <- cookies
				return
			}
		}
	}()

	select {
	case cookies := <-resultChan:
		if cookies["__Secure-1PSID"] == "" {
			return nil, fmt.Errorf("cookie not found, please login to Google in this profile")
		}
		return cookies, nil
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for cookies")
	}
}

func RunFetchCookies() error {
	fmt.Println("=== Chrome Cookie Fetcher ===")
	fmt.Println("\n[!] Please close Chrome browser before proceeding!")
	fmt.Println("    (Press Enter to continue after closing Chrome...)")
	bufio.NewReader(os.Stdin).ReadString('\n')

	fmt.Println("Scanning Chrome profiles...")
	profiles, err := ListChromeProfiles()
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		return fmt.Errorf("no Chrome profiles found")
	}

	fmt.Println("\nAvailable Chrome profiles:")
	for i, p := range profiles {
		if p.Name == "Default" {
			fmt.Printf("  [%d] %s (default account)\n", i+1, p.DisplayName)
		} else {
			fmt.Printf("  [%d] %s → __%s\n", i+1, p.DisplayName, strings.ReplaceAll(p.DisplayName, " ", "_"))
		}
	}

	fmt.Println("\nEnter profile numbers (e.g., 1,2,3) or ALL:")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	var selectedProfiles []ChromeProfile
	if strings.ToUpper(input) == "ALL" {
		selectedProfiles = profiles
	} else {
		parts := strings.Split(input, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 1 || idx > len(profiles) {
				fmt.Printf("Invalid selection: %s\n", p)
				continue
			}
			selectedProfiles = append(selectedProfiles, profiles[idx-1])
		}
	}

	if len(selectedProfiles) == 0 {
		return fmt.Errorf("no profiles selected")
	}

	fmt.Printf("\nFetching cookies from %d profile(s)...\n", len(selectedProfiles))
	fmt.Println("Note: Chrome will start in headless mode for each profile.")

	type result struct {
		index   int
		profile ChromeProfile
		cookies map[string]string
		err     error
	}

	results := make(chan result, len(selectedProfiles))
	for idx, profile := range selectedProfiles {
		go func(i int, p ChromeProfile) {
			var cookies map[string]string
			var err error
			for retry := 0; retry < 3; retry++ {
				cookies, err = FetchCookiesFromProfile(p)
				if err == nil {
					break
				}
				if retry < 2 {
					time.Sleep(time.Duration(retry+1) * time.Second)
				}
			}
			results <- result{index: i, profile: p, cookies: cookies, err: err}
		}(idx, profile)
	}

	allResults := make([]result, len(selectedProfiles))
	for i := 0; i < len(selectedProfiles); i++ {
		res := <-results
		allResults[res.index] = res
	}

	allCookies := make(map[string]string)
	successCount := 0
	for _, res := range allResults {
		fmt.Printf("Processing %s... ", res.profile.DisplayName)
		if res.err != nil {
			fmt.Printf("FAILED: %v\n", res.err)
			continue
		}
		suffix := ""
		if res.profile.Name != "Default" {
			suffix = "_" + strings.ReplaceAll(res.profile.DisplayName, " ", "_")
		}
		allCookies["__Secure-1PSID"+suffix] = res.cookies["__Secure-1PSID"]
		allCookies["__Secure-1PSIDTS"+suffix] = res.cookies["__Secure-1PSIDTS"]
		successCount++
		fmt.Println("OK")
	}

	if len(allCookies) == 0 {
		return fmt.Errorf("no cookies fetched")
	}

	var orderedKeys []string
	orderedCookies := make(map[string]string)
	for _, res := range allResults {
		if res.err != nil {
			continue
		}
		suffix := ""
		if res.profile.Name != "Default" {
			suffix = "_" + strings.ReplaceAll(res.profile.DisplayName, " ", "_")
		}
		psidKey := "__Secure-1PSID" + suffix
		psidtsKey := "__Secure-1PSIDTS" + suffix

		orderedKeys = append(orderedKeys, psidKey, psidtsKey)
		orderedCookies[psidKey] = allCookies[psidKey]
		orderedCookies[psidtsKey] = allCookies[psidtsKey]
	}

	saveToEnvWithOrder(orderedKeys, orderedCookies)
	fmt.Printf("\nDone! Saved %d/%d cookie pairs to .env\n", successCount, len(selectedProfiles))
	return nil
}
