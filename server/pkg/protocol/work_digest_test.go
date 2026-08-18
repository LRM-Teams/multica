package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func validWorkDigest() WorkDigest {
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	return WorkDigest{
		ComputerID: "computer-1",
		Window: WorkDigestWindow{
			Start: start,
			End:   start.Add(7 * 24 * time.Hour),
		},
		Repos: []WorkDigestRepo{{
			Root:    "/home/owner/code/app",
			Remotes: []string{"git@github.com:org/app.git"},
			Commits: []WorkDigestCommit{{
				Hash:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				At:         start.Add(2 * time.Hour),
				Author:     "owner",
				Subject:    "wire SSO login",
				FileCount:  3,
				Insertions: 40,
				Deletions:  8,
			}},
			Dirty: []WorkDigestDirtyPath{{
				Path:   "internal/auth/sso.go",
				Status: WorkDigestDirtyModified,
			}},
		}},
	}
}

func TestWorkDigestValidateAcceptsBoundedMetadataOnlyDigest(t *testing.T) {
	digest := validWorkDigest()
	if err := digest.Validate(); err != nil {
		t.Fatalf("valid digest rejected: %v", err)
	}
}

func TestParseWorkDigestAcceptsMetadataOnlyJSON(t *testing.T) {
	raw, err := json.Marshal(validWorkDigest())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseWorkDigest(raw)
	if err != nil {
		t.Fatalf("metadata digest rejected: %v", err)
	}
	if got.ComputerID != "computer-1" || len(got.Repos) != 1 || got.Repos[0].Commits[0].Subject != "wire SSO login" {
		t.Fatalf("parsed digest lost metadata: %+v", got)
	}
}

func TestParseWorkDigestRejectsFileBodyFields(t *testing.T) {
	base := `{
  "computer_id": "computer-1",
  "window": {"start": "2026-08-10T00:00:00Z", "end": "2026-08-17T00:00:00Z"},
  "repos": [{
    "root": "/home/owner/code/app",
    "commits": [{"hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "at": "2026-08-10T02:00:00Z", "author": "owner", "subject": "wire SSO", "file_count": 1, "insertions": 4, "deletions": 0}],
    "dirty": [{"path": "main.go", "status": "modified"}]
  }]
}`
	cases := []struct {
		name string
		raw  string
	}{
		{name: "top-level content", raw: `{"computer_id":"computer-1","window":{"start":"2026-08-10T00:00:00Z","end":"2026-08-17T00:00:00Z"},"content":"secret"}`},
		{name: "repo diff", raw: strings.Replace(base, `"root": "/home/owner/code/app"`, `"root": "/home/owner/code/app", "diff": "--- a\n+++ b"`, 1)},
		{name: "commit patch", raw: strings.Replace(base, `"deletions": 0}`, `"deletions": 0, "patch": "+secret"}`, 1)},
		{name: "dirty body", raw: strings.Replace(base, `"status": "modified"}`, `"status": "modified", "body": "package main"}`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseWorkDigest([]byte(tc.raw)); err == nil {
				t.Fatal("file-body JSON was accepted")
			}
		})
	}
}

func TestWorkDigestValidateRejectsOverLimit(t *testing.T) {
	t.Run("201 commits", func(t *testing.T) {
		digest := validWorkDigest()
		commits := make([]WorkDigestCommit, 201)
		for i := range commits {
			commits[i] = digest.Repos[0].Commits[0]
		}
		digest.Repos[0].Commits = commits
		if err := digest.Validate(); err == nil {
			t.Fatal("201 commits were accepted")
		}
	})
	t.Run("201 dirty paths", func(t *testing.T) {
		digest := validWorkDigest()
		dirty := make([]WorkDigestDirtyPath, 201)
		for i := range dirty {
			dirty[i] = digest.Repos[0].Dirty[0]
		}
		digest.Repos[0].Dirty = dirty
		if err := digest.Validate(); err == nil {
			t.Fatal("201 dirty paths were accepted")
		}
	})
	t.Run("201-rune subject", func(t *testing.T) {
		digest := validWorkDigest()
		digest.Repos[0].Commits[0].Subject = strings.Repeat("字", 201)
		if utf8.RuneCountInString(digest.Repos[0].Commits[0].Subject) != 201 {
			t.Fatal("fixture subject is not 201 runes")
		}
		if err := digest.Validate(); err == nil {
			t.Fatal("201-rune subject was accepted")
		}
	})
	t.Run("513-byte path", func(t *testing.T) {
		digest := validWorkDigest()
		digest.Repos[0].Dirty[0].Path = strings.Repeat("a", 513)
		if err := digest.Validate(); err == nil {
			t.Fatal("513-byte path was accepted")
		}
	})
	t.Run("200 commits still valid", func(t *testing.T) {
		digest := validWorkDigest()
		commits := make([]WorkDigestCommit, 200)
		for i := range commits {
			commits[i] = digest.Repos[0].Commits[0]
		}
		digest.Repos[0].Commits = commits
		if err := digest.Validate(); err != nil {
			t.Fatalf("200 commits rejected: %v", err)
		}
	})
}

func TestWorkDigestValidateRejectsInvalidDirtyStatus(t *testing.T) {
	digest := validWorkDigest()
	digest.Repos[0].Dirty[0].Status = "copied"
	if err := digest.Validate(); err == nil {
		t.Fatal("unknown dirty status was accepted")
	}
}

func TestWorkDigestValidateRequiresComputerAndWindow(t *testing.T) {
	t.Run("missing computer", func(t *testing.T) {
		digest := validWorkDigest()
		digest.ComputerID = ""
		if err := digest.Validate(); err == nil {
			t.Fatal("empty computer_id was accepted")
		}
	})
	t.Run("end not after start", func(t *testing.T) {
		digest := validWorkDigest()
		digest.Window.End = digest.Window.Start
		if err := digest.Validate(); err == nil {
			t.Fatal("zero-length window was accepted")
		}
	})
}

func TestWorkDigestJSONHasNoFileBodyFieldNames(t *testing.T) {
	raw, err := json.Marshal(validWorkDigest())
	if err != nil {
		t.Fatal(err)
	}
	wire := string(raw)
	for _, field := range []string{`"content"`, `"diff"`, `"patch"`, `"body"`} {
		if strings.Contains(wire, field) {
			t.Fatalf("digest JSON contains file-body field %s: %s", field, wire)
		}
	}
}
