package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	_ "time/tzdata"

	"github.com/Mag1cFall/Gemini-Web2API/internal/app"
)

func init() {
	runtime.LockOSThread()
}

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
