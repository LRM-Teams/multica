package computer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHarvestWorkJournalCollectsWindowGitMetadataAndSkipsDenylist(t *testing.T) {
	home, appRoot := setupWorkJournalHome(t)
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	window := protocol.WorkDigestWindow{Start: start, End: start.Add(7 * 24 * time.Hour)}

	digest, err := HarvestWorkJournal(context.Background(), WorkJournalHarvestRequest{
		ComputerID: "computer-1",
		Home:       home,
		Window:     window,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if digest.Disabled {
		t.Fatal("enabled journal returned disabled digest")
	}
	if digest.ComputerID != "computer-1" {
		t.Fatalf("computer_id=%q", digest.ComputerID)
	}
	if len(digest.Repos) != 1 {
		t.Fatalf("repos=%d want 1 (denylist repo must be omitted): %#v", len(digest.Repos), digest.Repos)
	}
	repo := digest.Repos[0]
	if repo.Root != appRoot {
		t.Fatalf("root=%q want %q", repo.Root, appRoot)
	}
	if len(repo.Remotes) != 1 || repo.Remotes[0] != "git@github.com:org/app.git" {
		t.Fatalf("remotes=%v", repo.Remotes)
	}
	if len(repo.Commits) != 1 {
		t.Fatalf("commits=%d want 1 in-window commit: %#v", len(repo.Commits), repo.Commits)
	}
	commit := repo.Commits[0]
	if commit.Subject != "wire SSO login" {
		t.Fatalf("subject=%q", commit.Subject)
	}
	if commit.Author != "owner" {
		t.Fatalf("author=%q", commit.Author)
	}
	if commit.Hash == "" || commit.FileCount != 1 || commit.Insertions < 1 {
		t.Fatalf("commit metadata %#v", commit)
	}
	if !commit.At.Equal(start.Add(2 * time.Hour)) {
		t.Fatalf("commit at %s", commit.At)
	}
	if len(repo.Dirty) != 1 || repo.Dirty[0].Path != "internal/auth/sso.go" || repo.Dirty[0].Status != protocol.WorkDigestDirtyUntracked {
		t.Fatalf("dirty=%#v want only untracked sso.go", repo.Dirty)
	}
}

func TestHarvestWorkJournalDisabledReturnsEmptyRepos(t *testing.T) {
	home, _ := setupWorkJournalHome(t)
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	digest, err := HarvestWorkJournal(context.Background(), WorkJournalHarvestRequest{
		ComputerID: "computer-1",
		Home:       home,
		Window:     protocol.WorkDigestWindow{Start: start, End: start.Add(7 * 24 * time.Hour)},
		Enabled:    false,
	})
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if !digest.Disabled {
		t.Fatal("disabled journal did not set disabled")
	}
	if len(digest.Repos) != 0 {
		t.Fatalf("disabled journal returned repos %#v", digest.Repos)
	}
}

func TestHostHarvestWorkDigestDisabledReturnsEmptyRepos(t *testing.T) {
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	host := &Host{
		processIdentity:    HostProcessIdentity{ComputerID: "computer-1"},
		workJournalHome:    t.TempDir(),
		workJournalEnabled: false,
	}
	digest, err := host.HarvestWorkDigest(context.Background(), protocol.ComputerWorkDigestPayload{
		RequestID: "digest-1",
		Start:     start,
		End:       start.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("host harvest: %v", err)
	}
	if !digest.Disabled || len(digest.Repos) != 0 || digest.ComputerID != "computer-1" {
		t.Fatalf("host disabled digest %+v", digest)
	}
}

func TestHostWorkJournalToggleChangesHarvestFromEmptyToFixtureRepos(t *testing.T) {
	home, appRoot := setupWorkJournalHome(t)
	root := t.TempDir()
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	command := protocol.ComputerWorkDigestPayload{
		RequestID: "digest-1",
		Start:     start,
		End:       start.Add(7 * 24 * time.Hour),
	}
	host := &Host{
		processIdentity: HostProcessIdentity{ComputerID: "computer-1"},
		workJournalHome: home,
		workJournalRoot: root,
	}
	off, err := host.HarvestWorkDigest(context.Background(), command)
	if err != nil {
		t.Fatalf("disabled harvest: %v", err)
	}
	if !off.Disabled || len(off.Repos) != 0 {
		t.Fatalf("default journal %+v", off)
	}
	if err := host.SetWorkJournalEnabled(true); err != nil {
		t.Fatal(err)
	}
	on, err := host.HarvestWorkDigest(context.Background(), command)
	if err != nil {
		t.Fatalf("enabled harvest: %v", err)
	}
	if on.Disabled || len(on.Repos) != 1 || on.Repos[0].Root != appRoot {
		t.Fatalf("enabled journal %+v", on)
	}
	if err := host.SetWorkJournalEnabled(false); err != nil {
		t.Fatal(err)
	}
	again, err := host.HarvestWorkDigest(context.Background(), command)
	if err != nil {
		t.Fatalf("re-disabled harvest: %v", err)
	}
	if !again.Disabled || len(again.Repos) != 0 {
		t.Fatalf("re-disabled journal %+v", again)
	}
	reloaded := &Host{workJournalRoot: root}
	reloaded.loadWorkJournalSetting()
	if reloaded.WorkJournalEnabled() {
		t.Fatal("persisted setting should be disabled after toggle off")
	}
}

func setupWorkJournalHome(t *testing.T) (home, appRoot string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required for Work Journal harvest tests")
	}
	home = t.TempDir()
	appRoot = filepath.Join(home, "code", "app")
	deniedRoot := filepath.Join(home, "code", "app", "node_modules", "pkg")
	if err := os.MkdirAll(filepath.Join(appRoot, "internal", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deniedRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(appRoot, "README.md"), "app\n")
	writeFile(t, filepath.Join(appRoot, "internal", "auth", ".gitkeep"), "")
	initGitRepo(t, appRoot)
	runGit(t, appRoot, nil, "remote", "add", "origin", "git@github.com:org/app.git")
	runGit(t, appRoot, gitDateEnv(start.Add(-24*time.Hour)), "add", "README.md", "internal/auth/.gitkeep")
	runGit(t, appRoot, gitDateEnv(start.Add(-24*time.Hour)), "commit", "-m", "seed outside window")
	writeFile(t, filepath.Join(appRoot, "README.md"), "app\nwire sso\n")
	runGit(t, appRoot, gitDateEnv(start.Add(2*time.Hour)), "add", "README.md")
	runGit(t, appRoot, gitDateEnv(start.Add(2*time.Hour)), "commit", "-m", "wire SSO login")
	writeFile(t, filepath.Join(appRoot, "internal", "auth", "sso.go"), "package auth\n")
	writeFile(t, filepath.Join(appRoot, ".env"), "SECRET=1\n")

	writeFile(t, filepath.Join(deniedRoot, "index.js"), "module.exports = 1\n")
	initGitRepo(t, deniedRoot)
	runGit(t, deniedRoot, gitDateEnv(start.Add(2*time.Hour)), "add", "index.js")
	runGit(t, deniedRoot, gitDateEnv(start.Add(2*time.Hour)), "commit", "-m", "vendor noise")
	return home, appRoot
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, nil, "init", "-b", "main")
	runGit(t, dir, nil, "config", "user.name", "owner")
	runGit(t, dir, nil, "config", "user.email", "owner@test.local")
}

func gitDateEnv(at time.Time) []string {
	stamp := at.UTC().Format(time.RFC3339)
	return []string{
		"GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_DATE=" + stamp,
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", dir, "-c", "safe.directory=" + dir, "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", cmdArgs...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=owner",
		"GIT_AUTHOR_EMAIL=owner@test.local",
		"GIT_COMMITTER_NAME=owner",
		"GIT_COMMITTER_EMAIL=owner@test.local",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
