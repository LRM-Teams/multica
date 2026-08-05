package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sidecarManifestFile records the context files and directories Multica
// created during the latest Agent workspace refresh. The next refresh uses it
// to remove only Multica-owned paths before materializing current context.
const sidecarManifestFile = ".multica_sidecar_manifest.json"

// errPathPreExists is the sentinel recordWriteFile returns when the
// target path already exists. The manifest contract is that we never
// mutate paths we don't own: a pre-existing file belongs to the user
// (or to stale state from a crashed prior run we cannot safely
// distinguish from intentional user content) and the write must be
// refused so cleanup can be a pure deletion of paths we created.
//
// Callers handle this in one of two ways:
//
//   - For per-skill directories the caller allocates a collision-free
//     alternative slug (see allocateCollisionFreeSkillDir) and retries
//     so the agent still discovers the Multica skill, just under a
//     different directory name.
//   - For Multica-only namespaces (.agent_context/issue_context.md) the caller
//     swallows the error
//     and proceeds — the agent's runtime brief already carries every
//     fact that would have appeared in those files, so missing-from-
//     disk is degraded behavior, not failure.
var errPathPreExists = errors.New("execenv: refuse to overwrite pre-existing path")

// sidecarManifest records the filesystem mutations writeContextFiles and its
// callees make inside AgentRoot during one refresh:
//
//   - Files lists absolute paths of regular files we created. Files are
//     recorded only after recordWriteFile has verified the target did
//     NOT pre-exist; recordWriteFile refuses to overwrite a pre-existing
//     path, so the manifest's existence rule and the write side's
//     refuse-to-clobber rule are the same invariant viewed from two
//     sides.
//   - Dirs lists absolute paths of directories we created, in root-first
//     creation order. Cleanup walks the list in reverse so deepest dirs
//     get tried first; rmdir of a directory the user has populated since
//     (e.g. .claude/skills/my-own-skill alongside our .claude/skills/
//     issue-review) fails ENOTEMPTY and is skipped silently — the
//     user's content is preserved without any per-dir bookkeeping. A
//     directory is recorded only when it did NOT pre-exist for the same
//     reason files are conditional.
//
// The manifest is intentionally minimal: it carries only the paths needed to
// replace Multica-managed context on the next refresh.
type sidecarManifest struct {
	Files []string `json:"files,omitempty"`
	Dirs  []string `json:"dirs,omitempty"`
}

// recordMkdirAll behaves like os.MkdirAll(path, perm) but additionally
// records every parent directory it had to create (skipping any that
// already existed) into m so CleanupSidecars can rmdir them later. The
// recorded paths are appended in root-first order; Cleanup iterates in
// reverse so the deepest directory is removed first.
//
// When m is nil this is identical to os.MkdirAll.
func recordMkdirAll(path string, perm os.FileMode, m *sidecarManifest) error {
	if path == "" {
		return os.MkdirAll(path, perm)
	}
	if m == nil {
		return os.MkdirAll(path, perm)
	}
	// Walk leaf-first, collecting ancestors that don't currently exist.
	// We stop at the first existing ancestor (or the filesystem root) so
	// pre-existing user directories are never recorded — Cleanup must
	// not rmdir a path the user owned before this task started.
	var toCreate []string
	cur := filepath.Clean(path)
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat ancestor %s: %w", cur, err)
		}
		toCreate = append(toCreate, cur)
		parent := filepath.Dir(cur)
		if parent == cur || parent == "." {
			break
		}
		cur = parent
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	// Reverse leaf-first → root-first so Cleanup can reverse-iterate
	// to peel directories from the leaves upward.
	for i, j := 0, len(toCreate)-1; i < j; i, j = i+1, j-1 {
		toCreate[i], toCreate[j] = toCreate[j], toCreate[i]
	}
	m.Dirs = append(m.Dirs, toCreate...)
	return nil
}

// recordWriteFile writes data to path with perm and records the path in
// m for later cleanup, but ONLY when path does not already exist. When
// path is occupied — by a regular file, a symlink, a directory, or any
// other filesystem entry — the function returns errPathPreExists
// without touching the path. The user's bytes (or pre-existing entry
// type) are preserved exactly.
//
// This is the invariant the manifest design rests on: cleanup is a
// pure deletion of paths we created, never a restore. Overwriting a
// pre-existing path and then refusing to delete it on cleanup (the
// pre-fix behavior) destroys user data twice — once at write time and
// once by leaving the corrupted bytes in place at exit. Refusing to
// overwrite removes both halves of that failure mode.
//
// When m is nil this collapses to a plain os.WriteFile.
func recordWriteFile(path string, data []byte, perm os.FileMode, m *sidecarManifest) error {
	if m == nil {
		return os.WriteFile(path, data, perm)
	}
	_, statErr := os.Lstat(path)
	if statErr == nil {
		// Any existing entry — regular file, symlink, directory —
		// is a collision. Refuse to touch it.
		return fmt.Errorf("%w: %s", errPathPreExists, path)
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("stat target %s: %w", path, statErr)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	m.Files = append(m.Files, path)
	return nil
}

// managedSkillMarker is written into every skill directory Multica creates
// under a provider-native skills parent (e.g. .cursor/skills/). It lets a
// later refresh reclaim our own leftover dirs without confusing them with
// Agent-created skill directories.
const managedSkillMarker = ".multica-managed-skill"

func isManagedSkillDir(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, managedSkillMarker))
	return err == nil
}

