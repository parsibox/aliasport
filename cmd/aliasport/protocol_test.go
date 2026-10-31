package main

import (
 "net"
 "net/http"
 "strconv"
 "testing"
 "time"
)

func freeProxyPort(t *testing.T)int{
 t.Helper()
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 port:=listener.Addr().(*net.TCPAddr).Port
 _=listener.Close()
 if port==8787{return freeProxyPort(t)}
 return port
}
func TestGatewayProfilePortSwitch(t *testing.T){
 store:=testStore(t)
 firstPort:=freeProxyPort(t)
 secondPort:=freeProxyPort(t)
 for firstPort==secondPort{secondPort=freeProxyPort(t)}
 first,err:=store.SaveGateway(Gateway{Name:"Anthropic client",InputType:ProtocolAnthropic,Port:firstPort})
 if err!=nil{t.Fatal(err)}
 second,err:=store.SaveGateway(Gateway{Name:"OpenAI client",InputType:ProtocolOpenAIChat,Port:secondPort})
 if err!=nil{t.Fatal(err)}
 app:=&App{store:store,client:&http.Client{}}
 app.proxy=newProxyManager(app)
 if err=app.proxy.switchTo(first.ID);err!=nil{t.Fatal(err)}
 defer app.proxy.close()
 client:=&http.Client{Timeout:1*time.Second}
 get:=func(port int)(int,error){
  r,err:=client.Get("http://127.0.0.1:"+strconv.Itoa(port)+"/health")
  if err!=nil{return 0,err}
  defer r.Body.Close()
  return r.StatusCode,nil
 }
 if code,err:=get(firstPort);err!=nil||code!=200{t.Fatalf("first port not listening: %d, %v",code,err)}
 if err=app.proxy.switchTo(second.ID);err!=nil{t.Fatal(err)}
 if code,err:=get(secondPort);err!=nil||code!=200{t.Fatalf("second port not listening: %d, %v",code,err)}
 for i:=0;i<30;i++{
  c,err:=get(firstPort)
  if err!=nil{break}
  if i==29{t.Fatalf("old proxy port remains open: status %d",c)}
  time.Sleep(15*time.Millisecond)
 }
 if err=app.proxy.deactivate(second.ID);err!=nil{t.Fatal(err)}
 for i:=0;i<30;i++{
  _,err:=get(secondPort)
  if err!=nil{break}
  if i==29{t.Fatal("deactivated gateway still listens")}
  time.Sleep(15*time.Millisecond)
 }
 if _,err=store.ActiveGateway();err!=ErrNotFound{t.Errorf("expected no active gateway after deactivation: %v",err)}
}

func TestPortAlreadyTakenDoesNotChangeActiveProfile(t *testing.T){
 s:=testStore(t)
 firstPort:=freeProxyPort(t)
 l,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 defer l.Close()
 blockedPort:=l.Addr().(*net.TCPAddr).Port
 g1,err:=s.SaveGateway(Gateway{Name:"Working",Port:firstPort,InputType:ProtocolAnthropic})
 if err!=nil{t.Fatal(err)}
 g2,err:=s.SaveGateway(Gateway{Name:"Blocked",Port:blockedPort,InputType:ProtocolOpenAIChat})
 if err!=nil{t.Fatal(err)}
 app:=&App{store:s,client:&http.Client{}}
 app.proxy=newProxyManager(app)
 if err=app.proxy.switchTo(g1.ID);err!=nil{t.Fatal(err)}
 defer app.proxy.close()
 if err=app.proxy.switchTo(g2.ID);err==nil{t.Fatal("activated a profile on a blocked port")}
 active,err:=s.ActiveGateway()
 if err!=nil||active.ID!=g1.ID{t.Fatalf("active gateway unexpectedly changed: %+v %v",active,err)}
}

func TestProviderEndpointNormalizesVersionPath(t *testing.T){
 cases:=[]struct{base,protocol,path,want string}{
  {"https://api.apinex.bond/v1",ProtocolOpenAIChat,"/v1/chat/completions","https://api.apinex.bond/v1/chat/completions"},
  {"https://api.apinex.bond/v1/",ProtocolAnthropic,"/v1/messages","https://api.apinex.bond/v1/messages"},
  {"https://zro.moonmath.ai",ProtocolAnthropic,"/v1/messages","https://zro.moonmath.ai/v1/messages"},
  {"https://example.org/custom/v1",ProtocolOpenAIChat,"/v1/chat/completions","https://example.org/custom/v1/chat/completions"},
 }
 for _,tt:=range cases{
  got,err:=providerEndpoint(tt.base,tt.protocol,tt.path)
  if err!=nil||got!=tt.want{t.Errorf("providerEndpoint(%s)=%s error=%v, want %s",tt.base,got,err,tt.want)}
 }
}
func TestUpgradeOldStorePreservesDefaultProtocolAndPort(t *testing.T){
 s:=testStore(t)
 g,err:=s.SaveGateway(Gateway{Name:"Legacy"})
 if err!=nil{t.Fatal(err)}
 if g.Port!=18765||g.InputType!=ProtocolAnthropic{t.Fatalf("unexpected legacy defaults: %+v",g)}
 p,err:=s.SaveProvider(Provider{Name:"Legacy provider",BaseURL:"https://example.org",APIKey:"legacy-key"})
 if err!=nil{t.Fatal(err)}
 if p.APIType!=ProtocolAnthropic{t.Fatalf("unexpected provider default protocol: %s",p.APIType)}
}
