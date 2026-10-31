//go:build windows

package main

import "os/exec"

func openBrowser(url string) {
 _=exec.Command("rundll32.exe","url.dll,FileProtocolHandler",url).Start()
}
