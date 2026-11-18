//go:build desktop

package desktop

// #cgo CFLAGS: -x objective-c
// #include <Cocoa/Cocoa.h>
// static void dockSetVisible(int visible) {
//   NSImage *icon = [NSApp applicationIconImage];
//   [NSApp setActivationPolicy:visible ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory];
//   if (visible) {
//     dispatch_async(dispatch_get_main_queue(), ^{ [NSApp setApplicationIconImage:icon]; });
//   }
//   [NSApp activateIgnoringOtherApps:YES];
// }
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// setDockIcon 在主线程切换 Dock 图标的显示并把应用带到前台，隐藏后应用只保留菜单栏图标；重新显示时设回应用图标，Dock 不会显示空白图标
func setDockIcon(visible bool) {
	application.InvokeSync(func() {
		flag := C.int(0)
		if visible {
			flag = 1
		}
		C.dockSetVisible(flag)
	})
}
