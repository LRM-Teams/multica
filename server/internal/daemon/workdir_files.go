package daemon

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workdirIgnoredDirs are directory names skipped when listing a project
// workdir — VCS internals, dependency/build caches, and editor metadata that
// would bury the actual source and blow past the entry cap.
var workdirIgnoredDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, "dist": {}, "build": {}, "out": {},
	".next": {}, ".turbo": {}, ".cache": {}, ".venv": {}, "venv": {},
	"__pycache__": {}, ".pytest_cache": {}, ".mypy_cache": {}, "target": {},
	".gradle": {}, ".idea": {}, "vendor": {},
}

const (
	defaultWorkdirMaxEntries = 2000
	defaultWorkdirMaxDepth   = 12
)

// errStopWalk is the sentinel used to abort filepath.WalkDir once the entry
// cap is reached. It never escapes walkWorkdirFiles.
var errStopWalk = errors.New("workdir walk: entry cap reached")

// walkWorkdirFiles enumerates root into a flat, slash-separated node list
// (paths relative to root). It skips ignored directories and symlinks, never
// follows symlinked directories, and caps both the number of entries and the
// recursion depth — hitting either cap sets truncated. The result is sorted
// by path so the frontend can rebuild a stable tree. root must be an existing
// directory; an unreadable child entry is skipped rather than aborting.
func walkWorkdirFiles(root string, maxEntries, maxDepth int) (nodes []protocol.WorkdirFileNode, truncated bool, err error) {
	if maxEntries <= 0 || maxEntries > defaultWorkdirMaxEntries {
		maxEntries = defaultWorkdirMaxEntries
	}
	if maxDepth <= 0 || maxDepth > defaultWorkdirMaxDepth {
		maxDepth = defaultWorkdirMaxDepth
	}

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, inErr error) error {
		if inErr != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir // unreadable subtree — skip, don't abort
			}
			return nil
		}
		if path == root {
			return nil
		}
		// Skip symlinks entirely: WalkDir won't descend into them, and we
		// don't want to surface links that could point outside the workdir.
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		isDir := entry.IsDir()

		if isDir {
			if _, ignored := workdirIgnoredDirs[entry.Name()]; ignored {
				return fs.SkipDir
			}
		}

		if len(nodes) >= maxEntries {
			truncated = true
			return errStopWalk
		}

		var size int64
		if !isDir {
			if info, e := entry.Info(); e == nil {
				size = info.Size()
			}
		}
		nodes = append(nodes, protocol.WorkdirFileNode{Path: rel, IsDir: isDir, Size: size})

		// Depth = number of path segments. Stop descending past the cap, but
		// keep the directory node itself so the tree shows it's not a leaf.
		if isDir && strings.Count(rel, "/")+1 >= maxDepth {
			truncated = true
			return fs.SkipDir
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStopWalk) {
		return nil, truncated, walkErr
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	return nodes, truncated, nil
}
