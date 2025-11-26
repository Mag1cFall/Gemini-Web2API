package gemini

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"regexp"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	EndpointGoogle   = "https://www.google.com"
	EndpointInit     = "https://gemini.google.com/app"
	EndpointGenerate = "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	UserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// ModelHeaders maps model names to their specific required headers.
// You can add new models here by inspecting the 'x-goog-ext-525001261-jspb' header in browser DevTools.
var ModelHeaders = map[string]string{
	"gemini-2.5-flash":     `[1,null,null,null,"71c2d248d3b102ff"]`,
	"gemini-3-pro-preview": `[1,null,null,null,"e6fa609c3fa255c0"]`,
}

type Client struct {
	httpClient tls_client.HttpClient
	Cookies    map[string]string
	SNlM0e     string
	VersionBL  string
	FSID       string
	ReqID      int
}

func NewClient(cookies map[string]string) (*Client, error) {
	jar := tls_client.NewCookieJar()

	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(300),
		tls_client.WithClientProfile(profiles.Chrome_120),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, err
	}

	u, _ := url.Parse("https://gemini.google.com")
	var cookieList []*http.Cookie
	for k, v := range cookies {
		cookieList = append(cookieList, &http.Cookie{
			Name:   k,
			Value:  v,
			Domain: ".google.com",
			Path:   "/",
		})
	}
	client.SetCookies(u, cookieList)

	return &Client{
		httpClient: client,
		Cookies:    cookies,
		ReqID:      GenerateReqID(),
	}, nil
}

func (c *Client) Init() error {
	req, _ := http.NewRequest(http.MethodGet, EndpointInit, nil)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to visit init page: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("init page returned status: %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyString := string(bodyBytes)

	reSN := regexp.MustCompile(`"SNlM0e":"(.*?)"`)
	matchSN := reSN.FindStringSubmatch(bodyString)
	if len(matchSN) < 2 {
		return fmt.Errorf("SNlM0e token not found. Cookies might be invalid")
	}
	c.SNlM0e = matchSN[1]

	reBL := regexp.MustCompile(`"bl":"(.*?)"`)
	matchBL := reBL.FindStringSubmatch(bodyString)
	if len(matchBL) >= 2 {
		c.VersionBL = matchBL[1]
	} else {
		reBL2 := regexp.MustCompile(`data-bl="(.*?)"`)
		matchBL2 := reBL2.FindStringSubmatch(bodyString)
		if len(matchBL2) >= 2 {
			c.VersionBL = matchBL2[1]
		}
	}

	if c.VersionBL == "" {
		log.Println("Warning: Could not extract 'bl' version, using fallback.")
		c.VersionBL = "boq_assistant-bard-web-server_20240319.09_p0"
	} else {
		log.Printf("Extracted BL Version: %s", c.VersionBL)
	}

	reSID := regexp.MustCompile(`"f.sid":"(.*?)"`)
	matchSID := reSID.FindStringSubmatch(bodyString)
	if len(matchSID) >= 2 {
		c.FSID = matchSID[1]
	}

	return nil
}

func (c *Client) StreamGenerateContent(prompt string, model string, files []FileData, meta *ChatMetadata) (io.ReadCloser, error) {
	payload := BuildGeneratePayload(prompt, c.ReqID, files, meta)
	c.ReqID++

	form := url.Values{}
	form.Set("f.req", payload)
	form.Set("at", c.SNlM0e)
	data := form.Encode()

	req, _ := http.NewRequest(http.MethodPost, EndpointGenerate, strings.NewReader(data))

	q := req.URL.Query()
	q.Add("bl", c.VersionBL)
	q.Add("_reqid", fmt.Sprintf("%d", c.ReqID))
	q.Add("rt", "c")
	if c.FSID != "" {
		q.Add("f.sid", c.FSID)
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")
	req.Header.Set("X-Same-Domain", "1")

	if headerVal, ok := ModelHeaders[model]; ok {
		req.Header.Set("x-goog-ext-525001261-jspb", headerVal)
	} else {
		log.Printf("Warning: Unknown model '%s', using default header (gemini-2.5-flash).", model)
		req.Header.Set("x-goog-ext-525001261-jspb", ModelHeaders["gemini-2.5-flash"])
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("generate request failed with status: %d", resp.StatusCode)
	}

	return resp.Body, nil
}
