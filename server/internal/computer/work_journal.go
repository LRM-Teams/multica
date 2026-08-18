package computer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	maxWorkJournalCommitsPerRepo = 200
	maxWorkJournalDirtyPerRepo   = 200
	maxWorkJournalSubjectRunes   = 200
	maxWorkJournalPathBytes      = 512
)

// WorkJournalHarvestRequest is the Computer-local harvest input. Home is the
// Owner's home directory for this machine; callers must not pass "/" or
// another user's home.
type WorkJournalHarvestRequest struct {
	ComputerID string
	Home       string
	Window     protocol.WorkDigestWindow
	Enabled    bool
}

// HarvestWorkJournal discovers git roots under Home and collects commit
// metadata plus dirty paths for the window. It does not read file bodies or
// invoke git with a patch diff. Disabled journals return an empty repo list.
func HarvestWorkJournal(ctx context.Context, req WorkJournalHarvestRequest) (protocol.WorkDigest, error) {
	digest := protocol.WorkDigest{
		ComputerID: strings.TrimSpace(req.ComputerID),
		Window:     req.Window,
		Repos:      []protocol.WorkDigestRepo{},
	}
	if !req.Enabled {
		digest.Disabled = true
		return digest, digest.Validate()
	}
	home := filepath.Clean(strings.TrimSpace(req.Home))
	if home == "" || home == "." || home == string(filepath.Separator) {
		return protocol.WorkDigest{}, fmt.Errorf("work journal home is required")
	}
	info, err := os.Stat(home)
	if err != nil {
		return protocol.WorkDigest{}, fmt.Errorf("work journal home: %w", err)
	}
	if !info.IsDir() {
		return protocol.WorkDigest{}, fmt.Errorf("work journal home is not a directory")
	}
	for _, root := range discoverWorkJournalRepoRoots(home) {
		repo, ok, err := harvestWorkJournalRepo(ctx, root, req.Window)
		if err != nil {
			return protocol.WorkDigest{}, err
		}
		if !ok {
			continue
		}
		digest.Repos = append(digest.Repos, repo)
	}
	if err := digest.Validate(); err != nil {
		return protocol.WorkDigest{}, err
	}
	return digest, nil
}

