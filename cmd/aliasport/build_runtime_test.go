package main

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestWindowsBuildStopsThenStartsViaShutdownRoute(t *testing.T) {
 root:=filepath.Join("..","..")
 bat,err:=os.ReadFile(filepath.Join(root,"build.bat"))
 if err!=nil{t.Fatal(err)}
 script,err:=os.ReadFile(filepath.Join(root,"scripts","build-runtime.ps1"))
 if err!=nil{t.Fatal(err)}
 workflow:=strings.ToLower(string(bat))
 stop:=strings.Index(workflow,"-action stop")
 tests:=strings.Index(workflow,"go test ./...")
 build:=strings.Index(workflow,"go build -trimpath")
 start:=strings.Index(workflow,"-action start")
 if stop<0||tests<0||build<0||start<0||!(stop<tests&&tests<build&&build<start){
  t.Fatalf("invalid build lifecycle order: stop=%d tests=%d build=%d start=%d",stop,tests,build,start)
 }
 if !strings.Contains(workflow,"if errorlevel 1 exit /b 1"){
  t.Fatal("build lacks fail-fast checks")
 }
 if !strings.Contains(workflow,"aliasport_no_start"){
  t.Fatal("missing automatic-start opt-out")
 }
 helper:=string(script)
 for _,text:=range []string{
  "/health", "X-AliasPort-Admin", "aliasport-admin",
  "/api/overview", "/api/shutdown", "confirm",
  "Test-LocalPort", "Start-Process", "[DateTime]::UtcNow.AddSeconds(20)",
 }{
  if !strings.Contains(helper,text){t.Errorf("runtime helper missing %q",text)}
 }
 if strings.Contains(strings.ToLower(helper),"taskkill") || strings.Contains(workflow,"taskkill ") {
  t.Fatal("build must not forcibly kill processes")
 }
}
