package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	_ "github.com/bdandy/go-socks4" // go-socks4 向 x/net/proxy 注册 socks4 与 socks4a
	"golang.org/x/mod/semver"
	xproxy "golang.org/x/net/proxy"

	"github.com/Mag1cFall/Gemini-Web2API/internal/admin"
	"github.com/Mag1cFall/Gemini-Web2API/internal/config"
	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
	"github.com/Mag1cFall/Gemini-Web2API/internal/proxypool"
)

// releaseAPI 是 GitHub 最新发行版本接口
const releaseAPI = "https://api.github.com/repos/Mag1cFall/Gemini-Web2API/releases/latest"

// releaseCheckInterval 是最新发行版本的缓存时长
const releaseCheckInterval = time.Hour

// githubRelease 是 GitHub 发行版本接口中用到的字段
type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

// githubAsset 是发行版本中的一个文件
type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

// selfUpdater 读取最新发行版本、下载校验发行包并安装到当前可执行文件
type selfUpdater struct {
	version    string
	mode       string
	releaseURL string
	executable string
	proxy      func() string
	restart    func()
	mu         sync.Mutex
	release    *githubRelease
	checked    time.Time
	checkErr   error
	updating   bool
}

// newSelfUpdater 按构建版本创建更新器，proxy 返回下载使用的代理，restart 在安装完成后重启到新版本
func newSelfUpdater(version string, executable string, proxy func() string, restart func()) *selfUpdater {
	return &selfUpdater{version: version, mode: updateMode(version), releaseURL: releaseAPI, executable: executable, proxy: proxy, restart: restart}
}

// updateMode 返回更新方式：development 为源码构建，container 随镜像更新，binary 可原地替换
func updateMode(version string) string {
	if !semver.IsValid(version) || semver.Prerelease(version) != "" || semver.Build(version) != "" {
		return "development"
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return "container"
		}
	}
	return "binary"
}

// updateProxy 返回下载更新使用的代理：配置的第一个代理，未配置时为系统代理
func updateProxy(configured string) string {
	if proxies := config.SplitList(configured); len(proxies) > 0 {
		return proxies[0]
	}
	return proxypool.System()
}

