package main

import (
 "bytes"
 "database/sql"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"
)

func TestAutomaticProviderScopedAliases(t *testing.T){
 s:=testStore(t)
 p1,err:=s.SaveProvider(Provider{Name:"ZRO",BaseURL:"https://zro.example",APIKey:"zro-key",Priority:10})
 if err!=nil{t.Fatal(err)}
 p2,err:=s.SaveProvider(Provider{Name:"APInex",BaseURL:"https://apinex.example",APIKey:"apx-key",Priority:20})
 if err!=nil{t.Fatal(err)}
 g,err:=s.SaveGateway(Gateway{Name:"Claude"})
 if err!=nil{t.Fatal(err)}
 if err=s.ActivateGateway(g.ID);err!=nil{t.Fatal(err)}
 for _,v:=range []struct{p Provider;target string}{{p1,"deepseek"},{p2,"glm-5.3"}}{
  if _,err=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:v.p.ID,Incoming:"*",Target:v.target,Enabled:true});err!=nil{t.Fatalf("per-Provider wildcard conflict: %v",err)}
 }
 if _,err=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p1.ID,Incoming:"*",Target:"duplicate",Enabled:true});err==nil{
  t.Fatal("duplicate wildcard in same Gateway and Provider should conflict")
 }
 routes,err:=s.RoutesFor("claude-sonnet-4-5")
 if err!=nil||len(routes)!=2{t.Fatalf("expected two eligible Providers, got %+v %v",routes,err)}
 if routes[0].ProviderID!=p1.ID||routes[0].Target!="deepseek"||routes[1].ProviderID!=p2.ID||routes[1].Target!="glm-5.3"{
  t.Fatalf("wrong ordered provider fallbacks: %+v",routes)
 }
 _,err=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p2.ID,Incoming:"claude-sonnet-4-5",Target:"glm-exact",Enabled:true})
 if err!=nil{t.Fatal(err)}
 routes,err=s.RoutesFor("claude-sonnet-4-5")
 if err!=nil||routes[0].Target!="deepseek"||routes[1].Target!="glm-exact"{
  t.Fatalf("exact alias must apply ONLY within its Provider: %+v %v",routes,err)
 }
 // Lower the APInex priority; it must now be tried first.
 _,err=s.SaveProvider(Provider{ID:p2.ID,Name:p2.Name,BaseURL:p2.BaseURL,APIType:p2.APIType,Priority:1})
 if err!=nil{t.Fatal(err)}
 routes,err=s.RoutesFor("claude-sonnet-4-5")
 if err!=nil||routes[0].ProviderID!=p2.ID||routes[0].Target!="glm-exact"{t.Fatalf("priority update ignored: %+v %v",routes,err)}
 off:=false
 _,err=s.SaveProvider(Provider{ID:p2.ID,Name:p2.Name,BaseURL:p2.BaseURL,APIType:p2.APIType,Priority:1,Enabled:&off})
 if err!=nil{t.Fatal(err)}
 routes,err=s.RoutesFor("claude-sonnet-4-5")
 if err!=nil||len(routes)!=1||routes[0].ProviderID!=p1.ID{t.Fatalf("disabled Provider should be excluded: %+v %v",routes,err)}
}

func TestAutomaticProviderFailoverOnCreditsAndRateLimit(t *testing.T){
 var seen []string
 upstream:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var doc map[string]any
  if err:=json.NewDecoder(r.Body).Decode(&doc);err!=nil{t.Error(err);return}
  model,_:=doc["model"].(string)
  seen=append(seen,model)
  switch model{
  case "first-model":
   w.Header().Set("Content-Type","application/json")
   w.WriteHeader(402)
   _,_=w.Write([]byte(`{"error":{"code":"insufficient_credits"}}`))
  case "second-model":
   w.Header().Set("Content-Type","application/json")
   _,_=w.Write([]byte(`{"id":"msg_2","type":"message","role":"assistant","content":[{"type":"text","text":"second provider succeeded"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
  default:t.Errorf("unexpected model %q",model)
  }
 }))
 defer upstream.Close()
 s:=testStore(t)
 p1,e:=s.SaveProvider(Provider{Name:"First",BaseURL:upstream.URL,Priority:10,APIKey:"first-key"})
 if e!=nil{t.Fatal(e)}
 p2,e:=s.SaveProvider(Provider{Name:"Second",BaseURL:upstream.URL,Priority:20,APIKey:"second-key"})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"Claude"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 for _,item:=range []struct{p Provider;model string}{{p1,"first-model"},{p2,"second-model"}}{
  if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:item.p.ID,Incoming:"*",Target:item.model,Enabled:true});e!=nil{t.Fatal(e)}
 }
 app:=&App{store:s,client:upstream.Client()}
 invoke:=func()string{
  r:=httptest.NewRequest("POST","/v1/messages",bytes.NewReader([]byte(`{"model":"claude-opus-4-5","max_tokens":30,"messages":[{"role":"user","content":"Hi"}]}`)))
  r.Header.Set("Authorization","Bearer "+g.ClientKey)
  w:=httptest.NewRecorder()
  app.messages(w,r)
  if w.Code!=200{t.Fatalf("proxy status %d %s",w.Code,w.Body.String())}
  return w.Body.String()
 }
 if !strings.Contains(invoke(),"second provider succeeded"){t.Fatal("automatic failover did not reach second Provider")}
 if strings.Join(seen,",")!="first-model,second-model"{t.Fatalf("wrong attempts: %v",seen)}
 keys,e:=s.ProviderKeys(p1.ID)
 if e!=nil||keys[0].Status!="billing_blocked"{t.Fatalf("first Provider key not marked exhausted: %+v %v",keys,e)}
 if !strings.Contains(invoke(),"second provider succeeded"){t.Fatal("second call failed")}
 if strings.Join(seen,",")!="first-model,second-model,second-model"{t.Fatalf("blocked Provider key was retried: %v",seen)}
}

func TestAutomaticFallbackWhenProviderHasNoActiveKeys(t *testing.T){
 s:=testStore(t)
 a,e:=s.SaveProvider(Provider{Name:"A",BaseURL:"https://a.example",APIKey:"key-a",Priority:1})
 if e!=nil{t.Fatal(e)}
 b,e:=s.SaveProvider(Provider{Name:"B",BaseURL:"https://b.example",APIKey:"key-b",Priority:2})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"G"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 for _,item:=range []struct{p Provider;model string}{{a,"a-target"},{b,"b-target"}}{
  if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:item.p.ID,Incoming:"*",Target:item.model,Enabled:true});e!=nil{t.Fatal(e)}
 }
 keys,e:=s.ProviderKeys(a.ID)
 if e!=nil{t.Fatal(e)}
 if e=s.DeleteProviderKey(a.ID,keys[0].ID);e!=nil{t.Fatal(e)}
 routes,e:=s.RoutesFor("random-model")
 if e!=nil||len(routes)!=2{t.Fatalf("no-key Provider remains configured but should be skipped by runtime: %+v %v",routes,e)}
 usable,e:=s.EligibleProviderKeys(a.ID)
 if e!=ErrNoUsableKeys||len(usable)!=0{t.Fatalf("missing first Provider should yield no keys: %+v %v",usable,e)}
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

