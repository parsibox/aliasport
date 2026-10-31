package main

import (
 "regexp"
 "strings"
 "testing"
)

// CSS grid items with margin:auto on both axes are vertically centered when
// their content is shorter than the sidebar / viewport. All pages share one
// main container, so it must keep only horizontal auto margins.
func TestAllPagesStartAtTopInsteadOfVerticalCenter(t *testing.T) {
 content,err:=webAssets.ReadFile("web/index.html")
 if err!=nil{t.Fatal(err)}
 html:=string(content)
 mainRule:=regexp.MustCompile(`\.main\{([^}]*)\}`).FindStringSubmatch(html)
 if len(mainRule)!=2{t.Fatal("missing common .main CSS rule")}
 css:=mainRule[1]
 if strings.Contains(css,"margin:auto")||strings.Contains(css,"margin: auto"){
  t.Fatal(".main must not use vertical auto margins: short pages drift down")
 }
 if !strings.Contains(css,"margin:0 auto")||!strings.Contains(css,"align-self:start"){
  t.Fatalf("main layout must remain top-aligned with horizontal centering; CSS: %s",css)
 }
 if strings.Count(html,`<main class="main">`)!=1{
  t.Fatal("the application must use a single shared main layout")
 }
 for _,page:=range []string{"dashboard","providers","gateways","keys","aliases","events","settings"} {
  if !strings.Contains(html,`<section id="`+page+`" class="page`){
   t.Errorf("page %s does not use the shared main layout",page)
  }
 }
}
