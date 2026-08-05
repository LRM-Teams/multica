package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	AgentHonorRulesVersion = "2026-07-31.1"
	MaxAgentHonorLevel     = 30
)

type AgentHonorClassThreshold struct {
	ClassID string  `json:"class_id"`
	Label   string  `json:"label"`
	Score   float64 `json:"score"`
}

type AgentHonorRules struct {
	Version             string                     `json:"version"`
	CompletionXP        int32                      `json:"completion_xp"`
	FleetWindowDays     int                        `json:"fleet_window_days"`
	FleetMinSampleTasks int                        `json:"fleet_min_sample_tasks"`
	FleetWeights        map[string]float64         `json:"fleet_weights"`
	FleetClasses        []AgentHonorClassThreshold `json:"fleet_classes"`
	AchievementTargets  map[string]int64           `json:"achievement_targets"`
	AchievementEnabled  map[string]bool            `json:"achievement_enabled"`
	Changelog           []string                   `json:"changelog"`
}

type AgentAchievementDefinition struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SvgKey      string `json:"svg_key"`
	Category    string `json:"category"`
	Metric      string `json:"metric"`
	Target      int64  `json:"target"`
	XPReward    int32  `json:"xp_reward"`
	Rarity      int    `json:"rarity"`
	Secret      bool   `json:"secret"`
}

var agentAchievementCatalog = []AgentAchievementDefinition{
	{ID: "first_launch", Title: "First Launch", Description: "Complete the first accepted task.", SvgKey: "agent_armor_first_launch", Category: "delivery", Metric: "completed", Target: 1, XPReward: 25, Rarity: 10},
	{ID: "proven_crew", Title: "Proven Crew", Description: "Complete 10 accepted tasks.", SvgKey: "agent_armor_proven_crew", Category: "delivery", Metric: "completed", Target: 10, XPReward: 50, Rarity: 18},
	{ID: "veteran_core", Title: "Veteran Core", Description: "Complete 50 accepted tasks.", SvgKey: "agent_armor_veteran_core", Category: "delivery", Metric: "completed", Target: 50, XPReward: 125, Rarity: 42},
	{ID: "centurion", Title: "Centurion", Description: "Complete 100 accepted tasks.", SvgKey: "agent_armor_centurion", Category: "delivery", Metric: "completed", Target: 100, XPReward: 250, Rarity: 65},
	{ID: "streak_5", Title: "Clean Burn", Description: "Complete 5 tasks in a row without failure.", SvgKey: "agent_armor_streak_5", Category: "reliability", Metric: "success_streak", Target: 5, XPReward: 75, Rarity: 30},
	{ID: "streak_20", Title: "Unbroken Orbit", Description: "Complete 20 tasks in a row without failure.", SvgKey: "agent_armor_streak_20", Category: "reliability", Metric: "success_streak", Target: 20, XPReward: 250, Rarity: 78, Secret: true},
	{ID: "memory_spark", Title: "Memory Spark", Description: "Create 3 valid memory updates.", SvgKey: "agent_armor_memory_spark", Category: "growth", Metric: "memory_writes", Target: 3, XPReward: 30, Rarity: 15},
	{ID: "memory_archive", Title: "Living Archive", Description: "Create 24 valid memory updates.", SvgKey: "agent_armor_memory_archive", Category: "growth", Metric: "memory_writes", Target: 24, XPReward: 100, Rarity: 40},
	{ID: "memory_constellation", Title: "Memory Constellation", Description: "Create 100 valid memory updates.", SvgKey: "agent_armor_memory_constellation", Category: "growth", Metric: "memory_writes", Target: 100, XPReward: 300, Rarity: 82, Secret: true},
	{ID: "evolution_seed", Title: "Evolution Seed", Description: "Promote the first evolution unit.", SvgKey: "agent_armor_evolution_seed", Category: "evolution", Metric: "evolution_promotions", Target: 1, XPReward: 100, Rarity: 35},
	{ID: "evolution_engine", Title: "Evolution Engine", Description: "Promote 10 evolution units.", SvgKey: "agent_armor_evolution_engine", Category: "evolution", Metric: "evolution_promotions", Target: 10, XPReward: 350, Rarity: 90, Secret: true},
	{ID: "deep_space_explorer", Title: "Deep Space Explorer", Description: "Deliver accepted work in 3 projects.", SvgKey: "agent_armor_deep_space", Category: "mastery", Metric: "distinct_projects", Target: 3, XPReward: 125, Rarity: 48},
	{ID: "phoenix_protocol", Title: "Phoenix Protocol", Description: "Recover 3 issues after an earlier failed attempt.", SvgKey: "agent_armor_phoenix", Category: "reliability", Metric: "recoveries", Target: 3, XPReward: 175, Rarity: 70, Secret: true},
	{ID: "corvette_command", Title: "Corvette Command", Description: "Reach Corvette fleet class.", SvgKey: "agent_armor_corvette", Category: "fleet", Metric: "fleet_class", Target: 1, XPReward: 50, Rarity: 20},
	{ID: "cruiser_command", Title: "Cruiser Command", Description: "Reach Cruiser fleet class.", SvgKey: "agent_armor_cruiser", Category: "fleet", Metric: "fleet_class", Target: 3, XPReward: 175, Rarity: 62},
	{ID: "dreadnought_command", Title: "Dreadnought Command", Description: "Reach Dreadnought fleet class.", SvgKey: "agent_armor_dreadnought", Category: "fleet", Metric: "fleet_class", Target: 5, XPReward: 500, Rarity: 96, Secret: true},
}

