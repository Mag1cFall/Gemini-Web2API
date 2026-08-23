//go:build !windows

package auth

import "os"

// replaceFile 在同一文件系统原子替换状态文件
func replaceFile(source string, destination string) error {
	return os.Rename(source, destination)
}
