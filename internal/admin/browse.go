package admin

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// directories 列出 path 下的子目录供设置页选择认证目录，path 为空时取数据目录；相对路径相对数据目录
func (h *handler) directories(c *gin.Context) {
	path := strings.TrimSpace(c.Query("path"))
	if path == "" {
		path = "."
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	parent := filepath.Dir(absolute)
	if parent == absolute {
		parent = ""
	}
	c.JSON(http.StatusOK, gin.H{"path": absolute, "parent": parent, "directories": names})
}