func DefaultAgentHonorRules() AgentHonorRules {
	targets := make(map[string]int64, len(agentAchievementCatalog))
	enabled := make(map[string]bool, len(agentAchievementCatalog))
	for _, def := range agentAchievementCatalog {
		targets[def.ID] = def.Target
		enabled[def.ID] = true
	}
	return AgentHonorRules{
		Version:             AgentHonorRulesVersion,
		CompletionXP:        10,
		FleetWindowDays:     FleetWindowDays,
		FleetMinSampleTasks: FleetMinSampleTasks,
		FleetWeights: map[string]float64{
			"delivery":   fleetWeightDelivery,
			"evolution":  fleetWeightEvolution,
			"growth":     fleetWeightGrowth,
			"efficiency": fleetWeightEfficiency,
		},
		FleetClasses: []AgentHonorClassThreshold{
			{ClassID: "dreadnought", Label: "Dreadnought", Score: 85},
			{ClassID: "battleship", Label: "Battleship", Score: 70},
			{ClassID: "cruiser", Label: "Cruiser", Score: 55},
			{ClassID: "frigate", Label: "Frigate", Score: 40},
			{ClassID: "corvette", Label: "Corvette", Score: 25},
			{ClassID: "reserve", Label: "Reserve", Score: 0},
		},
		AchievementTargets: targets,
		AchievementEnabled: enabled,
		Changelog: []string{
			"2026-07-31: lifetime XP, achievements, fleet history, showcase, and workspace rules",
		},
	}
}

func AgentHonorLevelFromXP(totalXP int64) int {
	if totalXP <= 0 {
		return 1
	}
	level := int(math.Floor(math.Sqrt(float64(totalXP)/25))) + 1
	if level > MaxAgentHonorLevel {
		return MaxAgentHonorLevel
	}
	return level
}

func AgentHonorXPToNextLevel(totalXP int64, level int) int64 {
	if level >= MaxAgentHonorLevel {
		return 0
	}
	next := int64(25 * level * level)
	if next <= totalXP {
		return 0
	}
	return next - totalXP
}

func agentHonorLevelTransition(previousLevel int, totalXP int64) (int, bool) {
	level := AgentHonorLevelFromXP(totalXP)
	return level, level != previousLevel
}

