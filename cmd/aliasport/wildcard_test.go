package main

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync"
 "testing"
)

// A wildcard is a per-gateway fallback, not a replacement for more specific
// model mappings. It must never leak into /v1/models discovery as a real ID.
func TestWildcardFallbackWithExactPrecedence(t *testing.T) {
 store:=testStore(t)
 var mu sync.Mutex
 received:=[]string{}
 upstream:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  var body struct {Model string `json:"model"`}
  if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil{t.Error(err);return}
  mu.Lock();received=append(received,body.Model);mu.Unlock()
  w.Header().Set("Content-Type","application/json")
  _,_=w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
 }))
 defer upstream.Close()

 provider,err:=store.SaveProvider(Provider{Name:"Fixture",BaseURL:upstream.URL,APIKey:"test-key"})
 if err!=nil{t.Fatal(err)}
 gateway,err:=store.SaveGateway(Gateway{Name:"Wildcard Gateway"})
 if err!=nil{t.Fatal(err)}
 if err=store.ActivateGateway(gateway.ID);err!=nil{t.Fatal(err)}
 fallback,err:=store.SaveAlias(Alias{GatewayID:gateway.ID,ProviderID:provider.ID,Incoming:"*",Target:"fallback-model",Enabled:true})
 if err!=nil{t.Fatal(err)}
 exact,err:=store.SaveAlias(Alias{GatewayID:gateway.ID,ProviderID:provider.ID,Incoming:"claude-haiku-4-5",Target:"special-haiku",Enabled:true})
 if err!=nil{t.Fatal(err)}

 app:=&App{store:store,client:upstream.Client()}
 call:=func(incoming string)int{
  t.Helper()
  body,_:=json.Marshal(map[string]any{
   "model":incoming,
   "max_tokens":20,
   "messages":[]map[string]string{{"role":"user","content":"test"}},
  })
  req:=httptest.NewRequest("POST","/v1/messages",strings.NewReader(string(body)))
  req.Header.Set("Authorization","Bearer "+gateway.ClientKey)
  rec:=httptest.NewRecorder()
  app.messages(rec,req)
  return rec.Code
 }
 if code:=call("claude-opus-5-5");code!=200{t.Fatalf("fallback request: status %d",code)}
 if code:=call("claude-haiku-4-5");code!=200{t.Fatalf("exact request: status %d",code)}
 mu.Lock()
 if len(received)!=2 || received[0]!="fallback-model" || received[1]!="special-haiku"{
  t.Errorf("model route order unexpected: %v",received)
 }
 mu.Unlock()

 // The wildcard is a rule, not a discoverable model.
 ids,err:=store.ActiveModels()
 if err!=nil{t.Fatal(err)}
 if len(ids)!=1 || ids[0]!="claude-haiku-4-5" {t.Errorf("model discovery includes wildcard: %v",ids)}
 req:=httptest.NewRequest("GET","/v1/models",nil)
 req.Header.Set("Authorization","Bearer "+gateway.ClientKey)
 rec:=httptest.NewRecorder()
 app.models(rec,req)
 if rec.Code!=200 || strings.Contains(rec.Body.String(),"\"id\":\"*\"") {
  t.Fatalf("model discovery incorrect: %d %s",rec.Code,rec.Body.String())
 }

 // Disabling an exact mapping should allow the wildcard to take over.
 exact.Enabled=false
 if _,err=store.SaveAlias(exact);err!=nil{t.Fatal(err)}
 if code:=call("claude-haiku-4-5");code!=200{t.Fatalf("disabled exact fallback: status %d",code)}
 mu.Lock()
 if len(received)!=3 || received[2]!="fallback-model"{t.Errorf("disabled exact did not fall back: %v",received)}
 mu.Unlock()

 // Disabling wildcard restores unknown-alias errors for unmatched models.
 fallback.Enabled=false
 if _,err=store.SaveAlias(fallback);err!=nil{t.Fatal(err)}
 if code:=call("claude-opus-5-5");code!=400{t.Fatalf("disabled wildcard expected 400, got %d",code)}
}

func TestWildcardScopedToActiveGateway(t *testing.T) {
 store:=testStore(t)
 p,err:=store.SaveProvider(Provider{Name:"P",BaseURL:"https://example.com",APIKey:"test"})
 if err!=nil{t.Fatal(err)}
 g1,err:=store.SaveGateway(Gateway{Name:"One"})
 if err!=nil{t.Fatal(err)}
 g2,err:=store.SaveGateway(Gateway{Name:"Two"})
 if err!=nil{t.Fatal(err)}
 if _,err=store.SaveAlias(Alias{GatewayID:g1.ID,ProviderID:p.ID,Incoming:"*",Target:"only-one",Enabled:true});err!=nil{t.Fatal(err)}
 if err=store.ActivateGateway(g1.ID);err!=nil{t.Fatal(err)}
 route,err:=store.RouteFor("any-unknown-model")
 if err!=nil || route.Target!="only-one" {t.Fatalf("active profile one should match: %+v %v",route,err)}
 if err=store.ActivateGateway(g2.ID);err!=nil{t.Fatal(err)}
 if _,err=store.RoutesForGateway(g2.ID,"any-unknown-model");err!=ErrNotFound{
  t.Fatalf("wildcard leaked to Gateway without assignment: %v",err)
 }
 first,err:=store.RoutesForGateway(g1.ID,"any-unknown-model")
 if err!=nil||len(first)!=1||first[0].Target!="only-one"{
  t.Fatalf("first Gateway wildcard stopped working: %+v %v",first,err)
 }
}
