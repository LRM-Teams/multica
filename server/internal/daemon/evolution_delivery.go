package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (d *Daemon) evolutionDeliveryLoop(ctx context.Context) {
	interval := d.cfg.SharedSkillsSyncInterval
	if interval <= 0 {
		return
	}
	d.syncEvolutionDeliveriesOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.syncEvolutionDeliveriesOnce(ctx)
		}
	}
}

func (d *Daemon) syncEvolutionDeliveriesOnce(ctx context.Context) {
	if !d.ready.Load() {
		return
	}
	for _, rt := range d.sharedSkillSyncRuntimes() {
		if rt.Provider != "pi" || strings.TrimSpace(rt.WorkspaceID) == "" {
			continue
		}
		if err := d.syncEvolutionDeliveriesForRuntime(ctx, rt); err != nil && ctx.Err() == nil {
			d.logger.Warn("evolution deliveries sync failed", "runtime_id", rt.ID, "provider", rt.Provider, "error", err)
		}
	}
}

func (d *Daemon) syncEvolutionDeliveriesForRuntime(ctx context.Context, rt Runtime) error {
	agentIDs := d.piAgentIDsForWorkspace(rt.WorkspaceID)
	for _, agentID := range agentIDs {
		deliveries, err := d.client.ListEvolutionDeliveries(ctx, rt.ID, agentID)
		if err != nil {
			return err
		}
		for _, delivery := range deliveries {
			deliveredPath, err := d.writeEvolutionDelivery(rt, agentID, delivery)
			if err != nil {
				d.logger.Warn("evolution delivery write failed", "runtime_id", rt.ID, "agent_id", agentID, "delivery_id", delivery.ID, "error", err)
				_ = d.client.FailEvolutionDelivery(ctx, rt.ID, agentID, delivery.ID, err.Error())
				continue
			}
			if delivery.DeliveryType == "generated" && delivery.Status == "accepted" {
				deliveredPath, err = d.enableGeneratedSkillDelivery(rt.WorkspaceID, agentID, delivery, deliveredPath)
				if err != nil {
					d.logger.Warn("generated skill enable failed", "runtime_id", rt.ID, "agent_id", agentID, "delivery_id", delivery.ID, "error", err)
					_ = d.client.FailEvolutionDelivery(ctx, rt.ID, agentID, delivery.ID, err.Error())
					continue
				}
			}
			if err := d.client.MarkEvolutionDeliveryDelivered(ctx, rt.ID, agentID, delivery.ID, deliveredPath); err != nil {
				return err
			}
			d.logger.Debug("evolution delivery written", "runtime_id", rt.ID, "agent_id", agentID, "delivery_id", delivery.ID, "path", deliveredPath)
		}
	}
	return nil
}

func (d *Daemon) piAgentIDsForWorkspace(workspaceID string) []string {
	base := filepath.Join(d.cfg.WorkspacesRoot, workspaceID, ".pi", "agents")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	agentIDs := []string{}
	for _, entry := range entries {
		if entry.IsDir() && !isIgnoredLocalSkillEntry(entry.Name()) {
			agentIDs = append(agentIDs, entry.Name())
		}
	}
	return agentIDs
}

func (d *Daemon) writeEvolutionDelivery(rt Runtime, agentID string, delivery EvolutionDelivery) (string, error) {
	agentRoot := piAgentRoot(d.cfg, rt.WorkspaceID, agentID)
	if err := ensurePiAgentRoot(agentRoot); err != nil {
		return "", err
	}
	switch delivery.UnitType {
	case "skill":
		return writeGeneratedSkillDelivery(agentRoot, delivery)
	case "memory", "preference", "tool_pattern", "workflow":
		return writeMemoryInboxDelivery(agentRoot, delivery)
	default:
		return "", fmt.Errorf("unsupported evolution unit type %q", delivery.UnitType)
	}
}

func writeGeneratedSkillDelivery(agentRoot string, delivery EvolutionDelivery) (string, error) {
	unitDir := filepath.Join(agentRoot, "skills", "generated", safePathName(delivery.UnitID))
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return "", err
	}
	for _, file := range delivery.Files {
		if !isSafeRelativePath(file.Path) {
			return "", fmt.Errorf("invalid delivery file path %q", file.Path)
		}
		path := filepath.Join(unitDir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(file.Content), 0o644); err != nil {
			return "", err
		}
	}
	metadata, _ := json.MarshalIndent(map[string]any{
		"delivery_id": delivery.ID,
		"unit_id":     delivery.UnitID,
		"version_id":  delivery.VersionID,
		"enabled":     false,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(unitDir, ".multica-delivery.json"), metadata, 0o644); err != nil {
		return "", err
	}
	return unitDir, nil
}

func (d *Daemon) enableGeneratedSkillDelivery(workspaceID, agentID string, delivery EvolutionDelivery, generatedDir string) (string, error) {
	enabledRoot := filepath.Join(piAgentRoot(d.cfg, workspaceID, agentID), "skills", "enabled")
	if err := os.MkdirAll(enabledRoot, 0o755); err != nil {
		return "", err
	}
	enabledDir := filepath.Join(enabledRoot, safePathName(delivery.UnitID))
	if err := os.RemoveAll(enabledDir); err != nil {
		return "", err
	}
	if err := copyDir(generatedDir, enabledDir); err != nil {
		return "", err
	}
	metadataPath := filepath.Join(enabledDir, ".multica-delivery.json")
	metadata, err := json.MarshalIndent(map[string]any{
		"delivery_id": delivery.ID,
		"unit_id":     delivery.UnitID,
		"version_id":  delivery.VersionID,
		"enabled":     true,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(metadataPath, metadata, 0o644); err != nil {
		return "", err
	}
	return enabledDir, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func writeMemoryInboxDelivery(agentRoot string, delivery EvolutionDelivery) (string, error) {
	dir := filepath.Join(agentRoot, "inbox", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, safePathName(delivery.UnitID)+".md")
	content := fmt.Sprintf("---\ndelivery_id: %q\nunit_id: %q\nversion_id: %q\nunit_type: %q\ntitle: %q\n---\n\n# %s\n\n%s\n\n%s\n", delivery.ID, delivery.UnitID, delivery.VersionID, delivery.UnitType, delivery.Title, delivery.Title, delivery.CanonicalSummary, delivery.Content)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func isSafeRelativePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return false
	}
	return filepath.Clean(path) != "."
}

func safePathName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", "..", "-", " ", "-")
	return replacer.Replace(raw)
}
