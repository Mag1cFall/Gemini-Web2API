package chromeauth

import (
	"errors"
	"io"
	"os"
)

var errBrowserRunning = errors.New("browser is running and locking cookie database")

// copyFile 把 src 复制为 dst
func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
