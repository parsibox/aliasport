package main

import (
 "bytes"
 "crypto/subtle"
 "database/sql"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "net/url"
 "strings"
 "time"
)

func localClientKey(r *http.Request) string {
 if value:=r.Header.Get("x-api-key");value!=""{return value}
 value:=r.Header.Get("Authorization")
 if len(value)>=7&&strings.EqualFold(value[:7],"Bearer "){return strings.TrimSpace(value[7:])}
 return ""
}
func authorized(r *http.Request,hash string)bool{
 incoming:=hashKey(localClientKey(r))
 return subtle.ConstantTimeCompare([]byte(incoming),[]byte(hash))==1 && localClientKey(r)!=""
}
func (a *App) gatewayAuth(w http.ResponseWriter,r *http.Request)bool{
 hash,err:=a.store.ActiveAuth()
 if errors.Is(err,sql.ErrNoRows){apiError(w,503,"no active gateway; activate a profile in AliasPort");return false}
 if err!=nil{apiError(w,500,"gateway unavailable");return false}
 if !authorized(r,hash){apiError(w,401,"invalid local gateway API key");return false}
 return true
}

func (a *App) models(w http.ResponseWriter,r *http.Request){
 if r.Method!="GET"{w.Header().Set("Allow","GET");apiError(w,405,"method not allowed");return}
 if !a.gatewayAuth(w,r){return}
 ids,err:=a.store.ActiveModels()
 if err!=nil{apiError(w,500,"cannot list models");return}
 data:=make([]map[string]any,0,len(ids))
 for _,id:=range ids{
  data=append(data,map[string]any{"id":id,"type":"model","display_name":id,"created_at":"2026-01-01T00:00:00Z"})
 }
 writeJSON(w,200,map[string]any{"data":data,"has_more":false,"first_id":first(ids),"last_id":last(ids)})
}
func first(values []string)any{if len(values)>0{return values[0]};return nil}
func last(values []string)any{if len(values)>0{return values[len(values)-1]};return nil}

func (a *App) messages(w http.ResponseWriter,r *http.Request){
 if r.Method!="POST"{w.Header().Set("Allow","POST");apiError(w,405,"method not allowed");return}
 if !a.gatewayAuth(w,r){return}
 started:=time.Now()
 status:=500;incoming:="";target:=""
 defer func(){go a.store.LogEvent(incoming,target,status,time.Since(started))}()

 body,err:=io.ReadAll(http.MaxBytesReader(w,r.Body,32<<20))
 if err!=nil{status=413;apiError(w,status,"request body too large or unreadable");return}
 var content map[string]json.RawMessage
 if err=json.Unmarshal(body,&content);err!=nil{status=400;apiError(w,status,"invalid JSON request");return}
 if err=json.Unmarshal(content["model"],&incoming);err!=nil||incoming==""{
  status=400;apiError(w,status,"model is required");return
 }
 route,err:=a.store.RouteFor(incoming)
 if errors.Is(err,ErrNotFound){status=400;apiError(w,status,"unknown or disabled model alias");return}
 if err!=nil{status=502;apiError(w,status,"cannot load provider configuration");return}
 if !authorized(r,route.ClientHash){status=401;apiError(w,status,"gateway profile changed; use active profile key");return}
 target=route.Target
 content["model"],err=json.Marshal(target)
 if err!=nil{status=500;apiError(w,status,"invalid model mapping");return}
 outgoing,err:=json.Marshal(content)
 if err!=nil{status=400;apiError(w,status,"unable to encode message");return}
 base,err:=url.Parse(route.BaseURL)
 if err!=nil{status=502;apiError(w,status,"bad provider URL");return}
 base.Path=strings.TrimSuffix(base.Path,"/")+r.URL.Path
 base.RawQuery=r.URL.RawQuery

 // A snapshot is used for the duration of this request. Each failed billing key
 // is quarantined so later requests skip it until an administrator resets it.
 keys,err:=a.store.EligibleProviderKeys(route.ProviderID)
 if errors.Is(err,ErrNoUsableKeys){
  status=503
  apiError(w,status,"no usable provider API keys (disabled or billing blocked)")
  return
 }
 if err!=nil{status=502;apiError(w,status,"cannot read provider API keys");return}

 for index,key:=range keys {
  if r.Context().Err()!=nil {return}
  req,reqErr:=http.NewRequestWithContext(r.Context(),"POST",base.String(),bytes.NewReader(outgoing))
  if reqErr!=nil{status=500;apiError(w,status,"request could not be prepared");return}
  req.Header.Set("Content-Type","application/json")
  if accept:=r.Header.Get("Accept");accept!=""{req.Header.Set("Accept",accept)}
  req.Header.Set("Authorization","Bearer "+string(key.Secret))
  req.Header.Set("anthropic-version",r.Header.Get("anthropic-version"))
  if req.Header.Get("anthropic-version")==""{req.Header.Set("anthropic-version","2023-06-01")}
  if v:=r.Header.Get("anthropic-beta");v!=""{req.Header.Set("anthropic-beta",v)}
  req.Header.Set("User-Agent","AliasPort/0.2")
  // Do not retain the decrypted key longer than necessary.
  upstream,callErr:=a.client.Do(req)
  if callErr!=nil{
   status=502
   apiError(w,status,"upstream connection failed (no automatic retry for transport errors)")
   return
  }
  status=upstream.StatusCode
  a.store.MarkKeyUsed(route.ProviderID,key.ID)

  var inspected []byte
  if status==400||status==402||status==403||status==429 {
   inspected,_=io.ReadAll(io.LimitReader(upstream.Body,131073))
  }
  if isBillingFailure(status,inspected) {
   _=upstream.Body.Close()
   if e:=a.store.BlockBillingKey(route.ProviderID,key.ID);e!=nil{
    status=502;apiError(w,status,"could not quarantine exhausted API key");return
   }
   if index<len(keys)-1 {continue}
   status=402
   apiError(w,status,"billing failure: all available provider API keys were exhausted")
   return
  }

  // Once a non-billing response (including SSE) starts, no key retry is allowed.
  // Preserve inspected bytes in the response, never buffer successful streams.
  forwardUpstream(w,upstream,inspected)
  return
 }
 status=503
 apiError(w,status,"no usable provider API keys remain")
}

func forwardUpstream(w http.ResponseWriter,upstream *http.Response,inspected []byte){
 defer upstream.Body.Close()
 for _,header:=range []string{
  "Content-Type","Request-Id","Anthropic-Request-Id","Retry-After",
  "Cache-Control","X-Request-Id",
 }{
  for _,value:=range upstream.Header.Values(header){w.Header().Add(header,value)}
 }
 w.Header().Set("X-Content-Type-Options","nosniff")
 w.Header().Set("Cache-Control","no-cache")
 w.WriteHeader(upstream.StatusCode)
 source:=io.MultiReader(bytes.NewReader(inspected),upstream.Body)
 buf:=make([]byte,32*1024)
 flusher,_:=w.(http.Flusher)
 for{
  n,readErr:=source.Read(buf)
  if n>0{
   if _,writeErr:=w.Write(buf[:n]);writeErr!=nil{return}
   if flusher!=nil{flusher.Flush()}
  }
  if readErr!=nil{return}
 }
}
