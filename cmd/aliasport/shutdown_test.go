package main

import (
 "bytes"
 "encoding/json"
 "io"
 "net"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync"
 "testing"
 "time"
)

func TestShutdownRequiresLocalConfirmation(t *testing.T) {
 app:=&App{stopCh:make(chan struct{}),stopOnce:sync.Once{}}
 server:=httptest.NewServer(adminSecurity(http.HandlerFunc(app.adminAPI)))
 defer server.Close()
 client:=server.Client()
 origin:=server.URL

 makeRequest:=func(method,body,headerOrigin,action string)(int,error){
  req,err:=http.NewRequest(method,origin+"/api/shutdown",strings.NewReader(body))
  if err!=nil{return 0,err}
  req.Header.Set("Content-Type","application/json")
  if headerOrigin!=""{req.Header.Set("Origin",headerOrigin)}
  if action!=""{req.Header.Set("X-AliasPort-Action",action)}
  resp,err:=client.Do(req)
  if err!=nil{return 0,err}
  defer resp.Body.Close()
  _,_=io.Copy(io.Discard,resp.Body)
  return resp.StatusCode,nil
 }
 cases:=[]struct{
  name,method,body,origin,action string
  expected int
 }{
  {"GET disallowed","GET","","","","",405},
  {"missing origin","POST",`{"confirm":"shutdown"}`,"","shutdown",403},
  {"cross origin","POST",`{"confirm":"shutdown"}`,"http://evil.example","shutdown",403},
  {"missing action header","POST",`{"confirm":"shutdown"}`,origin,"",403},
  {"wrong confirmation","POST",`{"confirm":"cancel"}`,origin,"shutdown",400},
 }
 for _,tt:=range cases{
  t.Run(tt.name,func(t *testing.T){
   code,err:=makeRequest(tt.method,tt.body,tt.origin,tt.action)
   if err!=nil{t.Fatal(err)}
   if code!=tt.expected{t.Errorf("code=%d expected=%d",code,tt.expected)}
   select{case <-app.stopCh:t.Fatal("invalid request triggered shutdown");default:}
  })
 }
 code,err:=makeRequest("POST",`{"confirm":"shutdown"}`,origin,"shutdown")
 if err!=nil{t.Fatal(err)}
 if code!=200{t.Fatalf("valid shutdown returned %d",code)}
 select{
 case <-app.stopCh:
 case <-time.After(2*time.Second):t.Fatal("shutdown signal not emitted")
 }
}

func freeLoopbackAddress(t *testing.T)string {
 t.Helper()
 l,err:=net.Listen("tcp","127.0.0.1:0")
 if err!=nil{t.Fatal(err)}
 address:=l.Addr().String()
 if err=l.Close();err!=nil{t.Fatal(err)}
 return address
}

func TestStopClosesBothListeners(t *testing.T){
 root:=t.TempDir()
 t.Setenv("XDG_CONFIG_HOME",root)
 t.Setenv("LOCALAPPDATA",root)
 adminAddress:=freeLoopbackAddress(t)
 gatewayAddress:=freeLoopbackAddress(t)
 for gatewayAddress==adminAddress{gatewayAddress=freeLoopbackAddress(t)}

 result:=make(chan error,1)
 go func(){result<-run(adminAddress,gatewayAddress,false)}()

 client:=&http.Client{Timeout:500*time.Millisecond}
 ready:=false
 for i:=0;i<200;i++{
  select{
  case err:=<-result:t.Fatalf("AliasPort exited before startup: %v",err)
  default:
  }
  response,err:=client.Get("http://"+adminAddress+"/health")
  if err==nil {
   _,_=io.Copy(io.Discard,response.Body)
   _=response.Body.Close()
   if response.StatusCode==200{ready=true;break}
  }
  time.Sleep(10*time.Millisecond)
 }
 if !ready{t.Fatal("admin listener did not start")}

 gatewayResp,err:=client.Get("http://"+gatewayAddress+"/health")
 if err!=nil{t.Fatalf("gateway did not start: %v",err)}
 _=gatewayResp.Body.Close()
 if gatewayResp.StatusCode!=200{t.Fatalf("gateway health code %d",gatewayResp.StatusCode)}

 raw,_:=json.Marshal(map[string]string{"confirm":"shutdown"})
 req,err:=http.NewRequest("POST","http://"+adminAddress+"/api/shutdown",bytes.NewReader(raw))
 if err!=nil{t.Fatal(err)}
 req.Header.Set("Content-Type","application/json")
 req.Header.Set("Origin","http://"+adminAddress)
 req.Header.Set("X-AliasPort-Action","shutdown")
 response,err:=client.Do(req)
 if err!=nil{t.Fatalf("shutdown request failed: %v",err)}
 _,_=io.Copy(io.Discard,response.Body)
 _=response.Body.Close()
 if response.StatusCode!=200{t.Fatalf("shutdown returned %d",response.StatusCode)}

 select{
 case err=<-result:
  if err!=nil{t.Fatalf("run returned an error: %v",err)}
 case <-time.After(5*time.Second):
  t.Fatal("AliasPort did not exit after shutdown")
 }
 for _,addr:=range []string{adminAddress,gatewayAddress}{
  connection,dialErr:=net.DialTimeout("tcp",addr,500*time.Millisecond)
  if dialErr==nil{
   _=connection.Close()
   t.Fatalf("port %s still accepts connections after shutdown",addr)
  }
 }
}

func TestShutdownCannotBeCrossOrigin(t *testing.T) {
 app:=&App{stopCh:make(chan struct{})}
 handler:=adminSecurity(http.HandlerFunc(app.adminAPI))
 req:=httptest.NewRequest("POST","http://127.0.0.1:8787/api/shutdown",bytes.NewReader([]byte(`{"confirm":"shutdown"}`)))
 req.Header.Set("Origin","http://example.com")
 req.Header.Set("Content-Type","application/json")
 req.Header.Set("X-AliasPort-Action","shutdown")
 rec:=httptest.NewRecorder()
 handler.ServeHTTP(rec,req)
 if rec.Code!=403 {t.Errorf("cross-origin status was %d",rec.Code)}
 select{case <-app.stopCh:t.Fatal("cross-origin shutdown was accepted");default:}
}
