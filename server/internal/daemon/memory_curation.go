package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

func (d *Daemon) handleMemoryCuration(ctx context.Context, rt Runtime, pending PendingMemoryCuration) {
	defer d.finishMemoryCurationRun(rt.ID, pending.ID)
	d.logger.Info("memory curation requested", "runtime_id", rt.ID, "run_id", pending.ID, "stage", pending.Stage)
	payload := map[string]any{"status": "failed", "claim_token": pending.ClaimToken}

	stage, err := memorycuration.NormalizeStage(pending.Stage)
	if err != nil {
		payload["error"] = err.Error()
		d.reportMemoryCurationResult(ctx, rt, pending.ID, payload)
		return
	}
	since, err := time.Parse("2006-01-02", pending.DateFrom)
	if err != nil {
		payload["error"] = "invalid date_from"
		d.reportMemoryCurationResult(ctx, rt, pending.ID, payload)
		return
	}
	until, err := time.Parse("2006-01-02", pending.DateTo)
	if err != nil {
		payload["error"] = "invalid date_to"
		d.reportMemoryCurationResult(ctx, rt, pending.ID, payload)
		return
	}
	entry, ok := d.cfg.Agents[rt.Provider]
	if !ok || strings.TrimSpace(entry.Path) == "" {
		payload["error"] = fmt.Sprintf("no CLI configured for provider %q", rt.Provider)
		d.reportMemoryCurationResult(ctx, rt, pending.ID, payload)
		return
	}
	model := strings.TrimSpace(pending.CuratorModel)
	if model == "" {
		model = entry.Model
	}
	curatorRoot := multicaAgentRoot(d.cfg, pending.WorkspaceID, pending.CuratorAgentID)
	reviewerCfg := memorycuration.AgentL3ReviewerConfig{
		Provider:       rt.Provider,
		Path:           entry.Path,
		Model:          model,
		ThinkingLevel:  pending.CuratorThinkingLevel,
		CustomArgs:     pending.CuratorCustomArgs,
		McpConfig:      pending.CuratorMcpConfig,
		Timeout:        d.cfg.MemoryCurationL3ReviewTimeout,
		CuratorAgentID: pending.CuratorAgentID,
		CuratorRoot:    curatorRoot,
		Instructions:   pending.CuratorInstructions,
	}
	reviewer := memorycuration.NewConfiguredL3Reviewer(false, reviewerCfg)
	stageAgent, err := memorycuration.NewAgentStageRunner(reviewerCfg)
	if err != nil {
		payload["error"] = err.Error()
		d.reportMemoryCurationResult(ctx, rt, pending.ID, payload)
		return
	}
	dbEvidence := make(map[string][]memorycuration.EvidenceItem, len(pending.DBEvidence))
	for _, bundle := range pending.DBEvidence {
		items := make([]memorycuration.EvidenceItem, 0, len(bundle.Items))
		for _, item := range bundle.Items {
			createdAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
			items = append(items, memorycuration.EvidenceItem{Kind: item.Kind, ID: item.ID, Title: item.Title, Snippet: item.Snippet, CreatedAt: createdAt})
		}
		dbEvidence[bundle.AgentID] = items
	}
	effectiveDryRun := pending.DryRun || strings.EqualFold(strings.TrimSpace(pending.Mode), "observe")
	res, runErr := memorycuration.NewEngine(reviewer).Run(memorycuration.Options{
		Context:             ctx,
		DBEvidence:          dbEvidence,
		StageAgent:          stageAgent,
		WorkspacesRoot:      d.cfg.WorkspacesRoot,
		WorkspaceID:         pending.WorkspaceID,
		AgentIDs:            pending.AgentIDs,
		Stage:               stage,
		Since:               since,
		Until:               until,
		IncludeHistory:      pending.IncludeHistory,
		DryRun:              effectiveDryRun,
		Force:               pending.Force,
		Now:                 time.Now().UTC(),
		Timezone:            pending.Timezone,
		Mode:                pending.Mode,
		ConfidenceThreshold: pending.ConfidenceThreshold,
	})
	payload["result"] = res
	if runErr != nil {
		payload["error"] = runErr.Error()
	} else if len(res.Errors) > 0 {
		payload["error"] = "one or more agents failed"
	} else if stage == memorycuration.StageL3 && shouldRetryLocalL3(res) {
		payload["error"] = "L3 reviewer deferred retryable candidates"
	} else {
		payload["status"] = "succeeded"
	}
	d.reportMemoryCurationResult(ctx, rt, pending.ID, payload)
}

func (d *Daemon) reportMemoryCurationResult(ctx context.Context, rt Runtime, runID string, payload map[string]any) {
	// Normalize result through JSON so map payloads and engine structs share the
	// same daemon callback contract.
	if result, ok := payload["result"]; ok {
		if encoded, err := json.Marshal(result); err == nil {
			payload["result"] = json.RawMessage(encoded)
		}
	}
	d.reportRuntimeResultWithRetry(context.WithoutCancel(ctx), "memory_curation", rt.ID, runID, func(ctx context.Context) error {
		return d.client.ReportMemoryCurationResult(ctx, rt.ID, runID, payload)
	})
}

func (d *Daemon) beginMemoryCurationRun(runtimeID, runID string) bool {
	d.memoryCurationMu.Lock()
	defer d.memoryCurationMu.Unlock()
	if d.activeCurationRuns == nil {
		d.activeCurationRuns = make(map[string]string)
	}
	if d.activeCurationRuns[runtimeID] != "" {
		return false
	}
	d.activeCurationRuns[runtimeID] = runID
	return true
}

func (d *Daemon) finishMemoryCurationRun(runtimeID, runID string) {
	d.memoryCurationMu.Lock()
	defer d.memoryCurationMu.Unlock()
	if d.activeCurationRuns[runtimeID] == runID {
		delete(d.activeCurationRuns, runtimeID)
	}
}

func (d *Daemon) activeMemoryCurationRun(runtimeID string) string {
	d.memoryCurationMu.Lock()
	defer d.memoryCurationMu.Unlock()
	return d.activeCurationRuns[runtimeID]
}
