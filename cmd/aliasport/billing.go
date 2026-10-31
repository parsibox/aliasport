package main

import (
 "encoding/json"
 "strings"
)

// Only a recognized upstream billing failure may trigger key failover. Ordinary
// rate limits, invalid credentials, permissions, transport failures and 5xx do not.
func isBillingFailure(status int, body []byte) bool {
 if status==402 {return true}
 if status!=400 && status!=403 && status!=429 {return false}
 if len(body)==0 || len(body)>131072 {return false}
 var value map[string]json.RawMessage
 if json.Unmarshal(body,&value)!=nil {return false}
 fields:=[]string{}
 for _,name:=range []string{"code","type","message"}{
  var v string
  if raw:=value[name];len(raw)>0 && json.Unmarshal(raw,&v)==nil {fields=append(fields,v)}
 }
 if raw:=value["error"];len(raw)>0 {
  var nested map[string]json.RawMessage
  if json.Unmarshal(raw,&nested)==nil{
   for _,name:=range []string{"code","type","message"} {
    var v string
    if b:=nested[name];len(b)>0 && json.Unmarshal(b,&v)==nil {fields=append(fields,v)}
   }
  }
 }
 for _,f:=range fields {
  normalized:=strings.ToLower(strings.TrimSpace(f))
  normalized=strings.ReplaceAll(normalized,"-","_")
  switch normalized {
  case "billing_issue","billing_error","billing_not_active","billing_inactive",
   "payment_required","insufficient_quota","insufficient_credits",
   "insufficient_balance","insufficient_funds","credit_balance_exhausted",
   "credit_balance_too_low","credits_exhausted","billing_hard_limit_reached":
   return true
  }
  for _,phrase:=range []string{
   "billing issue","insufficient credits","insufficient balance",
   "insufficient funds","out of credits","payment required",
   "credit balance is too low","credit balance exhausted",
   "no credits remaining","billing account inactive","billing is not active",
   "purchase credits",
  }{
   if strings.Contains(normalized,phrase){return true}
  }
 }
 return false
}
