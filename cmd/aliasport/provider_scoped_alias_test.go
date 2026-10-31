package main

import (
 "bytes"
 "database/sql"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"
)

func TestProviderScopedWildcardAliases(t *testing.T){
 s:=testStore(t)
 p1,e:=s.SaveProvider(Provider{Name:"ZRO",BaseURL:"https://zro.example",APIKey:"zro-key"})
 if e!=nil{t.Fatal(e)}
 p2,e:=s.SaveProvider(Provider{Name:"APInex",BaseURL:"https://apinex.example",APIKey:"apx-key"})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"Claude Desktop"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 zro,e:=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p1.ID,Incoming:"*",Target:"deepseek",Enabled:true})
 if e!=nil{t.Fatal(e)}
 apx,e:=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p2.ID,Incoming:"*",Target:"glm-5.3",Enabled:true})
 if e!=nil{t.Fatalf("different Provider's wildcard must not conflict: %v",e)}
 if zro.ID==apx.ID{t.Fatal("Provider-specific aliases must be different rows")}
 if _,e=s.RouteFor("claude-sonnet-4-5");!errors.Is(e,ErrAmbiguousProvider){
  t.Fatalf("two wildcard matches without default should be ambiguous, got %v",e)
 }
 if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p1.ID,Incoming:"*",Target:"duplicate",Enabled:true});e==nil{
  t.Fatal("duplicate wildcard for the same Gateway AND Provider must conflict")
 }
 g.DefaultProviderID=p1.ID
 if _,e=s.SaveGateway(Gateway{ID:g.ID,Name:g.Name,Port:g.Port,InputType:g.InputType,DefaultProviderID:p1.ID});e!=nil{t.Fatal(e)}
 defaultRoute,e:=s.RouteFor("claude-haiku-4-5")
 if e!=nil||defaultRoute.ProviderID!=p1.ID||defaultRoute.Target!="deepseek"{
  t.Fatalf("unexpected default ZRO fallback %+v %v",defaultRoute,e)
 }
 selected,e:=s.RouteForProvider("claude-haiku-4-5","APInex")
 if e!=nil||selected.ProviderID!=p2.ID||selected.Target!="glm-5.3"{
  t.Fatalf("explicit APInex fallback failed %+v %v",selected,e)
 }
 selected,e=s.RouteForProvider("claude-haiku-4-5",p2.ID)
 if e!=nil||selected.ProviderID!=p2.ID{t.Fatalf("provider ID selector failed %+v %v",selected,e)}
 exact,e:=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p1.ID,Incoming:"claude-haiku-4-5",Target:"zro-exact",Enabled:true})
 if e!=nil{t.Fatal(e)}
 direct,e:=s.RouteFor("claude-haiku-4-5")
 if e!=nil||direct.Target!="zro-exact"{t.Fatalf("exact rule should take priority within default Provider: %+v %v",direct,e)}
 other,e:=s.RouteForProvider("claude-haiku-4-5",p2.ID)
 if e!=nil||other.Target!="glm-5.3"{t.Fatalf("ZRO exact should not override APInex fallback: %+v %v",other,e)}
 exact.Enabled=false
 if _,e=s.SaveAlias(exact);e!=nil{t.Fatal(e)}
 route,e:=s.RouteFor("claude-haiku-4-5")
 if e!=nil||route.Target!="deepseek"{t.Fatalf("disabled exact rule should fall back to ZRO wildcard: %+v %v",route,e)}
 if _,e=s.RouteForProvider("other","not-a-provider");!errors.Is(e,ErrNotFound){t.Fatalf("unknown selector: %v",e)}
}

func TestProviderScopedExactAliasesAndModelDiscovery(t *testing.T){
 s:=testStore(t)
 p1,e:=s.SaveProvider(Provider{Name:"ZRO",BaseURL:"https://zro.example",APIKey:"key-one"})
 if e!=nil{t.Fatal(e)}
 p2,e:=s.SaveProvider(Provider{Name:"APInex",BaseURL:"https://apinex.example",APIKey:"key-two"})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"Models"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 for _,provider:=range []Provider{p1,p2}{
  if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:provider.ID,Incoming:"claude-sonnet-4-5",Target:provider.Name+"-model",Enabled:true});e!=nil{t.Fatal(e)}
 }
 if _,e=s.RouteFor("claude-sonnet-4-5");!errors.Is(e,ErrAmbiguousProvider){t.Fatalf("duplicate exact should not route arbitrarily: %v",e)}
 models,e:=s.ActiveModels()
 if e!=nil{t.Fatal(e)}
 if len(models)!=0{t.Fatalf("ambiguous models should not be listed by default: %v",models)}
 g.DefaultProviderID=p2.ID
 if _,e=s.SaveGateway(Gateway{ID:g.ID,Name:g.Name,Port:g.Port,InputType:g.InputType,DefaultProviderID:p2.ID});e!=nil{t.Fatal(e)}
 models,e=s.ActiveModels()
 if e!=nil||len(models)!=1||models[0]!="claude-sonnet-4-5"{t.Fatalf("default Provider should filter discoverable models: %v %v",models,e)}
 route,e:=s.RouteFor("claude-sonnet-4-5")
 if e!=nil||route.ProviderID!=p2.ID{t.Fatalf("wrong default Provider: %+v %v",route,e)}
}

