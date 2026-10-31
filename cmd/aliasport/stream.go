package main

import (
 "bufio"
 "encoding/json"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "strings"
 "time"
)

func sendSSE(w http.ResponseWriter,event string,data any,openAI bool)error{
 payload,e:=json.Marshal(data);if e!=nil{return e}
 if !openAI&&event!=""{
  if _,e=fmt.Fprintf(w,"event: %s\n",event);e!=nil{return e}
 }
 if _,e=fmt.Fprintf(w,"data: %s\n\n",payload);e!=nil{return e}
 if f,ok:=w.(http.Flusher);ok{f.Flush()}
 return nil
}
func scanEvents(source io.Reader,receive func(event string,data []byte)error)error{
 scanner:=bufio.NewScanner(source)
 scanner.Buffer(make([]byte,64*1024),8*1024*1024)
 var event string
 var lines []string
 flush:=func()error{
  if len(lines)==0{event="";return nil}
  data:=[]byte(strings.Join(lines,"\n"))
  lines=nil;current:=event;event=""
  return receive(current,data)
 }
 for scanner.Scan(){
  line:=strings.TrimSuffix(scanner.Text(),"\r")
  if line==""{if err:=flush();err!=nil{return err};continue}
  if strings.HasPrefix(line,"event:"){event=strings.TrimSpace(strings.TrimPrefix(line,"event:"))}
  if strings.HasPrefix(line,"data:"){lines=append(lines,strings.TrimPrefix(strings.TrimPrefix(line,"data:")," "))}
 }
 if err:=scanner.Err();err!=nil{return err}
 return flush()
}

func emitAnthropicFromChat(w http.ResponseWriter,source io.Reader,target string)error{
 id:="msg_aliasport_"+newID()
 started:=false
 textStarted:=false
 stopReason:="end_turn"
 promptTokens,completionTokens:=0,0
 type pendingTool struct{id,name,args string}
 toolCalls:=map[int]*pendingTool{}
 order:=[]int{}
 start:=func()error{
  if started{return nil}
  started=true
  return sendSSE(w,"message_start",map[string]any{"type":"message_start","message":map[string]any{
   "id":id,"type":"message","role":"assistant","model":target,"content":[]any{},
   "stop_reason":nil,"stop_sequence":nil,
   "usage":map[string]int{"input_tokens":promptTokens,"output_tokens":0},
  }},false)
 }
 err:=scanEvents(source,func(_ string,data []byte)error{
  if string(data)=="[DONE]"{return nil}
  m,err:=parseObject(data);if err!=nil{return err}
  if val:=str(m["id"]);val!=""&&!started{id=val}
  if usage:=object(m["usage"]);len(usage)>0{
   promptTokens=count(usage["prompt_tokens"]);completionTokens=count(usage["completion_tokens"])
  }
  if err=start();err!=nil{return err}
  for _,raw:=range items(m["choices"]){
   choice:=object(raw)
   if count(choice["index"])!=0{continue}
   delta:=object(choice["delta"])
   if part:=str(delta["content"]);part!=""{
    if !textStarted{
     textStarted=true
     if err=sendSSE(w,"content_block_start",map[string]any{"type":"content_block_start","index":0,
      "content_block":map[string]any{"type":"text","text":""}},false);err!=nil{return err}
    }
    if err=sendSSE(w,"content_block_delta",map[string]any{"type":"content_block_delta","index":0,
      "delta":map[string]any{"type":"text_delta","text":part}},false);err!=nil{return err}
   }
   for _,rawTool:=range items(delta["tool_calls"]){
    tool:=object(rawTool)
    i:=count(tool["index"])
    pending,exists:=toolCalls[i]
    if !exists{
     pending=&pendingTool{}
     toolCalls[i]=pending
     order=append(order,i)
    }
    if value:=str(tool["id"]);value!=""{pending.id=value}
    fn:=object(tool["function"])
    if name:=str(fn["name"]);name!=""{pending.name=name}
    pending.args+=str(fn["arguments"])
   }
   if reason:=str(choice["finish_reason"]);reason!=""{stopReason=mapStopToAnthropic(reason)}
  }
  return nil
 })
 if err!=nil{return err}
 if err=start();err!=nil{return err}
 if textStarted{
  if err=sendSSE(w,"content_block_stop",map[string]any{"type":"content_block_stop","index":0},false);err!=nil{return err}
 }
 // OpenAI Chat chunks can interleave arguments for several tools. Buffer their
 // JSON fragments so that Anthropic blocks remain strictly sequential.
 for i,sourceIndex:=range order{
  tool:=toolCalls[sourceIndex]
  index:=i
  if textStarted{index++}
  if tool.id==""{tool.id="toolu_aliasport_"+newID()}
  if tool.name==""{return fmt.Errorf("OpenAI tool call has no function name")}
  if err=sendSSE(w,"content_block_start",map[string]any{"type":"content_block_start","index":index,
   "content_block":map[string]any{"type":"tool_use","id":tool.id,"name":tool.name,"input":map[string]any{}}},false);err!=nil{return err}
  arguments:=tool.args
  if arguments==""{arguments="{}"}
  if !json.Valid([]byte(arguments)){return fmt.Errorf("OpenAI tool call returned invalid JSON arguments")}
  if err=sendSSE(w,"content_block_delta",map[string]any{"type":"content_block_delta","index":index,
   "delta":map[string]any{"type":"input_json_delta","partial_json":arguments}},false);err!=nil{return err}
  if err=sendSSE(w,"content_block_stop",map[string]any{"type":"content_block_stop","index":index},false);err!=nil{return err}
 }
 if len(order)>0{stopReason="tool_use"}
 if err=sendSSE(w,"message_delta",map[string]any{"type":"message_delta",
  "delta":map[string]any{"stop_reason":stopReason,"stop_sequence":nil},
  "usage":map[string]any{"output_tokens":completionTokens}},false);err!=nil{return err}
 return sendSSE(w,"message_stop",map[string]any{"type":"message_stop"},false)
}

