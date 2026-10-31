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

// Automatic provider routing: try providers in priority order. Each provider
// uses its own exact alias or '*' and its ordered eligible API keys. Only known
// pre-response failures are retried. Never replay a request after streaming or
// when transport failure leaves upstream execution uncertain.
func(a *App)handleGateway(w http.ResponseWriter,r *http.Request,inputType string){
 if r.Method!="POST"{w.Header().Set("Allow","POST");apiError(w,405,"method not allowed");return}
 gateway,ok:=a.gatewayAuth(w,r);if !ok{return}
 if gateway.InputType!=inputType{
  apiError(w,404,"this gateway accepts "+gateway.InputType+" requests");return
 }
 started:=time.Now()
 status:=500;incoming:="";target:=""
 defer func(){go a.store.LogEvent(incoming,target,status,time.Since(started))}()
 body,err:=io.ReadAll(http.MaxBytesReader(w,r.Body,32<<20))
 if err!=nil{status=413;apiError(w,status,"request body too large or unreadable");return}
 original,err:=parseObject(body)
 if err!=nil{status=400;apiError(w,status,"invalid JSON request");return}
 incoming=str(original["model"])
 if incoming==""{status=400;apiError(w,status,"model is required");return}
 routes,err:=a.store.RoutesFor(incoming)
 if errors.Is(err,ErrNotFound){status=400;apiError(w,status,"no enabled Provider has an alias for this model (exact or *)");return}
 if err!=nil{status=502;apiError(w,status,"cannot load Provider routes");return}
 countTokens:=r.URL.Path=="/v1/messages/count_tokens"
 hadEligible:=false
 hadConversion:=false
 lastStatus:=503
 lastMessage:="no eligible Provider API keys; enable a Provider and its keys"
 for _,route:=range routes{
  if r.Context().Err()!=nil{return}
  if !authorized(r,route.ClientHash){
   status=401;apiError(w,status,"gateway profile changed; use active profile key");return
  }
  if countTokens&&route.ProviderType!=ProtocolAnthropic{
   continue
  }
  requestBody:=make(map[string]any,len(original))
  for k,v:=range original{requestBody[k]=v}
  requestBody["model"]=route.Target
  payload:=requestBody
  if !countTokens{
   payload,err=convertRequest(inputType,route.ProviderType,requestBody)
   if err!=nil{
    hadConversion=true
    lastStatus=422
    lastMessage="no Provider supports all requested API features: "+err.Error()
    continue
   }
  }
  outgoing,marshalErr:=json.Marshal(payload)
  if marshalErr!=nil{status=400;apiError(w,status,"unable to encode request");return}
  upstreamPath:="/v1/messages"
  if !countTokens&&route.ProviderType==ProtocolOpenAIChat{upstreamPath="/v1/chat/completions"}
  if countTokens{upstreamPath="/v1/messages/count_tokens"}
  endpoint,urlErr:=providerEndpoint(route.BaseURL,route.ProviderType,upstreamPath)
  if urlErr!=nil{lastStatus=502;lastMessage="invalid upstream Provider URL";continue}
  if r.URL.RawQuery!=""{endpoint+="?"+r.URL.RawQuery}
  keys,keyErr:=a.store.EligibleProviderKeys(route.ProviderID)
  if errors.Is(keyErr,ErrNoUsableKeys){continue}
  if keyErr!=nil{status=502;apiError(w,status,"cannot read Provider API keys");return}
  for _,key:=range keys{
   if r.Context().Err()!=nil{return}
   hadEligible=true
   req,reqErr:=http.NewRequestWithContext(r.Context(),"POST",endpoint,bytes.NewReader(outgoing))
   if reqErr!=nil{status=500;apiError(w,status,"could not create upstream request");return}
   req.Header.Set("Content-Type","application/json")
   if accept:=r.Header.Get("Accept");accept!=""{req.Header.Set("Accept",accept)}
   req.Header.Set("Authorization","Bearer "+string(key.Secret))
   if route.ProviderType==ProtocolAnthropic{
    version:=r.Header.Get("anthropic-version")
    if version==""{version="2023-06-01"}
    req.Header.Set("anthropic-version",version)
    if v:=r.Header.Get("anthropic-beta");v!=""{req.Header.Set("anthropic-beta",v)}
   }
   req.Header.Set("User-Agent","AliasPort/0.4")
   upstream,callErr:=a.client.Do(req)
   if callErr!=nil{
    // A network error does not prove the upstream did not process the POST.
    status=502;apiError(w,status,"upstream transport failed; automatic retry suppressed to prevent duplicate charges");return
   }
   status=upstream.StatusCode
   target=route.Target
   a.store.MarkKeyUsed(route.ProviderID,key.ID)
   var inspected []byte
   if status==400||status==401||status==402||status==403||status==404||status==429||status==502||status==503||status==504{
    inspected,_=io.ReadAll(io.LimitReader(upstream.Body,131073))
   }
   if isBillingFailure(status,inspected){
    _=upstream.Body.Close()
    if e:=a.store.BlockBillingKey(route.ProviderID,key.ID);e!=nil{status=502;apiError(w,status,"could not mark exhausted API key");return}
    lastStatus=402
    lastMessage="upstream Provider billing credits exhausted; trying next key or Provider"
    continue
   }
   if status==429{
    cooldown:=60*time.Second
    if sec,e:=strconv.Atoi(strings.TrimSpace(upstream.Header.Get("Retry-After")));e==nil&&sec>0{
     cooldown=time.Duration(sec)*time.Second
    }
    _=upstream.Body.Close()
    if e:=a.store.MarkKeyRateLimited(route.ProviderID,key.ID,cooldown);e!=nil{status=502;apiError(w,status,"could not cooldown rate-limited key");return}
    lastStatus=429;lastMessage="all matching Provider keys are rate-limited or temporarily unavailable"
    continue
   }
   if status==401||status==403||status==404||status==502||status==503||status==504{
    _=upstream.Body.Close()
    lastStatus=status
    lastMessage="matching Provider rejected or could not complete the request; trying next configured key or Provider"
    continue
   }
   // Unknown errors (including HTTP 400/500) are not automatically replayed.
   // If the upstream already produced a successful stream, it cannot be retried.
   if route.ProviderType==inputType||countTokens||status<200||status>=300{
    forwardUpstream(w,upstream,inspected);return
   }
   if payload["stream"]==true{
    w.Header().Set("Content-Type","text/event-stream")
    w.Header().Set("Cache-Control","no-cache")
    w.Header().Set("X-Content-Type-Options","nosniff")
    w.WriteHeader(status)
    if route.ProviderType==ProtocolOpenAIChat&&inputType==ProtocolAnthropic{
     err=emitAnthropicFromChat(w,upstream.Body,incoming)
    }else if route.ProviderType==ProtocolAnthropic&&inputType==ProtocolOpenAIChat{
     err=emitChatFromAnthropic(w,upstream.Body,incoming)
    }else{err=fmt.Errorf("unsupported streaming conversion")}
    _=upstream.Body.Close()
    if err!=nil{
     // Once streaming starts, never send a second upstream request.
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
   if convertErr!=nil{status=502;apiError(w,status,"provider response incompatible with input protocol: "+convertErr.Error());return}
   w.Header().Set("Content-Type","application/json")
   w.Header().Set("X-Content-Type-Options","nosniff")
   w.WriteHeader(status)
   _,_=w.Write(result)
   return
  }
 }
 if countTokens&&!hadEligible{
  status=501;apiError(w,status,"token counting requires an available Anthropic-compatible Provider");return
 }
 if !hadEligible&&!hadConversion{
  status=503;apiError(w,status,"no eligible Provider API keys. Check Provider enabled flags, credits and cooldowns");return
 }
 status=lastStatus
 apiError(w,status,lastMessage)
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
