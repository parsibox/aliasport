package main

import (
 "bytes"
 "crypto/subtle"
 "database/sql"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net"
 "net/http"
 "strconv"
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
func (a *App) gatewayAuth(w http.ResponseWriter,r *http.Request)(Gateway,bool){
 g,err:=a.store.ActiveGateway()
 if errors.Is(err,ErrNotFound){apiError(w,503,"no active gateway; activate a profile in AliasPort");return Gateway{},false}
 if err!=nil{apiError(w,500,"gateway unavailable");return Gateway{},false}
 // A drained listener on the previous profile's port must not accept a new
 // request after a profile switch. In-flight responses are unaffected.
 if addr,ok:=r.Context().Value(http.LocalAddrContextKey).(net.Addr);ok{
  if tcp,ok:=addr.(*net.TCPAddr);ok && tcp.Port!=g.Port{
   apiError(w,503,"gateway profile switched to a different port");return Gateway{},false
  }
 }
 hash,err:=a.store.ActiveAuth()
 if errors.Is(err,sql.ErrNoRows){apiError(w,503,"gateway unavailable");return Gateway{},false}
 if err!=nil{apiError(w,500,"gateway unavailable");return Gateway{},false}
 if !authorized(r,hash){apiError(w,401,"invalid local gateway API key");return Gateway{},false}
 return g,true
}
func (a *App) models(w http.ResponseWriter,r *http.Request){
 if r.Method!="GET"{w.Header().Set("Allow","GET");apiError(w,405,"method not allowed");return}
 gateway,ok:=a.gatewayAuth(w,r);if !ok{return}
 ids,err:=a.store.ActiveModels()
 if err!=nil{apiError(w,500,"cannot list models");return}
 data:=make([]map[string]any,0,len(ids))
 for _,id:=range ids{
  if gateway.InputType==ProtocolOpenAIChat{
   data=append(data,map[string]any{"id":id,"object":"model","created":time.Now().Unix(),"owned_by":"aliasport"})
  }else{
   data=append(data,map[string]any{"id":id,"type":"model","display_name":id,"created_at":"2026-01-01T00:00:00Z"})
  }
 }
 if gateway.InputType==ProtocolOpenAIChat{
  writeJSON(w,200,map[string]any{"object":"list","data":data});return
 }
 writeJSON(w,200,map[string]any{"data":data,"has_more":false,"first_id":first(ids),"last_id":last(ids)})
}
func first(values []string)any{if len(values)>0{return values[0]};return nil}
func last(values []string)any{if len(values)>0{return values[len(values)-1]};return nil}

func(a *App)messages(w http.ResponseWriter,r *http.Request){
 a.handleGateway(w,r,ProtocolAnthropic)
}
func(a *App)chatCompletions(w http.ResponseWriter,r *http.Request){
 a.handleGateway(w,r,ProtocolOpenAIChat)
}

func(a *App)handleGateway(w http.ResponseWriter,r *http.Request,inputType string){
 if r.Method!="POST"{w.Header().Set("Allow","POST");apiError(w,405,"method not allowed");return}
 gateway,ok:=a.gatewayAuth(w,r);if !ok{return}
 if gateway.InputType!=inputType{
  apiError(w,404,"this gateway accepts "+gateway.InputType+" requests; change its input protocol in Gateway Profiles")
  return
 }
 started:=time.Now()
 status:=500;incoming:="";target:=""
 defer func(){go a.store.LogEvent(incoming,target,status,time.Since(started))}()
 body,err:=io.ReadAll(http.MaxBytesReader(w,r.Body,32<<20))
 if err!=nil{status=413;apiError(w,status,"request body too large or unreadable");return}
 content,err:=parseObject(body)
 if err!=nil{status=400;apiError(w,status,"invalid JSON request");return}
 incoming=str(content["model"])
 if incoming==""{status=400;apiError(w,status,"model is required");return}
 route,err:=a.store.RouteForProvider(incoming,strings.TrimSpace(r.Header.Get("X-AliasPort-Provider")))
 if errors.Is(err,ErrNotFound){status=400;apiError(w,status,"unknown Provider or disabled model alias for the selected Provider");return}
 if errors.Is(err,ErrAmbiguousProvider){
  status=409;apiError(w,status,err.Error());return
 }
 if err!=nil{status=502;apiError(w,status,"cannot load provider configuration");return}
 if !authorized(r,route.ClientHash){status=401;apiError(w,status,"gateway profile changed; use active profile key");return}
 target=route.Target
 content["model"]=target
 countTokens:=r.URL.Path=="/v1/messages/count_tokens"
 if countTokens && route.ProviderType!=ProtocolAnthropic{
  status=501;apiError(w,status,"count_tokens cannot be translated to OpenAI Chat Completions; use an Anthropic provider");return
 }
 payload:=content
 if !countTokens{
  payload,err=convertRequest(inputType,route.ProviderType,content)
  if err!=nil{
   status=422;apiError(w,status,err.Error());return
  }
 }
 outgoing,err:=json.Marshal(payload)
 if err!=nil{status=400;apiError(w,status,"unable to encode message");return}
 path:="/v1/messages"
 if !countTokens&&route.ProviderType==ProtocolOpenAIChat{path="/v1/chat/completions"}
 if countTokens{path="/v1/messages/count_tokens"}
 url,err:=providerEndpoint(route.BaseURL,route.ProviderType,path)
 if err!=nil{status=502;apiError(w,status,"invalid provider endpoint");return}
 // Query strings aren't needed for normal Chat or Anthropic Messages requests,
 // but preserve any explicitly supplied parameters.
 if r.URL.RawQuery!=""{url+="?"+r.URL.RawQuery}
 keys,err:=a.store.EligibleProviderKeys(route.ProviderID)
 if errors.Is(err,ErrNoUsableKeys){status=503;apiError(w,status,"no usable provider API keys (disabled or billing blocked)");return}
 if err!=nil{status=502;apiError(w,status,"cannot read provider API keys");return}
 for index,key:=range keys{
  if r.Context().Err()!=nil{return}
  req,reqErr:=http.NewRequestWithContext(r.Context(),"POST",url,bytes.NewReader(outgoing))
  if reqErr!=nil{status=500;apiError(w,status,"request could not be prepared");return}
  req.Header.Set("Content-Type","application/json")
  if accept:=r.Header.Get("Accept");accept!=""{req.Header.Set("Accept",accept)}
  req.Header.Set("Authorization","Bearer "+string(key.Secret))
  if route.ProviderType==ProtocolAnthropic{
   req.Header.Set("anthropic-version",r.Header.Get("anthropic-version"))
   if req.Header.Get("anthropic-version")==""{req.Header.Set("anthropic-version","2023-06-01")}
   if v:=r.Header.Get("anthropic-beta");v!=""{req.Header.Set("anthropic-beta",v)}
  }
  req.Header.Set("User-Agent","AliasPort/0.3")
  upstream,callErr:=a.client.Do(req)
  if callErr!=nil{
   status=502;apiError(w,status,"upstream connection failed (no retry for transport errors)");return
  }
  status=upstream.StatusCode
  a.store.MarkKeyUsed(route.ProviderID,key.ID)
  var inspected []byte
  if status==400||status==402||status==403||status==429{
   inspected,_=io.ReadAll(io.LimitReader(upstream.Body,131073))
  }
  if isBillingFailure(status,inspected){
   _=upstream.Body.Close()
   if e:=a.store.BlockBillingKey(route.ProviderID,key.ID);e!=nil{
    status=502;apiError(w,status,"could not quarantine exhausted API key");return
   }
   if index<len(keys)-1{continue}
   status=402;apiError(w,status,"billing failure: all provider API keys were exhausted");return
  }

  if route.ProviderType==inputType || countTokens || status<200 || status>=300 {
   forwardUpstream(w,upstream,inspected);return
  }
  if payload["stream"]==true {
   w.Header().Set("Content-Type","text/event-stream")
   w.Header().Set("Cache-Control","no-cache")
   w.Header().Set("X-Content-Type-Options","nosniff")
   w.WriteHeader(200)
   if route.ProviderType==ProtocolOpenAIChat&&inputType==ProtocolAnthropic{
    err=emitAnthropicFromChat(w,upstream.Body,incoming)
   }else if route.ProviderType==ProtocolAnthropic&&inputType==ProtocolOpenAIChat{
    err=emitChatFromAnthropic(w,upstream.Body,incoming)
   }else{err=fmt.Errorf("unsupported streaming conversion")}
   _=upstream.Body.Close()
   if err!=nil{
    // We cannot change status after starting a stream. Emit an explicit event
    // in the requested stream protocol, then close this connection.
    if inputType==ProtocolAnthropic{
     _=sendSSE(w,"error",map[string]any{"type":"error","error":map[string]any{"type":"api_error","message":err.Error()}},false)
    }else{
     _=sendSSE(w,"",map[string]any{"error":map[string]any{"message":err.Error()}},true)
    }
   }
   return
  }
  converted,readErr:=io.ReadAll(io.LimitReader(upstream.Body,64<<20))
  _=upstream.Body.Close()
  if readErr!=nil{status=502;apiError(w,status,"unable to read upstream response");return}
  result,convertErr:=convertResponse(route.ProviderType,inputType,converted,incoming)
  if convertErr!=nil{status=502;apiError(w,status,"provider response incompatible with selected protocol: "+convertErr.Error());return}
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("X-Content-Type-Options","nosniff")
  w.WriteHeader(status)
  _,_=w.Write(result)
  return
 }
 status=503;apiError(w,status,"no usable provider API keys remain")
}

func forwardUpstream(w http.ResponseWriter,upstream *http.Response,inspected []byte){
 defer upstream.Body.Close()
 for _,header:=range []string{"Content-Type","Request-Id","Anthropic-Request-Id","Retry-After","Cache-Control","X-Request-Id"}{
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

// checkProxyPort protects the admin listener address from being accidentally reused.
func checkProxyPort(port int,adminAddr string)error{
 _,s,err:=net.SplitHostPort(adminAddr);if err!=nil{return err}
 n,err:=strconv.Atoi(s);if err!=nil{return err}
 if n==port{return errors.New("the proxy port cannot equal the management port")}
 return nil
}
