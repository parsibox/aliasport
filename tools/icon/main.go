// Command icon generates AliasPort's multi-resolution Windows icon and favicon.
// No Photoshop, ImageMagick or other graphics dependencies are required.
package main

import (
 "bytes"
 "encoding/binary"
 "fmt"
 "image"
 "image/color"
 "image/png"
 "math"
 "os"
 "path/filepath"
)

type point struct{x,y float64}
type tone struct{r,g,b,a float64}

func bound(v float64)float64{if v<0{return 0};if v>1{return 1};return v}
func lerp(a,b,t float64)float64{return a+(b-a)*bound(t)}
func interpolate(a,b tone,t float64)tone{
 return tone{lerp(a.r,b.r,t),lerp(a.g,b.g,t),lerp(a.b,b.b,t),lerp(a.a,b.a,t)}
}
func compose(img *image.RGBA,x,y int,src tone) {
 c:=img.RGBAAt(x,y)
 k:=bound(src.a)
 img.SetRGBA(x,y,color.RGBA{
  uint8(lerp(float64(c.R),src.r,k)+.5),
  uint8(lerp(float64(c.G),src.g,k)+.5),
  uint8(lerp(float64(c.B),src.b,k)+.5),
  uint8((k+(1-k)*float64(c.A)/255)*255+.5),
 })
}
func roundCoverage(x,y,l,t,r,b,radius float64)float64{
 px:=math.Max(l+radius,math.Min(x,r-radius))
 py:=math.Max(t+radius,math.Min(y,b-radius))
 return bound(1-(math.Hypot(x-px,y-py)-radius))
}
func polyHit(x,y float64,vertices []point)bool{
 inside:=false
 for i,j:=0,len(vertices)-1;i<len(vertices);j,i=i,i+1 {
  a,b:=vertices[i],vertices[j]
  if (a.y>y)!=(b.y>y) && x<(b.x-a.x)*(y-a.y)/(b.y-a.y)+a.x {inside=!inside}
 }
 return inside
}
func polygon(img *image.RGBA,vertices []point,top,bottom tone) {
 for y:=0;y<512;y++ { for x:=0;x<512;x++ {
  fX,fY:=float64(x)+.5,float64(y)+.5
  alpha:=0.
  for _,off:=range []point{{-.3,-.3},{.3,-.3},{-.3,.3},{.3,.3}} {
   if polyHit(fX+off.x,fY+off.y,vertices){alpha+=.25}
  }
  if alpha>0 {
   c:=interpolate(top,bottom,fY/512);c.a=alpha
   compose(img,x,y,c)
  }
 }}
}
func pathDistance(x,y,aX,aY,bX,bY float64)float64{
 dX,dY:=bX-aX,bY-aY
 denom:=dX*dX+dY*dY
 if denom==0{return math.Hypot(x-aX,y-aY)}
 fraction:=bound(((x-aX)*dX+(y-aY)*dY)/denom)
 return math.Hypot(x-aX-fraction*dX,y-aY-fraction*dY)
}
func path(img *image.RGBA,vertices []point,radius float64,first,last tone){
 for i:=0;i<len(vertices)-1;i++ {
  a,b:=vertices[i],vertices[i+1]
  minX:=int(math.Max(0,math.Floor(math.Min(a.x,b.x)-radius-2)))
  maxX:=int(math.Min(512,math.Ceil(math.Max(a.x,b.x)+radius+2)))
  minY:=int(math.Max(0,math.Floor(math.Min(a.y,b.y)-radius-2)))
  maxY:=int(math.Min(512,math.Ceil(math.Max(a.y,b.y)+radius+2)))
  c:=interpolate(first,last,float64(i)/float64(len(vertices)-1))
  for y:=minY;y<maxY;y++ {for x:=minX;x<maxX;x++{
   aa:=bound(radius+.5-pathDistance(float64(x)+.5,float64(y)+.5,a.x,a.y,b.x,b.y))
   if aa>0{p:=c;p.a=aa;compose(img,x,y,p)}
  }}
 }
}
func rect(img *image.RGBA,l,t,r,b,radius float64,top,bottom tone){
 for y:=int(t-1);y<int(b+1);y++ {for x:=int(l-1);x<int(r+1);x++{
  if x<0||y<0||x>=512||y>=512{continue}
  alpha:=roundCoverage(float64(x)+.5,float64(y)+.5,l,t,r,b,radius)
  if alpha>0{c:=interpolate(top,bottom,(float64(y)-t)/(b-t));c.a*=alpha;compose(img,x,y,c)}
 }}
}
func dot(img *image.RGBA,x,y,r float64,c tone){
 for py:=int(y-r-2);py<int(y+r+2);py++ {for px:=int(x-r-2);px<int(x+r+2);px++{
  if px<0||py<0||px>=512||py>=512{continue}
  aa:=bound(r+.5-math.Hypot(float64(px)+.5-x,float64(py)+.5-y))
  if aa>0{t:=c;t.a*=aa;compose(img,px,py,t)}
 }}
}
func draw()*image.RGBA{
 img:=image.NewRGBA(image.Rect(0,0,512,512))
 for y:=0;y<512;y++{for x:=0;x<512;x++{
  fX,fY:=float64(x)+.5,float64(y)+.5
  alpha:=roundCoverage(fX,fY,18,18,494,494,105)
  if alpha<=0{continue}
  base:=interpolate(tone{18,84,153,1},tone{24,19,82,1},(fX*.38+fY*.67)/512)
  blue:=math.Exp(-(math.Pow((fX-128)/160,2)+math.Pow((fY-92)/130,2)))
  purple:=math.Exp(-(math.Pow((fX-434)/210,2)+math.Pow((fY-425)/220,2)))
  base=interpolate(base,tone{11,178,236,1},.30*blue)
  base=interpolate(base,tone{103,38,194,1},.42*purple)
  img.SetRGBA(x,y,color.RGBA{uint8(base.r),uint8(base.g),uint8(base.b),uint8(alpha*255+.5)})
 }}
 polygon(img,[]point{{89,365},{215,126},{259,126},{169,365}},tone{252,250,255,1},tone{204,171,252,1})
 polygon(img,[]point{{218,126},{273,126},{371,326},{321,350}},tone{253,253,255,1},tone{180,168,244,1})
 curve:=make([]point,65)
 for i:=0;i<len(curve);i++{
  t:=float64(i)/64;u:=1-t
  curve[i]=point{u*u*u*115+3*u*u*t*180+3*u*t*t*348+t*t*t*401,
   u*u*u*361+3*u*u*t*322+3*u*t*t*250+t*t*t*259}
 }
 path(img,curve,17,tone{200,92,242,1},tone{30,214,245,1})
 rect(img,234,351,418,405,18,tone{229,233,255,1},tone{138,161,231,1})
 rect(img,242,360,411,397,12,tone{60,83,147,1},tone{27,43,107,1})
 rect(img,234,415,418,469,18,tone{207,211,255,1},tone{124,140,229,1})
 rect(img,242,424,411,461,12,tone{57,78,144,1},tone{29,35,101,1})
 for _,y:=range []float64{379,443}{
  dot(img,369,y,8.5,tone{68,236,252,1})
  dot(img,394,y,8.5,tone{68,236,252,1})
 }
 return img
}
func shrink(src image.Image,size int)*image.RGBA{
 dst:=image.NewRGBA(image.Rect(0,0,size,size))
 for y:=0;y<size;y++ {for x:=0;x<size;x++{
  sx0,sx1:=float64(x)*512/float64(size),float64(x+1)*512/float64(size)
  sy0,sy1:=float64(y)*512/float64(size),float64(y+1)*512/float64(size)
  var red,green,blue,alpha,total float64
  for py:=int(math.Floor(sy0));py<int(math.Ceil(sy1));py++{
   wy:=math.Min(float64(py+1),sy1)-math.Max(float64(py),sy0)
   for px:=int(math.Floor(sx0));px<int(math.Ceil(sx1));px++{
    wx:=math.Min(float64(px+1),sx1)-math.Max(float64(px),sx0)
    c:=color.RGBAModel.Convert(src.At(px,py)).(color.RGBA)
    weight:=wx*wy
    red+=float64(c.R)*weight;green+=float64(c.G)*weight;blue+=float64(c.B)*weight;alpha+=float64(c.A)*weight;total+=weight
   }
  }
  dst.SetRGBA(x,y,color.RGBA{uint8(red/total+.5),uint8(green/total+.5),uint8(blue/total+.5),uint8(alpha/total+.5)})
 }}
 return dst
}
func savePNG(filename string,img image.Image)error{
 if err:=os.MkdirAll(filepath.Dir(filename),0755);err!=nil{return err}
 f,err:=os.Create(filename);if err!=nil{return err};defer f.Close()
 return png.Encode(f,img)
}
func saveICO(filename string,img image.Image)error{
 sizes:=[]int{16,24,32,48,64,128,256}
 pngs:=make([][]byte,len(sizes))
 for i,size:=range sizes{
  var b bytes.Buffer
  if err:=png.Encode(&b,shrink(img,size));err!=nil{return err}
  pngs[i]=b.Bytes()
 }
 if err:=os.MkdirAll(filepath.Dir(filename),0755);err!=nil{return err}
 f,err:=os.Create(filename);if err!=nil{return err};defer f.Close()
 if err:=binary.Write(f,binary.LittleEndian,[3]uint16{0,1,uint16(len(sizes))});err!=nil{return err}
 offset:=uint32(6+16*len(sizes))
 for i,size:=range sizes{
  header:=struct{Width,Height,Colors,Reserved uint8;Planes,BPP uint16;Bytes,Offset uint32}{
   uint8(size),uint8(size),0,0,1,32,uint32(len(pngs[i])),offset,
  }
  if err:=binary.Write(f,binary.LittleEndian,header);err!=nil{return err}
  offset+=uint32(len(pngs[i]))
 }
 for _,p:=range pngs{if _,err:=f.Write(p);err!=nil{return err}}
 return nil
}
func main(){
 src:=draw()
 outputs:=[]struct{filename string;generate func(string)error}{
  {"assets/aliasport.ico",func(name string)error{return saveICO(name,src)}},
  {"assets/aliasport.png",func(name string)error{return savePNG(name,shrink(src,256))}},
  {"cmd/aliasport/web/favicon.png",func(name string)error{return savePNG(name,shrink(src,64))}},
 }
 for _,output:=range outputs{
  if err:=output.generate(output.filename);err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}
  fmt.Println("Generated",output.filename)
 }
}
