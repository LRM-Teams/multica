package main

import (
	"testing"

	"github.com/google/uuid"
)

func TestResolveAttachmentViewID(t *testing.T) {
	t.Parallel()

	id, err := resolveAttachmentViewID("abc", "")
	if err != nil || id != "abc" {
		t.Fatalf("positional: id=%q err=%v", id, err)
	}

	id, err = resolveAttachmentViewID("", "def")
	if err != nil || id != "def" {
		t.Fatalf("flag: id=%q err=%v", id, err)
	}

	if _, err := resolveAttachmentViewID("a", "b"); err == nil {
		t.Fatal("expected error when both positional and --id are set")
	}

	if _, err := resolveAttachmentViewID("", ""); err == nil {
		t.Fatal("expected error when neither id is set")
	}

	if _, err := resolveAttachmentViewID("  ", "  "); err == nil {
		t.Fatal("expected error for whitespace-only ids")
	}
}

func TestAttachmentCmdRegistersViewAndUploadNotDownload(t *testing.T) {
	t.Parallel()

	var names []string
	for _, c := range attachmentCmd.Commands() {
		names = append(names, c.Name())
	}
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has("view") {
		t.Fatalf("expected view subcommand, got %v", names)
	}
	if !has("upload") {
		t.Fatalf("expected upload subcommand, got %v", names)
	}
	if has("download") {
		t.Fatalf("download must be removed, got %v", names)
	}
}

func TestResolveChannelIDFromUploadTarget_UUID(t *testing.T) {
	t.Parallel()
	id := uuid.New().String()
	got, err := resolveChannelIDFromUploadTarget(t.Context(), nil, id)
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("got %q want %q", got, id)
	}
}

func TestResolveChannelIDFromUploadTarget_Empty(t *testing.T) {
	t.Parallel()
	got, err := resolveChannelIDFromUploadTarget(t.Context(), nil, "")
	if err != nil || got != "" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestResolveChannelIDFromUploadTarget_DMRejected(t *testing.T) {
	t.Parallel()
	_, err := resolveChannelIDFromUploadTarget(t.Context(), nil, "dm:@alice")
	if err == nil {
		t.Fatal("expected error for dm target")
	}
}

func TestResolveChannelIDFromUploadTarget_ThreadRejected(t *testing.T) {
	t.Parallel()
	_, err := resolveChannelIDFromUploadTarget(t.Context(), nil, "#eng:msg-123")
	if err == nil {
		t.Fatal("expected error for thread target")
	}
}