// allocateCollisionFreeSkillDir picks a directory under skillsParent
// for writeSkillFiles. The first attempt is always the natural baseSlug.
// On collision with an *unmarked* (user-owned) directory we append
// `-multica`, then `-multica-2`, `-multica-3`, … until a free slot is found.
//
// Directories Multica previously created (bearing managedSkillMarker) are
// reclaimed in place instead of bumping the slug on every refresh.
//
// The collision-free fallback name is still a sibling under the same
// skillsParent, so provider-native discovery still picks the skill up.
// Unmarked user directories at baseSlug are left bit-for-bit intact.
//
// The probe is bounded to a small ceiling — a user with thousands of
// unmarked collisions on the same slug indicates an upstream bug, not a
// realistic state. Returning an error in that case forces the caller
// to surface the problem instead of looping forever.
func allocateCollisionFreeSkillDir(skillsParent, baseSlug string) (slug, dir string, err error) {
	const maxAttempts = 64
	for i := 0; i < maxAttempts; i++ {
		var candidate string
		switch {
		case i == 0:
			candidate = baseSlug
		case i == 1:
			candidate = baseSlug + "-multica"
		default:
			candidate = fmt.Sprintf("%s-multica-%d", baseSlug, i)
		}
		path := filepath.Join(skillsParent, candidate)
		st, statErr := os.Lstat(path)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return candidate, path, nil
			}
			return "", "", fmt.Errorf("stat candidate %s: %w", path, statErr)
		}
		if !st.IsDir() {
			continue
		}
		if isManagedSkillDir(path) {
			if err := os.RemoveAll(path); err != nil {
				return "", "", fmt.Errorf("reclaim managed skill dir %s: %w", path, err)
			}
			return candidate, path, nil
		}
		// Unmarked directory — treat as user-owned and try the next slug.
	}
	return "", "", fmt.Errorf("allocate collision-free skill dir under %s: exhausted %d attempts for base %q", skillsParent, maxAttempts, baseSlug)
}

// reclaimManagedSkillCollisionSiblings removes Multica-owned collision
// leftovers for baseSlug (baseSlug-multica, baseSlug-multica-N, …) after a
// successful write to keepSlug. Unmarked user dirs are never touched.
func reclaimManagedSkillCollisionSiblings(skillsParent, baseSlug, keepSlug string) {
	entries, err := os.ReadDir(skillsParent)
	if err != nil {
		return
	}
	prefix := baseSlug + "-multica"
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == keepSlug {
			continue
		}
		if name != prefix && !strings.HasPrefix(name, prefix+"-") {
			continue
		}
		dir := filepath.Join(skillsParent, name)
		if !isManagedSkillDir(dir) {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

// writeSidecarManifest persists m to {agentRoot}/{sidecarManifestFile}.
// Empty manifests are still written so the next refresh knows tracking ran.
func writeSidecarManifest(envRoot string, m *sidecarManifest) error {
	if envRoot == "" {
		return nil
	}
	if m == nil {
		m = &sidecarManifest{}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal sidecar manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(envRoot, sidecarManifestFile), data, 0o644)
}

// CleanupSidecars removes context created by the previous Agent workspace
// refresh, then removes recorded empty directories deepest first.
//
// Two failure modes the function deliberately swallows:
//
//   - ENOENT on a recorded path. The file or directory was already
//     gone — either the user removed it during the task, or a prior
//     Cleanup run on the same envRoot already cleared it. Either
//     way there is nothing left for this call to do.
//   - Non-empty directory on rmdir. The user has populated a
//     directory we created (added a sibling file under .claude/
//     skills/, for example) and rmdir-ing would destroy that
//     content. We detect this by re-reading the directory after
//     rmdir fails: a non-empty listing means "user owns this — stop
//     here." This is the must-fix from PR #3444 review — the
//     previous version swallowed ANY non-ENOENT rmdir error as
//     "non-empty," which silently dropped real I/O failures
//     (EACCES, EPERM, EBUSY) and made cleanup look successful when
//     it wasn't.
//
// All other errors — ReadFile failure, JSON parse failure, real
// EACCES/EPERM/EIO during file deletion, real EACCES/EPERM/EIO
// during dir removal — are captured into firstErr and surfaced to
// the caller. Cleanup still continues for the remaining manifest
// entries so a single bad path does not strand the rest of the
// rollback.
//
// The function is a no-op when AgentRoot is empty or the manifest is absent.
func CleanupSidecars(envRoot string) error {
	if envRoot == "" {
		return nil
	}
	manifestPath := filepath.Join(envRoot, sidecarManifestFile)
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read sidecar manifest %s: %w", manifestPath, err)
	}
	var m sidecarManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse sidecar manifest %s: %w", manifestPath, err)
	}

	var firstErr error
	captureErr := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	for _, f := range m.Files {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			captureErr(fmt.Errorf("remove %s: %w", f, err))
		}
	}

	// Reverse iterate so the deepest directory is tried first. When
	// rmdir fails we re-read the directory to tell ENOTEMPTY (user
	// content present — skip silently) apart from real I/O errors
	// (permission denied, busy, etc. — capture and surface).
	for i := len(m.Dirs) - 1; i >= 0; i-- {
		d := m.Dirs[i]
		err := os.Remove(d)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			continue
		}
		hasEntries, ok := dirHasEntries(d)
		switch {
		case !ok:
			// ReadDir also failed — we can't tell ENOTEMPTY apart
			// from a real I/O error. Surface the ORIGINAL rmdir
			// error (not the ReadDir failure) so the operator sees
			// the actual cleanup blocker; the ReadDir branch is
			// just diagnostic plumbing and would distract from the
			// root cause. Silently skipping here was the v1 bug:
			// it hid EACCES on locked directories behind a phantom
			// "directory non-empty" assumption.
			captureErr(fmt.Errorf("rmdir %s: %w", d, err))
		case hasEntries:
			// User has populated this dir since Prepare ran. Leave
			// it in place without surfacing the rmdir error — the
			// whole point of the manifest design is to preserve
			// user content under directories we created.
		default:
			// Empty directory but rmdir still failed → real I/O
			// error (EACCES, EPERM, EBUSY, EIO, or a directory we
			// mistakenly recorded that we don't actually own).
			// Surface it so the caller can log a warning and an
			// operator can investigate.
			captureErr(fmt.Errorf("rmdir %s: %w", d, err))
		}
	}

	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		captureErr(fmt.Errorf("remove manifest %s: %w", manifestPath, err))
	}

	return firstErr
}

