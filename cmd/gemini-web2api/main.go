package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	_ "time/tzdata"

	"github.com/Mag1cFall/Gemini-Web2API/internal/app"
)

// main 执行单二进制命令入口
func main() {
	if err := app.Main(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
