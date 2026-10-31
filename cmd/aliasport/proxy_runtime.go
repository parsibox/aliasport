package main

import (
 "context"
 "errors"
 "fmt"
 "net"
 "net/http"
 "strconv"
 "sync"
 "time"
)

// Every active Gateway has its own listener and protocol. Sharing a Provider
// between Gateway profiles does not share their client credentials or aliases.
type gatewayContextKey struct{}
type proxyListener struct{
 server *http.Server
 listener net.Listener
 port int
}
type proxyManager struct {
 mu sync.Mutex
 app *App
 running map[string]*proxyListener
}
func newProxyManager(app *App)*proxyManager{return &proxyManager{app:app,running:make(map[string]*proxyListener)}}

func (m *proxyManager)listen(port int)(net.Listener,error){
 if port<1024||port>65535{return nil,errors.New("invalid gateway port")}
 if m.app.adminPort!=""{
  _,adminPort,err:=net.SplitHostPort(m.app.adminPort)
  if err==nil && adminPort==strconv.Itoa(port){return nil,errors.New("proxy port cannot equal management port")}
 }
 address:=net.JoinHostPort("127.0.0.1",strconv.Itoa(port))
 listener,err:=net.Listen("tcp",address)
 if err!=nil{return nil,fmt.Errorf("proxy port %d unavailable: %w",port,err)}
 return listener,nil
}
func(m *proxyManager)serve(l net.Listener,id string)*proxyListener{
 mux:=http.NewServeMux()
 mux.HandleFunc("/health",func(w http.ResponseWriter,r *http.Request){
  writeJSON(w,200,map[string]any{"ok":true,"service":"aliasport-gateway"})
 })
 mux.HandleFunc("/v1/models",m.app.models)
 mux.HandleFunc("/v1/messages",m.app.messages)
 mux.HandleFunc("/v1/messages/count_tokens",m.app.messages)
 mux.HandleFunc("/v1/chat/completions",m.app.chatCompletions)
 scoped:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  mux.ServeHTTP(w,r.WithContext(context.WithValue(r.Context(),gatewayContextKey{},id)))
 })
 server:=&http.Server{Handler:scoped,ReadHeaderTimeout:10*time.Second,IdleTimeout:2*time.Minute}
 go func(){_ = server.Serve(l)}()
 return &proxyListener{server:server,listener:l,port:l.Addr().(*net.TCPAddr).Port}
}
func(m *proxyManager)startFromDatabase()error{
 gateways,err:=m.app.store.ActiveGateways();if err!=nil{return err}
 m.mu.Lock();defer m.mu.Unlock()
 for _,g:=range gateways{
  l,e:=m.listen(g.Port)
  if e!=nil{
   for _,instance:=range m.running{_ = instance.server.Close()}
   m.running=make(map[string]*proxyListener)
   return fmt.Errorf("Gateway %s: %w",g.Name,e)
  }
  m.running[g.ID]=m.serve(l,g.ID)
 }
 return nil
}
func(m *proxyManager)switchTo(id string)error{
 m.mu.Lock();defer m.mu.Unlock()
 if _,ok:=m.running[id];ok{return nil}
 g,err:=m.app.store.GatewayByID(id);if err!=nil{return err}
 listener,err:=m.listen(g.Port);if err!=nil{return err}
 if err=m.app.store.ActivateGateway(id);err!=nil{_ = listener.Close();return err}
 m.running[id]=m.serve(listener,id)
 return nil
}
func(m *proxyManager)updateGateway(g Gateway)(Gateway,error){
 m.mu.Lock();defer m.mu.Unlock()
 old,err:=m.app.store.GatewayByID(g.ID);if err!=nil{return Gateway{},err}
 var replacement net.Listener
 if old.Active && old.Port!=g.Port {
  replacement,err=m.listen(g.Port)
  if err!=nil{return Gateway{},err}
 }
 saved,err:=m.app.store.SaveGateway(g)
 if err!=nil{if replacement!=nil{_ = replacement.Close()};return Gateway{},err}
 if replacement!=nil{
  previous:=m.running[g.ID]
  m.running[g.ID]=m.serve(replacement,g.ID)
  if previous!=nil{go drainProxy(previous.server)}
 }
 return saved,nil
}
func(m *proxyManager)removeGateway(id string)error{
 m.mu.Lock();defer m.mu.Unlock()
 if _,err:=m.app.store.GatewayByID(id);err!=nil{return err}
 if err:=m.app.store.DeleteGateway(id);err!=nil{return err}
 if previous,ok:=m.running[id];ok{
  delete(m.running,id)
  go drainProxy(previous.server)
 }
 return nil
}
func(m *proxyManager)deactivate(id string)error{
 m.mu.Lock();defer m.mu.Unlock()
 if err:=m.app.store.DeactivateGateway(id);err!=nil{return err}
 if previous,ok:=m.running[id];ok{
  delete(m.running,id)
  go drainProxy(previous.server)
 }
 return nil
}
func drainProxy(server *http.Server){
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Minute)
 defer cancel()
 if err:=server.Shutdown(ctx);err!=nil{_ = server.Close()}
}
func(m *proxyManager)close(){
 m.mu.Lock();defer m.mu.Unlock()
 for id,instance:=range m.running{
  _ = instance.server.Close()
  delete(m.running,id)
 }
}
func(s *Store)DeactivateGateway(id string)error{
 res,err:=s.db.Exec("UPDATE gateways SET active=0 WHERE id=?",id)
 if err!=nil{return err}
 n,_:=res.RowsAffected();if n==0{return ErrNotFound}
 return nil
}
