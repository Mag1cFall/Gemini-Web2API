package chromeauth

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/proxypool"
)

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ",")
}

func (values *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("参数值不能为空")
	}
	*values = append(*values, value)
	return nil
}

// RunSetup 执行首次账号配置
func RunSetup(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, interactive bool) error {
	stdin = bufio.NewReader(stdin)
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var profiles stringList
	var emails stringList
	flags.Var(&profiles, "profile", "要导入的浏览器 Profile，可重复")
	flags.Var(&emails, "email", "要导入的 Google 邮箱或 Profile 名称，可重复")
	browserFlag := flags.String("browser", "chrome", "要导入的浏览器 (chrome、brave、edge、firefox)")
	chromeRoot := flags.String("chrome-root", "", "浏览器 User Data 目录（可覆盖默认路径）")
	output := flags.String("output", "auth", "认证状态输出目录")
	remote := flags.String("remote", "", "把导入的账号上传到远程实例，例如 https://example.com，配对令牌从标准输入读取")
	proxy := flags.String("proxy", "", "账号固定代理 URL")
	cookieHeader := flags.String("cookie", "", "从一行 Cookie Header 导入账号")
	cookieFile := flags.String("cookie-file", "", "从浏览器导出的 Cookie JSON 或 Netscape cookies.txt 文件导入账号")
	accountID := flags.String("id", "", "Cookie 导入使用的账号名称")
	authUser := flags.Int("authuser", 0, "Cookie 中目标账号的序号，即官网地址 gemini.google.com/u/N/app 中的 N")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("setup 不接受位置参数")
	}
	proxyURL := strings.TrimSpace(*proxy)
	requestProxy := proxypool.System()
	if pool := config.SplitList(os.Getenv("PROXY")); len(pool) != 0 {
		requestProxy = pool[0]
	}

	outputPath, err := filepath.Abs(strings.TrimSpace(*output))
	if err != nil {
		return fmt.Errorf("解析输出目录: %w", err)
	}
	origin := ""
	if strings.TrimSpace(*remote) != "" {
		if origin, err = RemoteOrigin(*remote); err != nil {
			return err
		}
		if outputPath, err = os.MkdirTemp("", "gemini-web2api-setup-"); err != nil {
			return fmt.Errorf("创建临时目录: %w", err)
		}
		defer os.RemoveAll(outputPath)
	}
	finish := func(paths []string, modelCount int) error {
		if origin == "" {
			return writeJSON(stdout, map[string]any{"setup": true, "accounts": len(paths), "models": modelCount, "output": outputPath})
		}
		token, err := readPairingToken(stdin, stderr)
		if err != nil {
			return err
		}
		ids, err := uploadStates(ctx, origin, token, paths)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"setup": true, "accounts": len(ids), "remote": origin, "ids": ids})
	}
	cookies := strings.TrimSpace(*cookieHeader)
	if path := strings.TrimSpace(*cookieFile); path != "" {
		if cookies != "" {
			return fmt.Errorf("--cookie 与 --cookie-file 只能选一个")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 Cookie 文件: %w", err)
		}
		cookies = string(data)
	}
	if cookies != "" {
		if len(profiles) != 0 || len(emails) != 0 || strings.TrimSpace(*chromeRoot) != "" {
			return fmt.Errorf("--cookie、--cookie-file 不能与 Chrome 账号选择参数同时使用")
		}
		result, modelCount, err := ImportCookies(ctx, CookieImportOptions{
			Cookies: cookies, ID: *accountID, Output: outputPath, Proxy: proxyURL, RequestProxy: requestProxy, AuthUser: *authUser,
		})
		if err != nil {
			return err
		}
		return finish([]string{result.Path}, modelCount)
	}
	if strings.TrimSpace(*accountID) != "" || *authUser != 0 {
		return fmt.Errorf("--id、--authuser 只与 --cookie、--cookie-file 一起使用")
	}

	targetBrowser := strings.ToLower(strings.TrimSpace(*browserFlag))
	if targetBrowser == "" {
		targetBrowser = BrowserChrome
	}
	root := strings.TrimSpace(*chromeRoot)
	if root == "" {
		var err error
		root, err = DefaultBrowserRoot(targetBrowser)
		if err != nil {
			return err
		}
	} else {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return fmt.Errorf("解析浏览器目录: %w", err)
		}
	}

	if len(profiles) == 0 && len(emails) == 0 {
		if !interactive {
			return fmt.Errorf("非交互模式请通过 --profile 或 --email 选择账号")
		}
		accounts, err := DiscoverBrowser(targetBrowser, root)
		if (err != nil || len(accounts) == 0) && targetBrowser == BrowserChrome && *chromeRoot == "" {
			altBrowsers := []string{BrowserBrave, BrowserEdge, BrowserFirefox}
			for _, b := range altBrowsers {
				altRoot, rErr := DefaultBrowserRoot(b)
				if rErr == nil {
					if accs, dErr := DiscoverBrowser(b, altRoot); dErr == nil && len(accs) > 0 {
						targetBrowser = b
						root = altRoot
						accounts = accs
						err = nil
						fmt.Fprintf(stderr, "未发现 Chrome 账号，自动切换到 %s\n", b)
						break
					}
				}
			}
		}
		if err != nil {
			return err
		}
		emails, err = promptAccounts(accounts, stdin, stderr)
		if err != nil {
			return err
		}
	}

	results, modelCount, err := ImportBrowser(ctx, BrowserImportOptions{
		Browser:      targetBrowser,
		BrowserRoot:  root,
		Output:       outputPath,
		Proxy:        proxyURL,
		RequestProxy: requestProxy,
		Profiles:     profiles,
		Emails:       emails,
	})
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, result.Path)
	}
	return finish(paths, modelCount)
}

// promptAccounts 列出可导入账号并按输入的编号返回选中账号的邮箱或 Profile
func promptAccounts(accounts []Account, input io.Reader, output io.Writer) (stringList, error) {
	available := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Importable {
			available = append(available, account)
		}
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("没有可导入账号")
	}
	fmt.Fprintln(output, "可导入账号:")
	for index, account := range available {
		extra := ""
		if account.SourceType == "cookie" {
			extra = " [Cookie]"
		}
		label := account.Email
		if label == "" {
			label = account.Profile
		}
		fmt.Fprintf(output, "%d. %s  %s  %s%s\n", index+1, label, account.Profile, account.DisplayName, extra)
	}
	fmt.Fprint(output, "请输入逗号分隔的编号: ")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("读取账号选择: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("未选择账号")
	}
	selected := make(stringList, 0)
	seen := make(map[int]struct{})
	for _, raw := range strings.Split(line, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		index, parseErr := strconv.Atoi(raw)
		if parseErr != nil || index < 1 || index > len(available) {
			return nil, fmt.Errorf("编号无效: %q", raw)
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		target := available[index-1].Email
		if target == "" {
			target = available[index-1].Profile
		}
		selected = append(selected, target)
	}
	return selected, nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("输出 JSON: %w", err)
	}
	return nil
}
