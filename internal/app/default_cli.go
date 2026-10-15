//go:build !desktop

package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// enterDataDir 让命令行版以工作目录为数据目录
func enterDataDir() error {
	return nil
}

// runDefault 以命令行方式运行服务，收到中断信号后停机
func runDefault(args []string) error {
	cfg, openUI, err := LoadConfig(args)
	if err != nil {
		return err
	}
	instance, err := Start(cfg, nil, false)
	if err != nil {
		return err
	}
	instance.openUI = openUI
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return instance.Serve(ctx)
}
