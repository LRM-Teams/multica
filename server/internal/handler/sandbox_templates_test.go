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

func TestSandboxNodeDockerImagesFromMetadata(t *testing.T) {
	raw := json.RawMessage(`{
		"docker_images_synced_at": "2026-07-29T08:00:00Z",
		"docker_images": [
			{"image_ref": "multica/runtime:dev", "repository": "multica/runtime", "tag": "dev", "id": "sha256:abc", "size": "1.2GB"},
			{"repository": "ubuntu", "tag": "24.04", "ID": "sha256:def", "createdSince": "2 weeks ago"}
		]
	}`)
	resp := sandboxNodeDockerImagesFromMetadata(raw, true)
	if !resp.NodeOnline {
		t.Fatalf("expected node online")
	}
	if resp.SyncedAt != "2026-07-29T08:00:00Z" {
		t.Fatalf("synced_at = %q", resp.SyncedAt)
	}
	if len(resp.Images) != 2 {
		t.Fatalf("len(images) = %d", len(resp.Images))
	}
	if resp.Images[0].ImageRef != "multica/runtime:dev" || resp.Images[0].Size != "1.2GB" {
		t.Fatalf("first image = %+v", resp.Images[0])
	}
	if resp.Images[1].ImageRef != "ubuntu:24.04" || resp.Images[1].ID != "sha256:def" || resp.Images[1].CreatedSince != "2 weeks ago" {
		t.Fatalf("second image = %+v", resp.Images[1])
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

func TestMergeSandboxNodeHeartbeatMetadataPreservesDefaultTemplate(t *testing.T) {
	existing := json.RawMessage(`{"cube_template_id":"tpl-user","cube_domain":"old.app"}`)
	incoming := json.RawMessage(`{
		"cube_template_id":"tpl-from-sandboxd",
		"cube_domain":"cube.app",
		"templates":[{"templateID":"tpl-a","status":"READY"}],
		"templates_synced_at":"2026-07-16T09:00:00Z",
		"docker_images":[{"image_ref":"multica/runtime:dev"}],
		"docker_images_synced_at":"2026-07-29T08:00:00Z"
	}`)
	merged := mergeSandboxNodeHeartbeatMetadata(existing, incoming)
	var meta map[string]any
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["cube_template_id"] != "tpl-user" {
		t.Fatalf("cube_template_id = %v, want tpl-user", meta["cube_template_id"])
	}
	if meta["cube_domain"] != "cube.app" {
		t.Fatalf("cube_domain = %v", meta["cube_domain"])
	}
	if meta["templates_synced_at"] != "2026-07-16T09:00:00Z" {
		t.Fatalf("templates_synced_at = %v", meta["templates_synced_at"])
	}
	if meta["docker_images_synced_at"] != "2026-07-29T08:00:00Z" {
		t.Fatalf("docker_images_synced_at = %v", meta["docker_images_synced_at"])
	}
	if images, _ := meta["docker_images"].([]any); len(images) != 1 {
		t.Fatalf("docker_images = %#v", meta["docker_images"])
	}
}

func TestMergeSandboxNodeHeartbeatMetadataClearsStaleDockerImagesError(t *testing.T) {
	existing := json.RawMessage(`{
		"docker_images_error": "docker image ls: permission denied",
		"docker_images": [{"image_ref": "multica/runtime:old"}]
	}`)
	incoming := json.RawMessage(`{
		"docker_images": [{"image_ref": "multica/runtime:dev"}],
		"docker_images_synced_at": "2026-07-29T09:00:00Z",
		"docker_images_error": ""
	}`)
	merged := mergeSandboxNodeHeartbeatMetadata(existing, incoming)
	var meta map[string]any
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatal(err)
	}
	if errMsg, _ := meta["docker_images_error"].(string); errMsg != "" {
		t.Fatalf("docker_images_error = %q, want empty", errMsg)
	}
}

func TestMergeSandboxNodeHeartbeatMetadataClearsStaleDockerImagesErrorFromLegacyHeartbeat(t *testing.T) {
	existing := json.RawMessage(`{
		"docker_images_error": "docker image ls: ",
		"docker_images": []
	}`)
	incoming := json.RawMessage(`{
		"docker_images": [{"image_ref": "cube-leagent-template-test:local"}],
		"docker_images_synced_at": "2026-07-29T09:00:00Z"
	}`)
	merged := mergeSandboxNodeHeartbeatMetadata(existing, incoming)
	var meta map[string]any
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatal(err)
	}
	if _, ok := meta["docker_images_error"]; ok {
		t.Fatalf("docker_images_error = %v, want key removed", meta["docker_images_error"])
	}
}

func TestSandboxNodeDockerImagesFromMetadataIncludesStaleErrorField(t *testing.T) {
	raw := json.RawMessage(`{
		"docker_images_error": "docker image ls: ",
		"docker_images_synced_at": "2026-07-29T08:00:00Z",
		"docker_images": [
			{"image_ref": "multica/runtime:dev", "repository": "multica/runtime", "tag": "dev"}
		]
	}`)
	resp := sandboxNodeDockerImagesFromMetadata(raw, true)
	if resp.Error != "docker image ls:" {
		t.Fatalf("error = %q", resp.Error)
	}
	if len(resp.Images) != 1 {
		t.Fatalf("len(images) = %d", len(resp.Images))
	}
}
