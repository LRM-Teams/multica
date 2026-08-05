package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	defaultReadFileMaxBytes  = 256 * 1024
	defaultWriteFileMaxBytes = 256 * 1024
	// Media files (image/audio/video/pdf) are base64-encoded in the response,
	// so keep the cap modest — the JSON frame is ~1.34× this.
	mediaMaxBytes = 6 * 1024 * 1024
)

// mediaMimeByExt maps file extensions the preview renders directly (image /
// audio / video / pdf) to their MIME type. Anything not here is treated as
// text. SVG counts as an image so it renders rather than showing markup.
var mediaMimeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".ico": "image/x-icon", ".svg": "image/svg+xml", ".avif": "image/avif",
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
	".m4a": "audio/mp4", ".flac": "audio/flac",
	".pdf": "application/pdf",
}

// mediaMime returns the MIME type for a previewable media file, or "" for text.
func mediaMime(path string) string {
	return mediaMimeByExt[strings.ToLower(filepath.Ext(path))]
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func confinedWorkdirPath(workspacesRoot, relPath, filePath string) (root string, target string, err error) {
	base, err := filepath.Abs(workspacesRoot)
	if err != nil {
		return "", "", err
	}
	root, _ = filepath.Abs(filepath.Join(base, filepath.FromSlash(relPath)))
	target = root
	if filePath != "" {
		target, _ = filepath.Abs(filepath.Join(root, filepath.FromSlash(filePath)))
	}
	if root != base && !strings.HasPrefix(root, base+string(os.PathSeparator)) {
		return "", "", errors.New("invalid root path")
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", "", errors.New("invalid file path")
	}
	return root, target, nil
}

// sendDaemonFrame marshals payload into a typed Message and queues it on the
// wakeup writer. Best-effort: drops after 5s if the writer is backed up.
func (d *Daemon) sendDaemonFrame(msgType string, payload any, requestID string, writes chan<- []byte) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	frame, err := json.Marshal(protocol.Message{Type: msgType, Payload: body})
	if err != nil {
		return
	}
	select {
	case writes <- frame:
	case <-time.After(5 * time.Second):
		d.logger.Debug("daemon response dropped: write buffer full", "request_id", requestID, "type", msgType)
	}
}

// handleReadFileRequest reads one file from a project workdir for preview. The
// path is confined to the workdir root (under WorkspacesRoot); content is
// capped, and non-UTF8/NUL-containing files are reported as binary (no body).
func (d *Daemon) handleReadFileRequest(req protocol.ReadWorkdirFileRequestPayload, writes chan<- []byte) {
	resp := protocol.ReadWorkdirFileResponsePayload{RequestID: req.RequestID}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > defaultReadFileMaxBytes {
		maxBytes = defaultReadFileMaxBytes
	}

	root, target, err := confinedWorkdirPath(d.cfg.WorkspacesRoot, req.RelPath, req.FilePath)
	if err != nil {
		resp.Error = "workspaces root unavailable"
		d.sendDaemonFrame(protocol.EventDaemonReadFileResponse, resp, req.RequestID, writes)
		return
	}

	switch {
	case req.FilePath == "" || target == root:
		resp.Error = "invalid path"
	default:
		info, statErr := os.Stat(target)
		if statErr != nil || info.IsDir() {
			resp.Missing = true
			break
		}
		// Media files are returned base64 with a MIME type so the client can
		// render them as image/audio/video/pdf directly.
		if mime := mediaMime(target); mime != "" {
			if info.Size() > int64(mediaMaxBytes) {
				resp.TooLarge = true
				break
			}
			raw, readErr := os.ReadFile(target)
			if readErr != nil {
				resp.Error = "failed to read file"
				break
			}
			resp.MimeType = mime
			resp.Encoding = "base64"
			resp.Content = base64.StdEncoding.EncodeToString(raw)
			resp.ContentHash = contentHash(raw)
			break
		}
		f, openErr := os.Open(target)
		if openErr != nil {
			resp.Error = "failed to open file"
			break
		}
		defer f.Close()
		buf := make([]byte, maxBytes)
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			resp.Error = "failed to read file"
			break
		}
		data := buf[:n]
		if info.Size() > int64(maxBytes) {
			resp.Truncated = true
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			resp.Binary = true
		} else {
			resp.Content = string(data)
			resp.ContentHash = contentHash(data)
		}
	}

	d.sendDaemonFrame(protocol.EventDaemonReadFileResponse, resp, req.RequestID, writes)
}