func TestUpgradeLegacyAliasUniquenessPreservesData(t *testing.T){
 dir:=t.TempDir()
 t.Setenv("LOCALAPPDATA",dir)
 t.Setenv("XDG_CONFIG_HOME",dir)
 dbPath:=filepath.Join(dir,"legacy_aliases.db")
 db,err:=sql.Open("sqlite",dbPath)
 if err!=nil{t.Fatal(err)}
 statements:=[]string{
  `CREATE TABLE providers(id TEXT PRIMARY KEY,name TEXT NOT NULL,base_url TEXT NOT NULL,secret BLOB NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
  `CREATE TABLE gateways(id TEXT PRIMARY KEY,name TEXT NOT NULL,client_hash TEXT NOT NULL,active INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
  `CREATE TABLE aliases(id TEXT PRIMARY KEY,gateway_id TEXT NOT NULL REFERENCES gateways(id) ON DELETE CASCADE,provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,incoming TEXT NOT NULL,target TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,UNIQUE(gateway_id,incoming))`,
  `INSERT INTO providers(id,name,base_url,secret) VALUES('provider-one','ZRO','https://example.org',X''),('provider-two','APInex','https://api.example.org',X'')`,
  `INSERT INTO gateways(id,name,client_hash,active) VALUES('gateway-one','Legacy', 'hashed',1)`,
  `INSERT INTO aliases(id,gateway_id,provider_id,incoming,target,enabled) VALUES('old-alias','gateway-one','provider-one','*','deepseek',1)`,
 }
 for _,stmt:=range statements{if _,err=db.Exec(stmt);err!=nil{t.Fatal(err)}}
 if err=db.Close();err!=nil{t.Fatal(err)}
 upgraded,err:=OpenStore(dbPath)
 if err!=nil{t.Fatalf("upgrade failed: %v",err)}
 defer upgraded.Close()
 var id,target string
 var enabled bool
 if err=upgraded.db.QueryRow("SELECT id,target,enabled FROM aliases WHERE gateway_id='gateway-one' AND provider_id='provider-one'").Scan(&id,&target,&enabled);err!=nil{t.Fatal(err)}
 if id!="old-alias"||target!="deepseek"||!enabled{t.Fatalf("existing alias changed: %s %s %t",id,target,enabled)}
 if _,err=upgraded.db.Exec("INSERT INTO aliases(id,gateway_id,provider_id,incoming,target,enabled) VALUES('new-alias','gateway-one','provider-two','*','glm-5.3',1)");err!=nil{
  t.Fatalf("provider-two wildcard should be allowed after migration: %v",err)
 }
 // Restarting must be idempotent, not attempt another migration.
 if err=upgraded.Close();err!=nil{t.Fatal(err)}
 reopened,err:=OpenStore(dbPath)
 if err!=nil{t.Fatalf("migration repeated incorrectly: %v",err)}
 defer reopened.Close()
 aliases,err:=reopened.Aliases()
 if err!=nil||len(aliases)!=2{t.Fatalf("migrated data lost on restart: %+v %v",aliases,err)}
}

func TestGatewayRequiresExplicitProviderWhenMultipleWildcards(t *testing.T){
 first:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  _,_ = w.Write([]byte(`{"id":"zro","type":"message","role":"assistant","content":[{"type":"text","text":"ZRO reply"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
 }))
 defer first.Close()
 second:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  _,_ = w.Write([]byte(`{"id":"apx","type":"message","role":"assistant","content":[{"type":"text","text":"APInex reply"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
 }))
 defer second.Close()
 s:=testStore(t)
 p1,e:=s.SaveProvider(Provider{Name:"ZRO",BaseURL:first.URL,APIKey:"zro"})
 if e!=nil{t.Fatal(e)}
 p2,e:=s.SaveProvider(Provider{Name:"APInex",BaseURL:second.URL,APIKey:"apx"})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"Claude"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 for _,item:=range []struct{provider Provider;target string}{{p1,"deepseek"},{p2,"glm-5.3"}}{
  if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:item.provider.ID,Incoming:"*",Target:item.target,Enabled:true});e!=nil{t.Fatal(e)}
 }
 a:=&App{store:s,client:first.Client()}
 invoke:=func(provider string)(int,string){
  request:=httptest.NewRequest("POST","/v1/messages",bytes.NewReader([]byte(`{"model":"claude-sonnet-4-5","max_tokens":30,"messages":[{"role":"user","content":"Hi"}]}`)))
  request.Header.Set("Authorization","Bearer "+g.ClientKey)
  if provider!=""{request.Header.Set("X-AliasPort-Provider",provider)}
  response:=httptest.NewRecorder()
  a.messages(response,request)
  return response.Code,response.Body.String()
 }
 code,body:=invoke("")
 if code!=409||!strings.Contains(body,"multiple providers"){t.Fatalf("ambiguous request should return HTTP 409: %d %s",code,body)}
 code,body=invoke(p1.ID)
 if code!=200||!strings.Contains(body,"ZRO reply"){t.Fatalf("ZRO explicit header did not work: %d %s",code,body)}
 code,body=invoke("APInex")
 if code!=200||!strings.Contains(body,"APInex reply"){t.Fatalf("APInex selector did not work: %d %s",code,body)}
 var jsonBody map[string]any
 if err:=json.Unmarshal([]byte(body),&jsonBody);err!=nil{t.Fatal(err)}
}
