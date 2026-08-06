package handler

import "testing"

func TestParseGitHubRepoURL(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"https://github.com/le-czs/Snake-World-Cup.git", "le-czs", "Snake-World-Cup", true},
		{"https://github.com/le-czs/Snake-World-Cup", "le-czs", "Snake-World-Cup", true},
		{"https://github.com/le-czs/Snake-World-Cup/", "le-czs", "Snake-World-Cup", true},
		{"git@github.com:le-czs/Snake-World-Cup.git", "le-czs", "Snake-World-Cup", true},
		{"http://github.com/acme/repo", "acme", "repo", true},
		{"", "", "", false},
		{"https://github.com/onlyowner", "", "", false},
		{"https://gitlab.com/a/b", "", "", false},
	}
	for _, c := range cases {
		owner, repo, ok := parseGitHubRepoURL(c.in)
		if ok != c.ok || owner != c.owner || repo != c.repo {
			t.Fatalf("parseGitHubRepoURL(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, owner, repo, ok, c.owner, c.repo, c.ok)
		}
	}
}

func TestIgnoredTreePath(t *testing.T) {
	ignored := []string{"node_modules/x/y.js", "a/dist/b.js", ".git/config", "src/dist"}
	kept := []string{"src/app.ts", "README.md", "public/logo.png", "frontend-dist/app.js"}
	for _, p := range ignored {
		if !ignoredTreePath(p) {
			t.Fatalf("expected %q to be ignored", p)
		}
	}
	for _, p := range kept {
		if ignoredTreePath(p) {
			t.Fatalf("expected %q to be kept", p)
		}
	}
}
