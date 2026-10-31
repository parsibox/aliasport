package main

import (
 "context"
 "embed"
 "errors"
 "flag"
 "fmt"
 "io/fs"
 "log"
 "net"
 "net/http"
 "os"
 "os/signal"
 "path/filepath"
 "runtime"
 "strings"
 "sync"
 "syscall"
 "time"
)

//go:embed web/*
var webAssets embed.FS

type App struct {
 store *Store
 started time.Time
 adminPort string
 proxy *proxyManager
 client *http.Client
 stopCh chan struct{}
 stopOnce sync.Once
}

func main() {
 admin := flag.String("admin", "127.0.0.1:8787", "local management listener")
 noBrowser := flag.Bool("no-browser", false, "do not open web browser")
 flag.Parse()

 if err := run(*admin, !*noBrowser); err != nil {
  // Windows GUI binaries intentionally have no console; log errors to a local file.
  dir, e := dataDir()
  if e == nil {
   _ = os.MkdirAll(dir, 0700)
   f, e := os.OpenFile(filepath.Join(dir, "startup-error.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
   if e == nil { _, _ = fmt.Fprintln(f, time.Now().Format(time.RFC3339), err); _ = f.Close() }
  }
  log.Print(err)
  os.Exit(1)
 }
}

func dataDir() (string,error) {
 if runtime.GOOS == "windows" && os.Getenv("LOCALAPPDATA") != "" {
  return filepath.Join(os.Getenv("LOCALAPPDATA"), "AliasPort"),nil
 }
 config,err := os.UserConfigDir()
 if err != nil {return "",err}
 return filepath.Join(config,"AliasPort"),nil
}

func run(adminAddr string,launchBrowser bool) error {
 if !isLoopbackAddr(adminAddr) {return errors.New("management listener must bind to a loopback interface")}
 dir,err:=dataDir(); if err != nil{return err}
 if err=os.MkdirAll(dir,0700);err!=nil{return err}
 store,err:=OpenStore(filepath.Join(dir,"aliasport.db"));if err!=nil{return err}
 defer store.Close()
 transport:=http.DefaultTransport.(*http.Transport).Clone()
 transport.MaxIdleConnsPerHost=30
 transport.ResponseHeaderTimeout=120*time.Second
 // Never follow upstream redirects with an API key. A malicious redirect could
 // otherwise forward a provider secret to an unrelated host.
 app:=&App{store:store,started:time.Now(),adminPort:adminAddr,client:&http.Client{
  Transport:transport,
  CheckRedirect:func(_ *http.Request,_ []*http.Request)error{return http.ErrUseLastResponse},
 },stopCh:make(chan struct{})}
 app.proxy=newProxyManager(app)
 adminMux:=http.NewServeMux()
 staticFS,err:=fs.Sub(webAssets,"web");if err!=nil{return err}
 adminMux.Handle("/",http.FileServer(http.FS(staticFS)))
 adminMux.HandleFunc("/api/",app.adminAPI)
 adminMux.HandleFunc("/health",func(w http.ResponseWriter,r *http.Request){writeJSON(w,200,map[string]any{"ok":true,"service":"aliasport-admin"})})
 adminListener,err:=net.Listen("tcp",adminAddr)
 if err!=nil {
  // If the existing instance owns this local port, reuse its web UI.
  if launchBrowser {
   probe:=&http.Client{Timeout:1500*time.Millisecond}
   resp,probeErr:=probe.Get("http://"+adminAddr+"/health")
   if probeErr==nil {
    _=resp.Body.Close()
    if resp.StatusCode==200 && resp.Header.Get("X-AliasPort-Admin")=="true" {
     openBrowser("http://"+adminAddr)
     return nil
    }
   }
  }
  return fmt.Errorf("admin listen: %w",err)
 }
 defer adminListener.Close()
 adminServer:=&http.Server{Handler:adminSecurity(adminMux),ReadHeaderTimeout:10*time.Second,IdleTimeout:2*time.Minute}
 if err=app.proxy.startFromDatabase();err!=nil{return fmt.Errorf("gateway startup: %w",err)}
 errorsCh:=make(chan error,1)
 go func(){e:=adminServer.Serve(adminListener);if e!=nil&&!errors.Is(e,http.ErrServerClosed){errorsCh<-e}}()
 url:="http://"+adminAddr
 log.Printf("AliasPort management %s | gateway port is configured per profile",url)
 if launchBrowser {go func(){time.Sleep(350*time.Millisecond);openBrowser(url)}()}
 sig:=make(chan os.Signal,1)
 signal.Notify(sig,os.Interrupt,syscall.SIGTERM)
 defer signal.Stop(sig)
 select {
 case err=<-errorsCh:
 case <-sig:
  err=nil
 case <-app.stopCh:
  // The user explicitly requested full exit. Close both listeners and all
  // active connections (including long-running SSE) immediately, then return
  // from run so main exits and the SQLite store closes.
  _=adminServer.Close()
  app.proxy.close()
  return nil
 }
 ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second)
 defer cancel()
 _=adminServer.Shutdown(ctx)
 app.proxy.close()
 return err
}

func isLoopbackAddr(address string) bool {
 host,_,err:=net.SplitHostPort(address)
 if err!=nil{return false}
 host=strings.Trim(host,"[]")
 ip:=net.ParseIP(host)
 return ip!=nil&&ip.IsLoopback()
}

// Prevent cross-origin browser writes to the admin API. All API endpoints are local-only.
func adminSecurity(next http.Handler) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("X-Content-Type-Options","nosniff")
  w.Header().Set("X-AliasPort-Admin","true")
  w.Header().Set("X-Frame-Options","DENY")
  w.Header().Set("Referrer-Policy","no-referrer")
  w.Header().Set("Cache-Control","no-store")
  if strings.HasPrefix(r.URL.Path,"/api/") {
   if r.Header.Get("Sec-Fetch-Site")=="cross-site" {http.Error(w,"cross-site request blocked",http.StatusForbidden);return}
   if origin:=r.Header.Get("Origin");origin!=""&&origin!="http://"+r.Host {http.Error(w,"cross-origin request blocked",http.StatusForbidden);return}
   if r.Method!="GET" && r.Method!="HEAD" {
    if r.Header.Get("Content-Type")!="application/json" {http.Error(w,"application/json required",415);return}
   }
  }
  next.ServeHTTP(w,r)
 })
}