func emitChatFromAnthropic(w http.ResponseWriter,source io.Reader,target string)error{
 id:="chatcmpl_aliasport_"+newID()
 started:=false
 reason:="stop"
 lastUsage:=map[string]any{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}
 toolIndex:=map[int]int{}
 toolNext:=0
 sendChunk:=func(delta map[string]any,finish any)error{
  return sendSSE(w,"",map[string]any{
   "id":id,"object":"chat.completion.chunk","created":time.Now().Unix(),"model":target,
   "choices":[]any{map[string]any{"index":0,"delta":delta,"finish_reason":finish}},
  },true)
 }
 init:=func()error{
  if started{return nil};started=true
  return sendChunk(map[string]any{"role":"assistant"},nil)
 }
 err:=scanEvents(source,func(event string,data []byte)error{
  if string(data)=="[DONE]"{return nil}
  m,err:=parseObject(data);if err!=nil{return err}
  if event==""{event=str(m["type"])}
  switch event{
  case "message_start":
   if v:=str(object(m["message"])["id"]);v!=""{id=v}
   us:=object(object(m["message"])["usage"])
   lastUsage["prompt_tokens"]=count(us["input_tokens"])
   return init()
  case "content_block_start":
   if err=init();err!=nil{return err}
   block:=object(m["content_block"])
   if str(block["type"])=="tool_use"{
    idx:=count(m["index"])
    callIndex:=toolNext;toolNext++;toolIndex[idx]=callIndex
    return sendChunk(map[string]any{"tool_calls":[]any{map[string]any{"index":callIndex,"id":block["id"],"type":"function",
     "function":map[string]any{"name":block["name"],"arguments":""}}}},nil)
   }
  case "content_block_delta":
   if err=init();err!=nil{return err}
   delta:=object(m["delta"])
   if str(delta["type"])=="text_delta"{return sendChunk(map[string]any{"content":str(delta["text"])},nil)}
   if str(delta["type"])=="input_json_delta"{
    i,ok:=toolIndex[count(m["index"])]
    if !ok{return fmt.Errorf("tool delta before tool start")}
    return sendChunk(map[string]any{"tool_calls":[]any{map[string]any{"index":i,
     "function":map[string]any{"arguments":str(delta["partial_json"])}}}},nil)
   }
   if str(delta["type"])!="signature_delta" {return fmt.Errorf("unsupported Anthropic SSE delta type %s",str(delta["type"]))}
  case "message_delta":
   reason=mapStopToOpenAI(str(object(m["delta"])["stop_reason"]))
   usage:=object(m["usage"])
   lastUsage["completion_tokens"]=count(usage["output_tokens"])
   lastUsage["total_tokens"]=count(lastUsage["prompt_tokens"])+count(lastUsage["completion_tokens"])
  case "error":
   return fmt.Errorf("upstream Anthropic SSE error: %s",string(data))
  }
  return nil
 })
 if err!=nil{return err}
 if err=init();err!=nil{return err}
 if err=sendChunk(map[string]any{},reason);err!=nil{return err}
 if err=sendSSE(w,"",map[string]any{"id":id,"object":"chat.completion.chunk","created":time.Now().Unix(),"model":target,
  "choices":[]any{},"usage":lastUsage},true);err!=nil{return err}
 if _,err=io.WriteString(w,"data: [DONE]\n\n");err!=nil{return err}
 if f,ok:=w.(http.Flusher);ok{f.Flush()}
 return nil
}
