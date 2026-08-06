package execenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	skillpkg "github.com/multica-ai/multica/server/internal/skill"
)

// Marker placed inside each mirrored skill directory under
// {agentRoot}/skills/enabled/. Lets reconcile remove Multica-owned mirrors
// without deleting user-authored folders that happen to live alongside them.
const boundSkillMirrorMarker = ".multica-bound-mirror"

// mirrorBoundSkillsToAgentEnabled materializes the agent's currently bound
// workspace skills into {agentRoot}/skills/enabled/<slug>/.
//
// This is a durable, agent-local mirror for human/agent inspection. It does
// NOT change the task-time hydration path (provider-native workdir skills).
// Disk edits under enabled/ are not written back to the DB.
//
// Safety:
//   - Only creates/updates/removes directories that carry boundSkillMirrorMarker.
//   - Never touches skills/drafts.
//   - If an unmarked directory already occupies the natural slug, that skill
//     is skipped (no overwrite of user content).
func mirrorBoundSkillsToAgentEnabled(agentRoot string, skills []SkillContextForEnv) error {
	agentRoot = strings.TrimSpace(agentRoot)
	if agentRoot == "" {
		return nil
	}

	enabledDir := filepath.Join(agentRoot, "skills", "enabled")
	if err := os.MkdirAll(enabledDir, 0o755); err != nil {
		return fmt.Errorf("create skills/enabled: %w", err)
	}

	desired := map[string]SkillContextForEnv{}
	for _, skill := range skills {
		slug := sanitizeSkillName(skill.Name)
		// First wins on duplicate sanitized names; matches writeSkillFiles
		// colliding to a sibling for workdir skills, but for the durable
		// mirror we keep a single canonical slug per name.
		if _, exists := desired[slug]; exists {
			continue
		}
		desired[slug] = skill
	}

	if err := reconcileBoundSkillMirrors(enabledDir, desired); err != nil {
		return err
	}

	for slug, skill := range desired {
		if err := writeBoundSkillMirror(enabledDir, slug, skill); err != nil {
			return fmt.Errorf("mirror skill %q: %w", skill.Name, err)
		}
	}
	return nil
}

func reconcileBoundSkillMirrors(enabledDir string, desired map[string]SkillContextForEnv) error {
	entries, err := os.ReadDir(enabledDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read skills/enabled: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := filepath.Join(enabledDir, name)
		if !isBoundSkillMirrorDir(dir) {
			continue
		}
		if _, keep := desired[name]; keep {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove stale mirror %q: %w", name, err)
		}
	}
	return nil
}

func writeBoundSkillMirror(enabledDir, slug string, skill SkillContextForEnv) error {
	dir := filepath.Join(enabledDir, slug)
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", slug)
		}
		if !isBoundSkillMirrorDir(dir) {
			// User-owned folder — leave it alone.
			return nil
		}
		// Owned mirror: replace contents so unbind+rebind and content
		// updates stay accurate. Remove then recreate keeps supporting
		// files from a prior version from lingering.
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("reset mirror dir: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, boundSkillMirrorMarker), []byte(skill.Name+"\n"), 0o644); err != nil {
		return fmt.Errorf("write mirror marker: %w", err)
	}

	body := ensureSkillFrontmatter(skill.Content, slug, skill.Description)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		return fmt.Errorf("write SKILL.md: %w", err)
	}

	for _, f := range skill.Files {
		if skillpkg.IsReservedContentPath(f.Path) {
			continue
		}
		fpath, err := safeSkillFilePath(dir, f.Path)
		if err != nil {
			return fmt.Errorf("invalid supporting file path %q: %w", f.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fpath, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("write supporting file %q: %w", f.Path, err)
		}
	}
	return nil
}

func isBoundSkillMirrorDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, boundSkillMirrorMarker))
	return err == nil
}
