//go:build !desktop

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"syscall"

	"github.com/minio/selfupdate"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// install 下载当前系统与架构的命令行版发行包，校验 SHA-256 后替换可执行文件
func (u *selfUpdater) install(ctx context.Context, release *githubRelease) error {
	name, binary := releasePackage(release.TagName)
	archive, err := u.download(ctx, release, name)
	if err != nil {
		return err
	}
	executable, err := extractExecutable(archive, name, binary)
	if err != nil {
		return err
	}
	logging.Info(logging.SourceService, "在线更新", "阶段", "2/2 替换可执行文件", "版本", release.TagName)
	if err := selfupdate.Apply(bytes.NewReader(executable), selfupdate.Options{TargetPath: u.executable}); err != nil {
		if rollback := selfupdate.RollbackError(err); rollback != nil {
			return fmt.Errorf("替换可执行文件: %w；恢复原文件失败: %v", err, rollback)
		}
		return fmt.Errorf("替换可执行文件: %w", err)
	}
	return nil
}

// releasePackage 返回当前系统与架构的命令行版发行包文件名与其中的可执行文件名
func releasePackage(tag string) (string, string) {
	base := fmt.Sprintf("gemini-web2api-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		return base + ".zip", "gemini-web2api.exe"
	}
	return base + ".tar.gz", "gemini-web2api"
}

// relaunch 以原参数与启动前的环境变量启动替换后的可执行文件且不再打开管理页：Windows 上新进程沿用当前控制台，其他系统原地替换进程
func relaunch(executable string, args []string) error {
	unloadDotenv()
	args = append(slices.Clone(args), "--open-ui=false")
	if runtime.GOOS == "windows" {
		command := exec.Command(executable, args...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Start()
	}
	return syscall.Exec(executable, append([]string{executable}, args...), os.Environ())
}
