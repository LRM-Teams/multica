package handler

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Beckham's directed @mention nudges must carry the "主动发现" prefix (so they get
// the proactive pill and render as a normal main-timeline message) while the
// mention reference span still points exactly at the @label within content.
func TestDirectedAgentMentionContentBadgedAndSpanned(t *testing.T) {
	agent := db.Agent{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Name: "阿策"}
	content, parts := directedAgentMentionContent(agent, "去处理 #109 规则审核")

	if !strings.HasPrefix(content, "主动发现：") {
		t.Fatalf("content must start with the proactive prefix, got %q", content)
	}
	if len(parts) != 1 || parts[0].Type != "reference" || parts[0].RefType != "mention" {
		t.Fatalf("expected one mention reference part, got %+v", parts)
	}
	if parts[0].ContentStartUTF16 == nil || parts[0].ContentEndUTF16 == nil {
		t.Fatal("mention part is missing content span offsets")
	}
	u16 := utf16.Encode([]rune(content))
	start, end := int(*parts[0].ContentStartUTF16), int(*parts[0].ContentEndUTF16)
	if start < 0 || end > len(u16) || start >= end {
		t.Fatalf("invalid span [%d,%d) for content len %d", start, end, len(u16))
	}
	spanned := string(utf16.Decode(u16[start:end]))
	if spanned != parts[0].Label {
		t.Fatalf("mention span %q does not match label %q (offsets not shifted past prefix)", spanned, parts[0].Label)
	}
	if !strings.Contains(content, "去处理 #109") {
		t.Fatalf("directive body missing from content: %q", content)
	}
}
