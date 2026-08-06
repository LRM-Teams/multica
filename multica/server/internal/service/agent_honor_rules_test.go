package service

import "testing"

func TestAgentHonorLevelBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		totalXP int64
		level   int
		nextXP  int64
	}{
		{totalXP: 0, level: 1, nextXP: 25},
		{totalXP: 24, level: 1, nextXP: 1},
		{totalXP: 25, level: 2, nextXP: 75},
		{totalXP: 99, level: 2, nextXP: 1},
		{totalXP: 100, level: 3, nextXP: 125},
		{totalXP: 1_000_000, level: MaxAgentHonorLevel, nextXP: 0},
	}
	for _, test := range tests {
		if got := AgentHonorLevelFromXP(test.totalXP); got != test.level {
			t.Errorf("AgentHonorLevelFromXP(%d) = %d, want %d", test.totalXP, got, test.level)
		}
		if got := AgentHonorXPToNextLevel(test.totalXP, test.level); got != test.nextXP {
			t.Errorf(
				"AgentHonorXPToNextLevel(%d, %d) = %d, want %d",
				test.totalXP,
				test.level,
				got,
				test.nextXP,
			)
		}
	}
}

func TestDefaultAgentHonorRulesPublishCompleteCatalog(t *testing.T) {
	t.Parallel()

	rules := DefaultAgentHonorRules()
	if err := validateAgentHonorRules(rules); err != nil {
		t.Fatalf("default rules are invalid: %v", err)
	}
	if got, want := len(agentAchievementCatalog), 16; got != want {
		t.Fatalf("achievement catalog size = %d, want %d", got, want)
	}
	if got, want := len(effectiveAgentAchievementCatalog(rules)), 16; got != want {
		t.Fatalf("effective achievement catalog size = %d, want %d", got, want)
	}
	for _, achievement := range agentAchievementCatalog {
		if rules.AchievementTargets[achievement.ID] != achievement.Target {
			t.Errorf("target for %s does not match its catalog definition", achievement.ID)
		}
		if !rules.AchievementEnabled[achievement.ID] {
			t.Errorf("default rules disable achievement %s", achievement.ID)
		}
	}
}

func TestAgentHonorRuleValidationRejectsAmbiguousFleetProgression(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*AgentHonorRules)
	}{
		{
			name: "weights do not sum to one",
			mutate: func(rules *AgentHonorRules) {
				rules.FleetWeights["delivery"] = 0.9
			},
		},
		{
			name: "reserve starts above zero",
			mutate: func(rules *AgentHonorRules) {
				for index := range rules.FleetClasses {
					if rules.FleetClasses[index].ClassID == "reserve" {
						rules.FleetClasses[index].Score = 1
					}
				}
			},
		},
		{
			name: "higher class is not harder",
			mutate: func(rules *AgentHonorRules) {
				for index := range rules.FleetClasses {
					if rules.FleetClasses[index].ClassID == "cruiser" {
						rules.FleetClasses[index].Score = 40
					}
				}
			},
		},
		{
			name: "empty class label",
			mutate: func(rules *AgentHonorRules) {
				rules.FleetClasses[0].Label = " "
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rules := DefaultAgentHonorRules()
			test.mutate(&rules)
			if err := validateAgentHonorRules(rules); err == nil {
				t.Fatal("validateAgentHonorRules accepted invalid rules")
			}
		})
	}
}

func TestAgentAchievementMetricsAndNextFleetClass(t *testing.T) {
	t.Parallel()

	metrics := AgentHonorMetricsView{
		CompletedCount:      52,
		SuccessStreak:       8,
		MemoryWrites:        25,
		EvolutionPromotions: 2,
		DistinctProjects:    4,
		RecoveryCount:       3,
	}
	cases := map[string]int64{
		"completed":            52,
		"success_streak":       8,
		"memory_writes":        25,
		"evolution_promotions": 2,
		"distinct_projects":    4,
		"recoveries":           3,
		"fleet_class":          3,
	}
	for metric, want := range cases {
		if got := achievementMetricValue(metric, metrics, "cruiser"); got != want {
			t.Errorf("achievementMetricValue(%q) = %d, want %d", metric, got, want)
		}
	}

	next := nextFleetClass(
		DefaultAgentHonorRules(),
		AgentFleetRankView{FleetScore: 56, ClassID: "cruiser"},
	)
	if next == nil || next.ClassID != "battleship" || next.Score != 70 {
		t.Fatalf("next fleet class = %+v, want battleship at 70", next)
	}
}
