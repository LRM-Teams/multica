package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaterializationReceipt records what create-time materialize actually did
// (Barry B contract). Not used for reuse fingerprint — only diagnostics / gates.
type MaterializationReceipt struct {
	AgentsFinalSHA256    string                     `json:"agents_final_sha256,omitempty"`
	ResourcesFinalSHA256 string                     `json:"resources_final_sha256,omitempty"`
	Skills               []MaterializedSkillReceipt `json:"skills,omitempty"`
	ManagedInputDigest   string                     `json:"managed_input_digest,omitempty"`
}

// MaterializedSkillReceipt is one assigned skill after collision resolution.
type MaterializedSkillReceipt struct {
	LogicalName string             `json:"logical_name"`
	ActualSlug  string             `json:"actual_slug"`
	Decision    string             `json:"decision"` // "created" | "sibling"
	Files       []MaterializedFile `json:"files"`
}

// MaterializedFile is one file written under a managed skill package.
type MaterializedFile struct {
	RelPath string `json:"rel_path"`
	SHA256  string `json:"sha256"`
}

// MaterializeCanonicalTurnContextB implements Barry B: single resolved plan,
// fail-closed cleanup, collision-free skills, atomic AGENTS, MaterializationReceipt.
// Returns managed brief body (for logging) + receipt.
func MaterializeCanonicalTurnContextB(workDir, ledgerRoot, provider string, ctx TaskContextForEnv) (brief string, receipt MaterializationReceipt, err error) {
	workDir = strings.TrimSpace(workDir)
	ledgerRoot = strings.TrimSpace(ledgerRoot)
	if workDir == "" {
		return "", receipt, errors.New("canonical turn workdir is required")
	}
	if ledgerRoot == "" {
		return "", receipt, errors.New("canonical turn ledger root is required")
	}
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return "", receipt, fmt.Errorf("abs workdir: %w", err)
	}
	absLedger, err := filepath.Abs(ledgerRoot)
	if err != nil {
		return "", receipt, fmt.Errorf("abs ledger: %w", err)
	}
	if absLedger == absWork || pathWithin(absWork, absLedger) {
		return "", receipt, errors.New("canonical turn ledger must not live under provider workdir")
	}
	if err := validateManagedBase(absWork); err != nil {
		return "", receipt, fmt.Errorf("canonical workdir invalid: %w", err)
	}
	agentRoot := filepath.Dir(absWork)
	if err := mkdirAllWithoutSymlink(agentRoot, absLedger, 0o755); err != nil {
		return "", receipt, fmt.Errorf("create canonical turn ledger: %w", err)
	}

	staticCtx := StartupStaticContext(ctx)
	receipt.ManagedInputDigest = ManagedStartupInputDigest(provider, staticCtx)

	// Fail-closed cleanup (no swallowed errors).
	if err := CleanupSidecarsConfined(absLedger, absWork); err != nil {
		return "", receipt, fmt.Errorf("cleanup prior sidecars: %w", err)
	}
	if err := reclaimMarkedMulticaSidecars(absWork); err != nil {
		return "", receipt, fmt.Errorf("reclaim marked sidecars: %w", err)
	}
	if err := removeManagedSkillDirs(absWork, provider); err != nil {
		return "", receipt, fmt.Errorf("clear managed skills: %w", err)
	}
	if err := reclaimLegacyIssueContextOrphan(absWork); err != nil {
		return "", receipt, fmt.Errorf("reclaim legacy issue_context: %w", err)
	}

	// Resolve one write plan (slug collisions need FS).
	type skillFile struct {
		rel  string
		body []byte
	}
	type resolvedSkill struct {
		logical  string
		slug     string
		decision string
		files    []skillFile
	}
	var skills []resolvedSkill
	if len(staticCtx.AgentSkills) > 0 && provider != "codex" {
		skillsDir := skillsDirPath(absWork, provider)
		if err := mkdirAllWithoutSymlink(absWork, skillsDir, 0o755); err != nil {
			return "", receipt, fmt.Errorf("create skills dir: %w", err)
		}
		if err := validatePathUnderWorkDirNoSymlink(absWork, skillsDir); err != nil {
			return "", receipt, fmt.Errorf("skills dir unsafe: %w", err)
		}
		for _, sk := range staticCtx.AgentSkills {
			baseSlug := sanitizeSkillName(sk.Name)
			slug, dir, err := allocateCollisionFreeSkillDir(skillsDir, baseSlug)
			if err != nil {
				return "", receipt, fmt.Errorf("allocate skill dir %q: %w", sk.Name, err)
			}
			decision := "created"
			if slug != baseSlug {
				decision = "sibling"
			}
			// allocateCollisionFreeSkillDir may remove managed reclaim — ensure empty dir
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", receipt, err
			}
			body := ensureSkillFrontmatter(sk.Content, slug, sk.Description)
			files := []skillFile{
				{rel: "SKILL.md", body: []byte(body)},
				{rel: managedSkillMarker, body: []byte(sk.Name + "\n")},
			}
			for _, f := range sk.Files {
				rel := strings.TrimPrefix(filepath.ToSlash(f.Path), "/")
				if rel == "" || rel == "SKILL.md" {
					continue
				}
				files = append(files, skillFile{rel: rel, body: []byte(f.Content)})
			}
			skills = append(skills, resolvedSkill{
				logical: sk.Name, slug: slug, decision: decision, files: files,
			})
		}
	}

	// Managed brief (logical overlay — same renderer as input digest path).
	brief = buildMetaSkillContent(provider, staticCtx)

	// Synthesize final AGENTS bytes in memory, then atomic write.
	agentsPath := runtimeConfigPath(absWork, provider)
	var agentsFinal []byte
	if agentsPath != "" {
		if err := validatePathUnderWorkDirNoSymlink(absWork, filepath.Dir(agentsPath)); err != nil {
			return "", receipt, fmt.Errorf("AGENTS parent unsafe: %w", err)
		}
		if info, err := os.Lstat(agentsPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", receipt, fmt.Errorf("refusing symlink AGENTS path: %s", agentsPath)
		}
		agentsFinal, err = synthesizeRuntimeConfigBytes(agentsPath, brief)
		if err != nil {
			return "", receipt, err
		}
	}

	manifest := &sidecarManifest{}

	// Apply AGENTS atomic replace.
	if agentsPath != "" && agentsFinal != nil {
		if err := atomicWriteFilePreserveMode(agentsPath, agentsFinal, 0o644); err != nil {
			return "", receipt, fmt.Errorf("write AGENTS: %w", err)
		}
		receipt.AgentsFinalSHA256 = sha256Hex(agentsFinal)
	}

	// Apply skills from resolved plan only.
	for _, sk := range skills {
		skillDir := filepath.Join(skillsDirPath(absWork, provider), sk.slug)
		if err := mkdirAllWithoutSymlink(absWork, skillDir, 0o755); err != nil {
			return "", receipt, err
		}
		rec := MaterializedSkillReceipt{
			LogicalName: sk.logical,
			ActualSlug:  sk.slug,
			Decision:    sk.decision,
		}
		for _, f := range sk.files {
			full := filepath.Join(skillDir, filepath.FromSlash(f.rel))
			if err := mkdirAllWithoutSymlink(absWork, filepath.Dir(full), 0o755); err != nil {
				return "", receipt, err
			}
			if err := writeNewFileNoSymlink(absWork, full, f.body, 0o644, manifest); err != nil {
				return "", receipt, fmt.Errorf("write skill %s/%s: %w", sk.slug, f.rel, err)
			}
			rec.Files = append(rec.Files, MaterializedFile{RelPath: f.rel, SHA256: sha256Hex(f.body)})
		}
		receipt.Skills = append(receipt.Skills, rec)
	}

	// Project resources.
	if staticCtx.ProjectID != "" || len(staticCtx.ProjectResources) > 0 {
		resources := staticCtx.ProjectResources
		if resources == nil {
			resources = []ProjectResourceForEnv{}
		}
		data, err := json.MarshalIndent(projectResourceFile{
			ProjectID: staticCtx.ProjectID, ProjectTitle: staticCtx.ProjectTitle, Resources: resources,
		}, "", "  ")
		if err != nil {
			return "", receipt, err
		}
		data = append(data, '\n')
		projDir := filepath.Join(absWork, ".multica", "project")
		if err := mkdirAllWithoutSymlink(absWork, projDir, 0o755); err != nil {
			return "", receipt, err
		}
		marker := filepath.Join(projDir, managedResourcesMarker)
		resPath := filepath.Join(projDir, "resources.json")
		if err := writeNewFileNoSymlink(absWork, marker, []byte("resources.json\n"), 0o644, manifest); err != nil {
			return "", receipt, err
		}
		if err := writeNewFileNoSymlink(absWork, resPath, data, 0o644, manifest); err != nil {
			return "", receipt, err
		}
		receipt.ResourcesFinalSHA256 = sha256Hex(data)
	}

	if err := writeSidecarManifestAtomic(absLedger, manifest); err != nil {
		return "", receipt, fmt.Errorf("write ledger: %w", err)
	}
	// Persist receipt next to ledger (atomic).
	if err := writeReceiptAtomic(absLedger, receipt); err != nil {
		return "", receipt, fmt.Errorf("write receipt: %w", err)
	}
	return brief, receipt, nil
}