func (d *Daemon) handleWriteFileRequest(req protocol.WriteWorkdirFileRequestPayload, writes chan<- []byte) {
	root := filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(req.RelPath))
	// task #94/#204: this RPC can only edit an already-existing file (see
	// writeWorkdirTextFile's Missing check below) under a 256KB per-request
	// cap — it cannot create new files, so it is not the growth vector the
	// quota exists to bound (that's the agent's own direct filesystem
	// writes during a turn, gated at turn-start in runTask).
	//
	// Once a workspace is over cap, this is also the ONLY remaining path
	// that can shrink it back down (handleDeleteDirRequest can nuke the
	// whole directory, but nothing else can edit a single file down to
	// size) — the turn-start gate blocks every turn for this agent,
	// including one that might otherwise clean up its own workspace, so an
	// owner/admin editing a file smaller via this RPC is the recovery
	// path. Blocking all writes once over cap (an earlier version of this
	// check did exactly that) would have blocked that recovery path too.
	// Only refuse a write that would make the workspace bigger.
	if isAgentWorkspaceRelPath(req.RelPath) {
		// Shared quota gate with seed path (task #111) — growth refused when
		// already over cap; shrink/same-size still allowed for recovery.
		var oldSize int64
		if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(req.FilePath))); statErr == nil {
			oldSize = info.Size()
		}
		newSize := int64(len(req.Content))
		if msg := agentWorkspaceWriteQuotaError(root, d.cfg.AgentWorkspaceQuotaBytes, oldSize, newSize); msg != "" {
			resp := protocol.WriteWorkdirFileResponsePayload{
				RequestID: req.RequestID,
				Error:     msg,
			}
			d.sendDaemonFrame(protocol.EventDaemonWriteFileResponse, resp, req.RequestID, writes)
			return
		}
	}
	resp := writeWorkdirTextFile(root, req.FilePath, req.Content, req.ExpectedContentHash, req.MaxBytes)
	resp.RequestID = req.RequestID
	d.sendDaemonFrame(protocol.EventDaemonWriteFileResponse, resp, req.RequestID, writes)
}

// isAgentWorkspaceRelPath reports whether relPath (server-supplied, relative
// to WorkspacesRoot) points into a durable agent workspace
// ({workspaceID}/agents/{agentID}, see agentRootRelPath in handler/agent_files.go).
func isAgentWorkspaceRelPath(relPath string) bool {
	workspaceID, agentID, ok := agentworkspace.IDsFromRelPath(relPath)
	return ok && isCanonicalUUIDDirName(workspaceID) && isCanonicalUUIDDirName(agentID)
}

func isCanonicalUUIDDirName(name string) bool {
	parsed, err := uuid.Parse(name)
	return err == nil && parsed.String() == name
}

// handleDeleteDirRequest removes one confined directory under WorkspacesRoot.
// Used for agent workspace cleanup (including orphan dirs after agent delete).
func (d *Daemon) handleDeleteDirRequest(req protocol.DeleteWorkdirDirRequestPayload, writes chan<- []byte) {
	resp := protocol.DeleteWorkdirDirResponsePayload{RequestID: req.RequestID}

	base, err := filepath.Abs(d.cfg.WorkspacesRoot)
	if err != nil {
		resp.Error = "workspaces root unavailable"
	} else {
		rel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(req.RelPath)))
		if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			resp.Error = "invalid path"
		} else {
			target, _ := filepath.Abs(filepath.Join(base, rel))
			if target == base || !strings.HasPrefix(target, base+string(os.PathSeparator)) {
				resp.Error = "invalid path"
			} else if info, statErr := os.Stat(target); statErr != nil {
				if os.IsNotExist(statErr) {
					resp.Missing = true
				} else {
					resp.Error = "failed to stat directory"
				}
			} else if !info.IsDir() {
				resp.Error = "not a directory"
			} else if removeErr := os.RemoveAll(target); removeErr != nil {
				resp.Error = "failed to delete directory"
			}
		}
	}

	d.sendDaemonFrame(protocol.EventDaemonDeleteDirResponse, resp, req.RequestID, writes)
}

