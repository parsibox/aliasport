package main

import (
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "strings"
 "time"
)

func writeJSON(w http.ResponseWriter,status int,v any){
 w.Header().Set("Content-Type","application/json; charset=utf-8")
 w.WriteHeader(status)
 _=json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter,status int,msg string){
 writeJSON(w,status,map[string]string{"error":msg})
}
func parseJSON(w http.ResponseWriter,r *http.Request,v any)error{
 defer r.Body.Close()
 decoder:=json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20))
 decoder.DisallowUnknownFields()
 if e:=decoder.Decode(v);e!=nil{return e}
 if e:=decoder.Decode(new(any));e!=io.EOF{return errors.New("only one JSON object is allowed")}
 return nil
}
func method(w http.ResponseWriter,r *http.Request,allowed ...string)bool{
 for _,m:=range allowed{if r.Method==m{return true}}
 w.Header().Set("Allow",strings.Join(allowed,", "))
 apiError(w,405,"method not allowed")
 return false
}
func writeFailure(w http.ResponseWriter,err error){
 switch {
 case errors.Is(err,ErrNotFound):apiError(w,404,"record not found")
 case strings.Contains(err.Error(),"constraint failed"),strings.Contains(err.Error(),"FOREIGN KEY"):
  apiError(w,409,"conflicting alias or referenced record")
 default:
  apiError(w,400,err.Error())
 }
}
func (a *App) adminAPI(w http.ResponseWriter,r *http.Request){
 path:=strings.Trim(strings.TrimPrefix(r.URL.Path,"/api/"),"/")
 parts:=strings.Split(path,"/")
 if len(parts)==1 {
  switch parts[0] {
  case "shutdown":
   if !method(w,r,"POST"){return}
   // This is a destructive local action: require the management origin and a
   // non-simple header to prevent a cross-site form or navigation triggering it.
   if r.Header.Get("Origin")!="http://"+r.Host ||
      r.Header.Get("X-AliasPort-Action")!="shutdown" ||
      !isLoopbackAddr(r.Host) {
    apiError(w,403,"shutdown must be requested from the local management UI")
    return
   }
   var payload struct{Confirm string `json:"confirm"`}
   if e:=parseJSON(w,r,&payload);e!=nil{apiError(w,400,e.Error());return}
   if payload.Confirm!="shutdown"{apiError(w,400,"explicit shutdown confirmation required");return}
   if a.stopCh==nil {apiError(w,503,"shutdown is unavailable");return}
   a.stopOnce.Do(func(){
    // Give the browser a brief opportunity to receive the acknowledgement.
    time.AfterFunc(250*time.Millisecond,func(){close(a.stopCh)})
   })
   writeJSON(w,200,map[string]any{"accepted":true,"message":"AliasPort will stop immediately"})
   return
  case "overview":
   if !method(w,r,"GET"){return}
   providers,e1:=a.store.Providers()
   gateways,e2:=a.store.Gateways()
   aliases,e3:=a.store.Aliases()
   if e1!=nil||e2!=nil||e3!=nil{apiError(w,500,"database unavailable");return}
   active:=""
   for _,g:=range gateways{if g.Active{active=g.Name;break}}
   gatewayURL:=""
   for _,g:=range gateways{if g.Active{gatewayURL=fmt.Sprintf("http://127.0.0.1:%d",g.Port);break}}
   writeJSON(w,200,map[string]any{"providers":len(providers),"gateways":len(gateways),"aliases":len(aliases),"active_gateway":active,"uptime_seconds":int(time.Since(a.started).Seconds()),"gateway_url":gatewayURL})
   return
  case "events":
   if !method(w,r,"GET"){return}
   events,e:=a.store.Events();if e!=nil{apiError(w,500,"database unavailable");return};writeJSON(w,200,events);return
  case "providers":
   switch r.Method {
   case "GET":
    v,e:=a.store.Providers();if e!=nil{apiError(w,500,"database unavailable");return};writeJSON(w,200,v)
   case "POST":
    var v Provider
    if e:=parseJSON(w,r,&v);e!=nil{apiError(w,400,e.Error());return}
    v.ID="" // prevent overwriting another record
    saved,e:=a.store.SaveProvider(v);if e!=nil{writeFailure(w,e);return}
    writeJSON(w,201,saved)
   default:method(w,r,"GET","POST")
   }
   return
  case "gateways":
   switch r.Method {
   case "GET":
    v,e:=a.store.Gateways();if e!=nil{apiError(w,500,"database unavailable");return};writeJSON(w,200,v)
   case "POST":
    var v Gateway
    if e:=parseJSON(w,r,&v);e!=nil{apiError(w,400,e.Error());return}
    v.ID=""
    saved,e:=a.store.SaveGateway(v);if e!=nil{writeFailure(w,e);return}
    writeJSON(w,201,saved)
   default:method(w,r,"GET","POST")
   }
   return
  case "aliases":
   switch r.Method {
   case "GET":
    v,e:=a.store.Aliases();if e!=nil{apiError(w,500,"database unavailable");return};writeJSON(w,200,v)
   case "POST":
    var v Alias
    if e:=parseJSON(w,r,&v);e!=nil{apiError(w,400,e.Error());return}
    v.ID=""
    saved,e:=a.store.SaveAlias(v);if e!=nil{writeFailure(w,e);return}
    writeJSON(w,201,saved)
   default:method(w,r,"GET","POST")
   }
   return
  }
 }
 if len(parts)>=2{
  id:=parts[1]
  if id==""{apiError(w,400,"missing id");return}
  switch parts[0] {
  case "providers":
   // Provider API keys are scoped by provider ID and can be managed independently.
   if len(parts)>=3 && parts[2]=="keys" {
    if len(parts)==3 {
     switch r.Method {
     case "GET":
      keys,e:=a.store.ProviderKeys(id)
      if e!=nil{apiError(w,500,"cannot load provider keys");return}
      writeJSON(w,200,keys);return
     case "POST":
      var k ProviderKey
      if e:=parseJSON(w,r,&k);e!=nil{apiError(w,400,e.Error());return}
      k.ID="";k.ProviderID=id
      saved,e:=a.store.SaveProviderKey(k)
      if e!=nil{writeFailure(w,e);return}
      writeJSON(w,201,saved);return
     default:method(w,r,"GET","POST");return
     }
    }
    if len(parts)==4 {
     keyID:=parts[3]
     switch r.Method {
     case "PUT":
      var k ProviderKey
      if e:=parseJSON(w,r,&k);e!=nil{apiError(w,400,e.Error());return}
      k.ID=keyID;k.ProviderID=id
      saved,e:=a.store.SaveProviderKey(k)
      if e!=nil{writeFailure(w,e);return}
      writeJSON(w,200,saved);return
     case "DELETE":
      if e:=a.store.DeleteProviderKey(id,keyID);e!=nil{writeFailure(w,e);return}
      writeJSON(w,200,map[string]bool{"deleted":true});return
     default:method(w,r,"PUT","DELETE");return
     }
    }
    if len(parts)==5 && parts[4]=="reset" {
     if !method(w,r,"POST"){return}
     if e:=a.store.ResetProviderKey(id,parts[3]);e!=nil{writeFailure(w,e);return}
     writeJSON(w,200,map[string]bool{"reset":true});return
    }
    apiError(w,404,"unknown provider key action");return
   }
   switch r.Method {
   case "PUT":
    if len(parts)!=2{break}
    var v Provider
    if e:=parseJSON(w,r,&v);e!=nil{apiError(w,400,e.Error());return}
    v.ID=id
    saved,e:=a.store.SaveProvider(v);if e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,saved);return
   case "DELETE":
    if len(parts)!=2{break}
    if e:=a.store.DeleteProvider(id);e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,map[string]bool{"deleted":true});return
   }
   method(w,r,"PUT","DELETE");return
  case "gateways":
   if len(parts)==3&&parts[2]=="activate" {
    if !method(w,r,"POST"){return}
    if a.proxy==nil{apiError(w,503,"gateway manager unavailable");return}
    if e:=a.proxy.switchTo(id);e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,map[string]any{"activated":id});return
   }
   if len(parts)==3&&parts[2]=="deactivate" {
    if !method(w,r,"POST"){return}
    if a.proxy==nil{apiError(w,503,"gateway manager unavailable");return}
    if e:=a.proxy.deactivate(id);e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,map[string]any{"deactivated":id});return
   }
   if len(parts)==3&&parts[2]=="rotate" {
    if !method(w,r,"POST"){return}
    var payload struct {ClientKey string `json:"client_key"`}
    if e:=parseJSON(w,r,&payload);e!=nil{apiError(w,400,e.Error());return}
    key,e:=a.store.RotateKey(id,payload.ClientKey);if e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,map[string]string{"client_key":key});return
   }
   if len(parts)!=2{apiError(w,404,"unknown action");return}
   switch r.Method {
   case "PUT":
    var v Gateway
    if e:=parseJSON(w,r,&v);e!=nil{apiError(w,400,e.Error());return}
    v.ID=id
    if a.proxy==nil{apiError(w,503,"gateway manager unavailable");return}
    saved,e:=a.proxy.updateGateway(v);if e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,saved);return
   case "DELETE":
    if a.proxy==nil{apiError(w,503,"gateway manager unavailable");return}
    if e:=a.proxy.removeGateway(id);e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,map[string]bool{"deleted":true});return
   }
   method(w,r,"PUT","DELETE");return
  case "aliases":
   if len(parts)!=2{apiError(w,404,"unknown action");return}
   switch r.Method{
   case "PUT":
    var v Alias
    if e:=parseJSON(w,r,&v);e!=nil{apiError(w,400,e.Error());return}
    v.ID=id
    saved,e:=a.store.SaveAlias(v);if e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,saved);return
   case "DELETE":
    if e:=a.store.DeleteAlias(id);e!=nil{writeFailure(w,e);return}
    writeJSON(w,200,map[string]bool{"deleted":true});return
   }
   method(w,r,"PUT","DELETE");return
  }
 }
 apiError(w,404,fmt.Sprintf("unknown API route: %s",path))
}
