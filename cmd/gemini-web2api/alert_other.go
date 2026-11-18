//go:build !desktop || !windows

package main

import (
	"fmt"
	"os"
)

// alert 把启动失败的原因写到标准错误，macOS 与 Linux 的桌面启动器会收集它
func alert(err error) {
	fmt.Fprintln(os.Stderr, err)
}