// UpdateStatus 返回当前版本与最新发行版本，refresh 为 true 或缓存过期时重新读取
func (u *selfUpdater) UpdateStatus(ctx context.Context, refresh bool) (admin.UpdateStatus, error) {
	u.mu.Lock()
	stale := refresh || time.Since(u.checked) >= releaseCheckInterval
	u.mu.Unlock()
	if stale {
		release, err := u.fetchRelease(ctx)
		if ctx.Err() != nil {
			return admin.UpdateStatus{}, ctx.Err()
		}
		u.mu.Lock()
		u.checked, u.checkErr = time.Now(), err
		if err == nil {
			u.release = release
		}
		u.mu.Unlock()
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.statusLocked(), nil
}

// ApplyUpdate 下载最新发行包，校验 SHA-256 后安装并重启到新版本
func (u *selfUpdater) ApplyUpdate(ctx context.Context) (admin.UpdateStatus, error) {
	u.mu.Lock()
	switch {
	case u.mode == "container":
		u.mu.Unlock()
		return admin.UpdateStatus{}, admin.UpdateConflict("Docker 部署随镜像更新")
	case u.mode != "binary":
		u.mu.Unlock()
		return admin.UpdateStatus{}, admin.UpdateConflict("源码构建不支持在线更新")
	case u.updating:
		u.mu.Unlock()
		return admin.UpdateStatus{}, admin.UpdateConflict("更新正在进行")
	}
	u.updating = true
	u.mu.Unlock()
	release, err := u.fetchRelease(ctx)
	if err == nil && semver.Compare(release.TagName, u.version) <= 0 {
		err = admin.UpdateConflict("当前已是最新版本 " + u.version)
	}
	if err == nil {
		logging.Info(logging.SourceService, "在线更新", "阶段", "1/2 下载发行包", "版本", release.TagName)
		err = u.install(ctx, release)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil {
		u.updating = false
		logging.Error(logging.SourceService, "在线更新失败", "错误", err)
		return admin.UpdateStatus{}, err
	}
	u.release, u.checked, u.checkErr = release, time.Now(), nil
	logging.Info(logging.SourceService, "在线更新完成", "版本", release.TagName)
	status := u.statusLocked()
	u.restart()
	return status, nil
}

// statusLocked 在持有锁时生成更新状态
func (u *selfUpdater) statusLocked() admin.UpdateStatus {
	status := admin.UpdateStatus{Current: u.version, Mode: u.mode, CheckedAt: u.checked, Updating: u.updating}
	if u.checkErr != nil {
		status.Error = u.checkErr.Error()
	}
	if release := u.release; release != nil {
		status.Latest, status.Notes, status.URL, status.PublishedAt = release.TagName, release.Body, release.HTMLURL, release.PublishedAt
		status.Available = semver.Compare(release.TagName, u.version) > 0
	}
	return status
}

// fetchRelease 读取 GitHub 最新发行版本
func (u *selfUpdater) fetchRelease(ctx context.Context) (*githubRelease, error) {
	response, err := u.get(ctx, u.releaseURL, "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("读取最新发行版本: %w", err)
	}
	defer response.Body.Close()
	var release githubRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析最新发行版本: %w", err)
	}
	return &release, nil
}

// download 下载 release 中名为 name 的发行包并按发行版本记录的 SHA-256 校验
func (u *selfUpdater) download(ctx context.Context, release *githubRelease, name string) ([]byte, error) {
	index := slices.IndexFunc(release.Assets, func(asset githubAsset) bool { return asset.Name == name })
	if index < 0 {
		return nil, fmt.Errorf("发行版本 %s 没有 %s", release.TagName, name)
	}
	asset := release.Assets[index]
	expected, ok := strings.CutPrefix(asset.Digest, "sha256:")
	if !ok {
		return nil, fmt.Errorf("发行包 %s 缺少 SHA-256 摘要", name)
	}
	response, err := u.get(ctx, asset.URL, "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	archive, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != expected {
		return nil, fmt.Errorf("发行包 %s 的 SHA-256 与发行版本记录不一致", name)
	}
	return archive, nil
}

// get 经下载代理发出 GET，非 200 响应返回错误：SOCKS4 与 SOCKS4a 代理经 go-socks4 拨号，其他代理交给 http.Transport
func (u *selfUpdater) get(ctx context.Context, address string, accept string) (*http.Response, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy := u.proxy(); proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("解析代理: %w", err)
		}
		switch proxyURL.Scheme {
		case "socks4", "socks4a":
			dialer, err := xproxy.FromURL(proxyURL, xproxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("解析代理: %w", err)
			}
			transport.DialContext = func(_ context.Context, network, target string) (net.Conn, error) {
				return dialer.Dial(network, target)
			}
		default:
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		return nil, fmt.Errorf("HTTP %d %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return response, nil
}

// extractExecutable 从 zip 或 tar.gz 发行包中取出可执行文件
func extractExecutable(archive []byte, name string, binary string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("读取发行包: %w", err)
		}
		for _, file := range reader.File {
			if path.Base(file.Name) != binary {
				continue
			}
			opened, err := file.Open()
			if err != nil {
				return nil, fmt.Errorf("读取发行包: %w", err)
			}
			defer opened.Close()
			return io.ReadAll(opened)
		}
		return nil, fmt.Errorf("发行包中没有 %s", binary)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("读取发行包: %w", err)
	}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("发行包中没有 %s", binary)
		}
		if err != nil {
			return nil, fmt.Errorf("读取发行包: %w", err)
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == binary {
			return io.ReadAll(reader)
		}
	}
}
