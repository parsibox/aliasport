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

func bulkKeyFixtures(t *testing.T)(*Store,Provider,Provider,[]ProviderKey){
 t.Helper()
 store:=testStore(t)
 p1,err:=store.SaveProvider(Provider{Name:"First",BaseURL:"https://first.example",APIKey:"first-secret"})
 if err!=nil{t.Fatal(err)}
 p2,err:=store.SaveProvider(Provider{Name:"Second",BaseURL:"https://second.example",APIKey:"second-secret"})
 if err!=nil{t.Fatal(err)}
 primary1,err:=store.ProviderKeys(p1.ID)
 if err!=nil||len(primary1)!=1{t.Fatalf("provider 1 primary key: %v %v",primary1,err)}
 primary2,err:=store.ProviderKeys(p2.ID)
 if err!=nil||len(primary2)!=1{t.Fatalf("provider 2 primary key: %v %v",primary2,err)}
 extra,err:=store.SaveProviderKey(ProviderKey{
  ProviderID:p1.ID,Label:"Second key",APIKey:"extra-secret",Priority:100,Enabled:true,
 })
 if err!=nil{t.Fatal(err)}
 return store,p1,p2,[]ProviderKey{primary1[0],primary2[0],extra}
}

func TestBulkDeleteProviderKeysAcrossProviders(t *testing.T){
 store,p1,p2,keys:=bulkKeyFixtures(t)
 affected,err:=store.BulkDeleteProviderKeys([]string{keys[0].ID,keys[1].ID})
 if err!=nil||affected!=2{t.Fatalf("bulk delete: affected=%d err=%v",affected,err)}
 remaining1,err:=store.ProviderKeys(p1.ID)
 if err!=nil||len(remaining1)!=1||remaining1[0].ID!=keys[2].ID{
  t.Fatalf("provider 1 keys not preserved: %+v, %v",remaining1,err)
 }
 remaining2,err:=store.ProviderKeys(p2.ID)
 if err!=nil||len(remaining2)!=0{
  t.Fatalf("provider 2 keys should be empty: %+v, %v",remaining2,err)
 }
 providers,err:=store.Providers()
 if err!=nil||len(providers)!=2{t.Fatalf("bulk keys accidentally removed providers: %d %v",len(providers),err)}
}

func TestBulkDeleteProviderKeysRejectsWholeInvalidBatch(t *testing.T){
 store,p1,_,keys:=bulkKeyFixtures(t)
 missing:=strings.Repeat("f",24)
 for _,tt:=range []struct{
  name string;ids []string;wantNotFound bool
 }{
  {"empty",nil,false},
  {"duplicate",[]string{keys[0].ID,keys[0].ID},false},
  {"malformed",[]string{keys[0].ID,"not-an-id"},false},
  {"missing",[]string{keys[0].ID,missing},true},
 }{
  t.Run(tt.name,func(t *testing.T){
   count,err:=store.BulkDeleteProviderKeys(tt.ids)
   if count!=0||err==nil{t.Fatalf("bulk operation unexpectedly accepted: %d %v",count,err)}
   if tt.wantNotFound&&!errors.Is(err,ErrNotFound){t.Fatalf("wrong error: %v",err)}
   list,err:=store.ProviderKeys(p1.ID)
   if err!=nil||len(list)!=2{t.Fatalf("failed batch deleted existing key: %+v %v",list,err)}
  })
 }
 tooMany:=make([]string,5001)
 if count,err:=store.BulkDeleteProviderKeys(tooMany);count!=0||err==nil{t.Fatal("oversized batch accepted")}
}

func TestBulkDeleteProviderKeysAPI(t *testing.T){
 store,p1,p2,keys:=bulkKeyFixtures(t)
 app:=&App{store:store}
 invoke:=func(method string,payload any)(int,map[string]any){
  t.Helper()
  b,err:=json.Marshal(payload);if err!=nil{t.Fatal(err)}
  request:=httptest.NewRequest(method,"http://127.0.0.1:8787/api/keys/bulk",bytes.NewReader(b))
  request.Header.Set("Content-Type","application/json")
  response:=httptest.NewRecorder()
  adminSecurity(http.HandlerFunc(app.adminAPI)).ServeHTTP(response,request)
  var body map[string]any
  if err:=json.Unmarshal(response.Body.Bytes(),&body);err!=nil{t.Fatal(err)}
  return response.Code,body
 }
 code,_:=invoke("GET",map[string]any{})
 if code!=405{t.Fatalf("bulk endpoint GET returned %d",code)}
 code,_=invoke("POST",map[string]any{"action":"disable","ids":[]string{keys[0].ID}})
 if code!=400{t.Fatalf("non-delete action was accepted: %d",code)}
 code,_=invoke("POST",map[string]any{"action":"delete","ids":[]string{keys[0].ID,strings.Repeat("f",24)}})
 if code!=404{t.Fatalf("missing id returned %d",code)}
 list,_:=store.ProviderKeys(p1.ID)
 if len(list)!=2{t.Fatal("missing id caused partial delete")}
 code,body:=invoke("POST",map[string]any{"action":"delete","ids":[]string{keys[0].ID,keys[1].ID}})
 if code!=200||body["affected"]!=float64(2)||body["action"]!="delete"{
  t.Fatalf("unexpected bulk reply: %d %+v",code,body)
 }
 left1,_:=store.ProviderKeys(p1.ID)
 left2,_:=store.ProviderKeys(p2.ID)
 if len(left1)!=1||left1[0].ID!=keys[2].ID||len(left2)!=0{
  t.Fatalf("wrong keys removed: %v %v",left1,left2)
 }
}

func TestSearchAndProviderShortcutsMarkup(t *testing.T){
 contents,err:=webAssets.ReadFile("web/index.html")
 if err!=nil{t.Fatal(err)}
 html:=string(contents)
 for _,needed:=range []string{
  "keys-filter-provider","aliases-filter-provider","aliases-filter-gateway",
  "aliasesStatus","providersSearch","gatewaysSearch","eventsSearch",
  "function bulkDeleteProviderKeys()","function visibleAliases()",
  "function visibleProviderKeys()","function refreshProviderKeyBulkToolbar()",
  "function openAlias(id='',providerId='')",
  "v?.provider_id||providerId||ps[0].id",
 }{
  if !strings.Contains(html,needed){t.Errorf("missing UI element %q",needed)}
 }
}
