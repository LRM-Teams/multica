package memorygraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec §13: scope and resource-limit failures are auditable safe fallbacks;
// neither path may expose project-visible source text.
func TestSourceToolFailClosedCanaryAuditsFallbacks(t *testing.T) {
	t.Run("resource limit", func(t *testing.T) {
		store := newTestStore(t)
		_, id := appendMediaFile(t, store, "bomb.zip", "application/zip")
		limits := DefaultMediaLimits()
		limits.MaxDecodedBytes = 1 << 20
		payload := loadMedia(t, store, id, 1, map[string][]byte{
			"bomb.zip": makeZip(t, map[string][]byte{"secret.txt": make([]byte, 10<<20)}),
		}, limits)
		if payload.State != MediaEvidenceTruncated || strings.Contains(string(payload.Text), "secret") {
			t.Fatalf("limit fallback = %+v", payload)
		}
		audit, err := os.ReadFile(filepath.Join(store.Root, "shared", "sources", "audit.jsonl"))
		if err != nil || !strings.Contains(string(audit), "truncated") {
			t.Fatalf("limit audit = %q, err = %v", audit, err)
		}
	})
	t.Run("scope", func(t *testing.T) {
		store := newTestStore(t)
		identity := stampGraphIdentity(t, store, GraphDirKindChannel)
		_, id := appendMediaFile(t, store, "channel.txt", "text/plain")
		payload, err := (MediaLoader{Resolver: scriptedMediaResolver{"channel.txt": []byte("project-visible secret")}, Limits: DefaultMediaLimits()}).Load(
			store, GraphView{AllowProject: true, ChannelID: identity.OwnerID + "-other"}, id, 1)
		if err != nil || payload.State != MediaEvidenceDenied || len(payload.Text) != 0 {
			t.Fatalf("scope fallback = %+v, err = %v", payload, err)
		}
		audit, err := os.ReadFile(filepath.Join(store.Root, "shared", "sources", "audit.jsonl"))
		if err != nil || !strings.Contains(string(audit), "denied") {
			t.Fatalf("scope audit = %q, err = %v", audit, err)
		}
	})
}
