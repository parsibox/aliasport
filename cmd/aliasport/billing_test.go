package main

import (
 "bytes"
 "database/sql"
 "encoding/json"
 "io"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "sync"
 "testing"
)

func TestBillingClassification(t *testing.T){
 tests:=[]struct{status int;body string;want bool}{
  {402,"payment required",true},
  {403,`{"error":{"type":"billing_issue","message":"Account billing issue"}}`,true},
  {403,`{"error":"Billing issue"}`,true},
  {429,`{"error":{"code":"insufficient_quota","message":"insufficient credits"}}`,true},
  {400,`{"error":{"message":"Credit balance is too low"}}`,true},
  {429,`{"error":{"type":"rate_limit_error","message":"Too many requests"}}`,false},
  {401,`{"error":{"type":"billing_issue"}}`,false},
  {403,`{"error":{"type":"permission_error"}}`,false},
  {500,`{"error":{"type":"billing_issue"}}`,false},
  {400,`{"error":{"type":"invalid_request_error","message":"Invalid model"}}`,false},
  {429,"<html>billing issue</html>",false},
 }
 for _,tc:=range tests{
  if got:=isBillingFailure(tc.status,[]byte(tc.body));got!=tc.want{
   t.Errorf("status=%d body=%q got=%v want=%v",tc.status,tc.body,got,tc.want)
  }
 }
}

