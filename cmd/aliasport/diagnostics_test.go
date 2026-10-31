package main

import (
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestProviderDiagnosticModelsAndBalance(t *testing.T){
 requests:=[]string{}
 upstream:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  requests=append(requests,r.URL.Path)
  if r.Header.Get("Authorization")!="Bearer secret"{t.Error("provider key not passed")}
  w.Header().Set("Content-Type","application/json")
  switch r.URL.Path{
  case "/v1/models":
   _,_=w.Write([]byte(`{"object":"list","data":[{"id":"model-one","name":"Model One"},{"id":"model-two"}]}`))
  case "/v1/balance":
   _,_=w.Write([]byte(`{"balance":10.5,"currency":"USD"}`))
  default:
   http.NotFound(w,r)
  }
 }))
 defer upstream.Close()
 s:=testStore(t)
 p,err:=s.SaveProvider(Provider{Name:"APInex",APIType:ProtocolOpenAIChat,BaseURL:upstream.URL+"/v1",APIKey:"secret"})
 if err!=nil{t.Fatal(err)}
 app:=&App{store:s,client:upstream.Client()}
 for _,tc:=range []struct{suffix,expected string}{
  {"models",`"count":2`},
  {"balance",`"balance":10.5`},
 }{
  r:=httptest.NewRequest("GET","/api/providers/"+p.ID+"/"+tc.suffix,nil)
  w:=httptest.NewRecorder()
  app.adminAPI(w,r)
  if w.Code!=200||!strings.Contains(w.Body.String(),tc.expected){
   t.Errorf("%s returned %d %s",tc.suffix,w.Code,w.Body.String())
  }
 }
 if len(requests)!=2||requests[0]!="/v1/models"||requests[1]!="/v1/balance"{
  t.Errorf("unexpected upstream endpoints %v",requests)
 }
}
