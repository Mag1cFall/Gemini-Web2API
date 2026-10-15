package chromeauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// RemoteOrigin 校验远程实例地址只含协议与主机，非本机地址必须使用 HTTPS
func RemoteOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("--remote 必须是远程实例的 origin，例如 https://example.com")
	}
	ip := net.ParseIP(parsed.Hostname())
	loopback := strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !loopback) {
		return "", fmt.Errorf("--remote 必须使用 HTTPS；只有本机地址（例如 SSH 隧道）可以使用 HTTP")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// readPairingToken 从标准输入读取配对令牌，令牌不进入命令历史
func readPairingToken(input io.Reader, output io.Writer) (string, error) {
	fmt.Fprint(output, "请粘贴远程实例导入账号页生成的配对令牌: ")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("读取配对令牌: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", fmt.Errorf("配对令牌不能为空")
	}
	return token, nil
}

// uploadStates 凭配对令牌把认证状态文件逐个上传到远程实例，返回远程实例中的账号 ID
func uploadStates(ctx context.Context, origin string, token string, paths []string) ([]string, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return ids, fmt.Errorf("读取认证状态: %w", err)
		}
		payload, err := json.Marshal(map[string]json.RawMessage{"storage_state": data})
		if err != nil {
			return ids, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/pairing/accounts", bytes.NewReader(payload))
		if err != nil {
			return ids, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return ids, fmt.Errorf("连接远程实例: %w", err)
		}
		var result struct {
			Account struct {
				AccountID string `json:"account_id"`
			} `json:"account"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
		response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			if result.Error.Message != "" {
				return ids, fmt.Errorf("远程实例导入失败: HTTP %d: %s", response.StatusCode, result.Error.Message)
			}
			return ids, fmt.Errorf("远程实例导入失败: HTTP %d", response.StatusCode)
		}
		if decodeErr != nil {
			return ids, fmt.Errorf("解析远程实例响应: %w", decodeErr)
		}
		ids = append(ids, result.Account.AccountID)
	}
	return ids, nil
}
