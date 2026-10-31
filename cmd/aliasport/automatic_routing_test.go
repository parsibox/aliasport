package main

import (
 "bytes"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 "time"
)

func TestMultiProviderRateLimitAutomaticFallback(t *testing.T){
 firstCalls,secondCalls:=0,0
 first:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  firstCalls++
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Retry-After","120")
  w.WriteHeader(429)
  _,_=w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"Try again later"}}`))
 }))
 defer first.Close()
 second:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  secondCalls++
  var payload map[string]any
  if err:=json.NewDecoder(r.Body).Decode(&payload);err!=nil{t.Error(err)}
  if payload["model"]!="glm-flash"{t.Errorf("second Provider received the wrong alias target: %v",payload["model"])}
  w.Header().Set("Content-Type","application/json")
  _,_=w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"fallback response"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
 }))
 defer second.Close()
 s:=testStore(t)
 firstProvider,e:=s.SaveProvider(Provider{Name:"First",BaseURL:first.URL,APIKey:"first-key",Priority:1})
 if e!=nil{t.Fatal(e)}
 secondProvider,e:=s.SaveProvider(Provider{Name:"Second",BaseURL:second.URL,APIKey:"second-key",Priority:2})
 if e!=nil{t.Fatal(e)}
 g,e:=s.SaveGateway(Gateway{Name:"Claude"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 for _,p:=range []struct{id,model string}{{firstProvider.ID,"deepseek"},{secondProvider.ID,"glm-flash"}}{
  if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p.id,Incoming:"*",Target:p.model,Enabled:true});e!=nil{t.Fatal(e)}
 }
 a:=&App{store:s,client:&http.Client{Timeout:5*time.Second}}
 send:=func()int{
  request:=httptest.NewRequest("POST","/v1/messages",bytes.NewReader([]byte(`{"model":"anything","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)))
  request.Header.Set("Authorization","Bearer "+g.ClientKey)
  rec:=httptest.NewRecorder()
  a.messages(rec,request)
  if !strings.Contains(rec.Body.String(),"fallback response"){t.Fatalf("unexpected response %s",rec.Body.String())}
  return rec.Code
 }
 if got:=send();got!=200{t.Fatalf("first status=%d",got)}
 if firstCalls!=1||secondCalls!=1{t.Fatalf("fallback not attempted: %d %d",firstCalls,secondCalls)}
 if got:=send();got!=200{t.Fatalf("second status=%d",got)}
 if firstCalls!=1||secondCalls!=2{t.Fatalf("rate-limited key should cool down, calls %d %d",firstCalls,secondCalls)}
 keys,e:=s.ProviderKeys(firstProvider.ID)
 if e!=nil||len(keys)!=1||keys[0].Status!="ready"{t.Fatalf("429 must not permanently block API key: %+v %v",keys,e)}
}

func TestProviderPriorityAndEnabledPersistAcrossReopen(t *testing.T){
 s:=testStore(t)
 enabled:=false
 p,e:=s.SaveProvider(Provider{Name:"Inactive",BaseURL:"https://example.org",APIKey:"key",Enabled:&enabled,Priority:12})
 if e!=nil{t.Fatal(e)}
 listed,e:=s.Providers()
 if e!=nil||len(listed)!=1{t.Fatalf("list Provider: %+v %v",listed,e)}
 if listed[0].Enabled==nil||*listed[0].Enabled||listed[0].Priority!=12{t.Fatalf("Provider settings changed: %+v",listed[0])}
 g,e:=s.SaveGateway(Gateway{Name:"Any"})
 if e!=nil{t.Fatal(e)}
 if e=s.ActivateGateway(g.ID);e!=nil{t.Fatal(e)}
 if _,e=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p.ID,Incoming:"*",Target:"glm",Enabled:true});e!=nil{t.Fatal(e)}
 if _,e=s.RoutesFor("anything");e!=ErrNotFound{t.Fatalf("disabled Provider should not route, got %v",e)}
}
