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
 HasKey bool `json:"has_key"`
 APIKey string `json:"api_key,omitempty"` // accepted on write, never returned
}
type Gateway struct{
 ID string `json:"id"`
 Name string `json:"name"`
 Active bool `json:"active"`
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
 Secret []byte
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
  `CREATE TABLE IF NOT EXISTS gateways(
    id TEXT PRIMARY KEY, name TEXT NOT NULL, client_hash TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
  `CREATE UNIQUE INDEX IF NOT EXISTS one_active_gateway ON gateways(active) WHERE active=1`,
  `CREATE TABLE IF NOT EXISTS aliases(
    id TEXT PRIMARY KEY, gateway_id TEXT NOT NULL REFERENCES gateways(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
    incoming TEXT NOT NULL, target TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
    UNIQUE(gateway_id,incoming))`,
  `CREATE TABLE IF NOT EXISTS events(
    id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL,
    incoming TEXT NOT NULL, target TEXT NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL)`,
  `CREATE INDEX IF NOT EXISTS idx_events_recent ON events(id DESC)`,
 }{
  if _,err=db.Exec(statement);err!=nil {db.Close();return nil,fmt.Errorf("sqlite init: %w",err)}
 }
 return &Store{db},nil
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
 rows,e:=s.db.Query("SELECT id,name,base_url,length(secret)>0 FROM providers ORDER BY name");if e!=nil{return nil,e};defer rows.Close()
 result:=[]Provider{}
 for rows.Next(){var p Provider;if e=rows.Scan(&p.ID,&p.Name,&p.BaseURL,&p.HasKey);e!=nil{return nil,e};result=append(result,p)}
 return result,rows.Err()
}
func(s *Store)SaveProvider(p Provider)(Provider,error){
 p.Name=strings.TrimSpace(p.Name);p.BaseURL=strings.TrimRight(strings.TrimSpace(p.BaseURL),"/")
 if e:=validateProvider(p);e!=nil{return Provider{},e}
 if p.ID=="" {
  if p.APIKey==""{return Provider{},errors.New("provider API key is required")}
  cipher,e:=sealSecret([]byte(p.APIKey));if e!=nil{return Provider{},e}
  p.ID=newID()
  _,e=s.db.Exec("INSERT INTO providers(id,name,base_url,secret) VALUES(?,?,?,?)",p.ID,p.Name,p.BaseURL,cipher)
  p.APIKey="";p.HasKey=true
  return p,e
 }
 if p.APIKey!="" {
  cipher,e:=sealSecret([]byte(p.APIKey));if e!=nil{return Provider{},e}
  result,e:=s.db.Exec("UPDATE providers SET name=?,base_url=?,secret=? WHERE id=?",p.Name,p.BaseURL,cipher,p.ID)
  if e!=nil{return Provider{},e};n,_:=result.RowsAffected();if n==0{return Provider{},ErrNotFound}
 } else {
  result,e:=s.db.Exec("UPDATE providers SET name=?,base_url=? WHERE id=?",p.Name,p.BaseURL,p.ID)
  if e!=nil{return Provider{},e};n,_:=result.RowsAffected();if n==0{return Provider{},ErrNotFound}
 }
 p.HasKey=true;p.APIKey=""
 return p,nil
}
func(s *Store)DeleteProvider(id string)error{
 r,e:=s.db.Exec("DELETE FROM providers WHERE id=?",id);if e!=nil{return e}
 n,_:=r.RowsAffected();if n==0{return ErrNotFound};return nil
}
func(s *Store)Gateways()([]Gateway,error){
 rows,e:=s.db.Query("SELECT id,name,active FROM gateways ORDER BY created_at,id");if e!=nil{return nil,e};defer rows.Close()
 results:=[]Gateway{}
 for rows.Next(){var g Gateway;if e=rows.Scan(&g.ID,&g.Name,&g.Active);e!=nil{return nil,e};results=append(results,g)}
 return results,rows.Err()
}
func(s *Store)SaveGateway(g Gateway)(Gateway,error){
 g.Name=strings.TrimSpace(g.Name)
 if g.Name==""||len(g.Name)>120{return Gateway{},errors.New("gateway name must be 1–120 characters")}
 if g.ID=="" {
  g.ID=newID();g.ClientKey=newClientKey()
  _,e:=s.db.Exec("INSERT INTO gateways(id,name,client_hash) VALUES(?,?,?)",g.ID,g.Name,hashKey(g.ClientKey))
  return g,e
 }
 r,e:=s.db.Exec("UPDATE gateways SET name=? WHERE id=?",g.Name,g.ID)
 if e!=nil{return Gateway{},e};n,_:=r.RowsAffected();if n==0{return Gateway{},ErrNotFound}
 return g,nil
}
func(s *Store)RotateKey(id string)(string,error){
 key:=newClientKey()
 r,e:=s.db.Exec("UPDATE gateways SET client_hash=? WHERE id=?",hashKey(key),id)
 if e!=nil{return "",e};n,_:=r.RowsAffected();if n==0{return "",ErrNotFound}
 return key,nil
}
func(s *Store)ActivateGateway(id string)error{
 tx,e:=s.db.Begin();if e!=nil{return e};defer tx.Rollback()
 var count int
 if e=tx.QueryRow("SELECT COUNT(*) FROM gateways WHERE id=?",id).Scan(&count);e!=nil{return e}
 if count==0{return ErrNotFound}
 if _,e=tx.Exec("UPDATE gateways SET active=0 WHERE active=1");e!=nil{return e}
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
func(s *Store)ActiveAuth()(string,error){
 var hash string
 e:=s.db.QueryRow("SELECT client_hash FROM gateways WHERE active=1").Scan(&hash)
 return hash,e
}
func(s *Store)RouteFor(incoming string)(Route,error){
 var out Route;var sealed []byte
 err:=s.db.QueryRow(`SELECT g.client_hash,a.target,p.base_url,p.secret
 FROM gateways g JOIN aliases a ON a.gateway_id=g.id
 JOIN providers p ON p.id=a.provider_id
 WHERE g.active=1 AND a.enabled=1 AND a.incoming=?`,incoming).
 Scan(&out.ClientHash,&out.Target,&out.BaseURL,&sealed)
 if errors.Is(err,sql.ErrNoRows){return Route{},ErrNotFound}
 if err!=nil{return Route{},err}
 out.Secret,err=openSecret(sealed)
 return out,err
}
func(s *Store)ActiveModels()([]string,error){
 rows,e:=s.db.Query(`SELECT a.incoming FROM aliases a
 JOIN gateways g ON g.id=a.gateway_id
 WHERE g.active=1 AND a.enabled=1 ORDER BY a.incoming`)
 if e!=nil{return nil,e};defer rows.Close()
 result:=[]string{}
 for rows.Next(){var name string;if e=rows.Scan(&name);e!=nil{return nil,e};result=append(result,name)}
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
