package logging

import (
	"gopkg.in/natefinch/lumberjack.v2"
)

// FileMB 是单个日志文件的轮转大小
const FileMB = 10

// SetFile 开启或关闭日志文件：开启时控制台的每条事件同时以同一格式、不带颜色写入 path，单文件 FileMB 轮转，目录总量超过 maxTotalMB 时删除最旧的文件；路径与总量上限不变时沿用已有的轮转日志，关闭只关闭文件
func SetFile(path string, enabled bool, maxTotalMB int) error {
	std.mu.Lock()
	defer std.mu.Unlock()
	backups := maxTotalMB/FileMB - 1
	same := std.file != nil && std.file.Filename == path && std.file.MaxBackups == backups
	if std.fileWrite != nil && (!enabled || !same) {
		std.fileWrite = nil
		if err := std.file.Close(); err != nil {
			return err
		}
	}
	if !same {
		std.file = nil
	}
	if !enabled || std.fileWrite != nil {
		return nil
	}
	if std.file == nil {
		std.file = &lumberjack.Logger{Filename: path, MaxSize: FileMB, MaxBackups: backups, LocalTime: true}
	}
	if std.format == "json" {
		std.fileWrite = newJSONConsole(std.file)
	} else {
		std.fileWrite = newTextConsole(std.file).write
	}
	return nil
}
