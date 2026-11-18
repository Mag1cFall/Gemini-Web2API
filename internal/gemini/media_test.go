package gemini

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

// TestRemoteMediaConnectsOnlyToPublicAddresses 验证下载客户端给出的媒体地址时在建立连接时检查解析结果，主机解析到本机地址时不连接，直连与经代理都一样
func TestRemoteMediaConnectsOnlyToPublicAddresses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var connected atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connected.Add(1)
			conn.Close()
		}
	}()
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	for _, proxyURL := range []string{"", "http://" + listener.Addr().String()} {
		client, err := newMediaClient(5, proxyURL, 0)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("http://localhost:%d/image.png", port), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Do(request); err == nil || !strings.Contains(err.Error(), "非公网地址") {
			t.Fatalf("代理 %q：err = %v", proxyURL, err)
		}
	}
	if connected.Load() != 0 {
		t.Fatalf("建立了 %d 次连接", connected.Load())
	}
}
