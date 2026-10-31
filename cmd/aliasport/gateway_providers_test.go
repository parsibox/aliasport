package main

import (
 "bytes"
 "encoding/json"
 "errors"
 "net"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strconv"
 "strings"
 "testing"
 "time"
)

func TestProviderAssignedToMultipleGatewaysAndIsolated(t *testing.T){
 s:=testStore(t)
 p,e:=s.SaveProvider(Provider{Name:"Shared",BaseURL:"https://example.org",APIKey:"key",Priority:1})
 if e!=nil{t.Fatal(e)}
 other,e:=s.SaveProvider(Provider{Name:"Not allowed",BaseURL:"https://example.net",APIKey:"key-other",Priority:0})
 if e!=nil{t.Fatal(e)}
 g1,e:=s.SaveGateway(Gateway{Name:"Claude",Port:freeProxyPort(t),InputType:ProtocolAnthropic})
 if e!=nil{t.Fatal(e)}
 port2:=freeProxyPort(t)
 for port2==g1.Port{port2=freeProxyPort(t)}
 g2,e:=s.SaveGateway(Gateway{Name:"OpenAI",Port:port2,InputType:ProtocolOpenAIChat})
 if e!=nil{t.Fatal(e)}
 if e=s.SetProviderGateways(p.ID,[]string{g1.ID,g2.ID});e!=nil{t.Fatal(e)}
 if e=s.SetProviderGateways(other.ID,[]string{g1.ID});e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g1.ID);e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g2.ID);e!=nil{t.Fatal(e)}
 for _,row:=range []struct{gateway,provider,target string}{
  {g1.ID,p.ID,"claude-target"},
  {g2.ID,p.ID,"openai-target"},
  {g2.ID,other.ID,"disallowed"},
 }{
  if _,e=s.SaveAlias(Alias{GatewayID:row.gateway,ProviderID:row.provider,Incoming:"*",Target:row.target,Enabled:true});e!=nil{t.Fatal(e)}
 }
 // Creating an alias implies assignment for backwards compatibility. Revoke
 // Other from Gateway 2, without deleting the alias or affecting Gateway 1.
 if e=s.SetProviderGateways(other.ID,[]string{g1.ID});e!=nil{t.Fatal(e)}
 routes,e:=s.RoutesForGateway(g1.ID,"any-model")
 if e!=nil||len(routes)!=2{t.Fatalf("Claude routes: %+v, %v",routes,e)}
 routes,e=s.RoutesForGateway(g2.ID,"any-model")
 if e!=nil||len(routes)!=1||routes[0].ProviderID!=p.ID||routes[0].Target!="openai-target"{
  t.Fatalf("OpenAI gateway leaks unassigned Provider: %+v %v",routes,e)
 }
 if e=s.SetProviderGateways(p.ID,[]string{g1.ID});e!=nil{t.Fatal(e)}
 routes,e=s.RoutesForGateway(g2.ID,"any-model")
 if !errors.Is(e,ErrNotFound)||len(routes)!=0{t.Fatalf("unassigned shared Provider still routes: %+v %v",routes,e)}
 routes,e=s.RoutesForGateway(g1.ID,"any-model")
 if e!=nil||len(routes)!=2{t.Fatalf("Gateway 1 was affected by Gateway 2 revocation: %+v %v",routes,e)}
 providers,e:=s.Providers()
 if e!=nil{t.Fatal(e)}
 for _,provider:=range providers{
  if provider.ID==p.ID && (len(provider.GatewayIDs)!=1||provider.GatewayIDs[0]!=g1.ID){
   t.Fatalf("Provider-to-Gateway assignment not reflected in API: %+v",provider)
  }
 }
}

func TestProviderGatewayAssignmentsValidateAndRollback(t *testing.T){
 s:=testStore(t)
 p,e:=s.SaveProvider(Provider{Name:"Provider",BaseURL:"https://example.org",APIKey:"key"})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"Gateway"})
 if e!=nil{t.Fatal(e)}
 if e=s.SetProviderGateways(p.ID,[]string{g.ID});e!=nil{t.Fatal(e)}
 for _,bad:=range [][]string{{g.ID,g.ID},{g.ID,"000000000000000000000000"},{g.ID,"not-valid"}}{
  if e=s.SetProviderGateways(p.ID,bad);e==nil{t.Fatalf("invalid assignment succeeded: %v",bad)}
  ids,e:=s.ProviderGatewayIDs(p.ID)
  if e!=nil||len(ids)!=1||ids[0]!=g.ID{t.Fatalf("assignment changed after failed update: %v %v",ids,e)}
 }
 if e=s.SetProviderGateways(p.ID,[]string{});e!=nil{t.Fatal(e)}
 ids,e:=s.ProviderGatewayIDs(p.ID)
 if e!=nil||len(ids)!=0{t.Fatalf("explicit empty assignments should revoke all: %+v %v",ids,e)}
}

