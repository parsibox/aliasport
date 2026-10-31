package main

import (
 "bytes"
 "encoding/binary"
 "image"
 "image/color"
 "image/png"
 "os"
 "path/filepath"
 "testing"
)

func TestArtworkHasVisiblePixels(t *testing.T) {
 icon:=draw()
 center:=icon.RGBAAt(256,256)
 if center.A==0 {t.Fatal("app icon is transparent in its center")}
 corner:=icon.RGBAAt(0,0)
 if corner.A!=0 {t.Fatal("outer corner should be transparent")}
 small:=shrink(icon,16)
 if small.Bounds()!=image.Rect(0,0,16,16){t.Fatalf("16px icon has unexpected size: %v",small.Bounds())}
 for _,p:=range []image.Point{{8,8},{8,12}} {
  if c:=color.RGBAModel.Convert(small.At(p.X,p.Y)).(color.RGBA);c.A==0{
   t.Fatalf("16px icon is transparent at %v",p)
  }
 }
}

func TestICOContainsSevenValidPNGResolutions(t *testing.T){
 path:=filepath.Join(t.TempDir(),"AliasPort.ico")
 if err:=saveICO(path,draw());err!=nil{t.Fatal(err)}
 raw,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
 if len(raw)<118{t.Fatal("ICO file is too small")}
 if reserved:=binary.LittleEndian.Uint16(raw[:2]);reserved!=0{t.Fatalf("invalid ICO reserved header: %d",reserved)}
 if typ:=binary.LittleEndian.Uint16(raw[2:4]);typ!=1{t.Fatalf("ICO type must be icon: %d",typ)}
 if number:=binary.LittleEndian.Uint16(raw[4:6]);number!=7{t.Fatalf("expected seven resolutions, got %d",number)}
 sizes:=[]int{16,24,32,48,64,128,256}
 for idx,want:=range sizes{
  base:=6+idx*16
  width:=int(raw[base]);if width==0{width=256}
  height:=int(raw[base+1]);if height==0{height=256}
  if width!=want||height!=want{t.Fatalf("resolution %d should be %dpx, got %dx%d",idx,want,width,height)}
  length:=binary.LittleEndian.Uint32(raw[base+8:base+12])
  offset:=binary.LittleEndian.Uint32(raw[base+12:base+16])
  if uint64(offset)+uint64(length)>uint64(len(raw)){t.Fatalf("resolution %d points outside ICO file",idx)}
  data:=raw[offset:offset+length]
  if !bytes.HasPrefix(data,[]byte{137,'P','N','G',13,10,26,10}){t.Fatalf("resolution %d is not a PNG",idx)}
  cfg,err:=png.DecodeConfig(bytes.NewReader(data))
  if err!=nil{t.Fatal(err)}
  if cfg.Width!=want||cfg.Height!=want{t.Fatalf("PNG resolution %d mismatch: %dx%d",idx,cfg.Width,cfg.Height)}
 }
}
