//go:build desktop && !darwin

package desktop

// setDockIcon 在 macOS 以外的系统上没有 Dock 图标
func setDockIcon(bool) {}
