package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxAgentHonorShowcase = 3

type AgentHonorUnlockEvent struct {
	WorkspaceID pgtype.UUID
	AgentID     pgtype.UUID
	Achievement AgentAchievementView
}

type AgentFleetClassEvent struct {
	WorkspaceID pgtype.UUID
	AgentID     pgtype.UUID
	Previous    string
	Current     string
	FleetScore  float64
}

type AgentHonorMetricsView struct {
	CompletedCount      int64 `json:"completed_count"`
	FailedCount         int64 `json:"failed_count"`
	SuccessStreak       int64 `json:"success_streak"`
	MemoryWrites        int64 `json:"memory_writes"`
	EvolutionPromotions int64 `json:"evolution_promotions"`
	DistinctProjects    int64 `json:"distinct_projects"`
	RecoveryCount       int64 `json:"recovery_count"`
}

type AgentAchievementProgress struct {
	Current int64 `json:"current"`
	Target  int64 `json:"target"`
}

type AgentAchievementView struct {
	ID          string                    `json:"id"`
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	SvgKey      string                    `json:"svg_key"`
	Category    string                    `json:"category"`
	XPReward    int32                     `json:"xp_reward"`
	Rarity      int                       `json:"rarity"`
	Secret      bool                      `json:"secret"`
	Unlocked    bool                      `json:"unlocked"`
	UnlockedAt  *string                   `json:"unlocked_at,omitempty"`
	UnlockPct   float64                   `json:"unlock_pct,omitempty"`
	Progress    *AgentAchievementProgress `json:"progress,omitempty"`
}