func discoverWorkJournalRepoRoots(home string) []string {
	var roots []string
	seen := make(map[string]struct{})
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name == ".git" {
			root := filepath.Clean(filepath.Dir(path))
			if workJournalRootInsideHome(home, root) && !WorkJournalDeniedRepoRoot(root) {
				if _, exists := seen[root]; !exists {
					seen[root] = struct{}{}
					roots = append(roots, root)
				}
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && workJournalDeniedDirName(name) {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(roots)
	return roots
}

func workJournalDeniedDirName(name string) bool {
	_, denied := workJournalDeniedDirNames[strings.ToLower(name)]
	return denied
}

func workJournalRootInsideHome(home, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(home), filepath.Clean(root))
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func harvestWorkJournalRepo(ctx context.Context, root string, window protocol.WorkDigestWindow) (protocol.WorkDigestRepo, bool, error) {
	commits, err := harvestWorkJournalCommits(ctx, root, window)
	if err != nil {
		return protocol.WorkDigestRepo{}, false, err
	}
	dirty, err := harvestWorkJournalDirty(ctx, root)
	if err != nil {
		return protocol.WorkDigestRepo{}, false, err
	}
	if len(commits) == 0 && len(dirty) == 0 {
		return protocol.WorkDigestRepo{}, false, nil
	}
	remotes, err := harvestWorkJournalRemotes(ctx, root)
	if err != nil {
		return protocol.WorkDigestRepo{}, false, err
	}
	return protocol.WorkDigestRepo{
		Root:    root,
		Remotes: remotes,
		Commits: commits,
		Dirty:   dirty,
	}, true, nil
}

func harvestWorkJournalCommits(ctx context.Context, root string, window protocol.WorkDigestWindow) ([]protocol.WorkDigestCommit, error) {
	out, err := runWorkJournalGit(ctx, root,
		"log",
		"--branches",
		"--after="+window.Start.UTC().Format(time.RFC3339),
		"--before="+window.End.UTC().Format(time.RFC3339),
		"--pretty=format:%H%x00%cI%x00%an%x00%s",
		"--numstat",
	)
	if err != nil {
		if isWorkJournalEmptyRepoLog(err, out) {
			return nil, nil
		}
		return nil, fmt.Errorf("git log %s: %w", root, err)
	}
	return parseWorkJournalLog(out, window), nil
}

func harvestWorkJournalDirty(ctx context.Context, root string) ([]protocol.WorkDigestDirtyPath, error) {
	out, err := runWorkJournalGit(ctx, root, "status", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("git status %s: %w", root, err)
	}
	return parseWorkJournalPorcelain(out), nil
}

func harvestWorkJournalRemotes(ctx context.Context, root string) ([]string, error) {
	out, err := runWorkJournalGit(ctx, root, "remote", "-v")
	if err != nil {
		return nil, fmt.Errorf("git remote %s: %w", root, err)
	}
	return parseWorkJournalRemotes(out), nil
}

func runWorkJournalGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	full := append([]string{
		"-C", dir,
		"-c", "safe.directory=" + dir,
		"-c", "core.quotepath=false",
		"-c", "log.showSignature=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return out, nil
}

func isWorkJournalEmptyRepoLog(err error, out []byte) bool {
	msg := strings.ToLower(string(out) + err.Error())
	return strings.Contains(msg, "does not have any commits") ||
		strings.Contains(msg, "your current branch") && strings.Contains(msg, "does not have any commits")
}

func parseWorkJournalLog(out []byte, window protocol.WorkDigestWindow) []protocol.WorkDigestCommit {
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var commits []protocol.WorkDigestCommit
	var current *protocol.WorkDigestCommit
	flush := func() {
		if current == nil {
			return
		}
		at := current.At
		if !at.Before(window.Start) && at.Before(window.End) {
			if len(commits) < maxWorkJournalCommitsPerRepo {
				commits = append(commits, *current)
			}
		}
		current = nil
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "\x00") {
			flush()
			parts := strings.SplitN(line, "\x00", 4)
			if len(parts) != 4 {
				continue
			}
			at, err := time.Parse(time.RFC3339, parts[1])
			if err != nil {
				continue
			}
			current = &protocol.WorkDigestCommit{
				Hash:    strings.TrimSpace(parts[0]),
				At:      at.UTC(),
				Author:  parts[2],
				Subject: truncateRunes(parts[3], maxWorkJournalSubjectRunes),
			}
			continue
		}
		if current == nil {
			continue
		}
		if line == "" {
			flush()
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		current.FileCount++
		if fields[0] != "-" {
			n, convErr := strconv.Atoi(fields[0])
			if convErr == nil && n > 0 {
				current.Insertions += n
			}
		}
		if fields[1] != "-" {
			n, convErr := strconv.Atoi(fields[1])
			if convErr == nil && n > 0 {
				current.Deletions += n
			}
		}
	}
	flush()
	return commits
}

func parseWorkJournalPorcelain(out []byte) []protocol.WorkDigestDirtyPath {
	if len(out) == 0 {
		return nil
	}
	parts := strings.Split(string(out), "\x00")
	var dirty []protocol.WorkDigestDirtyPath
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if rec == "" {
			continue
		}
		if len(rec) < 4 || rec[2] != ' ' {
			continue
		}
		xy := rec[:2]
		path := rec[3:]
		if xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C' {
			i++
		}
		if WorkJournalDeniedDirtyPath(path) || len(path) > maxWorkJournalPathBytes {
			continue
		}
		status := workJournalDirtyStatus(xy)
		if status == "" {
			continue
		}
		dirty = append(dirty, protocol.WorkDigestDirtyPath{Path: path, Status: status})
		if len(dirty) >= maxWorkJournalDirtyPerRepo {
			break
		}
	}
	return dirty
}

func workJournalDirtyStatus(xy string) string {
	if xy == "??" {
		return protocol.WorkDigestDirtyUntracked
	}
	for _, ch := range xy {
		switch ch {
		case 'D':
			return protocol.WorkDigestDirtyDeleted
		}
	}
	for _, ch := range xy {
		switch ch {
		case 'A':
			return protocol.WorkDigestDirtyAdded
		}
	}
	for _, ch := range xy {
		switch ch {
		case 'M', 'T', 'R', 'C':
			return protocol.WorkDigestDirtyModified
		}
	}
	return ""
}

func parseWorkJournalRemotes(out []byte) []string {
	seen := make(map[string]struct{})
	var remotes []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.Contains(fields[2], "fetch") {
			continue
		}
		url := fields[1]
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		remotes = append(remotes, url)
	}
	return remotes
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
