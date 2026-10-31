package main

import (
 "database/sql"
 "errors"
 "fmt"
 "strings"
 "net/url"
)

const (
 ProtocolAnthropic = "anthropic_messages"
 ProtocolOpenAIChat = "openai_chat_completions"
)

func validateProtocol(v string)error{
 switch v {
 case ProtocolAnthropic,ProtocolOpenAIChat:return nil
 default:return fmt.Errorf("unsupported API protocol %q; choose Anthropic Messages or OpenAI Chat Completions",v)
 }
}

func hasColumn(tx *sql.Tx,table,column string)(bool,error){
 rows,err:=tx.Query("PRAGMA table_info("+table+")")
 if err!=nil{return false,err}
 defer rows.Close()
 for rows.Next(){
  var id,notNull,pk int
  var name,dataType string
  var defaultValue sql.NullString
  if err=rows.Scan(&id,&name,&dataType,&notNull,&defaultValue,&pk);err!=nil{return false,err}
  if strings.EqualFold(name,column){return true,nil}
 }
 return false,rows.Err()
}

func(s *Store)migrateProtocols()error{
 tx,err:=s.db.Begin();if err!=nil{return err}
 defer tx.Rollback()
 operations:=[]struct{table,column,statement string}{
  {"providers","api_type","ALTER TABLE providers ADD COLUMN api_type TEXT NOT NULL DEFAULT 'anthropic_messages'"},
  {"gateways","port","ALTER TABLE gateways ADD COLUMN port INTEGER NOT NULL DEFAULT 18765"},
  {"gateways","input_type","ALTER TABLE gateways ADD COLUMN input_type TEXT NOT NULL DEFAULT 'anthropic_messages'"},
 }
 for _,operation:=range operations{
  exists,e:=hasColumn(tx,operation.table,operation.column)
  if e!=nil{return e}
  if !exists {
   if _,e=tx.Exec(operation.statement);e!=nil{return e}
  }
 }
 // Existing installs keep their active gateway on the old proxy port 18765.
 // Inactive profiles may intentionally share the port: only one is active at a time.
 return tx.Commit()
}

func providerEndpoint(baseURL,protocol,endpoint string)(string,error){
 if err:=validateProtocol(protocol);err!=nil{return "",err}
 if endpoint!="/v1/messages" && endpoint!="/v1/messages/count_tokens" &&
  endpoint!="/v1/chat/completions" && endpoint!="/v1/models" && endpoint!="/v1/balance" {
  return "",errors.New("unsupported upstream endpoint")
 }
 parsed,err:=url.Parse(baseURL)
 if err!=nil{return "",err}
 base:=strings.TrimRight(parsed.Path,"/")
 if strings.HasSuffix(base,"/v1"){base=strings.TrimSuffix(base,"/v1")}
 parsed.Path=base+endpoint
 parsed.RawPath=""
 parsed.RawQuery=""
 parsed.Fragment=""
 return parsed.String(),nil
}
