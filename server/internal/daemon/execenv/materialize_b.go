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

// MaterializationReceipt records create-time materialize actions (Barry B).
// Not used for reuse fingerprint.
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

// MaterializedFile is one file under a managed skill package.
type MaterializedFile struct {
	RelPath string `json:"rel_path"`
	SHA256  string `json:"sha256"`
}

// ResolvedSkillPackage is one skill in the single ResolvedMaterializationPlan.
type ResolvedSkillPackage struct {
	LogicalName string
	ActualSlug  string
	Decision    string // created | sibling
	Files       []ResolvedSkillFile
}

// ResolvedSkillFile is one file body to write under ActualSlug.
type ResolvedSkillFile struct {
	RelPath string
	Body    []byte
}

// ResolvedMaterializationPlan is the single create-time plan consumed by
// brief skill index, disk writer, and MaterializationReceipt — no second slug math.
type ResolvedMaterializationPlan struct {
	Provider    string
	Brief       string // managed brief including skill index with actual slugs
	AgentsFinal []byte // full AGENTS/CLAUDE file after synthesis
	Skills      []ResolvedSkillPackage
	Resources   []byte // resources.json body or nil
	InputDigest string
}

// MaterializeCanonicalTurnContextB implements Barry B with one resolved plan.
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

	// --- Phase 1: fail-closed cleanup (writes only reclaim Multica-owned) ---
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

	// --- Phase 2: resolve plan (read-only slug planning; no apply writes) ---
	plan, err := resolveMaterializationPlan(absWork, provider, staticCtx)
	if err != nil {
		return "", receipt, fmt.Errorf("resolve materialization plan: %w", err)
	}
	receipt.ManagedInputDigest = plan.InputDigest
	brief = plan.Brief

	// --- Phase 3: apply resolved plan only ---
	manifest := &sidecarManifest{}
	if len(plan.AgentsFinal) > 0 {
		agentsPath := runtimeConfigPath(absWork, provider)
		if err := atomicWriteFilePreserveMode(agentsPath, plan.AgentsFinal, 0o644); err != nil {
			return "", receipt, fmt.Errorf("write AGENTS: %w", err)
		}
		receipt.AgentsFinalSHA256 = sha256Hex(plan.AgentsFinal)
	}

	skillsParent := skillsDirPath(absWork, provider)
	for _, sk := range plan.Skills {
		// Package staging: write all files under .tmp then rename into place.
		finalDir := filepath.Join(skillsParent, sk.ActualSlug)
		stageDir := finalDir + ".multica-staging"
		_ = os.RemoveAll(stageDir)
		if err := mkdirAllWithoutSymlink(absWork, stageDir, 0o755); err != nil {
			return "", receipt, err
		}
		// Marker first inside staging.
		markerBody := []byte(sk.LogicalName + "\n")
		if err := os.WriteFile(filepath.Join(stageDir, managedSkillMarker), markerBody, 0o644); err != nil {
			_ = os.RemoveAll(stageDir)
			return "", receipt, err
		}
		rec := MaterializedSkillReceipt{
			LogicalName: sk.LogicalName,
			ActualSlug:  sk.ActualSlug,
			Decision:    sk.Decision,
			Files: []MaterializedFile{
				{RelPath: managedSkillMarker, SHA256: sha256Hex(markerBody)},
			},
		}
		for _, f := range sk.Files {
			full := filepath.Join(stageDir, filepath.FromSlash(f.RelPath))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				_ = os.RemoveAll(stageDir)
				return "", receipt, err
			}
			if err := os.WriteFile(full, f.Body, 0o644); err != nil {
				_ = os.RemoveAll(stageDir)
				return "", receipt, err
			}
			rec.Files = append(rec.Files, MaterializedFile{RelPath: f.RelPath, SHA256: sha256Hex(f.Body)})
		}
		// Reclaim final if managed leftover, then atomic rename staging → final.
		if err := validatePathUnderWorkDirNoSymlink(absWork, filepath.Dir(finalDir)); err != nil {
			_ = os.RemoveAll(stageDir)
			return "", receipt, err
		}
		if isManagedSkillDir(finalDir) {
			if err := os.RemoveAll(finalDir); err != nil {
				_ = os.RemoveAll(stageDir)
				return "", receipt, err
			}
		}
		if _, err := os.Lstat(finalDir); err == nil {
			// Unmarked collision should have been resolved to sibling; fail closed.
			_ = os.RemoveAll(stageDir)
			return "", receipt, fmt.Errorf("skill final dir still exists after resolve: %s", finalDir)
		}
		if err := os.Rename(stageDir, finalDir); err != nil {
			_ = os.RemoveAll(stageDir)
			return "", receipt, fmt.Errorf("activate skill package %s: %w", sk.ActualSlug, err)
		}
		// Record package root in manifest for cleanup.
		if manifest != nil {
			manifest.Dirs = append(manifest.Dirs, finalDir)
			for _, f := range rec.Files {
				manifest.Files = append(manifest.Files, filepath.Join(finalDir, filepath.FromSlash(f.RelPath)))
			}
		}
		receipt.Skills = append(receipt.Skills, rec)
	}

	if len(plan.Resources) > 0 {
		projDir := filepath.Join(absWork, ".multica", "project")
		if err := mkdirAllWithoutSymlink(absWork, projDir, 0o755); err != nil {
			return "", receipt, err
		}
		marker := filepath.Join(projDir, managedResourcesMarker)
		resPath := filepath.Join(projDir, "resources.json")
		if err := writeNewFileNoSymlink(absWork, marker, []byte("resources.json\n"), 0o644, manifest); err != nil {
			return "", receipt, err
		}
		if err := writeNewFileNoSymlink(absWork, resPath, plan.Resources, 0o644, manifest); err != nil {
			return "", receipt, err
		}
		receipt.ResourcesFinalSHA256 = sha256Hex(plan.Resources)
	}

	if err := writeSidecarManifestAtomic(absLedger, manifest); err != nil {
		return "", receipt, fmt.Errorf("write ledger: %w", err)
	}
	if err := writeReceiptAtomic(absLedger, receipt); err != nil {
		return "", receipt, fmt.Errorf("write receipt: %w", err)
	}
	return brief, receipt, nil
}

