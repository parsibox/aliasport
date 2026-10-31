package main

import (
 "crypto/sha256"
 "database/sql"
 "encoding/hex"
 "errors"
 "fmt"
 "strings"
 "time"
)

// A provider can have multiple ordered, separately encrypted upstream credentials.
// Billing-blocked credentials remain disabled for routing until manually reset.
type ProviderKey struct {
 ID string `json:"id"`
 ProviderID string `json:"provider_id"`
 Label string `json:"label"`
 Priority int `json:"priority"`
 Enabled bool `json:"enabled"`
 Status string `json:"status"`
 Fingerprint string `json:"fingerprint"`
 LastBillingAt string `json:"last_billing_at,omitempty"`
 LastUsedAt string `json:"last_used_at,omitempty"`
 APIKey string `json:"api_key,omitempty"` // write-only
}
type UpstreamKey struct {
 ID string
 Secret []byte
}
var ErrNoUsableKeys = errors.New("no usable provider API keys")

func secretFingerprint(secret []byte)string {
 sum:=sha256.Sum256(secret)
 return hex.EncodeToString(sum[:6])
}

// Called after legacy schema is created. Preserves existing single-key installations.
func (s *Store) migrateProviderKeys()error {
 tx,err:=s.db.Begin();if err!=nil{return err}
 defer tx.Rollback()
 rows,err:=tx.Query("SELECT id,secret FROM providers WHERE length(secret)>0")
 if err!=nil{return err}
 type legacy struct {id string;secret []byte}
 var existing []legacy
 for rows.Next(){
  var item legacy
  if err=rows.Scan(&item.id,&item.secret);err!=nil{rows.Close();return err}
  existing=append(existing,item)
 }
 if err=rows.Err();err!=nil{rows.Close();return err}
 rows.Close()
 for _,p:=range existing {
  var count int
  if err=tx.QueryRow("SELECT COUNT(*) FROM provider_keys WHERE provider_id=?",p.id).Scan(&count);err!=nil{return err}
  if count!=0{continue}
  plain,e:=openSecret(p.secret)
  if e!=nil{return fmt.Errorf("cannot migrate provider %s credentials: %w",p.id,e)}
  _,err=tx.Exec(`INSERT INTO provider_keys(id,provider_id,label,secret,fingerprint,priority,enabled,status)
     VALUES(?,?,?,?,?,0,1,'ready')`,newID(),p.id,"Primary (migrated)",p.secret,secretFingerprint(plain))
  if err!=nil{return err}
 }
 return tx.Commit()
}

func(s *Store) ProviderKeys(providerID string)([]ProviderKey,error){
 rows,err:=s.db.Query(`SELECT id,provider_id,label,priority,enabled,status,fingerprint,
 COALESCE(last_billing_at,''),COALESCE(last_used_at,'')
 FROM provider_keys WHERE provider_id=? ORDER BY priority ASC,created_at ASC,id ASC`,providerID)
 if err!=nil{return nil,err}
 defer rows.Close()
 keys:=[]ProviderKey{}
 for rows.Next(){
  var k ProviderKey
  if err=rows.Scan(&k.ID,&k.ProviderID,&k.Label,&k.Priority,&k.Enabled,&k.Status,&k.Fingerprint,&k.LastBillingAt,&k.LastUsedAt);err!=nil{return nil,err}
  keys=append(keys,k)
 }
 return keys,rows.Err()
}

func validateKeyInput(k ProviderKey)error{
 k.Label=strings.TrimSpace(k.Label)
 if len(k.Label)==0||len(k.Label)>100{return errors.New("key label must contain 1–100 characters")}
 if k.Priority < 0 || k.Priority > 1000000{return errors.New("key priority must be between 0 and 1000000")}
 if len(k.APIKey)>8192{return errors.New("API key is too long")}
 if strings.ContainsAny(k.APIKey,"\r\n"){return errors.New("API key may not contain line breaks")}
 return nil
}