type AgentHonorEventView struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	SourceRef string `json:"source_ref"`
	XPDelta   int32  `json:"xp_delta"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

type AgentFleetHistoryView struct {
	FleetScore float64           `json:"fleet_score"`
	ClassID    string            `json:"class_id"`
	ClassLabel string            `json:"class_label"`
	FleetRank  int               `json:"fleet_rank"`
	FleetSize  int               `json:"fleet_size"`
	Pillars    FleetPillarScores `json:"pillars"`
	RecordedAt string            `json:"recorded_at"`
}

type AgentHonorDashboard struct {
	AgentID                string                    `json:"agent_id"`
	Level                  int                       `json:"level"`
	TotalXP                int64                     `json:"total_xp"`
	XPToNextLevel          int64                     `json:"xp_to_next_level"`
	EquippedAchievementID  *string                   `json:"equipped_achievement_id,omitempty"`
	ShowcaseAchievementIDs []string                  `json:"showcase_achievement_ids"`
	Metrics                AgentHonorMetricsView     `json:"metrics"`
	Fleet                  AgentFleetRankView        `json:"fleet"`
	NextFleetClass         *AgentHonorClassThreshold `json:"next_fleet_class,omitempty"`
	Achievements           []AgentAchievementView    `json:"achievements"`
	RecentEvents           []AgentHonorEventView     `json:"recent_events"`
	FleetHistory           []AgentFleetHistoryView   `json:"fleet_history"`
	RulesVersion           string                    `json:"rules_version"`
}

type AgentHonorRulesView struct {
	Revision     int32                        `json:"revision"`
	Rules        AgentHonorRules              `json:"rules"`
	Achievements []AgentAchievementDefinition `json:"achievements"`
}

type AgentHonorAdminAuditView struct {
	ID        string         `json:"id"`
	AgentID   *string        `json:"agent_id,omitempty"`
	Action    string         `json:"action"`
	Details   map[string]any `json:"details"`
	CreatedBy string         `json:"created_by"`
	CreatedAt string         `json:"created_at"`
}

type AgentHonorService struct {
	Queries               *db.Queries
	Fleet                 *AgentFleetRankService
	OnAchievementUnlocked func(ctx context.Context, evt AgentHonorUnlockEvent)
	OnFleetClassChanged   func(ctx context.Context, evt AgentFleetClassEvent)
}

func NewAgentHonorService(queries *db.Queries, fleet *AgentFleetRankService) *AgentHonorService {
	return &AgentHonorService{Queries: queries, Fleet: fleet}
}

func (s *AgentHonorService) GetRules(ctx context.Context, workspaceID pgtype.UUID) (AgentHonorRulesView, error) {
	rules, revision, err := loadAgentHonorRules(ctx, s.Queries, workspaceID)
	if err != nil {
		return AgentHonorRulesView{}, err
	}
	return AgentHonorRulesView{
		Revision:     revision,
		Rules:        rules,
		Achievements: effectiveAgentAchievementCatalog(rules),
	}, nil
}

func (s *AgentHonorService) UpdateRules(
	ctx context.Context,
	workspaceID, updatedBy pgtype.UUID,
	rules AgentHonorRules,
) (AgentHonorRulesView, error) {
	if err := validateAgentHonorRules(rules); err != nil {
		return AgentHonorRulesView{}, err
	}
	rules.Version = AgentHonorRulesVersion
	raw, err := json.Marshal(rules)
	if err != nil {
		return AgentHonorRulesView{}, err
	}
	row, err := s.Queries.UpsertAgentHonorRuleConfig(ctx, db.UpsertAgentHonorRuleConfigParams{
		WorkspaceID: workspaceID,
		Config:      raw,
		UpdatedBy:   updatedBy,
	})
	if err != nil {
		return AgentHonorRulesView{}, err
	}
	_, _ = s.Queries.InsertAgentHonorAdminAudit(ctx, db.InsertAgentHonorAdminAuditParams{
		WorkspaceID: workspaceID,
		Action:      "rules.update",
		Details:     raw,
		CreatedBy:   updatedBy,
	})
	return AgentHonorRulesView{
		Revision:     row.Version,
		Rules:        rules,
		Achievements: effectiveAgentAchievementCatalog(rules),
	}, nil
}

func (s *AgentHonorService) RefreshAgent(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
	triggerReason string,
) error {
	if s == nil || s.Queries == nil || !workspaceID.Valid || !agentID.Valid {
		return nil
	}
	rules, _, err := loadAgentHonorRules(ctx, s.Queries, workspaceID)
	if err != nil {
		return err
	}
	if _, err := s.Queries.CreateAgentHonorStateIfMissing(ctx, db.CreateAgentHonorStateIfMissingParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	}); err != nil {
		return err
	}
	if _, err := s.Queries.BackfillAgentDeliveryHonorEvents(ctx, db.BackfillAgentDeliveryHonorEventsParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		XpDelta:     rules.CompletionXP,
	}); err != nil {
		return err
	}

	if s.Fleet != nil {
		changes, err := s.Fleet.RefreshWorkspace(ctx, workspaceID, triggerReason)
		if err != nil {
			return err
		}
		for _, change := range changes {
			if change.PreviousClassID == "" || change.PreviousClassID == change.Current.ClassID {
				continue
			}
			if s.OnFleetClassChanged != nil {
				s.OnFleetClassChanged(ctx, AgentFleetClassEvent{
					WorkspaceID: workspaceID,
					AgentID:     change.CurrentAgentID,
					Previous:    change.PreviousClassID,
					Current:     change.Current.ClassID,
					FleetScore:  change.Current.FleetScore,
				})
			}
		}
	}

	metrics, err := s.loadMetrics(ctx, workspaceID, agentID)
	if err != nil {
		return err
	}
	fleet, err := s.currentFleet(ctx, workspaceID, agentID)
	if err != nil {
		return err
	}
	unlockedRows, err := s.Queries.ListAgentHonorUnlocks(ctx, db.ListAgentHonorUnlocksParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		return err
	}
	unlocked := make(map[string]struct{}, len(unlockedRows))
	for _, row := range unlockedRows {
		unlocked[row.AchievementID] = struct{}{}
	}
	newlyUnlocked := make([]string, 0, 3)
	for _, def := range effectiveAgentAchievementCatalog(rules) {
		if _, ok := unlocked[def.ID]; ok {
			continue
		}
		if achievementMetricValue(def.Metric, metrics, fleet.ClassID) < def.Target {
			continue
		}
		row, err := s.Queries.InsertAgentHonorUnlockIfNew(ctx, db.InsertAgentHonorUnlockIfNewParams{
			WorkspaceID:   workspaceID,
			AgentID:       agentID,
			AchievementID: def.ID,
			Source:        "auto",
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return err
		}
		metadata, _ := json.Marshal(map[string]any{
			"metric":  def.Metric,
			"current": achievementMetricValue(def.Metric, metrics, fleet.ClassID),
			"target":  def.Target,
		})
		if _, err := s.Queries.InsertAgentHonorEventIfNew(ctx, db.InsertAgentHonorEventIfNewParams{
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			EventType:   "achievement",
			SourceRef:   def.ID,
			XpDelta:     def.XPReward,
			Reason:      def.Title,
			Metadata:    metadata,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if s.OnAchievementUnlocked != nil {
			unlockedAt := row.UnlockedAt.Time.UTC().Format(time.RFC3339)
			s.OnAchievementUnlocked(ctx, AgentHonorUnlockEvent{
				WorkspaceID: workspaceID,
				AgentID:     agentID,
				Achievement: AgentAchievementView{
					ID: def.ID, Title: def.Title, Description: def.Description,
					SvgKey: def.SvgKey, Category: def.Category, XPReward: def.XPReward,
					Rarity: def.Rarity, Secret: def.Secret, Unlocked: true, UnlockedAt: &unlockedAt,
				},
			})
		}
		newlyUnlocked = append(newlyUnlocked, def.ID)
	}
	if len(newlyUnlocked) > 0 {
		state, stateErr := s.Queries.GetAgentHonorState(ctx, db.GetAgentHonorStateParams{
			WorkspaceID: workspaceID,
			AgentID:     agentID,
		})
		if stateErr != nil {
			return stateErr
		}
		if !state.EquippedAchievementID.Valid {
			showcase := append([]string(nil), state.ShowcaseAchievementIds...)
			for _, achievementID := range newlyUnlocked {
				if len(showcase) >= maxAgentHonorShowcase {
					break
				}
				if !containsString(showcase, achievementID) {
					showcase = append(showcase, achievementID)
				}
			}
			if _, err := s.Queries.UpdateAgentHonorShowcase(ctx, db.UpdateAgentHonorShowcaseParams{
				WorkspaceID:            workspaceID,
				AgentID:                agentID,
				ShowcaseAchievementIds: showcase,
				Column4:                newlyUnlocked[0],
			}); err != nil {
				return err
			}
		}
	}
	return s.reconcileState(ctx, workspaceID, agentID)
}

func (s *AgentHonorService) GetDashboard(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
) (AgentHonorDashboard, error) {
	rules, _, err := loadAgentHonorRules(ctx, s.Queries, workspaceID)
	if err != nil {
		return AgentHonorDashboard{}, err
	}
	state, err := s.Queries.GetAgentHonorState(ctx, db.GetAgentHonorStateParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return AgentHonorDashboard{}, err
		}
		state, err = s.Queries.CreateAgentHonorStateIfMissing(ctx, db.CreateAgentHonorStateIfMissingParams{
			WorkspaceID: workspaceID,
			AgentID:     agentID,
		})
		if err != nil {
			return AgentHonorDashboard{}, err
		}
	}
	metrics, err := s.loadMetrics(ctx, workspaceID, agentID)
	if err != nil {
		return AgentHonorDashboard{}, err
	}
	fleet, err := s.currentFleet(ctx, workspaceID, agentID)
	if err != nil {
		return AgentHonorDashboard{}, err
	}
	unlocks, err := s.Queries.ListAgentHonorUnlocks(ctx, db.ListAgentHonorUnlocksParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		return AgentHonorDashboard{}, err
	}
	unlockByID := make(map[string]db.AgentHonorUnlock, len(unlocks))
	for _, row := range unlocks {
		unlockByID[row.AchievementID] = row
	}
	unlockPct, err := s.unlockPctMap(ctx)
	if err != nil {
		return AgentHonorDashboard{}, err
	}
	achievements := make([]AgentAchievementView, 0, len(agentAchievementCatalog))
	for _, def := range effectiveAgentAchievementCatalog(rules) {
		current := achievementMetricValue(def.Metric, metrics, fleet.ClassID)
		row, isUnlocked := unlockByID[def.ID]
		view := AgentAchievementView{
			ID: def.ID, Title: def.Title, Description: def.Description, SvgKey: def.SvgKey,
			Category: def.Category, XPReward: def.XPReward, Rarity: def.Rarity,
			Secret: def.Secret, Unlocked: isUnlocked, UnlockPct: unlockPct[def.ID],
			Progress: &AgentAchievementProgress{Current: minInt64(current, def.Target), Target: def.Target},
		}
		if def.Secret && !isUnlocked {
			view.Title = "Secret achievement"
			view.Description = "Keep developing to reveal this achievement."
			view.SvgKey = "agent_armor_locked"
			view.Progress = nil
		}
		if isUnlocked {
			value := row.UnlockedAt.Time.UTC().Format(time.RFC3339)
			view.UnlockedAt = &value
		}
		achievements = append(achievements, view)
	}
	events, err := s.Queries.ListRecentAgentHonorEvents(ctx, db.ListRecentAgentHonorEventsParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		Limit:       24,
	})
	if err != nil {
		return AgentHonorDashboard{}, err
	}
	eventViews := make([]AgentHonorEventView, len(events))
	for i, row := range events {
		eventViews[i] = AgentHonorEventView{
			ID: util.UUIDToString(row.ID), EventType: row.EventType, SourceRef: row.SourceRef,
			XPDelta: row.XpDelta, Reason: row.Reason,
			CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
	}
	historyRows, err := s.Queries.ListAgentFleetHistory(ctx, db.ListAgentFleetHistoryParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		Limit:       30,
	})
	if err != nil {
		return AgentHonorDashboard{}, err
	}
	history := make([]AgentFleetHistoryView, len(historyRows))
	for i, row := range historyRows {
		history[i] = AgentFleetHistoryView{
			FleetScore: numericToFloat64(row.FleetScore),
			ClassID:    row.ClassID,
			ClassLabel: fleetClassLabel(row.ClassID),
			FleetRank:  int(row.FleetRank),
			FleetSize:  int(row.FleetSize),
			Pillars: FleetPillarScores{
				Delivery: numericToFloat64(row.PillarDelivery), Evolution: numericToFloat64(row.PillarEvolution),
				Growth: numericToFloat64(row.PillarGrowth), Efficiency: numericToFloat64(row.PillarEfficiency),
			},
			RecordedAt: row.RecordedAt.Time.UTC().Format(time.RFC3339),
		}
	}
	var equipped *string
	if state.EquippedAchievementID.Valid {
		value := state.EquippedAchievementID.String
		equipped = &value
	}
	showcase := state.ShowcaseAchievementIds
	if showcase == nil {
		showcase = []string{}
	}
	return AgentHonorDashboard{
		AgentID: util.UUIDToString(agentID), Level: int(state.Level), TotalXP: state.TotalXp,
		XPToNextLevel:         AgentHonorXPToNextLevel(state.TotalXp, int(state.Level)),
		EquippedAchievementID: equipped, ShowcaseAchievementIDs: showcase,
		Metrics: metrics, Fleet: fleet, NextFleetClass: nextFleetClass(rules, fleet),
		Achievements: achievements, RecentEvents: eventViews, FleetHistory: history,
		RulesVersion: rules.Version,
	}, nil
}

func (s *AgentHonorService) SetShowcase(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
	achievementIDs []string,
	equippedID string,
) error {
	if len(achievementIDs) > maxAgentHonorShowcase {
		return fmt.Errorf("showcase supports at most %d achievements", maxAgentHonorShowcase)
	}
	unlocks, err := s.Queries.ListAgentHonorUnlocks(ctx, db.ListAgentHonorUnlocksParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		return err
	}
	allowed := make(map[string]struct{}, len(unlocks))
	for _, row := range unlocks {
		allowed[row.AchievementID] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, id := range achievementIDs {
		if _, ok := allowed[id]; !ok {
			return errors.New("showcase achievement is not unlocked")
		}
		if _, duplicate := seen[id]; duplicate {
			return errors.New("showcase achievements must be unique")
		}
		seen[id] = struct{}{}
	}
	if equippedID != "" {
		if _, ok := allowed[equippedID]; !ok {
			return errors.New("equipped achievement is not unlocked")
		}
	}
	_, err = s.Queries.UpdateAgentHonorShowcase(ctx, db.UpdateAgentHonorShowcaseParams{
		WorkspaceID:            workspaceID,
		AgentID:                agentID,
		ShowcaseAchievementIds: achievementIDs,
		Column4:                equippedID,
	})
	return err
}

func (s *AgentHonorService) GrantXP(
	ctx context.Context,
	workspaceID, agentID, grantedBy pgtype.UUID,
	xp int32,
	reason, grantID string,
) error {
	if xp == 0 || xp < -10000 || xp > 10000 {
		return errors.New("xp must be between -10000 and 10000 and cannot be zero")
	}
	if grantID == "" {
		return errors.New("grant_id is required")
	}
	metadata, _ := json.Marshal(map[string]any{"reason": reason})
	if _, err := s.Queries.InsertAgentHonorEventIfNew(ctx, db.InsertAgentHonorEventIfNewParams{
		WorkspaceID: workspaceID, AgentID: agentID, EventType: "manual", SourceRef: grantID,
		XpDelta: xp, Reason: reason, Metadata: metadata, CreatedBy: grantedBy,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	_, _ = s.Queries.InsertAgentHonorAdminAudit(ctx, db.InsertAgentHonorAdminAuditParams{
		WorkspaceID: workspaceID, AgentID: agentID, Action: "xp.grant",
		Details: metadata, CreatedBy: grantedBy,
	})
	return s.reconcileState(ctx, workspaceID, agentID)
}

func (s *AgentHonorService) GrantAchievement(
	ctx context.Context,
	workspaceID, agentID, grantedBy pgtype.UUID,
	achievementID, reason string,
) error {
	def, ok := findAgentAchievement(achievementID)
	if !ok {
		return errors.New("unknown achievement")
	}
	if _, err := s.Queries.InsertAgentHonorUnlockIfNew(ctx, db.InsertAgentHonorUnlockIfNewParams{
		WorkspaceID: workspaceID, AgentID: agentID, AchievementID: achievementID, Source: "manual",
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"reason": reason, "achievement_id": achievementID})
	if _, err := s.Queries.InsertAgentHonorEventIfNew(ctx, db.InsertAgentHonorEventIfNewParams{
		WorkspaceID: workspaceID, AgentID: agentID, EventType: "achievement", SourceRef: achievementID,
		XpDelta: def.XPReward, Reason: def.Title, Metadata: metadata, CreatedBy: grantedBy,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, _ = s.Queries.InsertAgentHonorAdminAudit(ctx, db.InsertAgentHonorAdminAuditParams{
		WorkspaceID: workspaceID, AgentID: agentID, Action: "achievement.grant",
		Details: metadata, CreatedBy: grantedBy,
	})
	return s.reconcileState(ctx, workspaceID, agentID)
}

func (s *AgentHonorService) RevokeAchievement(
	ctx context.Context,
	workspaceID, agentID, revokedBy pgtype.UUID,
	achievementID, reason string,
) error {
	rows, err := s.Queries.DeleteAgentHonorUnlock(ctx, db.DeleteAgentHonorUnlockParams{
		WorkspaceID: workspaceID, AgentID: agentID, AchievementID: achievementID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("achievement is not unlocked")
	}
	if err := s.Queries.DeleteAgentAchievementHonorEvent(ctx, db.DeleteAgentAchievementHonorEventParams{
		WorkspaceID: workspaceID, AgentID: agentID, SourceRef: achievementID,
	}); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"reason": reason, "achievement_id": achievementID})
	_, _ = s.Queries.InsertAgentHonorAdminAudit(ctx, db.InsertAgentHonorAdminAuditParams{
		WorkspaceID: workspaceID, AgentID: agentID, Action: "achievement.revoke",
		Details: metadata, CreatedBy: revokedBy,
	})
	state, stateErr := s.Queries.GetAgentHonorState(ctx, db.GetAgentHonorStateParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if stateErr != nil {
		return stateErr
	}
	showcase := make([]string, 0, len(state.ShowcaseAchievementIds))
	for _, id := range state.ShowcaseAchievementIds {
		if id != achievementID {
			showcase = append(showcase, id)
		}
	}
	equipped := ""
	if state.EquippedAchievementID.Valid && state.EquippedAchievementID.String != achievementID {
		equipped = state.EquippedAchievementID.String
	}
	if _, err := s.Queries.UpdateAgentHonorShowcase(ctx, db.UpdateAgentHonorShowcaseParams{
		WorkspaceID:            workspaceID,
		AgentID:                agentID,
		ShowcaseAchievementIds: showcase,
		Column4:                equipped,
	}); err != nil {
		return err
	}
	return s.reconcileState(ctx, workspaceID, agentID)
}

func (s *AgentHonorService) ListAdminAudit(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
) ([]AgentHonorAdminAuditView, error) {
	rows, err := s.Queries.ListAgentHonorAdminAudit(ctx, db.ListAgentHonorAdminAuditParams{
		WorkspaceID: workspaceID,
		Column2:     agentID,
		Limit:       100,
	})
	if err != nil {
		return nil, err
	}
	out := make([]AgentHonorAdminAuditView, 0, len(rows))
	for _, row := range rows {
		details := map[string]any{}
		_ = json.Unmarshal(row.Details, &details)
		var auditAgentID *string
		if row.AgentID.Valid {
			value := util.UUIDToString(row.AgentID)
			auditAgentID = &value
		}
		out = append(out, AgentHonorAdminAuditView{
			ID: util.UUIDToString(row.ID), AgentID: auditAgentID, Action: row.Action,
			Details: details, CreatedBy: util.UUIDToString(row.CreatedBy),
			CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func (s *AgentHonorService) reconcileState(ctx context.Context, workspaceID, agentID pgtype.UUID) error {
	total, err := s.Queries.SumAgentHonorXP(ctx, db.SumAgentHonorXPParams{
		WorkspaceID: workspaceID, AgentID: agentID,
	})
	if err != nil {
		return err
	}
	level := AgentHonorLevelFromXP(total)
	_, err = s.Queries.UpdateAgentHonorStats(ctx, db.UpdateAgentHonorStatsParams{
		WorkspaceID: workspaceID, AgentID: agentID, TotalXp: total, Level: int32(level),
	})
	return err
}

func (s *AgentHonorService) loadMetrics(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
) (AgentHonorMetricsView, error) {
	row, err := s.Queries.GetAgentHonorMetrics(ctx, db.GetAgentHonorMetricsParams{
		WorkspaceID: workspaceID, AgentID: agentID,
	})
	if err != nil {
		return AgentHonorMetricsView{}, err
	}
	outcomes, err := s.Queries.ListAgentRecentTerminalOutcomes(ctx, db.ListAgentRecentTerminalOutcomesParams{
		WorkspaceID: workspaceID, AgentID: agentID, Limit: 200,
	})
	if err != nil {
		return AgentHonorMetricsView{}, err
	}
	streak := int64(0)
	for _, outcome := range outcomes {
		if !outcome.Valid || outcome.String != "completed" {
			break
		}
		streak++
	}
	return AgentHonorMetricsView{
		CompletedCount: row.CompletedCount, FailedCount: row.FailedCount, SuccessStreak: streak,
		MemoryWrites: row.MemoryWrites, EvolutionPromotions: row.EvolutionPromotions,
		DistinctProjects: row.DistinctProjects, RecoveryCount: row.RecoveryCount,
	}, nil
}

func (s *AgentHonorService) currentFleet(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
) (AgentFleetRankView, error) {
	if s.Fleet == nil {
		return AgentFleetRankView{
			AgentID: util.UUIDToString(agentID), ClassID: "reserve", ClassLabel: "Reserve",
			MinSampleTasks: FleetMinSampleTasks,
		}, nil
	}
	return s.Fleet.GetAgentRank(ctx, workspaceID, agentID)
}

func (s *AgentHonorService) unlockPctMap(ctx context.Context) (map[string]float64, error) {
	counts, err := s.Queries.CountAgentAchievementUnlocks(ctx)
	if err != nil {
		return nil, err
	}
	total, err := s.Queries.CountAgentHonorParticipants(ctx)
	if err != nil || total == 0 {
		return map[string]float64{}, err
	}
	out := make(map[string]float64, len(counts))
	for _, row := range counts {
		out[row.AchievementID] = float64(row.UnlockCount) / float64(total) * 100
	}
	return out, nil
}

func achievementMetricValue(metric string, values AgentHonorMetricsView, classID string) int64 {
	switch metric {
	case "completed":
		return values.CompletedCount
	case "success_streak":
		return values.SuccessStreak
	case "memory_writes":
		return values.MemoryWrites
	case "evolution_promotions":
		return values.EvolutionPromotions
	case "distinct_projects":
		return values.DistinctProjects
	case "recoveries":
		return values.RecoveryCount
	case "fleet_class":
		return int64(fleetClassOrder(classID))
	default:
		return 0
	}
}

func fleetClassOrder(classID string) int {
	switch classID {
	case "corvette":
		return 1
	case "frigate":
		return 2
	case "cruiser":
		return 3
	case "battleship":
		return 4
	case "dreadnought":
		return 5
	default:
		return 0
	}
}

func nextFleetClass(rules AgentHonorRules, fleet AgentFleetRankView) *AgentHonorClassThreshold {
	classes := append([]AgentHonorClassThreshold(nil), rules.FleetClasses...)
	sort.Slice(classes, func(i, j int) bool { return classes[i].Score < classes[j].Score })
	for _, class := range classes {
		if class.Score > fleet.FleetScore {
			value := class
			return &value
		}
	}
	return nil
}

func findAgentAchievement(id string) (AgentAchievementDefinition, bool) {
	for _, def := range agentAchievementCatalog {
		if def.ID == id {
			return def, true
		}
	}
	return AgentAchievementDefinition{}, false
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
