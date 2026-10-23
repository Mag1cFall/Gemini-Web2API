package adapter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/Mag1cFall/Gemini-Web2API/internal/balancer"
	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// errNoMediaClient 表示没有就绪账号可以下载远程媒体
var errNoMediaClient = errors.New("没有就绪账号可以下载远程媒体")

// fetchRemoteMedia 经 client 的浏览器身份与出口下载客户端给出的 http(s) 媒体地址，拒绝解析到非公网地址的主机，大小不超过 fileUploadLimit
func fetchRemoteMedia(ctx context.Context, client *gemini.Client, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("媒体地址必须是 http 或 https: %s", rawURL)
	}
	if client == nil {
		return nil, errNoMediaClient
	}
	return client.FetchRemoteMedia(ctx, rawURL, checkRemoteMediaHost, fileUploadLimit)
}

// countClient 返回第一个就绪账号的客户端，计数入口用它下载远程媒体
func countClient(pool *balancer.AccountPool) *gemini.Client {
	for _, account := range pool.CatalogAccounts() {
		if account.Ready {
			return account.Client
		}
	}
	return nil
}

// checkRemoteMediaHost 拒绝解析到本机、私网、链路本地、组播或未指定地址的主机
func checkRemoteMediaHost(ctx context.Context, host string) error {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		address = address.Unmap()
		if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
			return fmt.Errorf("主机 %s 解析到非公网地址 %s", host, address)
		}
	}
	return nil
}
