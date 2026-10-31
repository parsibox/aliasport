//go:build windows

package main

import (
 "errors"
 "syscall"
 "unsafe"
)

type dataBlob struct {
 size uint32
 data *byte
}

var (
 crypt32 = syscall.NewLazyDLL("crypt32.dll")
 kernel32 = syscall.NewLazyDLL("kernel32.dll")
 protectData = crypt32.NewProc("CryptProtectData")
 unprotectData = crypt32.NewProc("CryptUnprotectData")
 localFree = kernel32.NewProc("LocalFree")
)

const cryptProtectUIForbidden = 0x1

func dpapi(input []byte, encrypt bool)([]byte,error){
 if len(input)==0{return nil,errors.New("empty secret")}
 in := dataBlob{size:uint32(len(input)),data:&input[0]}
 var out dataBlob
 proc:=protectData
 if !encrypt {proc=unprotectData}
 ret,_,callErr:=proc.Call(
  uintptr(unsafe.Pointer(&in)),0,0,0,0,cryptProtectUIForbidden,uintptr(unsafe.Pointer(&out)),
 )
 if ret==0 {return nil,callErr}
 defer localFree.Call(uintptr(unsafe.Pointer(out.data)))
 if out.data==nil{return nil,errors.New("DPAPI returned no data")}
 result:=append([]byte(nil),unsafe.Slice(out.data,int(out.size))...)
 return result,nil
}
func sealSecret(plain []byte)([]byte,error){return dpapi(plain,true)}
func openSecret(sealed []byte)([]byte,error){return dpapi(sealed,false)}
