//go:build !windows

package main

import (
 "os/exec"
 "runtime"
)

func openBrowser(url string) {
 if runtime.GOOS=="darwin" {_=exec.Command("open",url).Start()} else {_=exec.Command("xdg-open",url).Start()}
}
