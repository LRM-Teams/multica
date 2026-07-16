package handler

import (
	"encoding/json"
	"testing"
)

func TestSandboxNodeTemplatesFromMetadata(t *testing.T) {
	raw := json.RawMessage(`{
		"cube_template_id": "tpl-default",
		"templates_synced_at": "2026-07-16T08:00:00Z",
		"templates": [
			{"templateID": "tpl-default", "status": "READY", "imageInfo": "img:1"},
			{"template_id": "tpl-other", "status": "BUILDING", "instanceType": "cpu.small"}
		]
	}`)
	resp := sandboxNodeTemplatesFromMetadata(raw, true)
	if !resp.NodeOnline {
		t.Fatalf("expected node online")
	}
	if resp.DefaultTemplateID != "tpl-default" {
		t.Fatalf("default = %q", resp.DefaultTemplateID)
	}
	if resp.SyncedAt != "2026-07-16T08:00:00Z" {
		t.Fatalf("synced_at = %q", resp.SyncedAt)
	}
	if len(resp.Templates) != 2 {
		t.Fatalf("len(templates) = %d", len(resp.Templates))
	}
	if !resp.Templates[0].IsDefault || resp.Templates[0].TemplateID != "tpl-default" {
		t.Fatalf("first template = %+v", resp.Templates[0])
	}
	if resp.Templates[1].IsDefault || resp.Templates[1].InstanceType != "cpu.small" {
		t.Fatalf("second template = %+v", resp.Templates[1])
	}
}

func TestSandboxNodeTemplatesFromMetadataEmpty(t *testing.T) {
	resp := sandboxNodeTemplatesFromMetadata(nil, false)
	if resp.NodeOnline {
		t.Fatalf("expected offline")
	}
	if resp.Templates == nil || len(resp.Templates) != 0 {
		t.Fatalf("expected empty non-nil templates, got %#v", resp.Templates)
	}
}
