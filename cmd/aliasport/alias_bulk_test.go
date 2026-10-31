package main

import (
 "bytes"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func buildBulkAliasFixtures(t *testing.T)(*Store,[]Alias){
 t.Helper()
 store:=testStore(t)
 provider,err:=store.SaveProvider(Provider{Name:"Bulk provider",BaseURL:"https://example.org",APIKey:"test-key"})
 if err!=nil{t.Fatal(err)}
 first,err:=store.SaveGateway(Gateway{Name:"First"})
 if err!=nil{t.Fatal(err)}
 second,err:=store.SaveGateway(Gateway{Name:"Second"})
 if err!=nil{t.Fatal(err)}
 aliases:=[]Alias{
  {GatewayID:first.ID,ProviderID:provider.ID,Incoming:"claude-sonnet-4-5",Target:"target-sonnet",Enabled:true},
  {GatewayID:first.ID,ProviderID:provider.ID,Incoming:"claude-haiku-4-5",Target:"target-haiku",Enabled:false},
  {GatewayID:first.ID,ProviderID:provider.ID,Incoming:"*",Target:"fallback",Enabled:true},
  {GatewayID:second.ID,ProviderID:provider.ID,Incoming:"gpt-4o",Target:"target-other",Enabled:true},
 }
 for i:=range aliases{
  aliases[i],err=store.SaveAlias(aliases[i])
  if err!=nil{t.Fatal(err)}
 }
 return store,aliases
}
func loadAliasEnabled(t *testing.T,s *Store,id string)bool{
 t.Helper()
 var enabled bool
 if err:=s.db.QueryRow("SELECT enabled FROM aliases WHERE id=?",id).Scan(&enabled);err!=nil{t.Fatal(err)}
 return enabled
}
func TestBulkAliasEnabledDisabledAndDeleted(t *testing.T){
 store,aliases:=buildBulkAliasFixtures(t)
 selected:=[]string{aliases[0].ID,aliases[1].ID}
 count,err:=store.BulkAliases("disable",selected)
 if err!=nil||count!=2{t.Fatalf("bulk disable: count %d err %v",count,err)}
 if loadAliasEnabled(t,store,aliases[0].ID)||loadAliasEnabled(t,store,aliases[1].ID){
  t.Fatal("selected aliases were not disabled")
 }
 if !loadAliasEnabled(t,store,aliases[2].ID)||!loadAliasEnabled(t,store,aliases[3].ID){
  t.Fatal("bulk disable changed unselected aliases or another gateway")
 }
 count,err=store.BulkAliases("enable",selected)
 if err!=nil||count!=2{t.Fatalf("bulk enable: count %d err %v",count,err)}
 if !loadAliasEnabled(t,store,aliases[0].ID)||!loadAliasEnabled(t,store,aliases[1].ID){
  t.Fatal("selected aliases were not enabled")
 }
 count,err=store.BulkAliases("delete",selected)
 if err!=nil||count!=2{t.Fatalf("bulk delete: count %d err %v",count,err)}
 got,err:=store.Aliases()
 if err!=nil{t.Fatal(err)}
 if len(got)!=2||got[0].ID==aliases[0].ID||got[1].ID==aliases[1].ID {
  t.Fatalf("unexpected aliases after deletion: %+v",got)
 }
 for _,alias:=range got{
  if alias.ID!=aliases[2].ID&&alias.ID!=aliases[3].ID{
   t.Fatalf("unexpected remaining alias: %+v",alias)
  }
 }
}
func TestBulkAliasInvalidRequestsDoNotPartiallyApply(t *testing.T){
 store,aliases:=buildBulkAliasFixtures(t)
 missing:=strings.Repeat("a",24)
 invalid:=[]struct{
  name,action string
  ids []string
  wantNotFound bool
 }{
  {"unknown action","toggle",[]string{aliases[0].ID},false},
  {"no selection","delete",[]string{},false},
  {"duplicate selection","disable",[]string{aliases[0].ID,aliases[0].ID},false},
  {"malformed id","disable",[]string{aliases[0].ID,"bad-id"},false},
  {"missing id rolls back","delete",[]string{aliases[0].ID,missing},true},
  {"missing id prevents disable","disable",[]string{aliases[0].ID,missing},true},
 }
 for _,tt:=range invalid{
  t.Run(tt.name,func(t *testing.T){
   count,err:=store.BulkAliases(tt.action,tt.ids)
   if err==nil||count!=0{t.Fatalf("expected full failure, count %d err %v",count,err)}
   if tt.wantNotFound&&!errors.Is(err,ErrNotFound){t.Fatalf("expected missing id error, got %v",err)}
   if !loadAliasEnabled(t,store,aliases[0].ID){t.Fatal("bulk failure unexpectedly disabled the first alias")}
   got,err:=store.Aliases()
   if err!=nil{t.Fatal(err)}
   if len(got)!=4{t.Fatalf("bulk failure deleted an alias: %+v",got)}
  })
 }
}
func TestBulkAliasesAPI(t *testing.T){
 store,aliases:=buildBulkAliasFixtures(t)
 app:=&App{store:store}
 send:=func(method string,body any)(int,map[string]any){
  t.Helper()
  payload,_:=json.Marshal(body)
  req:=httptest.NewRequest(method,"http://127.0.0.1:8787/api/aliases/bulk",bytes.NewReader(payload))
  req.Header.Set("Content-Type","application/json")
  rec:=httptest.NewRecorder()
  adminSecurity(http.HandlerFunc(app.adminAPI)).ServeHTTP(rec,req)
  var result map[string]any
  if err:=json.Unmarshal(rec.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
  return rec.Code,result
 }
 code,_:=send("GET",map[string]any{})
 if code!=http.StatusMethodNotAllowed{t.Fatalf("GET /api/aliases/bulk status %d",code)}
 code,_=send("POST",map[string]any{"action":"disable","ids":[]string{aliases[0].ID,strings.Repeat("f",24)}})
 if code!=http.StatusNotFound{t.Fatalf("unknown ID should return 404; got %d",code)}
 if !loadAliasEnabled(t,store,aliases[0].ID){t.Fatal("API partially modified an alias after failed validation")}
 code,result:=send("POST",map[string]any{"action":"disable","ids":[]string{aliases[0].ID,aliases[2].ID}})
 if code!=200||result["affected"]!=float64(2)||result["action"]!="disable"{
  t.Fatalf("bulk response code=%d result=%+v",code,result)
 }
 if loadAliasEnabled(t,store,aliases[0].ID)||loadAliasEnabled(t,store,aliases[2].ID){
  t.Fatal("bulk API did not disable selected aliases")
 }
 if !loadAliasEnabled(t,store,aliases[3].ID){t.Fatal("API changed an unselected gateway's alias")}
}
