//go:build desktop

package desktop

import "github.com/godbus/dbus/v5"

// trayHosted 判断会话总线上有没有托盘宿主 org.kde.StatusNotifierWatcher，没有托盘扩展的 GNOME 等桌面环境没有
func trayHosted() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var owned bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&owned)
	return err == nil && owned
}
