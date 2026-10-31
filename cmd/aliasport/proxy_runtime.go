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

// Only one profile listens at any given time. Each profile stores its own
// listening port, so switching profiles also switches the proxy endpoint.
type proxyManager struct {
 mu sync.Mutex
 app *App
 listener net.Listener
 server *http.Server
 port int
}

func newProxyManager(app *App)*proxyManager{return &proxyManager{app:app}}

func (m *proxyManager)listen(port int)(net.Listener,error){
 if port<1024||port>65535||port==8787 {return nil,errors.New("invalid gateway port")}
 address:=net.JoinHostPort("127.0.0.1",strconv.Itoa(port))
 listener,err:=net.Listen("tcp",address)
 if err!=nil{return nil,fmt.Errorf("proxy port %d is unavailable: %w",port,err)}
 return listener,nil
}
func(m *proxyManager)serve(l net.Listener) *http.Server{
 mux:=http.NewServeMux()
 mux.HandleFunc("/health",func(w http.ResponseWriter,r *http.Request){
  writeJSON(w,200,map[string]any{"ok":true,"service":"aliasport-gateway"})
 })
 mux.HandleFunc("/v1/models",m.app.models)
 mux.HandleFunc("/v1/messages",m.app.messages)
 mux.HandleFunc("/v1/messages/count_tokens",m.app.messages)
 mux.HandleFunc("/v1/chat/completions",m.app.chatCompletions)
 server:=&http.Server{Handler:mux,ReadHeaderTimeout:10*time.Second,IdleTimeout:2*time.Minute}
 go func(){_ = server.Serve(l)}()
 return server
}
func(m *proxyManager)startFromDatabase()error{
 g,err:=m.app.store.ActiveGateway()
 if errors.Is(err,ErrNotFound){return nil}
 if err!=nil{return err}
 m.mu.Lock();defer m.mu.Unlock()
 listener,err:=m.listen(g.Port);if err!=nil{return err}
 m.listener=listener
 m.server=m.serve(listener)
 m.port=g.Port
 return nil
}
func(m *proxyManager)switchTo(id string)error{
 g,err:=m.app.store.GatewayByID(id);if err!=nil{return err}
 m.mu.Lock()
 defer m.mu.Unlock()
 if m.listener!=nil&&m.port==g.Port{return m.app.store.ActivateGateway(id)}
 listener,err:=m.listen(g.Port);if err!=nil{return err}
 if err=m.app.store.ActivateGateway(id);err!=nil{_ = listener.Close();return err}
 previous:=m.server
 m.listener=listener
 m.server=m.serve(listener)
 m.port=g.Port
 if previous!=nil{go drainProxy(previous)}
 return nil
}
func(m *proxyManager)updateGateway(g Gateway)(Gateway,error){
 m.mu.Lock()
 defer m.mu.Unlock()
 // If an active profile changes port, acquire the replacement first.
 old,err:=m.app.store.GatewayByID(g.ID)
 if err!=nil{return Gateway{},err}
 var replacement net.Listener
 if old.Active && m.port!=g.Port {
  replacement,err=m.listen(g.Port)
  if err!=nil{return Gateway{},err}
 }
 saved,err:=m.app.store.SaveGateway(g)
 if err!=nil{if replacement!=nil{_ = replacement.Close()};return Gateway{},err}
 if replacement!=nil{
  oldServer:=m.server
  m.listener=replacement;m.server=m.serve(replacement);m.port=saved.Port
  if oldServer!=nil{go drainProxy(oldServer)}
 }
 return saved,nil
}
func(m *proxyManager)removeGateway(id string)error{
 m.mu.Lock()
 defer m.mu.Unlock()
 old,err:=m.app.store.GatewayByID(id);if err!=nil{return err}
 if err=m.app.store.DeleteGateway(id);err!=nil{return err}
 if old.Active{
  previous:=m.server;m.server=nil;m.listener=nil;m.port=0
  if previous!=nil{go drainProxy(previous)}
 }
 return nil
}
func(m *proxyManager)deactivate(id string)error{
 m.mu.Lock();defer m.mu.Unlock()
 g,err:=m.app.store.GatewayByID(id);if err!=nil{return err}
 if err=m.app.store.DeactivateGateway(id);err!=nil{return err}
 if g.Active{
  old:=m.server;m.server=nil;m.listener=nil;m.port=0
  if old!=nil{go drainProxy(old)}
 }
 return nil
}
func drainProxy(server *http.Server){
 // Preserve in-flight stream connections for up to five minutes on profile
 // changes; new requests cannot be accepted by the old listener.
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Minute)
 defer cancel()
 if err:=server.Shutdown(ctx);err!=nil{_ = server.Close()}
}
func(m *proxyManager)close(){
 m.mu.Lock();defer m.mu.Unlock()
 if m.server!=nil{_ = m.server.Close();m.server=nil;m.listener=nil;m.port=0}
}
func(s *Store)DeactivateGateway(id string)error{
 res,err:=s.db.Exec("UPDATE gateways SET active=0 WHERE id=?",id)
 if err!=nil{return err}
 n,_:=res.RowsAffected();if n==0{return ErrNotFound}
 return nil
}
