package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaterializationReceipt records create-time AGENTS write (slim D6-1b).
// Skills / resources / issue_context are intentionally NOT written to workdir.
type MaterializationReceipt struct {
	AgentsFinalSHA256  string `json:"agents_final_sha256,omitempty"`
	ManagedInputDigest string `json:"managed_input_digest,omitempty"`
}

// MaterializeCanonicalTurnContextB is the slim create-only path:
// atomic AGENTS/CLAUDE managed-block replace only. No skill tree, no
// resources.json, no issue_context, no ledger/staging cleanup.
//
// ledgerRoot is accepted for call-site compatibility but ignored (no ledger).
func MaterializeCanonicalTurnContextB(workDir, ledgerRoot, provider string, ctx TaskContextForEnv) (brief string, receipt MaterializationReceipt, err error) {
	_ = ledgerRoot
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return "", receipt, errors.New("canonical turn workdir is required")
	}
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return "", receipt, fmt.Errorf("abs workdir: %w", err)
	}
	if err := validateManagedBase(absWork); err != nil {
		return "", receipt, fmt.Errorf("canonical workdir invalid: %w", err)
	}

	staticCtx := StartupStaticContext(ctx)
	plan := RenderStartupMaterializationPlan(provider, staticCtx)
	receipt.ManagedInputDigest = plan.Digest()
	brief = plan.RuntimeBrief

	agentsPath := runtimeConfigPath(absWork, provider)
	if agentsPath == "" {
		return brief, receipt, nil
	}
	// Refuse symlink AGENTS path (do not follow / write through symlink).
	if info, err := os.Lstat(agentsPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", receipt, fmt.Errorf("refusing symlink AGENTS path: %s", agentsPath)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", receipt, err
	}
	if err := validatePathUnderWorkDirNoSymlink(absWork, filepath.Dir(agentsPath)); err != nil {
		return "", receipt, fmt.Errorf("AGENTS parent unsafe: %w", err)
	}

	final, err := synthesizeRuntimeConfigBytes(agentsPath, plan.RuntimeBrief)
	if err != nil {
		return "", receipt, err
	}
	if err := atomicWriteFilePreserveMode(agentsPath, final, 0o644); err != nil {
		return "", receipt, fmt.Errorf("write AGENTS: %w", err)
	}
	receipt.AgentsFinalSHA256 = sha256Hex(final)
	return brief, receipt, nil
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
// Refuses write through symlink.
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
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// ManagedStartupInputDigest is the fingerprint input (zero I/O pure render).
func ManagedStartupInputDigest(provider string, ctx TaskContextForEnv) string {
	return RenderStartupMaterializationPlan(provider, StartupStaticContext(ctx)).Digest()
}
