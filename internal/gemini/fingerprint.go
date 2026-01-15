package gemini

import (
	"log"
	"math/rand"
	"sync"
	"time"

	fakeUA "github.com/lib4u/fake-useragent"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type ProfileConfig struct {
	Profile    profiles.ClientProfile
	Browser    string
	OS         []string
	FallbackUA string
}

var profileConfigs = []ProfileConfig{
	{profiles.Chrome_133, "Chrome", []string{"Windows", "Mac OS X", "Linux"}, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"},
	{profiles.Chrome_131, "Chrome", []string{"Windows", "Mac OS X", "Linux"}, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	{profiles.Chrome_124, "Chrome", []string{"Windows", "Mac OS X"}, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"},
	{profiles.Chrome_120, "Chrome", []string{"Windows", "Mac OS X"}, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
	{profiles.Firefox_135, "Firefox", []string{"Windows", "Mac OS X", "Linux"}, "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0"},
	{profiles.Firefox_133, "Firefox", []string{"Windows", "Mac OS X", "Linux"}, "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0"},
	{profiles.Firefox_123, "Firefox", []string{"Windows", "Mac OS X"}, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:123.0) Gecko/20100101 Firefox/123.0"},
	{profiles.Safari_16_0, "Safari", []string{"Mac OS X"}, "Mozilla/5.0 (Macintosh; Intel Mac OS X 13_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Safari/605.1.15"},
	{profiles.Safari_IOS_18_0, "Safari", []string{"iOS"}, "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"},
	{profiles.Safari_IOS_17_0, "Safari", []string{"iOS"}, "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	{profiles.Opera_91, "Opera", []string{"Windows", "Mac OS X"}, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/105.0.0.0 Safari/537.36 OPR/91.0.0.0"},
}

var (
	currentProfile   ProfileConfig
	currentUserAgent string
	profileMu        sync.RWMutex
	rng              *rand.Rand
	uaGenerator      *fakeUA.UserAgent
)

func init() {
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))

	var err error
	uaGenerator, err = fakeUA.New()
	if err != nil {
		log.Printf("Warning: Failed to init fake-useragent, using fallbacks: %v", err)
	}

	selectRandomProfile()
}

func selectRandomProfile() {
	idx := rng.Intn(len(profileConfigs))
	currentProfile = profileConfigs[idx]
	currentUserAgent = generateUserAgentForProfile(currentProfile)
}

func generateUserAgentForProfile(config ProfileConfig) string {
	if uaGenerator == nil {
		return config.FallbackUA
	}

	osIdx := rng.Intn(len(config.OS))
	selectedOS := config.OS[osIdx]

	var ua string
	switch config.Browser {
	case "Chrome":
		ua = uaGenerator.Filter().Chrome().Os(selectedOS).Get()
	case "Firefox":
		ua = uaGenerator.Filter().Firefox().Os(selectedOS).Get()
	case "Safari":
		ua = uaGenerator.Filter().Safari().Os(selectedOS).Get()
	case "Opera":
		ua = uaGenerator.Filter().Opera().Os(selectedOS).Get()
	case "Edge":
		ua = uaGenerator.Filter().Edge().Os(selectedOS).Get()
	default:
		ua = uaGenerator.Filter().Os(selectedOS).Get()
	}

	if ua == "" {
		return config.FallbackUA
	}
	return ua
}

func GetRandomProfile() ProfileConfig {
	profileMu.Lock()
	defer profileMu.Unlock()
	selectRandomProfile()
	return currentProfile
}

func GetCurrentProfile() ProfileConfig {
	profileMu.RLock()
	defer profileMu.RUnlock()
	return currentProfile
}

func GetCurrentUserAgent() string {
	profileMu.RLock()
	defer profileMu.RUnlock()
	return currentUserAgent
}

func GetClientOptions(profile ProfileConfig) []tls_client.HttpClientOption {
	return []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(120),
		tls_client.WithClientProfile(profile.Profile),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
		tls_client.WithRandomTLSExtensionOrder(),
	}
}

func RandomDelay() {
	delay := time.Duration(100+rng.Intn(200)) * time.Millisecond
	time.Sleep(delay)
}
