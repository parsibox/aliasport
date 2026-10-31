package main

import (
 "regexp"
 "strings"
 "testing"
)

func TestEveryDialogHasTopLeftAccessibleCloseButton(t *testing.T) {
 content,err:=webAssets.ReadFile("web/index.html")
 if err!=nil{t.Fatal(err)}
 html:=string(content)
 dialogs:=regexp.MustCompile(`(?s)<dialog\b[^>]*\bid="([^"]+)"[^>]*>(.*?)</dialog>`)
 matches:=dialogs.FindAllStringSubmatch(html,-1)
 expected:=map[string]bool{
  "editor":true,
  "provider-preset-picker":true,
  "model-picker":true,
  "diagnostic-dialog":true,
  "key-dialog":true,
 }
 if len(matches)!=len(expected){
  t.Fatalf("dialog count changed: found %d, expected %d; update close button coverage",len(matches),len(expected))
 }
 for _,match:=range matches{
  id:=match[1]
  if !expected[id]{t.Errorf("unexpected dialog without review: %s",id);continue}
  closeCommand:="closeAliasPortDialog('"+id+"')"
  if strings.Count(match[2],`class="dialog-close"`)!=1{
   t.Errorf("%s requires exactly one upper-left close button",id)
  }
  if !strings.Contains(match[2],closeCommand){
   t.Errorf("%s close button does not target its own dialog",id)
  }
  if !strings.Contains(match[2],`aria-label="بستن پنجره"`){
   t.Errorf("%s close button is missing its accessible label",id)
  }
  if !strings.Contains(match[2],`type="button"`){
   t.Errorf("%s close button may accidentally submit a form",id)
  }
  delete(expected,id)
 }
 if len(expected)>0{t.Fatalf("missing dialogs: %+v",expected)}
 if !strings.Contains(html,".dialog-close{position:absolute;top:15px;left:16px"){
  t.Fatal("close buttons must be placed in the top-left corner, including RTL layouts")
 }
 if !strings.Contains(html,"function closeAliasPortDialog(id)"){
  t.Fatal("unified dialog close handler missing")
 }
}