// handleListFilesRequest resolves a project workdir under WorkspacesRoot, walks
// it, and writes the response frame back over the wakeup socket. Runs inline on
// the read loop — the walk is bounded (entry/depth caps) and projects are
// small, so it returns quickly. The path is confined to WorkspacesRoot so a
// crafted rel_path can't escape onto the rest of the host filesystem.
func (d *Daemon) handleListFilesRequest(req protocol.ListWorkdirFilesRequestPayload, writes chan<- []byte) {
	resp := protocol.ListWorkdirFilesResponsePayload{RequestID: req.RequestID}

	base, err := filepath.Abs(d.cfg.WorkspacesRoot)
	if err != nil {
		resp.Error = "workspaces root unavailable"
	} else {
		target, _ := filepath.Abs(filepath.Join(base, filepath.FromSlash(req.RelPath)))
		if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
			resp.Error = "invalid path"
		} else if info, statErr := os.Stat(target); statErr != nil || !info.IsDir() {
			resp.Missing = true
		} else {
			nodes, truncated, walkErr := walkWorkdirFilesWithOptions(target, req.MaxEntries, req.MaxDepth, workdirWalkOptions{
				HideDotfiles: req.HideDotfiles,
			})
			if walkErr != nil {
				resp.Error = "failed to read directory"
			} else {
				resp.Nodes = nodes
				resp.Truncated = truncated
			}
		}
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		return
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventDaemonListFilesResponse, Payload: payload})
	if err != nil {
		return
	}
	select {
	case writes <- frame:
	case <-time.After(5 * time.Second):
		d.logger.Debug("list files response dropped: write buffer full", "request_id", req.RequestID)
	}
}

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

type workdirWalkOptions struct {
	HideDotfiles bool
}

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
	return walkWorkdirFilesWithOptions(root, maxEntries, maxDepth, workdirWalkOptions{})
}

