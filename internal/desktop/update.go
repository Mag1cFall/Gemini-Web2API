//go:build desktop

package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// slot 是 Wails 更新器的唯一更新来源，转交每次安装传入的来源
var slot = &providerSlot{}

// configure 保证 Wails 更新器只配置一次
var configure sync.Once

// Install 以 provider 检查、下载并暂存新版本，version 为不带 v 的当前版本号；Unix 上暂存期间 TMPDIR 指向 stagingDir
func Install(ctx context.Context, version string, provider updater.Provider) error {
	app := application.Get()
	slot.set(provider)
	var err error
	configure.Do(func() {
		err = app.Updater.Init(updater.Config{CurrentVersion: version, Providers: []updater.Provider{slot}, Window: updater.WindowNone})
	})
	if err != nil {
		return err
	}
	release, err := app.Updater.Check(ctx)
	if err != nil {
		return err
	}
	if release == nil {
		return errors.New("没有可安装的新版本")
	}
	if runtime.GOOS != "windows" {
		dir, err := stagingDir()
		if err != nil {
			return err
		}
		previous, set := os.LookupEnv("TMPDIR")
		_ = os.Setenv("TMPDIR", dir)
		defer func() {
			if set {
				_ = os.Setenv("TMPDIR", previous)
			} else {
				_ = os.Unsetenv("TMPDIR")
			}
		}()
	}
	return app.Updater.DownloadAndInstall(ctx)
}

// stagingDir 返回 Unix 上暂存新版本的目录，即辅助进程替换的目标所在目录，暂存与替换位于同一文件系统：macOS 为 .app 包所在目录，Linux 为可执行文件所在目录
func stagingDir() (string, error) {
	target, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		target = filepath.Dir(filepath.Dir(filepath.Dir(target)))
	}
	return filepath.Dir(target), nil
}

// Restart 启动更新器辅助进程并退出桌面应用，辅助进程在当前进程结束后替换文件并启动新版本
func Restart() error {
	return application.Get().Updater.Restart(context.Background())
}

// providerSlot 把调用转交给最近一次设置的更新来源
type providerSlot struct {
	mu      sync.Mutex
	current updater.Provider
}

// set 设置之后检查与下载使用的更新来源
func (s *providerSlot) set(provider updater.Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = provider
}

// get 返回当前更新来源
func (s *providerSlot) get() updater.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Name 返回更新来源名称
func (s *providerSlot) Name() string { return "gemini-web2api" }

// Check 转交当前更新来源
func (s *providerSlot) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	return s.get().Check(ctx, request)
}

// Download 转交当前更新来源
func (s *providerSlot) Download(ctx context.Context, release *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	return s.get().Download(ctx, release, dst, onProgress)
}
