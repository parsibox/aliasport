package main

import (
 "bytes"
 "encoding/json"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestAnthropicToOpenAIChatConversion(t *testing.T){
 src:=map[string]any{
  "model":"chat-target","max_tokens":float64(100),"stream":false,
  "system":"You are a code assistant",
  "messages":[]any{
   map[string]any{"role":"user","content":[]any{map[string]any{"type":"text","text":"run function"}}},
   map[string]any{"role":"assistant","content":[]any{map[string]any{
    "type":"tool_use","id":"tool-1","name":"calculate","input":map[string]any{"x":float64(2)},
   }}},
   map[string]any{"role":"user","content":[]any{map[string]any{
    "type":"tool_result","tool_use_id":"tool-1","content":"4",
   }}},
  },
  "tools":[]any{map[string]any{"name":"calculate","description":"calculator",
   "input_schema":map[string]any{"type":"object","properties":map[string]any{}}}},
 }
 converted,err:=convertRequest(ProtocolAnthropic,ProtocolOpenAIChat,src)
 if err!=nil{t.Fatal(err)}
 msgs:=items(converted["messages"])
 if len(msgs)!=4{t.Fatalf("expected system+user+assistant+tool, got %d: %+v",len(msgs),msgs)}
 if str(object(msgs[0])["role"])!="system"{t.Fatal("system not converted")}
 toolMsg:=object(msgs[3])
 if str(toolMsg["role"])!="tool" || str(toolMsg["tool_call_id"])!="tool-1"{t.Errorf("tool result was lost: %+v",toolMsg)}
 defs:=items(converted["tools"])
 if len(defs)!=1||str(object(defs[0])["type"])!="function"{t.Fatal("tool definition was lost")}
 if converted["model"]!="chat-target"{t.Fatal("model changed in conversion")}
}

func TestOpenAIChatToAnthropicConversion(t *testing.T){
 src:=map[string]any{
  "model":"anthropic-target","max_tokens":float64(256),"stream":false,
  "messages":[]any{
   map[string]any{"role":"developer","content":"Be precise"},
   map[string]any{"role":"user","content":"hello"},
   map[string]any{"role":"assistant","content":nil,"tool_calls":[]any{map[string]any{
    "id":"call_5","type":"function","function":map[string]any{"name":"lookup","arguments":`{"id":5}`},
   }}},
   map[string]any{"role":"tool","tool_call_id":"call_5","content":"found"},
  },
  "tools":[]any{map[string]any{"type":"function","function":map[string]any{
   "name":"lookup","description":"Find item","parameters":map[string]any{"type":"object","properties":map[string]any{}},
  }}},
 }
 got,err:=convertRequest(ProtocolOpenAIChat,ProtocolAnthropic,src)
 if err!=nil{t.Fatal(err)}
 if str(got["system"])!="Be precise"{t.Fatal("developer message was not mapped to system")}
 messages:=items(got["messages"])
 if len(messages)!=3{t.Fatalf("expected 3 messages: %+v",messages)}
 assistantBlocks:=items(object(messages[1])["content"])
 if len(assistantBlocks)!=1||str(object(assistantBlocks[0])["type"])!="tool_use"{t.Fatal("assistant function call missing")}
 resultBlocks:=items(object(messages[2])["content"])
 if len(resultBlocks)!=1||str(object(resultBlocks[0])["type"])!="tool_result"{t.Fatal("tool result missing")}
}

func TestProtocolResponseConversion(t *testing.T){
 chat:=[]byte(`{"id":"chatcmpl_1","choices":[{"index":0,"message":{"role":"assistant","content":"text","tool_calls":[{"id":"call1","type":"function","function":{"name":"ping","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
 out,err:=convertResponse(ProtocolOpenAIChat,ProtocolAnthropic,chat,"claude-sonnet-5")
 if err!=nil{t.Fatal(err)}
 v,err:=parseObject(out);if err!=nil{t.Fatal(err)}
 if v["stop_reason"]!="tool_use"{t.Fatal("chat tool finish reason not converted")}
 blocks:=items(v["content"])
 if len(blocks)!=2||str(object(blocks[1])["type"])!="tool_use"{t.Fatalf("tool use block missing %+v",blocks)}
 if str(v["model"])!="claude-sonnet-5"{t.Fatal("original gateway model not preserved")}

 anthropic:=[]byte(`{"id":"msg_1","type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":3,"output_tokens":2}}`)
 chatResult,err:=convertResponse(ProtocolAnthropic,ProtocolOpenAIChat,anthropic,"gpt-4o")
 if err!=nil{t.Fatal(err)}
 v,err=parseObject(chatResult);if err!=nil{t.Fatal(err)}
 if v["object"]!="chat.completion"{t.Fatal("invalid OpenAI response type")}
 if str(object(object(items(v["choices"])[0])["message"])["content"])!="hello"{t.Fatal("content missing")}
}

func TestStreamingOpenAIToAnthropic(t *testing.T){
 source:=strings.Join([]string{
  `data: {"id":"chatcmpl_t","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,"",
  `data: {"id":"chatcmpl_t","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,"",
  `data: {"id":"chatcmpl_t","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"lookup","arguments":""}}]},"finish_reason":null}]}`,"",
  `data: {"id":"chatcmpl_t","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,"",
  `data: {"id":"chatcmpl_t","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,"",
  "data: [DONE]","",
 },"\n")
 rec:=httptest.NewRecorder()
 if err:=emitAnthropicFromChat(rec,strings.NewReader(source),"claude-sonnet");err!=nil{t.Fatal(err)}
 body:=rec.Body.String()
 for _,needed:=range []string{"event: message_start","event: content_block_start","event: content_block_delta","input_json_delta","event: message_delta","event: message_stop","tool_use"}{
  if !strings.Contains(body,needed){t.Fatalf("missing %s in SSE:\n%s",needed,body)}
 }
}
func TestStreamingAnthropicToOpenAI(t *testing.T){
 source:=strings.Join([]string{
  `event: message_start`,`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":7}}}`,"",
  `event: content_block_start`,`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,"",
  `event: content_block_delta`,`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi!"}}`,"",
  `event: message_delta`,`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,"",
  `event: message_stop`,`data: {"type":"message_stop"}`,"",
 },"\n")
 rec:=httptest.NewRecorder()
 if err:=emitChatFromAnthropic(rec,strings.NewReader(source),"gpt-4o");err!=nil{t.Fatal(err)}
 if !strings.Contains(rec.Body.String(),`"content":"Hi!"`){t.Fatalf("missing response delta %s",rec.Body.String())}
 if !strings.Contains(rec.Body.String(),"data: [DONE]"){t.Fatal("OpenAI stream does not terminate")}
}

func TestCrossProtocolProxyOpenAIProvider(t *testing.T){
 upstream:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/v1/chat/completions"{t.Errorf("wrong provider path %s",r.URL.Path)}
  payload,_:=io.ReadAll(r.Body)
  var v map[string]any
  if err:=json.Unmarshal(payload,&v);err!=nil{t.Error(err)}
  if v["model"]!="gpt-fixture"{t.Errorf("model not mapped: %v",v["model"])}
  if len(items(v["messages"]))<1{t.Error("OpenAI Chat messages missing")}
  w.Header().Set("Content-Type","application/json")
  _,_=w.Write([]byte(`{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from chat"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":4}}`))
 }))
 defer upstream.Close()
 s:=testStore(t)
 p,err:=s.SaveProvider(Provider{Name:"OpenAI fixture",BaseURL:upstream.URL+"/v1",APIType:ProtocolOpenAIChat,APIKey:"provider-key"})
 if err!=nil{t.Fatal(err)}
 g,err:=s.SaveGateway(Gateway{Name:"Claude",InputType:ProtocolAnthropic,Port:18766})
 if err!=nil{t.Fatal(err)}
 if err=s.ActivateGateway(g.ID);err!=nil{t.Fatal(err)}
 _,err=s.SaveAlias(Alias{GatewayID:g.ID,ProviderID:p.ID,Incoming:"claude-sonnet-5",Target:"gpt-fixture",Enabled:true})
 if err!=nil{t.Fatal(err)}
 app:=&App{store:s,client:upstream.Client()}
 req:=httptest.NewRequest("POST","/v1/messages",bytes.NewReader([]byte(`{"model":"claude-sonnet-5","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`)))
 req.Header.Set("Authorization","Bearer "+g.ClientKey)
 rec:=httptest.NewRecorder()
 app.messages(rec,req)
 if rec.Code!=200{t.Fatalf("unexpected HTTP status %d %s",rec.Code,rec.Body.String())}
 body,err:=parseObject(rec.Body.Bytes())
 if err!=nil{t.Fatal(err)}
 if body["type"]!="message" || body["model"]!="claude-sonnet-5"{t.Fatalf("not Anthropic response: %+v",body)}
 if str(object(items(body["content"])[0])["text"])!="Hello from chat"{t.Fatal("response not converted")}
}