// resolveMaterializationPlan is read-only for skill slug planning (no apply).
// Cleanup must already have run. Builds one plan for brief + writer + receipt.
func resolveMaterializationPlan(absWork, provider string, staticCtx TaskContextForEnv) (ResolvedMaterializationPlan, error) {
	plan := ResolvedMaterializationPlan{
		Provider:    provider,
		InputDigest: ManagedStartupInputDigest(provider, staticCtx),
	}

	slugByName := map[string]string{}
	if len(staticCtx.AgentSkills) > 0 && provider != "codex" {
		skillsDir := skillsDirPath(absWork, provider)
		// Parent may not exist yet — planning only Lstats candidates.
		for _, sk := range staticCtx.AgentSkills {
			baseSlug := sanitizeSkillName(sk.Name)
			slug, decision, err := planCollisionFreeSkillSlug(skillsDir, baseSlug)
			if err != nil {
				return plan, fmt.Errorf("plan skill slug %q: %w", sk.Name, err)
			}
			slugByName[sk.Name] = slug
			body := ensureSkillFrontmatter(sk.Content, slug, sk.Description)
			files := []ResolvedSkillFile{{RelPath: "SKILL.md", Body: []byte(body)}}
			for _, f := range sk.Files {
				rel := strings.TrimPrefix(filepath.ToSlash(f.Path), "/")
				if rel == "" || rel == "SKILL.md" {
					continue
				}
				files = append(files, ResolvedSkillFile{RelPath: rel, Body: []byte(f.Content)})
			}
			plan.Skills = append(plan.Skills, ResolvedSkillPackage{
				LogicalName: sk.Name,
				ActualSlug:  slug,
				Decision:    decision,
				Files:       files,
			})
		}
	}

	// Brief index uses the same actual slugs (single plan — no second sanitize).
	staticCtx.SkillDirSlugByName = slugByName
	plan.Brief = buildMetaSkillContent(provider, staticCtx)

	agentsPath := runtimeConfigPath(absWork, provider)
	if agentsPath != "" {
		if err := validatePathUnderWorkDirNoSymlink(absWork, filepath.Dir(agentsPath)); err != nil {
			return plan, fmt.Errorf("AGENTS parent unsafe: %w", err)
		}
		if info, err := os.Lstat(agentsPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return plan, fmt.Errorf("refusing symlink AGENTS path: %s", agentsPath)
		}
		final, err := synthesizeRuntimeConfigBytes(agentsPath, plan.Brief)
		if err != nil {
			return plan, err
		}
		plan.AgentsFinal = final
	}

	if staticCtx.ProjectID != "" || len(staticCtx.ProjectResources) > 0 {
		resources := staticCtx.ProjectResources
		if resources == nil {
			resources = []ProjectResourceForEnv{}
		}
		data, err := json.MarshalIndent(projectResourceFile{
			ProjectID: staticCtx.ProjectID, ProjectTitle: staticCtx.ProjectTitle, Resources: resources,
		}, "", "  ")
		if err != nil {
			return plan, err
		}
		plan.Resources = append(data, '\n')
	}
	return plan, nil
}

// planCollisionFreeSkillSlug chooses a slug without mutating the filesystem.
// Managed leftover dirs are treated as reclaimable (decision "created").
// Unmarked existing dirs force sibling allocation.
func planCollisionFreeSkillSlug(skillsParent, baseSlug string) (slug, decision string, err error) {
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
				if i == 0 {
					return candidate, "created", nil
				}
				return candidate, "sibling", nil
			}
			return "", "", statErr
		}
		if !st.IsDir() {
			continue
		}
		if isManagedSkillDir(path) {
			// Apply will RemoveAll then rename staging here.
			if i == 0 {
				return candidate, "created", nil
			}
			return candidate, "sibling", nil
		}
		// Unmarked user-owned — try next slug.
	}
	return "", "", fmt.Errorf("exhausted %d skill slug attempts for %q", maxAttempts, baseSlug)
}

const materializationReceiptFile = "materialization_receipt.json"

func writeReceiptAtomic(ledgerRoot string, receipt MaterializationReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFilePreserveMode(filepath.Join(ledgerRoot, materializationReceiptFile), data, 0o644)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

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

// atomicWriteFilePreserveMode: temp write → Sync → Close → Rename; keep mode.
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
	if err := tmp.Sync(); err != nil {
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
	// Best-effort directory durability (platform-dependent).
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

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
	if err := validatePathUnderWorkDirNoSymlink(workDir, ctxDir); err != nil {
		return fmt.Errorf("legacy issue_context parent unsafe: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("legacy issue_context is symlink: %s", path)
	}
	if _, err := os.Lstat(marker); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ManagedStartupInputDigest is the fingerprint input (zero I/O pure render).
func ManagedStartupInputDigest(provider string, ctx TaskContextForEnv) string {
	return RenderStartupMaterializationPlan(provider, StartupStaticContext(ctx)).Digest()
}
