package main

import (
 "encoding/json"
 "errors"
 "fmt"
 "strings"
 "time"
)

var ErrUnsupportedFeature = errors.New("request uses an unsupported cross-protocol feature")

func object(value any) map[string]any {
 if result,ok:=value.(map[string]any);ok{return result}
 return map[string]any{}
}
func items(v any)[]any{if result,ok:=v.([]any);ok{return result};return nil}
func str(v any)string{if s,ok:=v.(string);ok{return s};return ""}
func count(v any)int{if n,ok:=v.(float64);ok{return int(n)};if n,ok:=v.(int);ok{return n};return 0}
func marsh(v any)string{b,_:=json.Marshal(v);return string(b)}
func parseObject(data []byte)(map[string]any,error){
 var v map[string]any
 if err:=json.Unmarshal(data,&v);err!=nil{return nil,err}
 if v==nil{return nil,errors.New("JSON object required")}
 return v,nil
}
func unsupportedCondition(m map[string]any,names ...string)error{
 for _,name:=range names{
  if _,ok:=m[name];ok{return fmt.Errorf("%w: %s",ErrUnsupportedFeature,name)}
 }
 return nil
}
func convertRequest(from,to string,input map[string]any)(map[string]any,error){
 if from==to{return input,nil}
 if from==ProtocolAnthropic && to==ProtocolOpenAIChat{return anthropicToChatRequest(input)}
 if from==ProtocolOpenAIChat && to==ProtocolAnthropic{return chatToAnthropicRequest(input)}
 return nil,errors.New("unsupported protocol conversion")
}
func systemText(v any)(string,error){
 if s,ok:=v.(string);ok{return s,nil}
 if v==nil{return "",nil}
 arr:=items(v)
 if arr==nil{return "",fmt.Errorf("%w: system blocks",ErrUnsupportedFeature)}
 var result []string
 for _,block:=range arr{
  b:=object(block)
  if str(b["type"])!="text" {return "",fmt.Errorf("%w: non-text system block",ErrUnsupportedFeature)}
  result=append(result,str(b["text"]))
 }
 return strings.Join(result,"\n"),nil
}
func anthropicContent(content any)(any,[]any,error) {
 if s,ok:=content.(string);ok{return s,nil,nil}
 blocks:=items(content)
 if blocks==nil{return "",nil,fmt.Errorf("%w: invalid Anthropic content",ErrUnsupportedFeature)}
 textParts:=[]any{}
 var tools []any
 for _,raw:=range blocks{
  b:=object(raw)
  switch str(b["type"]){
  case "text":
   textParts=append(textParts,map[string]any{"type":"text","text":str(b["text"])})
  case "image":
   source:=object(b["source"])
   u:=""
   switch str(source["type"]){
   case "base64":u="data:"+str(source["media_type"])+";base64,"+str(source["data"])
   case "url":u=str(source["url"])
   default:return nil,nil,fmt.Errorf("%w: image source",ErrUnsupportedFeature)
   }
   textParts=append(textParts,map[string]any{"type":"image_url","image_url":map[string]any{"url":u}})
  case "tool_use":
   tools=append(tools,map[string]any{"id":str(b["id"]),"type":"function","function":map[string]any{"name":str(b["name"]),"arguments":marsh(b["input"])}})
  default:return nil,nil,fmt.Errorf("%w: Anthropic content block %s",ErrUnsupportedFeature,str(b["type"]))
  }
 }
 if len(textParts)==0{return nil,tools,nil}
 if len(textParts)==1 && str(object(textParts[0])["type"])=="text" {return str(object(textParts[0])["text"]),tools,nil}
 return textParts,tools,nil
}
func anthropicToChatRequest(input map[string]any)(map[string]any,error){
 if err:=unsupportedCondition(input,"thinking","output_config","container","mcp_servers","context_management");err!=nil{return nil,err}
 out:=map[string]any{"model":input["model"],"stream":input["stream"]==true}
 if v,ok:=input["max_tokens"];ok{out["max_tokens"]=v}
 for _,key:=range []string{"temperature","top_p","stop_sequences"}{
  if v,ok:=input[key];ok{
   if key=="stop_sequences"{out["stop"]=v}else{out[key]=v}
  }
 }
 messages:=[]any{}
 if sys,err:=systemText(input["system"]);err!=nil{return nil,err}else if sys!=""{
  messages=append(messages,map[string]any{"role":"system","content":sys})
 }
 for _,entry:=range items(input["messages"]){
  m:=object(entry);role:=str(m["role"])
  if role!="user"&&role!="assistant"{return nil,fmt.Errorf("%w: Anthropic message role %s",ErrUnsupportedFeature,role)}
  content,tools,err:=anthropicContent(m["content"]);if err!=nil{return nil,err}
  // Tool results become independent OpenAI tool-role messages.
  if role=="user" {
   normal:=[]any{}
   if val,ok:=content.(string);ok&&val!=""{normal=append(normal,map[string]any{"type":"text","text":val})}
   if vals,ok:=content.([]any);ok{normal=append(normal,vals...)}
   var resultMessages []any
   if blocks,ok:=m["content"].([]any);ok{
    for _,entry:=range blocks{
     b:=object(entry)
     if str(b["type"])=="tool_result"{
      val,err:=systemText(b["content"])
      if err!=nil {val=marsh(b["content"])}
      resultMessages=append(resultMessages,map[string]any{"role":"tool","tool_call_id":str(b["tool_use_id"]),"content":val})
     }
    }
   }
   if len(normal)>0 {
    var textValue any=normal
    if len(normal)==1 && str(object(normal[0])["type"])=="text" {textValue=str(object(normal[0])["text"])}
    messages=append(messages,map[string]any{"role":"user","content":textValue})
   }
   messages=append(messages,resultMessages...)
  } else {
   msg:=map[string]any{"role":"assistant","content":content}
   if len(tools)>0{msg["tool_calls"]=tools}
   messages=append(messages,msg)
  }
 }
 out["messages"]=messages
 if len(messages)==0{return nil,errors.New("messages cannot be empty")}
 if rawTools:=items(input["tools"]);len(rawTools)>0{
  defs:=[]any{}
  for _,entry:=range rawTools{
   m:=object(entry)
   if m["type"]!=nil && str(m["type"])!="custom"{return nil,fmt.Errorf("%w: Anthropic hosted tool",ErrUnsupportedFeature)}
   defs=append(defs,map[string]any{"type":"function","function":map[string]any{
    "name":m["name"],"description":m["description"],"parameters":m["input_schema"],
   }})
  }
  out["tools"]=defs
 }
 if choice,ok:=input["tool_choice"];ok{
  m:=object(choice)
  switch str(m["type"]){
  case "auto":out["tool_choice"]="auto"
  case "any":out["tool_choice"]="required"
  case "none":out["tool_choice"]="none"
  case "tool":out["tool_choice"]=map[string]any{"type":"function","function":map[string]any{"name":m["name"]}}
  default:return nil,fmt.Errorf("%w: tool_choice",ErrUnsupportedFeature)
  }
 }
 if input["stream"]==true{out["stream_options"]=map[string]any{"include_usage":true}}
 return out,nil
}
func chatContent(content any)(any,error){
 if s,ok:=content.(string);ok{return s,nil}
 if content==nil{return "",nil}
 a:=items(content);if a==nil{return nil,fmt.Errorf("%w: chat content",ErrUnsupportedFeature)}
 blocks:=[]any{}
 for _,entry:=range a {
  b:=object(entry)
  switch str(b["type"]){
  case "text":
   blocks=append(blocks,map[string]any{"type":"text","text":str(b["text"])})
  case "image_url":
   u:=str(object(b["image_url"])["url"])
   if u==""{return nil,fmt.Errorf("%w: missing image URL",ErrUnsupportedFeature)}
   source:=map[string]any{"type":"url","url":u}
   if strings.HasPrefix(u,"data:"){
    parts:=strings.SplitN(strings.TrimPrefix(u,"data:"),";base64,",2)
    if len(parts)!=2{return nil,fmt.Errorf("%w: non-base64 data URL",ErrUnsupportedFeature)}
    source=map[string]any{"type":"base64","media_type":parts[0],"data":parts[1]}
   }
   blocks=append(blocks,map[string]any{"type":"image","source":source})
  default:return nil,fmt.Errorf("%w: chat content part %s",ErrUnsupportedFeature,str(b["type"]))
  }
 }
 return blocks,nil
}
func chatToAnthropicRequest(input map[string]any)(map[string]any,error){
 if err:=unsupportedCondition(input,"response_format","logprobs","top_logprobs","audio","modalities","prediction","reasoning_effort");err!=nil{return nil,err}
 out:=map[string]any{"model":input["model"],"stream":input["stream"]==true}
 max:=input["max_tokens"]
 if v,ok:=input["max_completion_tokens"];ok{max=v}
 if max==nil{max=4096}
 out["max_tokens"]=max
 for _,key:=range []string{"temperature","top_p"}{if v,ok:=input[key];ok{out[key]=v}}
 if v,ok:=input["stop"];ok{
  if s,ok:=v.(string);ok{out["stop_sequences"]=[]string{s}}else{out["stop_sequences"]=v}
 }
 var systems []string
 var messages []any
 for _,raw:=range items(input["messages"]){
  m:=object(raw);role:=str(m["role"])
  c,err:=chatContent(m["content"]);if err!=nil{return nil,err}
  switch role{
  case "system","developer":
   textValue,ok:=c.(string)
   if !ok{return nil,fmt.Errorf("%w: non-text system message",ErrUnsupportedFeature)}
   systems=append(systems,textValue)
  case "user":
   messages=append(messages,map[string]any{"role":"user","content":c})
  case "assistant":
   parts:=[]any{}
   if ss,ok:=c.(string);ok && ss!=""{parts=append(parts,map[string]any{"type":"text","text":ss})}
   if blocks,ok:=c.([]any);ok{parts=append(parts,blocks...)}
   for _,tool:=range items(m["tool_calls"]){
    tc:=object(tool);fn:=object(tc["function"])
    if str(tc["type"])!="function"{return nil,fmt.Errorf("%w: non-function tool calls",ErrUnsupportedFeature)}
    var args any
    if err=json.Unmarshal([]byte(str(fn["arguments"])),&args);err!=nil{return nil,fmt.Errorf("%w: invalid tool-call arguments",ErrUnsupportedFeature)}
    parts=append(parts,map[string]any{"type":"tool_use","id":tc["id"],"name":fn["name"],"input":args})
   }
   if len(parts)==0{parts=append(parts,map[string]any{"type":"text","text":""})}
   messages=append(messages,map[string]any{"role":"assistant","content":parts})
  case "tool":
   messages=append(messages,map[string]any{"role":"user","content":[]any{map[string]any{
    "type":"tool_result","tool_use_id":m["tool_call_id"],"content":c,
   }}})
  default:return nil,fmt.Errorf("%w: OpenAI role %s",ErrUnsupportedFeature,role)
  }
 }
 if len(systems)>0{out["system"]=strings.Join(systems,"\n\n")}
 if len(messages)==0{return nil,errors.New("messages cannot be empty")}
 out["messages"]=messages
 if tools:=items(input["tools"]);len(tools)>0{
  defs:=[]any{}
  for _,tool:=range tools{
   entry:=object(tool)
   if str(entry["type"])!="function"{return nil,fmt.Errorf("%w: non-function tool definition",ErrUnsupportedFeature)}
   fn:=object(entry["function"])
   schema:=fn["parameters"]
   if schema==nil{schema=map[string]any{"type":"object","properties":map[string]any{}}}
   defs=append(defs,map[string]any{"name":fn["name"],"description":fn["description"],"input_schema":schema})
  }
  out["tools"]=defs
 }
 if choice,ok:=input["tool_choice"];ok{
  switch value:=choice.(type){
  case string:
   switch value{
   case "auto":out["tool_choice"]=map[string]any{"type":"auto"}
   case "required":out["tool_choice"]=map[string]any{"type":"any"}
   case "none":delete(out,"tools")
   default:return nil,fmt.Errorf("%w: tool_choice %s",ErrUnsupportedFeature,value)
   }
  case map[string]any:
   if str(value["type"])!="function"{return nil,fmt.Errorf("%w: tool choice",ErrUnsupportedFeature)}
   out["tool_choice"]=map[string]any{"type":"tool","name":object(value["function"])["name"]}
  default:return nil,fmt.Errorf("%w: tool choice",ErrUnsupportedFeature)
  }
 }
 return out,nil
}
func mapStopToAnthropic(reason string)string{
 switch reason{case "length":return "max_tokens";case "tool_calls","function_call":return "tool_use";default:return "end_turn"}
}
func mapStopToOpenAI(reason string)string{
 switch reason{case "max_tokens":return "length";case "tool_use":return "tool_calls";default:return "stop"}
}
func convertResponse(from,to string,data []byte,target string)([]byte,error){
 if from==to{return data,nil}
 v,err:=parseObject(data);if err!=nil{return nil,err}
 if from==ProtocolOpenAIChat&&to==ProtocolAnthropic{
  choices:=items(v["choices"]);if len(choices)==0{return nil,errors.New("OpenAI response has no choices")}
  choice:=object(choices[0]);msg:=object(choice["message"])
  var blocks []any
  if text:=str(msg["content"]);text!=""{blocks=append(blocks,map[string]any{"type":"text","text":text})}
  for _,tool:=range items(msg["tool_calls"]){
   t:=object(tool);fn:=object(t["function"])
   var arguments any
   if err=json.Unmarshal([]byte(str(fn["arguments"])),&arguments);err!=nil{return nil,fmt.Errorf("invalid upstream tool arguments: %w",err)}
   blocks=append(blocks,map[string]any{"type":"tool_use","id":t["id"],"name":fn["name"],"input":arguments})
  }
  if blocks==nil{blocks=[]any{}}
  usage:=object(v["usage"])
  out:=map[string]any{"id":v["id"],"type":"message","role":"assistant","model":target,"content":blocks,"stop_reason":mapStopToAnthropic(str(choice["finish_reason"])),"stop_sequence":nil,
   "usage":map[string]any{"input_tokens":count(usage["prompt_tokens"]),"output_tokens":count(usage["completion_tokens"])}}
  return json.Marshal(out)
 }
 if from==ProtocolAnthropic&&to==ProtocolOpenAIChat{
  var segments []string
  var calls []any
  for _,raw:=range items(v["content"]){
   b:=object(raw)
   switch str(b["type"]){
   case "text":segments=append(segments,str(b["text"]))
   case "tool_use":calls=append(calls,map[string]any{"id":b["id"],"type":"function","function":map[string]any{"name":b["name"],"arguments":marsh(b["input"])}})
   default:return nil,fmt.Errorf("%w: response block %s",ErrUnsupportedFeature,str(b["type"]))
   }
  }
  msg:=map[string]any{"role":"assistant","content":strings.Join(segments,"")}
  if len(calls)>0{msg["tool_calls"]=calls}
  usage:=object(v["usage"])
  out:=map[string]any{"id":v["id"],"object":"chat.completion","created":time.Now().Unix(),"model":target,
   "choices":[]any{map[string]any{"index":0,"message":msg,"finish_reason":mapStopToOpenAI(str(v["stop_reason"]))}},
   "usage":map[string]any{"prompt_tokens":count(usage["input_tokens"]),"completion_tokens":count(usage["output_tokens"]),
    "total_tokens":count(usage["input_tokens"])+count(usage["output_tokens"])}}
  return json.Marshal(out)
 }
 return nil,errors.New("unsupported response conversion")
}
