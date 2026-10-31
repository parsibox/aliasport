package main

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "strings"
 "time"
)

// Provider diagnostics use the first ready upstream credential and never send
// a test completion or consume paid inference tokens.
func (a *App) providerDiagnostic(w http.ResponseWriter,r *http.Request,providerID,kind string){
 if r.Method!="GET"{method(w,r,"GET");return}
 provider,err:=a.store.ProviderByID(providerID)
 if errors.Is(err,ErrNotFound){apiError(w,404,"provider not found");return}
 if err!=nil{apiError(w,500,"provider unavailable");return}
 keys,err:=a.store.EligibleProviderKeys(provider.ID)
 if errors.Is(err,ErrNoUsableKeys){apiError(w,503,"no usable API keys for this provider");return}
 if err!=nil{apiError(w,500,"cannot access provider credentials");return}
 endpoint:="/v1/models"
 if kind=="balance"{endpoint="/v1/balance"}else if kind!="models"{
  apiError(w,404,"unknown provider diagnostic");return
 }
 address,err:=providerEndpoint(provider.BaseURL,provider.APIType,endpoint)
 if err!=nil{apiError(w,400,"invalid provider URL");return}
 ctx,cancel:=context.WithTimeout(r.Context(),15*time.Second)
 defer cancel()
 request,err:=http.NewRequestWithContext(ctx,"GET",address,nil)
 if err!=nil{apiError(w,500,"cannot create diagnostic request");return}
 request.Header.Set("Authorization","Bearer "+string(keys[0].Secret))
 request.Header.Set("Accept","application/json")
 if provider.APIType==ProtocolAnthropic{request.Header.Set("anthropic-version","2023-06-01")}
 request.Header.Set("User-Agent","AliasPort/0.3")
 response,err:=a.client.Do(request)
 if err!=nil{apiError(w,502,"provider connection failed or timed out");return}
 defer response.Body.Close()
 raw,err:=io.ReadAll(io.LimitReader(response.Body,1<<20))
 if err!=nil{apiError(w,502,"could not read provider response");return}
 if response.StatusCode<200||response.StatusCode>=300 {
  if response.StatusCode==404 {
   apiError(w,501,"provider does not expose "+endpoint);return
  }
  // Never reflect untrusted upstream payloads into the local browser.
  apiError(w,502,"provider returned HTTP "+response.Status);return
 }
 var parsed map[string]any
 if err=json.Unmarshal(raw,&parsed);err!=nil{apiError(w,502,"provider returned invalid JSON");return}
 if kind=="balance"{
  writeJSON(w,200,map[string]any{"provider":provider.Name,"balance":parsed})
  return
 }
 candidates:=items(parsed["data"])
 if candidates==nil{candidates=items(parsed["models"])}
 models:=[]map[string]string{}
 for _,item:=range candidates {
  model:=object(item)
  id:=strings.TrimSpace(str(model["id"]))
  if id==""{id=str(model["name"])}
  if id==""{continue}
  label:=str(model["display_name"])
  if label==""{label=str(model["name"])}
  if label==""{label=id}
  models=append(models,map[string]string{"id":id,"name":label})
  if len(models)>=1000{break}
 }
 writeJSON(w,200,map[string]any{"provider":provider.Name,"count":len(models),"models":models})
}
