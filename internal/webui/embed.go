// Package webui 提供内嵌的管理端静态资源
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/vearutop/statigz"
)

//go:embed all:dist
var embedded embed.FS

// Handler 返回管理端静态资源处理器：gzip 构建产物直接返回给接受 gzip 的客户端，assets 下带哈希的文件长期缓存，未知页面路径回落到入口页，入口页的 <base> 指向管理端根目录
func Handler() http.Handler {
	index, err := fs.ReadFile(embedded, "dist/index.html")
	if err != nil {
		panic(err)
	}
	static := statigz.FileServer(embedded, statigz.FSPrefix("dist"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		asset := strings.HasPrefix(name, "assets/")
		if name != "" && name != "index.html" && static.Found(r) {
			if asset {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			static.ServeHTTP(w, r)
			return
		}
		if asset {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		page := bytes.Replace(index, []byte(`<base href="/" />`), []byte(`<base href="`+relativeRoot(r.URL.Path)+`" />`), 1)
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
	})
}

// relativeRoot 返回从页面路径回到管理端根目录的相对地址，反向代理把管理端挂在子路径下时资源、接口与路由仍以该子路径为根
func relativeRoot(page string) string {
	if depth := strings.Count(page, "/") - 1; depth > 0 {
		return strings.Repeat("../", depth)
	}
	return "./"
}