func validateAgentHonorRules(rules AgentHonorRules) error {
	if rules.CompletionXP < 1 || rules.CompletionXP > 100 {
		return errors.New("completion_xp must be between 1 and 100")
	}
	if rules.FleetWindowDays < 7 || rules.FleetWindowDays > 90 {
		return errors.New("fleet_window_days must be between 7 and 90")
	}
	if rules.FleetMinSampleTasks < 1 || rules.FleetMinSampleTasks > 100 {
		return errors.New("fleet_min_sample_tasks must be between 1 and 100")
	}
	requiredWeights := []string{"delivery", "evolution", "growth", "efficiency"}
	totalWeight := 0.0
	for _, key := range requiredWeights {
		value, ok := rules.FleetWeights[key]
		if !ok || value < 0 || value > 1 {
			return fmt.Errorf("fleet weight %s must be between 0 and 1", key)
		}
		totalWeight += value
	}
	if math.Abs(totalWeight-1) > 0.0001 {
		return errors.New("fleet_weights must sum to 1")
	}
	if len(rules.FleetClasses) != 6 {
		return errors.New("fleet_classes must contain all 6 classes")
	}
	allowedClasses := map[string]bool{
		"reserve": true, "corvette": true, "frigate": true,
		"cruiser": true, "battleship": true, "dreadnought": true,
	}
	seenClasses := map[string]bool{}
	scoreByClass := map[string]float64{}
	for _, class := range rules.FleetClasses {
		if !allowedClasses[class.ClassID] || seenClasses[class.ClassID] {
			return errors.New("fleet_classes contains an invalid or duplicate class")
		}
		if strings.TrimSpace(class.Label) == "" {
			return errors.New("fleet class labels cannot be empty")
		}
		if class.Score < 0 || class.Score > 100 {
			return errors.New("fleet class scores must be between 0 and 100")
		}
		seenClasses[class.ClassID] = true
		scoreByClass[class.ClassID] = class.Score
	}
	if scoreByClass["reserve"] != 0 {
		return errors.New("reserve fleet class score must be 0")
	}
	classOrder := []string{"reserve", "corvette", "frigate", "cruiser", "battleship", "dreadnought"}
	for index := 1; index < len(classOrder); index++ {
		if scoreByClass[classOrder[index]] <= scoreByClass[classOrder[index-1]] {
			return errors.New("fleet class scores must increase from reserve to dreadnought")
		}
	}
	for _, def := range agentAchievementCatalog {
		target, ok := rules.AchievementTargets[def.ID]
		if !ok || target < 1 {
			return fmt.Errorf("achievement target %s must be at least 1", def.ID)
		}
	}
	return nil
}

func loadAgentHonorRules(ctx context.Context, queries *db.Queries, workspaceID pgtype.UUID) (AgentHonorRules, int32, error) {
	defaults := DefaultAgentHonorRules()
	if queries == nil {
		return defaults, 0, nil
	}
	row, err := queries.GetAgentHonorRuleConfig(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return defaults, 0, nil
		}
		return AgentHonorRules{}, 0, err
	}
	var rules AgentHonorRules
	if err := json.Unmarshal(row.Config, &rules); err != nil {
		return AgentHonorRules{}, 0, fmt.Errorf("decode agent honor rules: %w", err)
	}
	if err := validateAgentHonorRules(rules); err != nil {
		return AgentHonorRules{}, 0, err
	}
	return rules, row.Version, nil
}

func effectiveAgentAchievementCatalog(rules AgentHonorRules) []AgentAchievementDefinition {
	out := make([]AgentAchievementDefinition, 0, len(agentAchievementCatalog))
	for _, base := range agentAchievementCatalog {
		if enabled, ok := rules.AchievementEnabled[base.ID]; ok && !enabled {
			continue
		}
		def := base
		if target, ok := rules.AchievementTargets[def.ID]; ok && target > 0 {
			def.Target = target
		}
		out = append(out, def)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Target < out[j].Target
	})
	return out
}
