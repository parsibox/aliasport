package main

import (
 "encoding/json"
 "fmt"
 "net/url"
 "strings"
 "testing"
)

type providerPresetCatalog struct {
 SchemaVersion int `json:"schema_version"`
 Presets []struct {
  ID string `json:"id"`
  Name string `json:"name"`
  BaseURL string `json:"base_url"`
  APIType string `json:"api_type"`
  Description string `json:"description"`
  DocumentationURL string `json:"documentation_url"`
 } `json:"presets"`
}

// Validate the exact catalog embedded into the Windows executable. Invalid
// additions should fail at build time rather than break the provider picker.
func TestEmbeddedProviderPresets(t *testing.T) {
 content,err:=webAssets.ReadFile("web/provider-presets.json")
 if err!=nil{t.Fatal("Provider preset catalog is not embedded:",err)}
 var catalog providerPresetCatalog
 if err=json.Unmarshal(content,&catalog);err!=nil{t.Fatal("Invalid provider-presets.json:",err)}
 if catalog.SchemaVersion!=1{t.Fatalf("unsupported schema version %d",catalog.SchemaVersion)}
 if len(catalog.Presets)<3{t.Fatal("expected initial provider presets")}
 var raw struct {
  Presets []map[string]json.RawMessage `json:"presets"`
 }
 if err=json.Unmarshal(content,&raw);err!=nil{t.Fatal(err)}
 ids:=make(map[string]bool)
 for i,p:=range catalog.Presets {
  tag:=fmt.Sprintf("entry %d (%s)",i,p.ID)
  if strings.TrimSpace(p.ID)==""||ids[p.ID]{t.Errorf("%s has empty or duplicate ID",tag)}
  ids[p.ID]=true
  if err=validateProvider(Provider{Name:p.Name,BaseURL:p.BaseURL});err!=nil{
   t.Errorf("%s: invalid URL or name: %v",tag,err)
  }
  if err=validateProtocol(p.APIType);err!=nil{t.Errorf("%s: %v",tag,err)}
  if strings.TrimSpace(p.Description)==""{t.Errorf("%s lacks a description",tag)}
  if p.DocumentationURL!=""{
   u,e:=url.Parse(p.DocumentationURL)
   if e!=nil||u.Scheme!="https"||u.Hostname()==""{
    t.Errorf("%s: invalid documentation URL",tag)
   }
  }
  for _,secretField:=range []string{"api_key","secret","client_key","token","authorization"}{
   if _,exists:=raw.Presets[i][secretField];exists{
    t.Errorf("%s must not include credentials (%s)",tag,secretField)
   }
  }
 }
}

func TestProviderCatalogIsServedByEmbeddedStaticAssets(t *testing.T) {
 page,err:=webAssets.ReadFile("web/index.html")
 if err!=nil{t.Fatal(err)}
 for _,expected:=range []string{
  "provider-presets.json",
  "openProviderPresetPicker()",
  "applyProviderPreset(id)",
  "f-base_url",
  "f-api_type",
 } {
  if !strings.Contains(string(page),expected){t.Errorf("provider form is missing %q",expected)}
 }
 if strings.Contains(string(page),"آدرس ZRO و کلید API واقعی خود را ثبت کنید."){
  t.Fatal("the dashboard still contains provider-specific onboarding copy")
 }
}