func TestGatewayProviderMigrationPreservesExistingAliases(t *testing.T){
 root:=t.TempDir()
 t.Setenv("LOCALAPPDATA",root);t.Setenv("XDG_CONFIG_HOME",root)
 path:=filepath.Join(root,"migration.db")
 store,e:=OpenStore(path);if e!=nil{t.Fatal(e)}
 p,e:=store.SaveProvider(Provider{Name:"Legacy",BaseURL:"https://example.org",APIKey:"old-key"})
 if e!=nil{t.Fatal(e)}
 g,e:=store.SaveGateway(Gateway{Name:"Legacy Gateway"})
 if e!=nil{t.Fatal(e)}
 if _,e=store.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p.ID,Incoming:"*",Target:"legacy-model",Enabled:true});e!=nil{t.Fatal(e)}
 // Simulate old installation with no explicit assignment table migrated yet.
 if _,e=store.db.Exec("DELETE FROM gateway_providers");e!=nil{t.Fatal(e)}
 if _,e=store.db.Exec("PRAGMA user_version=1");e!=nil{t.Fatal(e)}
 if e=store.Close();e!=nil{t.Fatal(e)}
 restored,e:=OpenStore(path);if e!=nil{t.Fatal(e)}
 defer restored.Close()
 ids,e:=restored.ProviderGatewayIDs(p.ID)
 if e!=nil||len(ids)!=1||ids[0]!=g.ID{t.Fatalf("legacy alias association lost: %v %v",ids,e)}
 if e=restored.SetProviderGateways(p.ID,[]string{});e!=nil{t.Fatal(e)}
 // Reopening must not restore intentionally revoked assignments.
 if e=restored.Close();e!=nil{t.Fatal(e)}
 again,e:=OpenStore(path);if e!=nil{t.Fatal(e)}
 defer again.Close()
 ids,e=again.ProviderGatewayIDs(p.ID)
 if e!=nil||len(ids)!=0{t.Fatalf("revoked association reappeared: %v %v",ids,e)}
}

func TestConcurrentGatewayPortsAuthenticateAndRouteSeparately(t *testing.T){
 upstream:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var v map[string]any
  if e:=json.NewDecoder(r.Body).Decode(&v);e!=nil{t.Error(e)}
  _,_=w.Write([]byte(`{"id":"msg-test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
 }))
 defer upstream.Close()
 s:=testStore(t)
 p,e:=s.SaveProvider(Provider{Name:"Shared",BaseURL:upstream.URL,APIKey:"upstream-secret"})
 if e!=nil{t.Fatal(e)}
 firstPort:=freeProxyPort(t)
 secondPort:=freeProxyPort(t)
 for firstPort==secondPort{secondPort=freeProxyPort(t)}
 g1,e:=s.SaveGateway(Gateway{Name:"Claude",Port:firstPort,InputType:ProtocolAnthropic})
 if e!=nil{t.Fatal(e)}
 g2,e:=s.SaveGateway(Gateway{Name:"OpenAI",Port:secondPort,InputType:ProtocolOpenAIChat})
 if e!=nil{t.Fatal(e)}
 for _,g:=range []Gateway{g1,g2}{
  if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p.ID,Incoming:"*",Target:"upstream-model",Enabled:true});e!=nil{t.Fatal(e)}
 }
 a:=&App{store:s,client:upstream.Client()}
 a.proxy=newProxyManager(a)
 for _,g:=range []Gateway{g1,g2}{
  if e=a.proxy.switchTo(g.ID);e!=nil{t.Fatal(e)}
 }
 defer a.proxy.close()
 client:=&http.Client{Timeout:2*time.Second}
 request:=func(port int,key,endpoint,body string)int{
  t.Helper()
  req,e:=http.NewRequest("POST","http://127.0.0.1:"+strconv.Itoa(port)+endpoint,bytes.NewBufferString(body))
  if e!=nil{t.Fatal(e)}
  req.Header.Set("Authorization","Bearer "+key)
  req.Header.Set("Content-Type","application/json")
  resp,e:=client.Do(req)
  if e!=nil{t.Fatal(e)}
  defer resp.Body.Close()
  return resp.StatusCode
 }
 anthropicBody:=`{"model":"any","max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`
 if got:=request(firstPort,g1.ClientKey,"/v1/messages",anthropicBody);got!=200{t.Fatalf("Claude gateway status=%d",got)}
 if got:=request(firstPort,g2.ClientKey,"/v1/messages",anthropicBody);got!=401{t.Fatalf("cross-Gateway key accepted: %d",got)}
 if got:=request(secondPort,g2.ClientKey,"/v1/messages",anthropicBody);got!=404{t.Fatalf("incorrect protocol accepted: %d",got)}
 chatBody:=`{"model":"any","messages":[{"role":"user","content":"Hello"}]}`
 if got:=request(secondPort,g2.ClientKey,"/v1/chat/completions",chatBody);got!=200{t.Fatalf("OpenAI gateway status=%d",got)}
 if got:=request(secondPort,g1.ClientKey,"/v1/chat/completions",chatBody);got!=401{t.Fatalf("cross-Gateway key accepted on second port: %d",got)}
 // The first listener must remain active after serving the second.
 connection,e:=net.DialTimeout("tcp","127.0.0.1:"+strconv.Itoa(firstPort),500*time.Millisecond)
 if e!=nil{t.Fatalf("first Gateway unexpectedly stopped: %v",e)}
 _=connection.Close()
}

func TestGatewayProviderMenuOrderAndSelector(t *testing.T){
 b,e:=webAssets.ReadFile("web/index.html")
 if e!=nil{t.Fatal(e)}
 page:=string(b)
 menu:=strings.Index(page,"<nav class=\"menu\"")
 if menu<0{t.Fatal("menu missing")}
 gateways:=strings.Index(page[menu:],`data-page="gateways"`)
 providers:=strings.Index(page[menu:],`data-page="providers"`)
 if gateways<0||providers<0||gateways>=providers{t.Fatal("Gateway menu must precede Providers")}
 for _,token:=range []string{"provider-gateway-check","gateway_ids:Array.from(","Gatewayهای مجاز"}{
  if !strings.Contains(page,token){t.Errorf("missing Provider assignment UI: %s",token)}
 }
}
