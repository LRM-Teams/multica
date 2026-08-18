package computer

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkJournalDeniedRepoRootSkipsNoiseAndKeepsApp(t *testing.T) {
	if WorkJournalDeniedRepoRoot("/home/owner/code/app") {
		t.Fatal("ordinary app repo was denied")
	}
	if !WorkJournalDeniedRepoRoot("/home/owner/code/app/node_modules/pkg") {
		t.Fatal("repo under node_modules was accepted")
	}
	if !WorkJournalDeniedRepoRoot("/home/owner/.ssh") {
		t.Fatal(".ssh repo root was accepted")
	}
	if !WorkJournalDeniedRepoRoot("/home/owner/code/app/.next/cache-repo") {
		t.Fatal("repo under .next was accepted")
	}
	if !WorkJournalDeniedRepoRoot("/home/owner/code/app/dist/pkg") {
		t.Fatal("repo under dist was accepted")
	}
	if !WorkJournalDeniedRepoRoot("/home/owner/.gnupg/keys") {
		t.Fatal(".gnupg repo root was accepted")
	}
}

func TestWorkJournalDeniedDirtyPathDropsSecretsAndKeepsSource(t *testing.T) {
	if WorkJournalDeniedDirtyPath("internal/auth/sso.go") {
		t.Fatal("source file was denied")
	}
	if !WorkJournalDeniedDirtyPath(".env") {
		t.Fatal(".env dirty path was accepted")
	}
	if !WorkJournalDeniedDirtyPath(".env.local") {
		t.Fatal(".env.local dirty path was accepted")
	}
	if !WorkJournalDeniedDirtyPath("secrets/id_rsa") {
		t.Fatal("id_rsa dirty path was accepted")
	}
	if !WorkJournalDeniedDirtyPath("certs/prod.pem") {
		t.Fatal("pem dirty path was accepted")
	}
	if !WorkJournalDeniedDirtyPath("config/credentials.json") {
		t.Fatal("credentials.json dirty path was accepted")
	}
	if !WorkJournalDeniedDirtyPath("node_modules/left-pad/index.js") {
		t.Fatal("node_modules dirty path was accepted")
	}
}

func TestFilterWorkJournalDirtyPathsDropsDeniedAndKeepsRepo(t *testing.T) {
	kept := FilterWorkJournalDirtyPaths([]protocol.WorkDigestDirtyPath{
		{Path: "internal/auth/sso.go", Status: protocol.WorkDigestDirtyModified},
		{Path: ".env", Status: protocol.WorkDigestDirtyUntracked},
		{Path: "node_modules/pkg/index.js", Status: protocol.WorkDigestDirtyModified},
	})
	if len(kept) != 1 || kept[0].Path != "internal/auth/sso.go" {
		t.Fatalf("kept %#v, want only sso.go", kept)
	}
}
