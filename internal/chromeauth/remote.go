package chromeauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/auth"
)

var (
	// remoteRenewalInterval 是源电脑为远程配对账号签发并上传新会话的间隔，短于 OAuthMultilogin 会话 2 小时的有效期
	remoteRenewalInterval = 100 * time.Minute
	// remoteRenewalRetry 是签发或上传失败后的重试间隔
	remoteRenewalRetry = 5 * time.Minute
	// remoteRenewalsFile 是源电脑保存远程配对续签记录的路径
	remoteRenewalsFile = filepath.Join("data", "remote-renewals.json")
)

// remoteRenewal 是源电脑为一个远程配对账号签发并上传新会话所需的材料，Metadata 为远程实例中的账号身份
type remoteRenewal struct {
	Origin       string             `json:"origin"`
	Token        string             `json:"token"`
	RequestProxy string             `json:"requestProxy,omitempty"`
	Metadata     auth.Metadata      `json:"metadata"`
	OAuth        auth.OAuthMaterial `json:"oauth"`
}

// remoteRejection 是远程实例拒绝上传时返回的状态码与原因
type remoteRejection struct {
	status  int
	message string
}

func (e *remoteRejection) Error() string {
	if e.message == "" {
		return fmt.Sprintf("远程实例导入失败: HTTP %d", e.status)
	}
	return fmt.Sprintf("远程实例导入失败: HTTP %d: %s", e.status, e.message)
}

// RemoteOrigin 校验远程实例地址只含协议、主机与反向代理的子路径，非本机地址必须使用 HTTPS
func RemoteOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("--remote 必须是远程实例的地址，例如 https://example.com 或 https://example.com/gemini")
	}
	ip := net.ParseIP(parsed.Hostname())
	loopback := strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !loopback) {
		return "", fmt.Errorf("--remote 必须使用 HTTPS；只有本机地址（例如 SSH 隧道）可以使用 HTTP")
	}
	return parsed.Scheme + "://" + parsed.Host + strings.TrimSuffix(parsed.Path, "/"), nil
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

// uploadStates 凭配对令牌把认证状态文件逐个上传到远程实例，返回远程实例中的账号 ID 与需要源电脑续签的记录；设备绑定的续签材料只能在本机使用，上传时去掉
func uploadStates(ctx context.Context, origin string, token string, paths []string, fallbackProxy string) ([]string, []remoteRenewal, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	ids := make([]string, 0, len(paths))
	var renewals []remoteRenewal
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return ids, nil, fmt.Errorf("读取认证状态: %w", err)
		}
		var state auth.StorageState
		if err := json.Unmarshal(data, &state); err != nil {
			return ids, nil, fmt.Errorf("解析认证状态: %w", err)
		}
		oauth := state.Metadata.OAuth
		bound := oauth != nil && len(oauth.WrappedBindingKey) > 0
		if bound {
			state.Metadata.OAuth, state.Metadata.BoundSession = nil, nil
		}
		accountID, renewalToken, err := postRemoteState(ctx, client, origin, token, state)
		if err != nil {
			return ids, nil, err
		}
		ids = append(ids, accountID)
		if bound {
			_, requestProxy, err := importProxies(state.Metadata.Proxy, fallbackProxy)
			if err != nil {
				return ids, nil, err
			}
			state.Metadata.ID = accountID
			renewals = append(renewals, remoteRenewal{Origin: origin, Token: renewalToken, RequestProxy: requestProxy, Metadata: state.Metadata, OAuth: *oauth})
		}
	}
	return ids, renewals, nil
}

// postRemoteState 凭配对令牌或续签凭据把认证状态上传到远程实例，返回远程账号 ID 与远程实例签发的续签凭据
func postRemoteState(ctx context.Context, client *http.Client, origin string, token string, state auth.StorageState) (string, string, error) {
	payload, err := json.Marshal(map[string]auth.StorageState{"storage_state": state})
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/pairing/accounts", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("连接远程实例: %w", err)
	}
	var result struct {
		Account struct {
			AccountID string `json:"account_id"`
		} `json:"account"`
		RenewalToken string `json:"renewal_token"`
		Error        struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return "", "", &remoteRejection{status: response.StatusCode, message: result.Error.Message}
	}
	if decodeErr != nil {
		return "", "", fmt.Errorf("解析远程实例响应: %w", decodeErr)
	}
	return result.Account.AccountID, result.RenewalToken, nil
}

// readRemoteRenewals 读取源电脑保存的远程配对续签记录
func readRemoteRenewals(path string) ([]remoteRenewal, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取远程续签记录: %w", err)
	}
	var renewals []remoteRenewal
	if err := json.Unmarshal(data, &renewals); err != nil {
		return nil, fmt.Errorf("解析远程续签记录: %w", err)
	}
	return renewals, nil
}

