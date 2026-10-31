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
 return subtle.ConstantTimeCompare([]byte(incoming),[]byte(hash))==1 &&
  localClientKey(r)!=""
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
 for _,id:=range ids {
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
 if err!=nil {status=413;apiError(w,status,"request body too large or unreadable");return}
 var content map[string]json.RawMessage
 if err=json.Unmarshal(body,&content);err!=nil {status=400;apiError(w,status,"invalid JSON request");return}
 if err=json.Unmarshal(content["model"],&incoming);err!=nil||incoming=="" {
  status=400;apiError(w,status,"model is required");return
 }
 route,err:=a.store.RouteFor(incoming)
 if errors.Is(err,ErrNotFound){status=400;apiError(w,status,"unknown or disabled model alias");return}
 if err!=nil{status=502;apiError(w,status,"cannot load provider configuration");return}
 // Re-authorize against the captured profile snapshot to avoid a race while switching profiles.
 if !authorized(r,route.ClientHash){status=401;apiError(w,status,"gateway profile changed; use active profile key");return}
 target=route.Target
 mapped,err:=json.Marshal(route.Target)
 if err!=nil{status=500;apiError(w,status,"invalid model mapping");return}
 content["model"]=mapped
 outgoing,err:=json.Marshal(content)
 if err!=nil{status=400;apiError(w,status,"unable to encode message");return}
 base,err:=url.Parse(route.BaseURL)
 if err!=nil{status=502;apiError(w,status,"bad provider URL");return}
 base.Path=strings.TrimSuffix(base.Path,"/")+r.URL.Path
 base.RawQuery=r.URL.RawQuery
 req,err:=http.NewRequestWithContext(r.Context(),"POST",base.String(),bytes.NewReader(outgoing))
 if err!=nil{status=500;apiError(w,status,"request could not be prepared");return}
 req.Header.Set("Content-Type","application/json")
 req.Header.Set("Accept",r.Header.Get("Accept"))
 req.Header.Set("Authorization","Bearer "+string(route.Secret))
 req.Header.Set("anthropic-version",r.Header.Get("anthropic-version"))
 if req.Header.Get("anthropic-version")==""{req.Header.Set("anthropic-version","2023-06-01")}
 if v:=r.Header.Get("anthropic-beta");v!=""{req.Header.Set("anthropic-beta",v)}
 req.Header.Set("User-Agent","AliasPort/0.1")
 upstream,err:=a.client.Do(req)
 if err!=nil{status=502;apiError(w,status,"upstream connection failed");return}
 defer upstream.Body.Close()
 status=upstream.StatusCode
 // Allow only response headers relevant to Anthropic protocol; never leak upstream auth data.
 for _,header:=range []string{"Content-Type","Request-Id","Anthropic-Request-Id","Retry-After","Cache-Control","X-Request-Id","Content-Encoding"}{
  if v:=upstream.Header.Values(header);len(v)>0 {
   for _,item:=range v{w.Header().Add(header,item)}
  }
 }
 w.Header().Set("X-Content-Type-Options","nosniff")
 w.Header().Set("Cache-Control","no-cache")
 w.WriteHeader(status)
 buf:=make([]byte,32*1024)
 flusher,_:=w.(http.Flusher)
 for {
  n,readErr:=upstream.Body.Read(buf)
  if n>0 {
   if _,writeErr:=w.Write(buf[:n]);writeErr!=nil{break}
   if flusher!=nil{flusher.Flush()}
  }
  if readErr!=nil{break}
 }
}
