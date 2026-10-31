package main

import (
 "crypto/rand"
 "crypto/sha256"
 "database/sql"
 "encoding/hex"
 "errors"
 "fmt"
 "net"
 "net/url"
 "strings"
 "time"

 _ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

type Provider struct {
 ID string `json:"id"`
 Name string `json:"name"`
 BaseURL string `json:"base_url"`
 APIType string `json:"api_type"`
 Priority int `json:"priority"`
 Enabled *bool `json:"enabled,omitempty"`
 GatewayIDs []string `json:"gateway_ids,omitempty"`
 HasKey bool `json:"has_key"`
 APIKey string `json:"api_key,omitempty"` // accepted on write, never returned
}
type Gateway struct{
 ID string `json:"id"`
 Name string `json:"name"`
 Active bool `json:"active"`
 Port int `json:"port"`
 InputType string `json:"input_type"`
 DefaultProviderID string `json:"default_provider_id"`
 ClientKey string `json:"client_key,omitempty"` // returned ONLY on create/rotate
}
type Alias struct{
 ID string `json:"id"`
 GatewayID string `json:"gateway_id"`
 ProviderID string `json:"provider_id"`
 Incoming string `json:"incoming"`
 Target string `json:"target"`
 Enabled bool `json:"enabled"`
}
type Event struct{
 ID int64 `json:"id"`
 When string `json:"when"`
 Incoming string `json:"incoming"`
 Target string `json:"target"`
 Status int `json:"status"`
 DurationMS int64 `json:"duration_ms"`
}
type Route struct {
 ClientHash string
 Target string
 BaseURL string
 ProviderID string
 ProviderType string
}
var ErrNotFound=errors.New("not found")

func OpenStore(path string)(*Store,error){
 db,err:=sql.Open("sqlite",path);if err!=nil{return nil,err}
 db.SetMaxOpenConns(1)
 for _,statement:=range []string{
  "PRAGMA journal_mode=WAL",
  "PRAGMA foreign_keys=ON",
  "PRAGMA busy_timeout=5000",
  `CREATE TABLE IF NOT EXISTS providers(
    id TEXT PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL,
    secret BLOB NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
  `CREATE TABLE IF NOT EXISTS provider_keys(
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    secret BLOB NOT NULL,
    fingerprint TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'ready' CHECK(status IN ('ready','billing_blocked')),
    last_billing_at TEXT,
    last_used_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
  `CREATE INDEX IF NOT EXISTS idx_provider_keys_selection ON provider_keys(provider_id,enabled,status,priority)`,
  `CREATE TABLE IF NOT EXISTS gateways(
    id TEXT PRIMARY KEY, name TEXT NOT NULL, client_hash TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
  `CREATE TABLE IF NOT EXISTS gateway_providers(
    gateway_id TEXT NOT NULL REFERENCES gateways(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    PRIMARY KEY(gateway_id,provider_id))`,
  `CREATE TABLE IF NOT EXISTS aliases(
    id TEXT PRIMARY KEY, gateway_id TEXT NOT NULL REFERENCES gateways(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
    incoming TEXT NOT NULL, target TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
    UNIQUE(gateway_id,provider_id,incoming))`,
  `CREATE TABLE IF NOT EXISTS events(
    id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL,
    incoming TEXT NOT NULL, target TEXT NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL)`,
  `CREATE INDEX IF NOT EXISTS idx_events_recent ON events(id DESC)`,
 }{
  if _,err=db.Exec(statement);err!=nil {db.Close();return nil,fmt.Errorf("sqlite init: %w",err)}
 }
 store:=&Store{db}
 if err=store.migrateProtocols();err!=nil{db.Close();return nil,fmt.Errorf("protocol migration: %w",err)}
 if err=store.migrateProviderScopedAliases();err!=nil{db.Close();return nil,fmt.Errorf("provider-scoped aliases migration: %w",err)}
 if err=store.migrateProviderKeys();err!=nil{db.Close();return nil,fmt.Errorf("provider key migration: %w",err)}
 if err=store.migrateGatewayProviders();err!=nil{db.Close();return nil,fmt.Errorf("gateway/provider migration: %w",err)}
 return store,nil
}
func(s *Store)Close()error{return s.db.Close()}

func newID()string{b:=make([]byte,12);if _,e:=rand.Read(b);e!=nil{panic(e)};return hex.EncodeToString(b)}
func newClientKey()string{b:=make([]byte,32);if _,e:=rand.Read(b);e!=nil{panic(e)};return "ap_"+hex.EncodeToString(b)}
func hashKey(k string)string{sum:=sha256.Sum256([]byte(k));return hex.EncodeToString(sum[:])}

func validateProvider(p Provider)error{
 if len(strings.TrimSpace(p.Name))==0||len(p.Name)>120{return errors.New("provider name must be 1–120 characters")}
 u,err:=url.Parse(strings.TrimSpace(p.BaseURL))
 if err!=nil||u.Hostname()==""||u.User!=nil||u.RawQuery!=""||u.Fragment!="" {return errors.New("invalid provider base URL")}
 if u.Scheme!="https" {
  host:=net.ParseIP(u.Hostname())
  if u.Scheme!="http" || host==nil || !host.IsLoopback(){return errors.New("provider URL must use HTTPS (HTTP allowed only for loopback IPs)")}
 }
 if len(p.BaseURL)>1024{return errors.New("provider base URL is too long")}
 return nil
}
func(s *Store)Providers()([]Provider,error){
 rows,e:=s.db.Query("SELECT p.id,p.name,p.base_url,p.api_type,p.priority,p.enabled,EXISTS(SELECT 1 FROM provider_keys k WHERE k.provider_id=p.id) FROM providers p ORDER BY p.priority,p.rowid");if e!=nil{return nil,e};defer rows.Close()
 result:=[]Provider{}
 for rows.Next(){var p Provider;if e=rows.Scan(&p.ID,&p.Name,&p.BaseURL,&p.APIType,&p.Priority,&p.Enabled,&p.HasKey);e!=nil{return nil,e};result=append(result,p)}
 if err:=rows.Err();err!=nil{return nil,err}
 if err:=rows.Close();err!=nil{return nil,err}
 for i:=range result{
  ids,e:=s.ProviderGatewayIDs(result[i].ID);if e!=nil{return nil,e}
  result[i].GatewayIDs=ids
 }
 return result,nil
}
func(s *Store)SaveProvider(p Provider)(Provider,error){
 p.Name=strings.TrimSpace(p.Name)
 p.BaseURL=strings.TrimRight(strings.TrimSpace(p.BaseURL),"/")
 if p.APIType==""{p.APIType=ProtocolAnthropic}
 if e:=validateProtocol(p.APIType);e!=nil{return Provider{},e}
 if e:=validateProvider(p);e!=nil{return Provider{},e}
 if p.Priority<0||p.Priority>1000000{return Provider{},errors.New("provider priority must be 0–1000000")}
 if p.Enabled==nil{t:=true;p.Enabled=&t}
 if p.ID==""{
  if p.APIKey==""{return Provider{},errors.New("provider API key is required")}
  cipher,e:=sealSecret([]byte(p.APIKey));if e!=nil{return Provider{},e}
  p.ID=newID()
  tx,e:=s.db.Begin();if e!=nil{return Provider{},e}
  defer tx.Rollback()
  if _,e=tx.Exec("INSERT INTO providers(id,name,base_url,secret,api_type,priority,enabled) VALUES(?,?,?,X'',?,?,?)",
   p.ID,p.Name,p.BaseURL,p.APIType,p.Priority,*p.Enabled);e!=nil{return Provider{},e}
  if _,e=tx.Exec(`INSERT INTO provider_keys(id,provider_id,label,secret,fingerprint,priority,enabled,status)
   VALUES(?,?,?,?,?,0,1,'ready')`,newID(),p.ID,"Primary",cipher,secretFingerprint([]byte(p.APIKey)));e!=nil{return Provider{},e}
  if e=tx.Commit();e!=nil{return Provider{},e}
  p.APIKey="";p.HasKey=true
  return p,nil
 }
 if p.APIKey==""{
  result,e:=s.db.Exec("UPDATE providers SET name=?,base_url=?,api_type=?,priority=?,enabled=? WHERE id=?",p.Name,p.BaseURL,p.APIType,p.Priority,*p.Enabled,p.ID)
  if e!=nil{return Provider{},e}
  n,_:=result.RowsAffected();if n==0{return Provider{},ErrNotFound}
 }else{
  cipher,e:=sealSecret([]byte(p.APIKey));if e!=nil{return Provider{},e}
  tx,e:=s.db.Begin();if e!=nil{return Provider{},e}
  defer tx.Rollback()
  result,e:=tx.Exec("UPDATE providers SET name=?,base_url=?,api_type=?,priority=?,enabled=?,secret=X'' WHERE id=?",p.Name,p.BaseURL,p.APIType,p.Priority,*p.Enabled,p.ID)
  if e!=nil{return Provider{},e}
  n,_:=result.RowsAffected();if n==0{return Provider{},ErrNotFound}
  var max sql.NullInt64
  if e=tx.QueryRow("SELECT MAX(priority) FROM provider_keys WHERE provider_id=?",p.ID).Scan(&max);e!=nil{return Provider{},e}
  priority:=0
  if max.Valid{priority=int(max.Int64)+100}
  if _,e=tx.Exec(`INSERT INTO provider_keys(id,provider_id,label,secret,fingerprint,priority,enabled,status)
   VALUES(?,?,?,?,?,?,1,'ready')`,newID(),p.ID,"Added via Provider editor",cipher,secretFingerprint([]byte(p.APIKey)),priority);e!=nil{return Provider{},e}
  if e=tx.Commit();e!=nil{return Provider{},e}
 }
 p.APIKey=""
 _=s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM provider_keys WHERE provider_id=?)",p.ID).Scan(&p.HasKey)
 return p,nil
}
func(s *Store)DeleteProvider(id string)error{
 tx,e:=s.db.Begin();if e!=nil{return e}
 defer tx.Rollback()
 if _,e=tx.Exec("UPDATE gateways SET default_provider_id='' WHERE default_provider_id=?",id);e!=nil{return e}
 r,e:=tx.Exec("DELETE FROM providers WHERE id=?",id);if e!=nil{return e}
 n,_:=r.RowsAffected();if n==0{return ErrNotFound}
 return tx.Commit()
}
func(s *Store)Gateways()([]Gateway,error){
 rows,e:=s.db.Query("SELECT id,name,active,port,input_type,default_provider_id FROM gateways ORDER BY created_at,id");if e!=nil{return nil,e};defer rows.Close()
 results:=[]Gateway{}
 for rows.Next(){var g Gateway;if e=rows.Scan(&g.ID,&g.Name,&g.Active,&g.Port,&g.InputType,&g.DefaultProviderID);e!=nil{return nil,e};results=append(results,g)}
 return results,rows.Err()
}
func validateClientKey(key string)error{
 if len(key)<24 || len(key)>256 {return errors.New("custom client key must be 24–256 characters")}
 for _,char:=range key {
  if char<33 || char>126{return errors.New("client key must contain printable ASCII characters without spaces")}
 }
 return nil
}
func(s *Store)SaveGateway(g Gateway)(Gateway,error){
 g.Name=strings.TrimSpace(g.Name)
 if g.InputType==""{g.InputType=ProtocolAnthropic}
 if err:=validateProtocol(g.InputType);err!=nil{return Gateway{},err}
 if g.Port==0{g.Port=18765}
 if g.Port<1024||g.Port>65535||g.Port==8787{return Gateway{},errors.New("gateway port must be 1024–65535 and cannot be management port 8787")}
 if g.Name==""||len(g.Name)>120{return Gateway{},errors.New("gateway name must be 1–120 characters")}
 if g.DefaultProviderID!=""{
  var exists int
  if e:=s.db.QueryRow("SELECT 1 FROM providers WHERE id=?",g.DefaultProviderID).Scan(&exists);errors.Is(e,sql.ErrNoRows){return Gateway{},errors.New("default provider does not exist") }else if e!=nil{return Gateway{},e}
 }
 if g.ID=="" {
  if g.ClientKey==""{g.ClientKey=newClientKey()} else if e:=validateClientKey(g.ClientKey);e!=nil{return Gateway{},e}
  g.ID=newID()
  _,e:=s.db.Exec("INSERT INTO gateways(id,name,client_hash,port,input_type,default_provider_id) VALUES(?,?,?,?,?,?)",g.ID,g.Name,hashKey(g.ClientKey),g.Port,g.InputType,g.DefaultProviderID)
  return g,e
 }
 if g.ClientKey!=""{return Gateway{},errors.New("rotate client keys using the rotate action")}
 r,e:=s.db.Exec("UPDATE gateways SET name=?,port=?,input_type=?,default_provider_id=? WHERE id=?",g.Name,g.Port,g.InputType,g.DefaultProviderID,g.ID)
 if e!=nil{return Gateway{},e};n,_:=r.RowsAffected();if n==0{return Gateway{},ErrNotFound}
 return g,nil
}
func(s *Store)RotateKey(id,customKey string)(string,error){
 key:=customKey
 if key==""{key=newClientKey()} else if e:=validateClientKey(key);e!=nil{return "",e}
 r,e:=s.db.Exec("UPDATE gateways SET client_hash=? WHERE id=?",hashKey(key),id)
 if e!=nil{return "",e};n,_:=r.RowsAffected();if n==0{return "",ErrNotFound}
 return key,nil
}
func(s *Store)ActivateGateway(id string)error{
 tx,e:=s.db.Begin();if e!=nil{return e};defer tx.Rollback()
 var count int
 if e=tx.QueryRow("SELECT COUNT(*) FROM gateways WHERE id=?",id).Scan(&count);e!=nil{return e}
 if count==0{return ErrNotFound}
 if _,e=tx.Exec("UPDATE gateways SET active=1 WHERE id=?",id);e!=nil{return e}
 return tx.Commit()
}
func(s *Store)DeleteGateway(id string)error{
 r,e:=s.db.Exec("DELETE FROM gateways WHERE id=?",id);if e!=nil{return e}
 n,_:=r.RowsAffected();if n==0{return ErrNotFound};return nil
}
func(s *Store)Aliases()([]Alias,error){
 rows,e:=s.db.Query("SELECT id,gateway_id,provider_id,incoming,target,enabled FROM aliases ORDER BY incoming");if e!=nil{return nil,e};defer rows.Close()
 result:=[]Alias{}
 for rows.Next(){var a Alias;if e=rows.Scan(&a.ID,&a.GatewayID,&a.ProviderID,&a.Incoming,&a.Target,&a.Enabled);e!=nil{return nil,e};result=append(result,a)}
 return result,rows.Err()
}
func(s *Store)SaveAlias(a Alias)(Alias,error){
 a.Incoming=strings.TrimSpace(a.Incoming);a.Target=strings.TrimSpace(a.Target)
 if a.GatewayID==""||a.ProviderID==""||a.Incoming==""||a.Target=="" {return Alias{},errors.New("gateway, provider, incoming and target model are required")}
 if len(a.Incoming)>200||len(a.Target)>200 {return Alias{},errors.New("model identifier is too long")}
 if a.ID==""{
  a.ID=newID()
  _,e:=s.db.Exec("INSERT INTO aliases(id,gateway_id,provider_id,incoming,target,enabled) VALUES(?,?,?,?,?,?)",a.ID,a.GatewayID,a.ProviderID,a.Incoming,a.Target,a.Enabled)
  return a,e
 }
 r,e:=s.db.Exec("UPDATE aliases SET gateway_id=?,provider_id=?,incoming=?,target=?,enabled=? WHERE id=?",a.GatewayID,a.ProviderID,a.Incoming,a.Target,a.Enabled,a.ID)
 if e!=nil{return Alias{},e};n,_:=r.RowsAffected();if n==0{return Alias{},ErrNotFound}
 return a,nil
}
func(s *Store)DeleteAlias(id string)error{
 r,e:=s.db.Exec("DELETE FROM aliases WHERE id=?",id);if e!=nil{return e}
 n,_:=r.RowsAffected();if n==0{return ErrNotFound};return nil
}
// BulkAliases applies a single operation to an explicit list of saved aliases.
// Validation and mutations run in the same SQLite transaction: a missing ID or
// invalid request rolls back the entire selection.
func(s *Store)BulkAliases(action string,ids []string)(int,error){
 if action!="enable"&&action!="disable"&&action!="delete"{
  return 0,errors.New("bulk alias action must be enable, disable or delete")
 }
 if len(ids)==0||len(ids)>5000{
  return 0,errors.New("select between 1 and 5000 aliases")
 }
 unique:=make(map[string]struct{},len(ids))
 for _,id:=range ids{
  if len(id)!=24{return 0,errors.New("invalid alias ID")}
  for _,c:=range id{
   if (c<'0'||c>'9')&&(c<'a'||c>'f'){return 0,errors.New("invalid alias ID")}
  }
  if _,exists:=unique[id];exists{return 0,errors.New("duplicate alias ID")}
  unique[id]=struct{}{}
 }
 tx,err:=s.db.Begin()
 if err!=nil{return 0,err}
 defer tx.Rollback()
 exists,err:=tx.Prepare("SELECT 1 FROM aliases WHERE id=?")
 if err!=nil{return 0,err}
 for _,id:=range ids{
  var found int
  err=exists.QueryRow(id).Scan(&found)
  if errors.Is(err,sql.ErrNoRows){_ = exists.Close();return 0,ErrNotFound}
  if err!=nil{_ = exists.Close();return 0,err}
 }
 if err=exists.Close();err!=nil{return 0,err}
 var statement string
 switch action{
 case "enable":statement="UPDATE aliases SET enabled=1 WHERE id=?"
 case "disable":statement="UPDATE aliases SET enabled=0 WHERE id=?"
 case "delete":statement="DELETE FROM aliases WHERE id=?"
 }
 update,err:=tx.Prepare(statement)
 if err!=nil{return 0,err}
 for _,id:=range ids{
  if _,err=update.Exec(id);err!=nil{_ = update.Close();return 0,err}
 }
 if err=update.Close();err!=nil{return 0,err}
 if err=tx.Commit();err!=nil{return 0,err}
 return len(ids),nil
}
func(s *Store)ActiveAuth()(string,error){
 var hash string
 e:=s.db.QueryRow("SELECT client_hash FROM gateways WHERE active=1").Scan(&hash)
 return hash,e
}
// RoutesFor returns an ordered provider chain. Each Provider receives its own
// exact alias first, falling back to its own '*' alias. Higher-priority
// Providers are attempted first; ties use creation order and stable ID.
func(s *Store)RoutesFor(incoming string)([]Route,error){
 g,e:=s.ActiveGateway();if e!=nil{return nil,e}
 return s.RoutesForGateway(g.ID,incoming)
}
func(s *Store)RoutesForGateway(gatewayID,incoming string)([]Route,error){
 rows,err:=s.db.Query(`SELECT g.client_hash,a.target,p.base_url,p.id,p.api_type
 FROM gateways g
 JOIN gateway_providers gp ON gp.gateway_id=g.id
 JOIN providers p ON p.id=gp.provider_id AND p.enabled=1
 JOIN aliases a ON a.gateway_id=g.id AND a.provider_id=p.id AND a.enabled=1
 WHERE g.active=1 AND g.id=? AND (a.incoming=? OR a.incoming='*')
 AND a.id=(
   SELECT best.id FROM aliases best
   WHERE best.gateway_id=g.id AND best.provider_id=p.id AND best.enabled=1
   AND (best.incoming=? OR best.incoming='*')
   ORDER BY CASE WHEN best.incoming=? THEN 0 ELSE 1 END
   LIMIT 1
 )
 ORDER BY p.priority ASC,p.rowid ASC`,gatewayID,incoming,incoming,incoming)
 if err!=nil{return nil,err}
 defer rows.Close()
 candidates:=[]Route{}
 for rows.Next(){
  var route Route
  if err=rows.Scan(&route.ClientHash,&route.Target,&route.BaseURL,&route.ProviderID,&route.ProviderType);err!=nil{return nil,err}
  candidates=append(candidates,route)
 }
 if err=rows.Err();err!=nil{return nil,err}
 if len(candidates)==0{return nil,ErrNotFound}
 return candidates,nil
}
// RouteFor remains for callers that need to inspect only the first eligible
// Provider; the runtime uses RoutesFor for transparent fallback.
func(s *Store)RouteFor(incoming string)(Route,error){
 all,err:=s.RoutesFor(incoming)
 if err!=nil{return Route{},err}
 return all[0],nil
}
func(s *Store)ActiveModels()([]string,error){
 g,e:=s.ActiveGateway();if e!=nil{return nil,e}
 return s.ModelsForGateway(g.ID)
}
func(s *Store)ModelsForGateway(gatewayID string)([]string,error){
 rows,e:=s.db.Query(`SELECT DISTINCT a.incoming FROM aliases a
 JOIN gateways g ON g.id=a.gateway_id
 JOIN gateway_providers gp ON gp.provider_id=a.provider_id AND gp.gateway_id=g.id
 JOIN providers p ON p.id=a.provider_id
 WHERE g.active=1 AND g.id=? AND p.enabled=1 AND a.enabled=1 AND a.incoming<>'*'
 ORDER BY a.incoming`,gatewayID)
 if e!=nil{return nil,e}
 defer rows.Close()
 result:=[]string{}
 for rows.Next(){
  var name string
  if e=rows.Scan(&name);e!=nil{return nil,e}
  result=append(result,name)
 }
 return result,rows.Err()
}
func(s *Store)LogEvent(incoming,target string,status int,duration time.Duration){
 _,_ = s.db.Exec("INSERT INTO events(at,incoming,target,status,duration_ms) VALUES(?,?,?,?,?)",
  time.Now().Format(time.RFC3339),incoming,target,status,duration.Milliseconds())
 // Limit on-disk history to recent 1000 entries.
 _,_ = s.db.Exec("DELETE FROM events WHERE id < (SELECT COALESCE(MAX(id)-1000,0) FROM events)")
}
func(s *Store)Events()([]Event,error){
 rows,e:=s.db.Query("SELECT id,at,incoming,target,status,duration_ms FROM events ORDER BY id DESC LIMIT 100");if e!=nil{return nil,e};defer rows.Close()
 result:=[]Event{}
 for rows.Next(){var v Event;if e=rows.Scan(&v.ID,&v.When,&v.Incoming,&v.Target,&v.Status,&v.DurationMS);e!=nil{return nil,e};result=append(result,v)}
 return result,rows.Err()
}

func(s *Store) ActiveGateway()(Gateway,error){
 var g Gateway
 err:=s.db.QueryRow("SELECT id,name,active,port,input_type,default_provider_id FROM gateways WHERE active=1 LIMIT 1").
  Scan(&g.ID,&g.Name,&g.Active,&g.Port,&g.InputType,&g.DefaultProviderID)
 if errors.Is(err,sql.ErrNoRows){return Gateway{},ErrNotFound}
 return g,err
}
func(s *Store) GatewayByID(id string)(Gateway,error){
 var g Gateway
 err:=s.db.QueryRow("SELECT id,name,active,port,input_type,default_provider_id FROM gateways WHERE id=? LIMIT 1",id).
  Scan(&g.ID,&g.Name,&g.Active,&g.Port,&g.InputType,&g.DefaultProviderID)
 if errors.Is(err,sql.ErrNoRows){return Gateway{},ErrNotFound}
 return g,err
}

func(s *Store)ProviderByID(id string)(Provider,error){
 var p Provider
 err:=s.db.QueryRow("SELECT id,name,base_url,api_type FROM providers WHERE id=?",id).
  Scan(&p.ID,&p.Name,&p.BaseURL,&p.APIType)
 if errors.Is(err,sql.ErrNoRows){return Provider{},ErrNotFound}
 return p,err
}