// writeRemoteRenewals 保存远程配对续签记录，keep 决定保留哪些已有记录，added 追加在后
func writeRemoteRenewals(path string, keep func(remoteRenewal) bool, added ...remoteRenewal) error {
	existing, err := readRemoteRenewals(path)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(append(slices.DeleteFunc(existing, func(item remoteRenewal) bool { return !keep(item) }), added...), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建远程续签记录目录: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("保存远程续签记录: %w", err)
	}
	return nil
}

// saveRemoteRenewals 保存新上传账号的续签记录，替换同一远程实例中同一账号的旧记录
func saveRemoteRenewals(path string, added []remoteRenewal) error {
	return writeRemoteRenewals(path, func(item remoteRenewal) bool {
		return !slices.ContainsFunc(added, func(next remoteRenewal) bool {
			return next.Origin == item.Origin && next.Metadata.ID == item.Metadata.ID
		})
	}, added...)
}

// originRemoteRenewals 返回源电脑为远程实例 origin 保存的续签记录
func originRemoteRenewals(path string, origin string) ([]remoteRenewal, error) {
	existing, err := readRemoteRenewals(path)
	if err != nil {
		return nil, err
	}
	selected := slices.DeleteFunc(existing, func(item remoteRenewal) bool { return item.Origin != origin })
	if len(selected) == 0 {
		return nil, fmt.Errorf("本机没有 %s 的远程续签记录，先用 setup --remote 上传 Chrome 导入的账号", origin)
	}
	return selected, nil
}

// renewRemoteStates 在前台按间隔为远程配对账号签发新会话并上传，first 后开始第一轮，失败时按重试间隔重试；Google 拒绝续签材料或远程实例拒绝续签凭据时停止该账号并删除其记录
func renewRemoteStates(ctx context.Context, path string, renewals []remoteRenewal, first time.Duration, output io.Writer) error {
	origin := renewals[0].Origin
	fmt.Fprintf(output, "保持此窗口打开，本机按时为远程实例 %s 续签账号；关闭后可用 setup --remote %s --renew 恢复\n", origin, origin)
	client := &http.Client{Timeout: 2 * time.Minute}
	due := make([]time.Time, len(renewals))
	for index := range due {
		due[index] = time.Now().Add(first)
	}
	for len(renewals) > 0 {
		timer := time.NewTimer(time.Until(slices.MinFunc(due, time.Time.Compare)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		for index := 0; index < len(renewals); index++ {
			if time.Now().Before(due[index]) {
				continue
			}
			renewal := renewals[index]
			err := pushRemoteRenewal(ctx, client, renewal)
			now := time.Now()
			var rejection *remoteRejection
			switch {
			case ctx.Err() != nil:
				return nil
			case errors.Is(err, ErrCredentialsRejected) || errors.As(err, &rejection) && rejection.status == http.StatusUnauthorized:
				fmt.Fprintf(output, "%s 账号 %s 停止续签: %v\n", now.Format(time.DateTime), renewal.Metadata.ID, err)
				if err := writeRemoteRenewals(path, func(item remoteRenewal) bool { return item.Origin != renewal.Origin || item.Token != renewal.Token }); err != nil {
					return err
				}
				renewals, due = slices.Delete(renewals, index, index+1), slices.Delete(due, index, index+1)
				index--
			case err != nil:
				due[index] = now.Add(remoteRenewalRetry)
				fmt.Fprintf(output, "%s 账号 %s 续签失败，%s 后重试: %v\n", now.Format(time.DateTime), renewal.Metadata.ID, remoteRenewalRetry, err)
			default:
				due[index] = now.Add(remoteRenewalInterval)
				fmt.Fprintf(output, "%s 账号 %s 已续签并上传，下次续签 %s\n", now.Format(time.DateTime), renewal.Metadata.ID, due[index].Format(time.TimeOnly))
			}
		}
	}
	return nil
}

// pushRemoteRenewal 以本机设备绑定密钥签发新会话，凭续签凭据上传到远程实例
func pushRemoteRenewal(ctx context.Context, client *http.Client, renewal remoteRenewal) error {
	identity := browserIdentity{chrome: renewal.Metadata.Chrome, language: renewal.Metadata.Language}
	cookies, _, err := fetchGoogleCookies(ctx, identity, renewal.OAuth.GaiaID, renewal.OAuth.RefreshToken, renewal.OAuth.WrappedBindingKey, renewal.RequestProxy)
	if err != nil {
		return err
	}
	_, _, err = postRemoteState(ctx, client, renewal.Origin, renewal.Token, auth.StorageState{Cookies: toStorageCookies(cookies), Metadata: renewal.Metadata})
	return err
}