// SaveProviderKey does not expose the secret in its response. An empty secret on
// update leaves the existing encrypted value unchanged.
func(s *Store) SaveProviderKey(k ProviderKey)(ProviderKey,error){
 k.Label=strings.TrimSpace(k.Label)
 if err:=validateKeyInput(k);err!=nil{return ProviderKey{},err}
 if k.ID=="" {
  if strings.TrimSpace(k.APIKey)==""{return ProviderKey{},errors.New("API key is required")}
  cipher,err:=sealSecret([]byte(k.APIKey));if err!=nil{return ProviderKey{},err}
  k.ID=newID();k.Status="ready";k.Fingerprint=secretFingerprint([]byte(k.APIKey))
  _,err=s.db.Exec(`INSERT INTO provider_keys(id,provider_id,label,secret,fingerprint,priority,enabled,status)
  VALUES(?,?,?,?,?,?,?,'ready')`,k.ID,k.ProviderID,k.Label,cipher,k.Fingerprint,k.Priority,k.Enabled)
  if err!=nil{return ProviderKey{},err}
 } else {
  if k.APIKey==""{
   result,err:=s.db.Exec("UPDATE provider_keys SET label=?,priority=?,enabled=? WHERE id=? AND provider_id=?",k.Label,k.Priority,k.Enabled,k.ID,k.ProviderID)
   if err!=nil{return ProviderKey{},err}
   n,_:=result.RowsAffected();if n==0{return ProviderKey{},ErrNotFound}
  } else {
   cipher,err:=sealSecret([]byte(k.APIKey));if err!=nil{return ProviderKey{},err}
   k.Fingerprint=secretFingerprint([]byte(k.APIKey))
   result,err:=s.db.Exec(`UPDATE provider_keys
   SET label=?,priority=?,enabled=?,secret=?,fingerprint=?,status='ready',last_billing_at=NULL
   WHERE id=? AND provider_id=?`,k.Label,k.Priority,k.Enabled,cipher,k.Fingerprint,k.ID,k.ProviderID)
   if err!=nil{return ProviderKey{},err}
   n,_:=result.RowsAffected();if n==0{return ProviderKey{},ErrNotFound}
  }
 }
 k.APIKey=""
 return k,nil
}

func(s *Store) DeleteProviderKey(providerID,id string)error{
 result,err:=s.db.Exec("DELETE FROM provider_keys WHERE id=? AND provider_id=?",id,providerID)
 if err!=nil{return err}
 n,_:=result.RowsAffected();if n==0{return ErrNotFound}
 return nil
}

func(s *Store) ResetProviderKey(providerID,id string)error{
 result,err:=s.db.Exec(`UPDATE provider_keys SET status='ready',last_billing_at=NULL
 WHERE id=? AND provider_id=?`,id,providerID)
 if err!=nil{return err}
 n,_:=result.RowsAffected();if n==0{return ErrNotFound}
 return nil
}

func(s *Store) EligibleProviderKeys(providerID string)([]UpstreamKey,error){
 rows,err:=s.db.Query(`SELECT id,secret FROM provider_keys
 WHERE provider_id=? AND enabled=1 AND status='ready'
 ORDER BY priority ASC,created_at ASC,id ASC`,providerID)
 if err!=nil{return nil,err}
 defer rows.Close()
 var results []UpstreamKey
 for rows.Next(){
  var k UpstreamKey
  var cipher []byte
  if err=rows.Scan(&k.ID,&cipher);err!=nil{return nil,err}
  k.Secret,err=openSecret(cipher)
  if err!=nil{return nil,err}
  results=append(results,k)
 }
 if err=rows.Err();err!=nil{return nil,err}
 if len(results)==0{return nil,ErrNoUsableKeys}
 return results,nil
}
func(s *Store) BlockBillingKey(providerID,id string)error{
 _,err:=s.db.Exec(`UPDATE provider_keys SET status='billing_blocked',last_billing_at=?
 WHERE provider_id=? AND id=? AND status='ready'`,time.Now().Format(time.RFC3339),providerID,id)
 return err
}
func(s *Store) MarkKeyUsed(providerID,id string) {
 _,_ = s.db.Exec("UPDATE provider_keys SET last_used_at=? WHERE provider_id=? AND id=?",
 time.Now().Format(time.RFC3339),providerID,id)
}

// Used for initial provider creation and backwards-compatible provider edits.
func(s *Store) AddLegacyProviderKey(providerID,apiKey,label string)error{
 var max sql.NullInt64
 if err:=s.db.QueryRow("SELECT MAX(priority) FROM provider_keys WHERE provider_id=?",providerID).Scan(&max);err!=nil{return err}
 priority:=0
 if max.Valid {priority=int(max.Int64)+100}
 _,err:=s.SaveProviderKey(ProviderKey{ProviderID:providerID,Label:label,Priority:priority,Enabled:true,APIKey:apiKey})
 return err
}
