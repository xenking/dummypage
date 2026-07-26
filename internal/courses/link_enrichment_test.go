package courses

import (
	"strconv"
	"strings"
	"testing"
)

func TestLoadLinkEnrichmentPolicyAndExtractEmbeddedObject(t *testing.T) {
	policy, err := LoadLinkEnrichmentPolicy(strings.NewReader(`{
		"schema_version":"link-enrichment-policy/v1",
		"max_body_bytes":1048576,
		"max_items":2,
		"stale_after":"168h",
		"rules":[{
			"host_suffixes":["assets.example.test"],
			"json_object_markers":["\"embeddedPayload\":"],
			"fields":{
				"name":"name",
				"kind":"kind",
				"size_bytes":"size",
				"file_count":"count.files",
				"folder_count":"count.folders",
				"items":"list",
				"item_name":"name",
				"item_kind":"kind",
				"item_size_bytes":"size"
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkEnrichmentPolicy() error = %v", err)
	}

	body := []byte(`<script>window.state={"ignored":{"value":"}"},"embeddedPayload":{
		"count":{"folders":1,"files":3},
		"name":"Example bundle",
		"size":42,
		"kind":"folder",
		"list":[
			{"name":"lesson.mp4","size":30,"kind":"file"},
			{"name":"notes.pdf","size":12,"kind":"file"},
			{"name":"extra.zip","size":5,"kind":"file"}
		]
	}};</script>`)
	content, found, err := policy.Extract("cdn.assets.example.test", body)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !found {
		t.Fatal("Extract() found = false, want true")
	}
	if content.Name != "Example bundle" || content.Kind != "folder" || content.SizeBytes != 42 {
		t.Fatalf("content identity = %+v", content)
	}
	if content.FileCount != 3 || content.FolderCount != 1 {
		t.Fatalf("content counts = %+v", content)
	}
	if len(content.Items) != 2 {
		t.Fatalf("items = %d, want capped 2", len(content.Items))
	}
	if content.Items[0].Name != "lesson.mp4" || content.Items[1].Name != "notes.pdf" {
		t.Fatalf("items = %+v", content.Items)
	}
	wantTypes := []string{"document", "video"}
	if strings.Join(content.MaterialTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("material_types = %v, want %v", content.MaterialTypes, wantTypes)
	}
}

func TestLinkEnrichmentExtractorHandlesEscapesAndMissingMarker(t *testing.T) {
	policy := mustLinkEnrichmentPolicy(t, 10)
	body := []byte(`<script>{"embeddedPayload":{"name":"A \"quoted\" {bundle}","kind":"folder","size":1,"count":{"files":0,"folders":0},"list":[]}}</script>`)

	content, found, err := policy.Extract("assets.example.test", body)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !found || content.Name != `A "quoted" {bundle}` {
		t.Fatalf("Extract() = (%+v, %v), want escaped name", content, found)
	}
	body = []byte(`{"embeddedPayload":"incidental marker"}<script>{"embeddedPayload":{"name":"Later bundle","kind":"folder","list":[]}}</script>`)
	content, found, err = policy.Extract("assets.example.test", body)
	if err != nil || !found || content.Name != "Later bundle" {
		t.Fatalf("later valid marker Extract() = (%+v, %v, %v)", content, found, err)
	}

	if content, found, err = policy.Extract("other.example.test", body); err != nil || found || content != nil {
		t.Fatalf("unmatched host Extract() = (%+v, %v, %v), want nil false nil", content, found, err)
	}
	if content, found, err = policy.Extract("assets.example.test", []byte(`<html></html>`)); err != nil || found || content != nil {
		t.Fatalf("missing marker Extract() = (%+v, %v, %v), want nil false nil", content, found, err)
	}
	policy.MaxBodyBytes = 1
	if _, _, err = policy.Extract("assets.example.test", body); err == nil {
		t.Fatal("oversized body Extract() error = nil")
	}
}

func TestLinkEnrichmentExtractorRejectsMalformedOrWrongTypedMetadata(t *testing.T) {
	policy := mustLinkEnrichmentPolicy(t, 10)
	tests := []struct {
		name string
		body string
	}{
		{"unterminated object", `{"embeddedPayload":{"name":"x"`},
		{"wrong size type", `{"embeddedPayload":{"name":"x","size":"large"}}`},
		{"wrong intermediate type", `{"embeddedPayload":{"name":"x","count":"bad"}}`},
		{"wrong items type", `{"embeddedPayload":{"name":"x","list":{}}}`},
		{"negative size", `{"embeddedPayload":{"name":"x","size":-1}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := policy.Extract("assets.example.test", []byte(test.body)); err == nil {
				t.Fatal("Extract() error = nil")
			}
		})
	}
}

func TestLoadLinkEnrichmentPolicyRejectsInvalidFiles(t *testing.T) {
	valid := `{
		"schema_version":"link-enrichment-policy/v1",
		"max_body_bytes":1024,
		"max_items":10,
		"stale_after":"24h",
		"rules":[{
			"host_suffixes":["assets.example.test"],
			"json_object_markers":["\"embeddedPayload\":"],
			"fields":{"name":"name","kind":"kind","size_bytes":"size","file_count":"count.files","folder_count":"count.folders","items":"list","item_name":"name","item_kind":"kind","item_size_bytes":"size"}
		}]
	}`
	tests := []struct {
		name string
		json string
	}{
		{"wrong schema", strings.Replace(valid, "link-enrichment-policy/v1", "bad", 1)},
		{"unknown field", strings.Replace(valid, `"max_items":10`, `"max_items":10,"extra":true`, 1)},
		{"duplicate key", strings.Replace(valid, `"max_items":10`, `"max_items":10,"max_items":11`, 1)},
		{"duplicate fields key", strings.Replace(valid, `"fields":{`, `"fields":{"name":"other",`, 1)},
		{"bad duration", strings.Replace(valid, `"24h"`, `"never"`, 1)},
		{"zero body cap", strings.Replace(valid, `"max_body_bytes":1024`, `"max_body_bytes":0`, 1)},
		{"zero item cap", strings.Replace(valid, `"max_items":10`, `"max_items":0`, 1)},
		{"unsafe host suffix", strings.Replace(valid, `"assets.example.test"`, `"127.0.0.1"`, 1)},
		{"empty marker", strings.Replace(valid, `"\"embeddedPayload\":"`, `""`, 1)},
		{"bad field path", strings.Replace(valid, `"count.files"`, `".count"`, 1)},
		{"item field without items", strings.Replace(valid, `"items":"list",`, `"items":"",`, 1)},
		{"items without item fields", strings.Replace(valid, `"item_name":"name","item_kind":"kind","item_size_bytes":"size"`, `"item_name":"","item_kind":"","item_size_bytes":""`, 1)},
		{"duplicate host across rules", `{"schema_version":"link-enrichment-policy/v1","max_body_bytes":1024,"max_items":10,"stale_after":"24h","rules":[{"host_suffixes":["assets.example.test"],"json_object_markers":["\"one\":"],"fields":{"name":"name"}},{"host_suffixes":["assets.example.test"],"json_object_markers":["\"two\":"],"fields":{"name":"name"}}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadLinkEnrichmentPolicy(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadLinkEnrichmentPolicy() error = nil")
			}
		})
	}
}

func mustLinkEnrichmentPolicy(t *testing.T, maxItems int) *LinkEnrichmentPolicy {
	t.Helper()
	policy, err := LoadLinkEnrichmentPolicy(strings.NewReader(strings.Replace(`{
		"schema_version":"link-enrichment-policy/v1",
		"max_body_bytes":1024,
		"max_items":MAX_ITEMS,
		"stale_after":"24h",
		"rules":[{
			"host_suffixes":["assets.example.test"],
			"json_object_markers":["\"embeddedPayload\":"],
			"fields":{"name":"name","kind":"kind","size_bytes":"size","file_count":"count.files","folder_count":"count.folders","items":"list","item_name":"name","item_kind":"kind","item_size_bytes":"size"}
		}]
	}`, "MAX_ITEMS", strconv.Itoa(maxItems), 1)))
	if err != nil {
		t.Fatalf("LoadLinkEnrichmentPolicy() error = %v", err)
	}
	return policy
}
