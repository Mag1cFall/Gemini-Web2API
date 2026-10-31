//go:build !windows

package chromeauth

func browserExeName(browser string) string {
	return ""
}

func copyLockedFile(src string, dst string, exeNames ...string) error {
	return copyFile(src, dst)
}