// removeReusedManagedSkillDirs force-removes the skill directories the prior
// dispatch recorded under skillsParent in its sidecar manifest at envRoot,
// even when they are now non-empty. It is the reuse-path companion to
// CleanupSidecars and runs just before it.
//
// CleanupSidecars deliberately preserves a recorded directory once it has
// become non-empty — the agent may have dropped a file inside a dir we
// created, and that content must survive generic cleanup. But that same
// preservation reopens #3684 on refresh: if a
// prior-run agent wrote into .claude/skills/issue-review/, CleanupSidecars
// deletes the recorded SKILL.md yet keeps the directory, so the canonical
// slug stays occupied and the refreshed skill dodges to
// issue-review-multica. A managed skill directory is platform-owned — the
// manifest is proof we created it — so on reuse we reclaim the whole
// directory (dropping any scratch the agent left inside it, exactly as the
// Codex path's os.RemoveAll(skillsDir) already does) and let the refresh
// re-create it at its natural slug.
//
// Only directories whose immediate parent is skillsParent are removed, so
// the blast radius is exactly the platform's own skill roots: sibling skills
// the agent installed under the same parent and the rest of AgentRoot are
// untouched.
//
// envRoot or skillsParent empty, a missing manifest, or a parse failure are
// all no-ops — the refresh simply proceeds. The manifest file is left in
// place; CleanupSidecars, which runs next, owns deleting it.
func removeReusedManagedSkillDirs(envRoot, skillsParent string) error {
	if envRoot == "" || skillsParent == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(envRoot, sidecarManifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read sidecar manifest for reuse skill rollback: %w", err)
	}
	var m sidecarManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse sidecar manifest for reuse skill rollback: %w", err)
	}

	cleanParent := filepath.Clean(skillsParent)
	var firstErr error
	for _, d := range m.Dirs {
		if filepath.Dir(filepath.Clean(d)) != cleanParent {
			continue
		}
		if err := os.RemoveAll(d); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove managed skill dir %s: %w", d, err)
		}
	}
	return firstErr
}

// dirHasEntries inspects dir and reports whether it currently contains
// any entries. The second return value distinguishes three states
// CleanupSidecars must handle separately:
//
//   - (false, true) — dir exists and is empty, OR dir disappeared
//     between the failed rmdir and our readdir (the race collapses
//     into "empty" so cleanup keeps moving). When paired with a
//     non-ENOENT rmdir failure in CleanupSidecars this is the
//     "empty + rmdir refused" branch — a real I/O error that gets
//     surfaced.
//   - (true, true) — dir has user content. When paired with a rmdir
//     failure this is the intended ENOTEMPTY branch — skip silently
//     so the user's content is preserved.
//   - (_, false) — readdir failed with a real I/O error (EACCES on a
//     chmod'd dir, ENOTDIR on a recorded path that isn't actually a
//     dir, EIO on a hardware fault, etc.). The caller cannot tell
//     ENOTEMPTY from a real failure and MUST surface the original
//     rmdir error instead of silently skipping. The v1 of this
//     helper returned `true` here, which made CleanupSidecars treat
//     every readdir failure as "user content present" and hid the
//     underlying rmdir error.
func dirHasEntries(dir string) (hasEntries bool, ok bool) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, true
		}
		return false, false
	}
	if !info.IsDir() {
		return false, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, true
		}
		return false, false
	}
	return len(entries) > 0, true
}
