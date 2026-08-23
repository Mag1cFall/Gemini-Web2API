package chromeauth

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
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
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var profiles stringList
	var emails stringList
	flags.Var(&profiles, "profile", "要导入的 Chrome Profile，可重复")
	flags.Var(&emails, "email", "要导入的 Google 邮箱，可重复")
	chromeRoot := flags.String("chrome-root", "", "Chrome User Data 目录")
	output := flags.String("output", "auth", "认证状态输出目录")
	proxy := flags.String("proxy", "", "账号固定代理 URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("setup 不接受位置参数")
	}

	root, err := resolveChromeRoot(*chromeRoot)
	if err != nil {
		return err
	}
	outputPath, err := filepath.Abs(strings.TrimSpace(*output))
	if err != nil {
		return fmt.Errorf("解析输出目录: %w", err)
	}
	if len(profiles) == 0 && len(emails) == 0 {
		if !interactive {
			return fmt.Errorf("非交互模式请通过 --profile 或 --email 选择账号")
		}
		accounts, err := Discover(root)
		if err != nil {
			return err
		}
		profiles, err = promptProfiles(accounts, stdin, stderr)
		if err != nil {
			return err
		}
	}

	results, err := Import(ctx, ImportOptions{
		ChromeRoot: root,
		Output:     outputPath,
		Proxy:      *proxy,
		Profiles:   profiles,
		Emails:     emails,
	})
	if err != nil {
		return err
	}
	modelCount, err := verifyImported(ctx, results)
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"setup": true, "accounts": len(results), "models": modelCount, "output": outputPath,
	})
}

func promptProfiles(accounts []Account, input io.Reader, output io.Writer) (stringList, error) {
	available := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Importable {
			available = append(available, account)
		}
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("本机 Chrome 没有可导入账号")
	}
	fmt.Fprintln(output, "可导入账号:")
	for index, account := range available {
		fmt.Fprintf(output, "%d. %s  %s  %s\n", index+1, account.Profile, account.DisplayName, account.Email)
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
		index, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || index < 1 || index > len(available) {
			return nil, fmt.Errorf("账号编号 %q 无效", strings.TrimSpace(raw))
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		selected = append(selected, available[index-1].Profile)
	}
	return selected, nil
}

func resolveChromeRoot(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return defaultChromeRoot()
	}
	root, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("解析 Chrome User Data 目录: %w", err)
	}
	return root, nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("输出 JSON: %w", err)
	}
	return nil
}