func setupTwoKeyProxy(t *testing.T,handler http.HandlerFunc)(*Store,*App,Gateway,Provider,ProviderKey){
 t.Helper()
 upstream:=httptest.NewServer(handler)
 t.Cleanup(upstream.Close)
 s:=testStore(t)
 p,e:=s.SaveProvider(Provider{Name:"ZRO local",BaseURL:upstream.URL,APIKey:"first-secret"})
 if e!=nil{t.Fatal(e)}
 initial,e:=s.ProviderKeys(p.ID);if e!=nil||len(initial)!=1{t.Fatalf("first key missing: %v %+v",e,initial)}
 second,e:=s.SaveProviderKey(ProviderKey{ProviderID:p.ID,Label:"Secondary",Priority:100,Enabled:true,APIKey:"second-secret"})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"Desktop"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p.ID,Incoming:"claude-sonnet",Target:"deepseek-model",Enabled:true})
 if e!=nil{t.Fatal(e)}
 return s,&App{store:s,client:upstream.Client()},g,p,second
}
func proxyOnce(app *App,key string)*httptest.ResponseRecorder {
 r:=httptest.NewRequest("POST","/v1/messages",
  strings.NewReader(`{"model":"claude-sonnet","stream":true,"max_tokens":24,"messages":[{"role":"user","content":"hi"}]}`))
 r.Header.Set("Authorization","Bearer "+key)
 rec:=httptest.NewRecorder()
 app.messages(rec,r)
 return rec
}
func TestBillingFailoverQuarantineAndReset(t *testing.T){
 var mu sync.Mutex
 attempts:=[]string{}
 handler:=func(w http.ResponseWriter,r *http.Request){
  key:=strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ")
  mu.Lock();attempts=append(attempts,key);mu.Unlock()
  b,_:=io.ReadAll(r.Body)
  if !bytes.Contains(b,[]byte("deepseek-model")){t.Error("model alias was not applied")}
  if key=="first-secret"{
   w.Header().Set("Content-Type","application/json")
   w.WriteHeader(402)
   _,_=w.Write([]byte(`{"error":{"type":"billing_issue","message":"No credits"}}`))
   return
  }
  w.Header().Set("Content-Type","text/event-stream")
  _,_=w.Write([]byte("event: content_block_delta\ndata: {\"ok\":true}\n\n"))
 }
 s,app,g,p,_:=setupTwoKeyProxy(t,handler)
 rec:=proxyOnce(app,g.ClientKey)
 if rec.Code!=200{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
 if !strings.Contains(rec.Body.String(),"content_block_delta"){t.Fatal("stream not forwarded")}
 keys,e:=s.ProviderKeys(p.ID);if e!=nil{t.Fatal(e)}
 if keys[0].Status!="billing_blocked"||keys[1].Status!="ready"{t.Fatalf("unexpected key state: %+v",keys)}
 rec=proxyOnce(app,g.ClientKey)
 if rec.Code!=200{t.Fatal("second request failed",rec.Code)}
 mu.Lock();got:=append([]string{},attempts...);mu.Unlock()
 if strings.Join(got,",")!="first-secret,second-secret,second-secret"{t.Fatalf("wrong failover sequence: %v",got)}
 if e=s.ResetProviderKey(p.ID,keys[0].ID);e!=nil{t.Fatal(e)}
 keys,e=s.ProviderKeys(p.ID);if e!=nil||keys[0].Status!="ready"{t.Fatalf("reset did not clear quarantine: %v",e)}
}

func TestRateLimitDoesNotRotateCredentials(t *testing.T){
 calls:=0
 handler:=func(w http.ResponseWriter,r *http.Request){
  calls++
  w.Header().Set("Content-Type","application/json")
  w.WriteHeader(429)
  _,_=w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"Too many requests"}}`))
 }
 s,app,g,p,_:=setupTwoKeyProxy(t,handler)
 rec:=proxyOnce(app,g.ClientKey)
 if rec.Code!=429||calls!=1{t.Fatalf("rate limit unexpectedly retried: status %d, calls %d",rec.Code,calls)}
 keys,e:=s.ProviderKeys(p.ID);if e!=nil{t.Fatal(e)}
 if keys[0].Status!="ready"{t.Fatal("rate limit quarantined a key")}
}

func TestProviderKeyPersistenceAndNoResurrection(t *testing.T){
 root:=t.TempDir()
 t.Setenv("XDG_CONFIG_HOME",root)
 t.Setenv("LOCALAPPDATA",root)
 path:=filepath.Join(root,"keys.db")
 s,e:=OpenStore(path);if e!=nil{t.Fatal(e)}
 p,e:=s.SaveProvider(Provider{Name:"P",BaseURL:"https://example.com",APIKey:"original-secret"})
 if e!=nil{t.Fatal(e)}
 keys,e:=s.ProviderKeys(p.ID);if e!=nil||len(keys)!=1{t.Fatalf("key missing: %v",e)}
 if keys[0].APIKey!=""{t.Fatal("API key exposed in list")}
 var encrypted []byte
 if e=s.db.QueryRow("SELECT secret FROM provider_keys WHERE id=?",keys[0].ID).Scan(&encrypted);e!=nil{t.Fatal(e)}
 if bytes.Contains(encrypted,[]byte("original-secret")){t.Fatal("key stored in plaintext")}
 if e=s.DeleteProviderKey(p.ID,keys[0].ID);e!=nil{t.Fatal(e)}
 if e=s.Close();e!=nil{t.Fatal(e)}
 s,e=OpenStore(path);if e!=nil{t.Fatal(e)}
 defer s.Close()
 keys,e=s.ProviderKeys(p.ID)
 if e!=nil||len(keys)!=0{t.Fatalf("deleted legacy key resurrected after restart: %+v %v",keys,e)}
}

func TestLocalGatewayCustomKey(t *testing.T){
 s:=testStore(t)
 if _,e:=s.SaveGateway(Gateway{Name:"weak",ClientKey:"123"});e==nil{t.Fatal("accepted weak key")}
 custom:="my-custom-local-client-key-at-least-24-characters"
 g,e:=s.SaveGateway(Gateway{Name:"Desktop",ClientKey:custom});if e!=nil{t.Fatal(e)}
 if g.ClientKey!=custom{t.Fatal("custom key was not accepted")}
 newKey:="another-custom-key-of-adequate-length-123"
 rotated,e:=s.RotateKey(g.ID,newKey);if e!=nil||rotated!=newKey{t.Fatalf("rotation failed: %v",e)}
 list,e:=s.Gateways();if e!=nil{t.Fatal(e)}
 raw,_:=json.Marshal(list)
 if bytes.Contains(raw,[]byte(newKey)){t.Fatal("gateway listing leaked client key")}
}

func TestLegacyProviderCredentialMigration(t *testing.T) {
 root:=t.TempDir()
 t.Setenv("XDG_CONFIG_HOME",root)
 t.Setenv("LOCALAPPDATA",root)
 path:=filepath.Join(root,"legacy.db")
 db,err:=sql.Open("sqlite",path)
 if err!=nil{t.Fatal(err)}
 _,err=db.Exec(`CREATE TABLE providers(
  id TEXT PRIMARY KEY,name TEXT NOT NULL,base_url TEXT NOT NULL,
  secret BLOB NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
 if err!=nil{t.Fatal(err)}
 encrypted,err:=sealSecret([]byte("old-provider-key"))
 if err!=nil{t.Fatal(err)}
 _,err=db.Exec("INSERT INTO providers(id,name,base_url,secret) VALUES(?,?,?,?)",
  "old-id","Legacy","https://zro.moonmath.ai",encrypted)
 if err!=nil{t.Fatal(err)}
 if err=db.Close();err!=nil{t.Fatal(err)}
 s,err:=OpenStore(path)
 if err!=nil{t.Fatal(err)}
 keys,err:=s.ProviderKeys("old-id")
 if err!=nil||len(keys)!=1{t.Fatalf("legacy key not imported: %v %+v",err,keys)}
 var copy []byte
 if err=s.db.QueryRow("SELECT secret FROM providers WHERE id='old-id'").Scan(&copy);err!=nil{t.Fatal(err)}
 if len(copy)!=0{t.Fatal("obsolete legacy secret was not cleared")}
 if err=s.Close();err!=nil{t.Fatal(err)}
 s,err=OpenStore(path)
 if err!=nil{t.Fatal(err)}
 defer s.Close()
 keys,err=s.ProviderKeys("old-id")
 if err!=nil||len(keys)!=1{t.Fatalf("migration was not idempotent: %v %+v",err,keys)}
}
