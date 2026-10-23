//go:build !desktop

package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

// enterDataDir 让命令行版以工作目录为数据目录
func enterDataDir() error {
	return nil
}

// runDefault 以命令行方式运行服务，收到中断信号后停机，在线更新完成后以原参数启动新版本
func runDefault(args []string) error {
	cfg, openUI, err := LoadConfig(args)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件: %w", err)
	}
	instance, err := Start(cfg, nil, false)
	if err != nil {
		return err
	}
	instance.openUI = openUI
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var restarting atomic.Bool
	instance.executable = executable
	instance.restart = func() {
		restarting.Store(true)
		stop()
	}
	if err := instance.Serve(ctx); err != nil || !restarting.Load() {
		return err
	}
	return relaunch(executable, args)
}
