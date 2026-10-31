//go:build !windows

package main

import (
 "crypto/aes"
 "crypto/cipher"
 "crypto/rand"
 "errors"
 "os"
 "path/filepath"
)

func fileKey()([]byte,error){
 dir,e:=dataDir();if e!=nil{return nil,e}
 if e=os.MkdirAll(dir,0700);e!=nil{return nil,e}
 path:=filepath.Join(dir,"secret.key")
 key,e:=os.ReadFile(path)
 if e==nil {
  if len(key)!=32{return nil,errors.New("invalid local encryption key")}
  return key,nil
 }
 if !os.IsNotExist(e){return nil,e}
 key=make([]byte,32)
 if _,e=rand.Read(key);e!=nil{return nil,e}
 f,e:=os.OpenFile(path,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if e!=nil{return nil,e}
 _,e=f.Write(key);closeErr:=f.Close()
 if e!=nil{return nil,e};if closeErr!=nil{return nil,closeErr}
 return key,nil
}
func sealSecret(plain []byte)([]byte,error){
 key,e:=fileKey();if e!=nil{return nil,e}
 block,e:=aes.NewCipher(key);if e!=nil{return nil,e}
 aead,e:=cipher.NewGCM(block);if e!=nil{return nil,e}
 nonce:=make([]byte,aead.NonceSize())
 if _,e=rand.Read(nonce);e!=nil{return nil,e}
 return append(nonce,aead.Seal(nil,nonce,plain,nil)...),nil
}
func openSecret(sealed []byte)([]byte,error){
 key,e:=fileKey();if e!=nil{return nil,e}
 block,e:=aes.NewCipher(key);if e!=nil{return nil,e}
 aead,e:=cipher.NewGCM(block);if e!=nil{return nil,e}
 n:=aead.NonceSize()
 if len(sealed)<n{return nil,errors.New("invalid encrypted secret")}
 return aead.Open(nil,sealed[:n],sealed[n:],nil)
}
