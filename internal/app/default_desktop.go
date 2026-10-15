//go:build desktop

package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/Mag1cFall/Gemini-Web2API/internal/desktop"
	"gopkg.in/natefinch/lumberjack.v2"
)

// appName 是桌面版的应用名与数据目录名
const appName = "Gemini-Web2API"

// enterDataDir 让桌面版切换到数据目录，.env、auth 与 data 都在其中：Windows 便携版为可执行文件所在目录，其他系统为用户配置目录下的 Gemini-Web2API
func enterDataDir() error {
	directory, err := desktopDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建数据目录 %s: %w", directory, err)
	}
	return os.Chdir(directory)
}

// desktopDataDir 返回桌面版的数据目录
func desktopDataDir() (string, error) {
	if runtime.GOOS == "windows" {
		executable, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("定位可执行文件: %w", err)
		}
		return filepath.Dir(executable), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("定位用户配置目录: %w", err)
	}
	return filepath.Join(base, appName), nil
}

// runDefault 以桌面窗口与托盘运行服务，日志写入数据目录并按大小轮转
func runDefault(args []string) error {
	hidden := slices.Contains(args, "--"+desktop.HiddenFlag)
	args = slices.DeleteFunc(slices.Clone(args), func(arg string) bool { return arg == "--"+desktop.HiddenFlag })
	cfg, _, err := LoadConfig(args)
	if err != nil {
		return err
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	logFile := &lumberjack.Logger{Filename: filepath.Join(dataDir, "logs", "gemini-web2api.log"), MaxSize: 10, MaxBackups: 5, LocalTime: true}
	defer logFile.Close()
	return desktop.Run(desktop.Options{
		URL:     AdminURL(cfg.ListenAddress),
		Title:   appName,
		DataDir: filepath.Join(workDir, dataDir, "webview"),
		Hidden:  hidden,
		Serve: func(ctx context.Context) error {
			instance, err := Start(cfg, fileTee{logFile}, true)
			if err != nil {
				return err
			}
			return instance.Serve(ctx)
		},
	})
}

// fileTee 把日志写到标准错误与日志文件，没有控制台时标准错误写入失败，文件照常写入
type fileTee struct {
	file io.Writer
}

func (tee fileTee) Write(data []byte) (int, error) {
	_, _ = os.Stderr.Write(data)
	return tee.file.Write(data)
}
