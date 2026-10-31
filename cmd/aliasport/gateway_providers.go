package main

import (
 "database/sql"
 "errors"
 "fmt"
)

// Legacy rows had a single active Gateway and per-Gateway aliases.
// Keep their Provider assignments on first upgrade, without restoring links
// deleted by the user on later launches.
func(s *Store)migrateGatewayProviders()error{
 tx,err:=s.db.Begin();if err!=nil{return err}
 defer tx.Rollback()
 var version int
 if err=tx.QueryRow("PRAGMA user_version").Scan(&version);err!=nil{return err}
 if version<2{
  if _,err=tx.Exec(`INSERT OR IGNORE INTO gateway_providers(gateway_id,provider_id)
    SELECT DISTINCT gateway_id,provider_id FROM aliases`);err!=nil{return err}
  if _,err=tx.Exec("PRAGMA user_version=2");err!=nil{return err}
 }
 if _,err=tx.Exec("DROP INDEX IF EXISTS one_active_gateway");err!=nil{return err}
 return tx.Commit()
}
func(s *Store)ProviderGatewayIDs(providerID string)([]string,error){
 rows,err:=s.db.Query("SELECT gateway_id FROM gateway_providers WHERE provider_id=? ORDER BY gateway_id",providerID)
 if err!=nil{return nil,err}
 defer rows.Close()
 ids:=[]string{}
 for rows.Next(){
  var id string
  if err=rows.Scan(&id);err!=nil{return nil,err}
  ids=append(ids,id)
 }
 return ids,rows.Err()
}
// ValidateProviderGatewayIDs runs before saving Provider details so a wrong
// Gateway selection cannot partially update the Provider's public settings.
func(s *Store)ValidateProviderGatewayIDs(ids []string)error{
 seen:=make(map[string]bool,len(ids))
 for _,id:=range ids{
  if len(id)!=24{return fmt.Errorf("invalid Gateway ID %q",id)}
  if seen[id]{return fmt.Errorf("duplicate Gateway ID %q",id)}
  seen[id]=true
  var found int
  err:=s.db.QueryRow("SELECT 1 FROM gateways WHERE id=?",id).Scan(&found)
  if errors.Is(err,sql.ErrNoRows){return fmt.Errorf("Gateway does not exist: %s",id)}
  if err!=nil{return err}
 }
 return nil
}
func(s *Store)SetProviderGateways(providerID string,ids []string)error{
 tx,err:=s.db.Begin();if err!=nil{return err}
 defer tx.Rollback()
 var found int
 if err=tx.QueryRow("SELECT 1 FROM providers WHERE id=?",providerID).Scan(&found);errors.Is(err,sql.ErrNoRows){return ErrNotFound}else if err!=nil{return err}
 seen:=make(map[string]bool,len(ids))
 for _,id:=range ids{
  if len(id)!=24{return fmt.Errorf("invalid Gateway ID %q",id)}
  if seen[id]{return fmt.Errorf("duplicate Gateway ID %q",id)}
  seen[id]=true
  if err=tx.QueryRow("SELECT 1 FROM gateways WHERE id=?",id).Scan(&found);errors.Is(err,sql.ErrNoRows){return fmt.Errorf("Gateway does not exist: %s",id)}else if err!=nil{return err}
 }
 if _,err=tx.Exec("DELETE FROM gateway_providers WHERE provider_id=?",providerID);err!=nil{return err}
 for _,id:=range ids{
  if _,err=tx.Exec("INSERT INTO gateway_providers(gateway_id,provider_id) VALUES(?,?)",id,providerID);err!=nil{return err}
 }
 return tx.Commit()
}
func(s *Store)ActiveGateways()([]Gateway,error){
 rows,err:=s.db.Query(`SELECT id,name,active,port,input_type,default_provider_id
 FROM gateways WHERE active=1 ORDER BY rowid`)
 if err!=nil{return nil,err}
 defer rows.Close()
 result:=[]Gateway{}
 for rows.Next(){
  var g Gateway
  if err=rows.Scan(&g.ID,&g.Name,&g.Active,&g.Port,&g.InputType,&g.DefaultProviderID);err!=nil{return nil,err}
  result=append(result,g)
 }
 return result,rows.Err()
}
func(s *Store)GatewayAuthHash(id string)(string,error){
 var hash string
 err:=s.db.QueryRow("SELECT client_hash FROM gateways WHERE id=? AND active=1",id).Scan(&hash)
 if errors.Is(err,sql.ErrNoRows){return "",ErrNotFound}
 return hash,err
}