func walkWorkdirFilesWithOptions(root string, maxEntries, maxDepth int, opts workdirWalkOptions) (nodes []protocol.WorkdirFileNode, truncated bool, err error) {
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

		if opts.HideDotfiles && strings.HasPrefix(entry.Name(), ".") {
			if isDir {
				return fs.SkipDir
			}
			return nil
		}
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

func (d *Daemon) handleSeedAgentContextRequest(req protocol.SeedAgentContextRequestPayload, writes chan<- []byte) {
	resp := protocol.SeedAgentContextResponsePayload{RequestID: req.RequestID}
	root, _, err := confinedWorkdirPath(d.cfg.WorkspacesRoot, req.RelPath, "")
	if err != nil {
		resp.Error = "workspaces root unavailable"
		d.sendDaemonFrame(protocol.EventDaemonSeedAgentContextResponse, resp, req.RequestID, writes)
		return
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > defaultWriteFileMaxBytes {
		maxBytes = defaultWriteFileMaxBytes
	}
	// task #111: seed appends always grow agent workspace files — refuse the
	// whole seed when already over capacity (same helper as write RPC).
	if seedWouldGrow(req.InitialNotes, req.InitialMemory) {
		if msg := agentWorkspaceWriteQuotaError(root, d.cfg.AgentWorkspaceQuotaBytes, 0, 1); msg != "" {
			resp.Error = msg
			d.sendDaemonFrame(protocol.EventDaemonSeedAgentContextResponse, resp, req.RequestID, writes)
			return
		}
	}
	written, seedErr := seedAgentContextFiles(root, req.InitialNotes, req.InitialMemory, maxBytes)
	resp.Written = written
	if seedErr != nil {
		if errors.Is(seedErr, errSeedContextTooLarge) {
			resp.TooLarge = true
		} else {
			resp.Error = "failed to seed agent context"
		}
	}
	d.sendDaemonFrame(protocol.EventDaemonSeedAgentContextResponse, resp, req.RequestID, writes)
}

// seedWouldGrow reports whether any non-empty whitelisted seed payload would
// append bytes under the agent workspace.
func seedWouldGrow(notes, memory map[string]string) bool {
	for rel, content := range notes {
		path := filepath.ToSlash(filepath.Clean(rel))
		if allowedInitialNotePath(path) && strings.TrimSpace(content) != "" {
			return true
		}
	}
	for rel, content := range memory {
		path := filepath.ToSlash(filepath.Clean(rel))
		if allowedInitialMemoryPath(path) && strings.TrimSpace(content) != "" {
			return true
		}
	}
	return false
}

var errSeedContextTooLarge = errors.New("seed context too large")

func seedAgentContextFiles(root string, notes, memory map[string]string, maxBytes int) ([]string, error) {
	if err := ensureMulticaAgentRoot(root); err != nil {
		return nil, err
	}
	written := []string{}
	for rel, content := range notes {
		path := filepath.ToSlash(filepath.Clean(rel))
		if !allowedInitialNotePath(path) || strings.TrimSpace(content) == "" {
			continue
		}
		if err := appendSeedContextFile(root, path, content, maxBytes); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	for rel, content := range memory {
		path := filepath.ToSlash(filepath.Clean(rel))
		if !allowedInitialMemoryPath(path) || strings.TrimSpace(content) == "" {
			continue
		}
		if err := appendSeedContextFile(root, path, content, maxBytes); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

func allowedInitialNotePath(path string) bool {
	switch path {
	case "notes/agents.md", "notes/channels.md", "notes/project-map.md", "notes/relationship-map.md", "notes/role-playbook.md", "notes/work-log.md", "notes/decisions.md":
		return true
	default:
		return false
	}
}

func allowedInitialMemoryPath(path string) bool {
	switch path {
	case "memory/MEMORY.md", "memory/STATE.md":
		return true
	default:
		return false
	}
}

func appendSeedContextFile(root, rel, content string, maxBytes int) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	if len([]byte(trimmed)) > maxBytes {
		return errSeedContextTooLarge
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	current, err := os.ReadFile(target)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		current = []byte(seedContextHeader(rel))
	}
	block := "\n\n## Initial Context\n\n" + trimmed + "\n"
	if len(current)+len([]byte(block)) > maxBytes {
		return errSeedContextTooLarge
	}
	return os.WriteFile(target, append(current, []byte(block)...), 0o644)
}

func seedContextHeader(rel string) string {
	if strings.HasPrefix(rel, "memory/") {
		return defaultHeaderForRel(rel)
	}
	headings := map[string]string{
		"notes/agents.md":           "Agents",
		"notes/channels.md":         "Channels",
		"notes/project-map.md":      "Project Map",
		"notes/relationship-map.md": "Relationship Map",
		"notes/role-playbook.md":    "Role Playbook",
		"notes/work-log.md":         "Work Log",
		"notes/decisions.md":        "Decisions",
	}
	return "# " + headings[rel] + "\n"
}

func writeWorkdirTextFile(root, filePath, content, expectedContentHash string, maxBytes int) protocol.WriteWorkdirFileResponsePayload {
	resp := protocol.WriteWorkdirFileResponsePayload{}
	if maxBytes <= 0 || maxBytes > defaultWriteFileMaxBytes {
		maxBytes = defaultWriteFileMaxBytes
	}
	data := []byte(content)
	if len(data) > maxBytes {
		resp.TooLarge = true
		return resp
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		resp.Binary = true
		return resp
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		resp.Error = "workdir root unavailable"
		return resp
	}
	target, _ := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(filePath)))
	if filePath == "" || target == rootAbs || (target != rootAbs && !strings.HasPrefix(target, rootAbs+string(os.PathSeparator))) {
		resp.Error = "invalid path"
		return resp
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			resp.Missing = true
			return resp
		}
		resp.Error = "failed to stat file"
		return resp
	}
	if info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		resp.Error = "invalid file"
		return resp
	}
	current, err := os.ReadFile(target)
	if err != nil {
		resp.Error = "failed to read file"
		return resp
	}
	currentHash := contentHash(current)
	if expectedContentHash != "" && expectedContentHash != currentHash {
		resp.Conflict = true
		resp.ContentHash = currentHash
		return resp
	}
	nextHash := contentHash(data)
	if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
		resp.Error = "failed to write file"
		return resp
	}
	resp.ContentHash = nextHash
	return resp
}