const materializationReceiptFile = "materialization_receipt.json"

func writeReceiptAtomic(ledgerRoot string, receipt MaterializationReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	final := filepath.Join(ledgerRoot, materializationReceiptFile)
	tmp, err := os.CreateTemp(ledgerRoot, ".receipt-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, final)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// synthesizeRuntimeConfigBytes returns final AGENTS/CLAUDE file bytes after
// Multica managed block inject (same logic as writeRuntimeConfigFile, pure).
func synthesizeRuntimeConfigBytes(path, brief string) ([]byte, error) {
	block := runtimeMarkerBegin + "\n" + strings.TrimRight(brief, "\n") + "\n" + runtimeMarkerEnd + "\n"
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte(block), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing runtime config %s: %w", path, err)
	}
	existingStr := string(existing)
	if start, end, ok := locateMarkerBlock(existingStr); ok {
		return []byte(existingStr[:start] + block + existingStr[end:]), nil
	}
	return []byte(existingStr + runtimeManagedSeparator + block), nil
}

// atomicWriteFilePreserveMode writes via temp+rename; keeps existing mode.
func atomicWriteFilePreserveMode(path string, data []byte, defaultMode os.FileMode) error {
	mode := defaultMode
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing atomic write through symlink: %s", path)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// writeNewFileNoSymlink creates a new file only (fail if exists or symlink).
func writeNewFileNoSymlink(workDir, path string, data []byte, perm os.FileMode, m *sidecarManifest) error {
	if err := validatePathUnderWorkDirNoSymlink(workDir, filepath.Dir(path)); err != nil {
		return fmt.Errorf("unsafe parent: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%w: %s", errPathPreExists, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return recordWriteFile(path, data, perm, m)
}

// reclaimLegacyIssueContextOrphan removes Multica-owned issue_context without
// marker (pre-B crash orphans). Symlink parent → error.
func reclaimLegacyIssueContextOrphan(workDir string) error {
	ctxDir := filepath.Join(workDir, ".agent_context")
	path := filepath.Join(ctxDir, "issue_context.md")
	marker := filepath.Join(ctxDir, managedIssueContextMarker)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	// Parent chain symlink check.
	if err := validatePathUnderWorkDirNoSymlink(workDir, ctxDir); err != nil {
		return fmt.Errorf("legacy issue_context parent unsafe: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("legacy issue_context is symlink: %s", path)
	}
	// If marker present, reclaimMarked handles it.
	if _, err := os.Lstat(marker); err == nil {
		return nil
	}
	// No marker: Multica-owned historical orphan — delete.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ManagedStartupInputDigest is the fingerprint input (zero I/O pure render).
func ManagedStartupInputDigest(provider string, ctx TaskContextForEnv) string {
	return RenderStartupMaterializationPlan(provider, StartupStaticContext(ctx)).Digest()
}
