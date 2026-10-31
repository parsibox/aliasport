package main

import (
 "bytes"
 "encoding/json"
 "io"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

func testStore(t *testing.T)*Store{
 t.Helper()
 root:=t.TempDir()
 t.Setenv("LOCALAPPDATA",root)
 t.Setenv("XDG_CONFIG_HOME",root)
 db,e:=OpenStore(filepath.Join(root,"unit.db"))
 if e!=nil{t.Fatal(e)}
 t.Cleanup(func(){_ = db.Close()})
 return db
}
func TestProviderValidation(t *testing.T){
 tests:=[]struct{url string;valid bool}{
  {"https://zro.moonmath.ai",true},
  {"http://127.0.0.1:9999",true},
  {"http://localhost:9999",false},
  {"http://example.com",false},
  {"file:///etc/passwd",false},
  {"https://user:pass@zro.moonmath.ai",false},
 }
 for _,tt:=range tests{
  got:=validateProvider(Provider{Name:"Test",BaseURL:tt.url})==nil
  if got!=tt.valid{t.Errorf("%s valid=%v want %v",tt.url,got,tt.valid)}
 }
}
func TestAliasRoutingAndProfileSwitch(t *testing.T){
 s:=testStore(t)
 seen:=make(chan string,2)
 upstream:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/v1/messages"{t.Errorf("bad path: %s",r.URL.Path)}
  if r.Header.Get("Authorization")!="Bearer provider-secret"{t.Errorf("wrong upstream auth")}
  body,_:=io.ReadAll(r.Body)
  var doc map[string]any
  if e:=json.Unmarshal(body,&doc);e!=nil{t.Error(e)}
  seen<-doc["model"].(string)
  w.Header().Set("Content-Type","text/event-stream")
  _,_=w.Write([]byte("event: message_start\ndata: {\"ok\":true}\n\n"))
 }))
 defer upstream.Close()
 p,e:=s.SaveProvider(Provider{Name:"Mock",BaseURL:upstream.URL,APIKey:"provider-secret"})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"One"})
 if e!=nil{t.Fatal(e)}
 g2,e:=s.SaveGateway(Gateway{Name:"Two"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p.ID,Incoming:"claude-sonnet-5",Target:"deepseek-v4.1-flash",Enabled:true})
 if e!=nil{t.Fatal(e)}
 app:=&App{store:s,client:&http.Client{Timeout:10*time.Second}}
 input:=[]byte(`{"model":"claude-sonnet-5","max_tokens":12,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
 req:=httptest.NewRequest("POST","/v1/messages",bytes.NewReader(input))
 req.Header.Set("Authorization","Bearer "+g.ClientKey)
 rec:=httptest.NewRecorder()
 app.messages(rec,req)
 if rec.Code!=200{t.Fatalf("code %d body %s",rec.Code,rec.Body.String())}
 if model:=<-seen;model!="deepseek-v4.1-flash"{t.Errorf("upstream model %s",model)}
 if !strings.Contains(rec.Body.String(),"message_start"){t.Errorf("stream was not forwarded")}
 if e=s.ActivateGateway(g2.ID);e!=nil{t.Fatal(e)}
 rec=httptest.NewRecorder()
 app.messages(rec,req.Clone(req.Context()))
 if rec.Code!=401{t.Errorf("old profile key accepted: %d",rec.Code)}
}
func TestProviderSecretIsEncryptedAtRest(t *testing.T){
 s:=testStore(t)
 p,e:=s.SaveProvider(Provider{Name:"ZRO",BaseURL:"https://zro.moonmath.ai",APIKey:"my-very-secret-api-key"})
 if e!=nil{t.Fatal(e)}
 var raw []byte
 if e=s.db.QueryRow("SELECT secret FROM providers WHERE id=?",p.ID).Scan(&raw);e!=nil{t.Fatal(e)}
 if bytes.Contains(raw,[]byte("my-very-secret-api-key")){t.Fatal("provider key persisted in plaintext")}
 plain,e:=openSecret(raw)
 if e!=nil||string(plain)!="my-very-secret-api-key"{t.Fatalf("decrypt failed: %v",e)}
}
