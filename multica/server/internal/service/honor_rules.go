package service

import "time"

// FoundingMemberCutoff is the registration deadline for Founding identity.
const FoundingMemberCutoff = "2026-08-01T00:00:00Z"

var foundingMemberCutoffTime = mustParseHonorTime(FoundingMemberCutoff)

// HonorRulesVersion is bumped when public scoring tables change.
const HonorRulesVersion = "2026-07-31.2"

// MaxHonorLevel is the highest level the progression curve can award.
const MaxHonorLevel = 60

type HonorPillar string

const (
	HonorPillarUsage     HonorPillar = "usage"
	HonorPillarPresence  HonorPillar = "presence"
	HonorPillarDelivery  HonorPillar = "delivery"
	HonorPillarCommunity HonorPillar = "community"
)

// Pillar tier thresholds follow the steep 1/2/4/8/16/30/60/100 difficulty shape.
var honorPillarTierThresholds = map[HonorPillar][]int64{
	HonorPillarUsage:     {10, 50, 200, 800, 3200, 6000, 12000, 20000},
	HonorPillarPresence:  {30, 120, 480, 1920, 7680, 14400, 28800, 48000},
	HonorPillarDelivery:  {1, 5, 20, 80, 320, 600, 1200, 2000},
	HonorPillarCommunity: {1, 3, 10, 40, 160, 300, 600, 1000},
}

func honorLevelThresholdXP(level int) int64 {
	if level <= 1 {
		return 0
	}
	var total int64
	for l := 2; l <= level; l++ {
		total += int64(10 * pow115(l-2))
	}
	return total
}

func pow115(exp int) float64 {
	result := 1.0
	base := 1.15
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
}

// LevelFromTotalXP maps cumulative XP to display level.
func LevelFromTotalXP(totalXP int64) int {
	level := 1
	for l := 2; l <= MaxHonorLevel; l++ {
		if totalXP >= honorLevelThresholdXP(l) {
			level = l
			continue
		}
		break
	}
	return level
}

// XPToNextLevel returns xp needed to reach next level from current total.
func XPToNextLevel(totalXP int64, level int) int64 {
	if level >= MaxHonorLevel {
		return 0
	}
	next := honorLevelThresholdXP(level + 1)
	if next <= totalXP {
		return 0
	}
	return next - totalXP
}

type honorActionRule struct {
	Pillar   HonorPillar
	XPDelta  int32
	DailyCap int32
	Counter  int64
}

var honorActionRules = map[string]honorActionRule{
	"issue.create":     {HonorPillarUsage, 8, 80, 1},
	"issue.update":     {HonorPillarUsage, 3, 60, 1},
	"issue.close":      {HonorPillarDelivery, 15, 75, 1},
	"comment.create":   {HonorPillarUsage, 5, 100, 1},
	"channel.message":  {HonorPillarUsage, 4, 120, 1},
	"member.invite":    {HonorPillarCommunity, 20, 40, 1},
	"presence.minute":  {HonorPillarPresence, 1, 120, 1},
	"research.session": {HonorPillarUsage, 12, 36, 1},
}

// HonorRulesDocument is the public rules payload for GET /api/honor/rules.
type HonorRulesDocument struct {
	Version          string                          `json:"version"`
	FoundingCutoff   string                          `json:"founding_cutoff"`
	LevelThresholds  []HonorLevelThresholdEntry      `json:"level_thresholds"`
	PillarTierTables map[string][]int64              `json:"pillar_tier_tables"`
	ActionRules      map[string]HonorActionRuleEntry `json:"action_rules"`
	NameStyleUnlocks []HonorNameStyleRuleEntry       `json:"name_style_unlocks"`
	BadgeCatalog     []HonorBadgeCatalogEntry        `json:"badge_catalog"`
	Changelog        []string                        `json:"changelog"`
}

type HonorLevelThresholdEntry struct {
	Level   int   `json:"level"`
	TotalXP int64 `json:"total_xp"`
}

type HonorActionRuleEntry struct {
	Pillar   string `json:"pillar"`
	XPDelta  int32  `json:"xp_delta"`
	DailyCap int32  `json:"daily_cap"`
}

type HonorNameStyleRuleEntry struct {
	ID       string `json:"id"`
	MinLevel int    `json:"min_level"`
}

type HonorBadgeCatalogEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SvgKey      string `json:"svg_key"`
	Rarity      int    `json:"rarity"`
}

var honorNameStyleRules = []HonorNameStyleRuleEntry{
	{ID: "default", MinLevel: 1},
	{ID: "ice", MinLevel: 3},
	{ID: "member", MinLevel: 5},
	{ID: "emerald", MinLevel: 7},
	{ID: "sapphire", MinLevel: 9},
	{ID: "gold", MinLevel: 12},
	{ID: "coral", MinLevel: 15},
	{ID: "amethyst", MinLevel: 18},
	{ID: "prismatic", MinLevel: 21},
	{ID: "aurora", MinLevel: 24},
	{ID: "glow", MinLevel: 27},
	{ID: "solar", MinLevel: 30},
	{ID: "shimmer", MinLevel: 33},
	{ID: "nebula", MinLevel: 36},
	{ID: "cyber", MinLevel: 39},
	{ID: "animated_prismatic", MinLevel: 42},
	{ID: "plasma", MinLevel: 45},
	{ID: "animated_glow", MinLevel: 48},
	{ID: "eclipse", MinLevel: 51},
	{ID: "nova", MinLevel: 53},
	{ID: "quantum", MinLevel: 55},
	{ID: "celestial", MinLevel: 57},
	{ID: "mythic", MinLevel: 59},
	{ID: "transcendent", MinLevel: 60},
	{ID: "founding", MinLevel: 1},
}

func BuildHonorRulesDocument(badges []HonorBadgeCatalogEntry) HonorRulesDocument {
	levels := make([]HonorLevelThresholdEntry, 0, MaxHonorLevel)
	for l := 1; l <= MaxHonorLevel; l++ {
		levels = append(levels, HonorLevelThresholdEntry{Level: l, TotalXP: honorLevelThresholdXP(l)})
	}
	actions := make(map[string]HonorActionRuleEntry, len(honorActionRules))
	for k, v := range honorActionRules {
		actions[k] = HonorActionRuleEntry{Pillar: string(v.Pillar), XPDelta: v.XPDelta, DailyCap: v.DailyCap}
	}
	pillars := make(map[string][]int64, len(honorPillarTierThresholds))
	for p, thresholds := range honorPillarTierThresholds {
		pillars[string(p)] = thresholds
	}
	return HonorRulesDocument{
		Version:          HonorRulesVersion,
		FoundingCutoff:   FoundingMemberCutoff,
		LevelThresholds:  levels,
		PillarTierTables: pillars,
		ActionRules:      actions,
		NameStyleUnlocks: append([]HonorNameStyleRuleEntry(nil), honorNameStyleRules...),
		BadgeCatalog:     badges,
		Changelog: []string{
			"2026-07-31: expand to 24 visible name styles and 51 badges",
			"2026-07-31: publish the complete level 1-60 progression table",
			"2026-07-30: initial honor rules v1",
		},
	}
}

func PillarTierFromCounter(pillar HonorPillar, counter int64) int {
	thresholds, ok := honorPillarTierThresholds[pillar]
	if !ok {
		return 0
	}
	tier := 0
	for i, threshold := range thresholds {
		if counter >= threshold {
			tier = i + 1
		}
	}
	return tier
}

func IsFoundingMember(createdAt time.Time) bool {
	return createdAt.Before(foundingMemberCutoffTime)
}

func mustParseHonorTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}
