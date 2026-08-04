package service

import (
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestLevelFromTotalXP_IncreasesWithXP(t *testing.T) {
	if LevelFromTotalXP(0) != 1 {
		t.Fatalf("expected level 1 at 0 xp")
	}
	if LevelFromTotalXP(100) < 2 {
		t.Fatalf("expected level >= 2 at 100 xp")
	}
}

func TestXPToNextLevelStopsAtMaximumLevel(t *testing.T) {
	t.Parallel()
	if MaxHonorLevel != 80 {
		t.Fatalf("MaxHonorLevel = %d, want 80 approved user honor levels", MaxHonorLevel)
	}

	totalXP := honorLevelThresholdXP(MaxHonorLevel)
	if got := LevelFromTotalXP(totalXP); got != MaxHonorLevel {
		t.Fatalf("LevelFromTotalXP(max threshold) = %d, want %d", got, MaxHonorLevel)
	}
	if got := XPToNextLevel(totalXP, MaxHonorLevel); got != 0 {
		t.Fatalf("XPToNextLevel(max level) = %d, want 0", got)
	}
}

func TestHonorLevelCurveUsesReachableNonDemotingBands(t *testing.T) {
	t.Parallel()

	wantThresholds := map[int]int64{
		20: 874,
		40: 7_474,
		60: 31_774,
		70: 68_024,
		80: 140_524,
	}
	for level, want := range wantThresholds {
		if got := honorLevelThresholdXP(level); got != want {
			t.Errorf("level %d threshold = %d, want %d", level, got, want)
		}
		if got := LevelFromTotalXP(want - 1); got != level-1 {
			t.Errorf("level before %d threshold = %d, want %d", level, got, level-1)
		}
		if got := LevelFromTotalXP(want); got != level {
			t.Errorf("level at %d threshold = %d, want %d", level, got, level)
		}
	}

	var legacyThreshold int64
	for level := 2; level <= MaxHonorLevel; level++ {
		legacyThreshold += int64(10 * pow115(level-2))
		if current := honorLevelThresholdXP(level); current > legacyThreshold {
			t.Errorf(
				"level %d threshold %d exceeds legacy threshold %d and would demote users",
				level,
				current,
				legacyThreshold,
			)
		}
	}
}

func TestHonorLevel80RequiresSixToTwelveMonthsAtAbsoluteDailyCap(t *testing.T) {
	t.Parallel()

	maxDailyXP := int64(0)
	for _, rule := range honorActionRules {
		maxDailyXP += int64(rule.DailyCap)
	}
	minimumDays := (honorLevelThresholdXP(MaxHonorLevel) + maxDailyXP - 1) / maxDailyXP
	if minimumDays < 180 || minimumDays > 365 {
		t.Fatalf("level 80 requires %d days at the absolute daily cap, want 180-365", minimumDays)
	}
}

func TestIsFoundingMember_BeforeCutoff(t *testing.T) {
	created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if !IsFoundingMember(created) {
		t.Fatal("expected founding member before cutoff")
	}
	after := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	if IsFoundingMember(after) {
		t.Fatal("expected non-founding after cutoff")
	}
}

func TestPillarTierFromCounter_SteepCurve(t *testing.T) {
	if tier := PillarTierFromCounter(HonorPillarUsage, 5); tier != 0 {
		t.Fatalf("expected tier 0, got %d", tier)
	}
	if tier := PillarTierFromCounter(HonorPillarUsage, 50); tier < 2 {
		t.Fatalf("expected tier >= 2 at 50 usage actions, got %d", tier)
	}
}

func TestBuildHonorRulesDocumentPublishesEverySupportedLevel(t *testing.T) {
	t.Parallel()

	document := BuildHonorRulesDocument(nil)
	if got, want := len(document.LevelThresholds), MaxHonorLevel; got != want {
		t.Fatalf("len(LevelThresholds) = %d, want %d", got, want)
	}

	for index, threshold := range document.LevelThresholds {
		wantLevel := index + 1
		if threshold.Level != wantLevel {
			t.Fatalf("LevelThresholds[%d].Level = %d, want %d", index, threshold.Level, wantLevel)
		}
		if threshold.TotalXP != honorLevelThresholdXP(wantLevel) {
			t.Fatalf(
				"LevelThresholds[%d].TotalXP = %d, want %d",
				index,
				threshold.TotalXP,
				honorLevelThresholdXP(wantLevel),
			)
		}
		if index > 0 && threshold.TotalXP <= document.LevelThresholds[index-1].TotalXP {
			t.Fatalf("level thresholds must increase: %+v", document.LevelThresholds[index-1:index+1])
		}
	}
}

func TestExpandedHonorCatalogPublishesTwentyFourNameStylesAndFiftyOneBadges(t *testing.T) {
	t.Parallel()

	document := BuildHonorRulesDocument(nil)
	visibleStyles := 0
	lastLevel := 0
	for _, style := range document.NameStyleUnlocks {
		if style.ID == "founding" {
			continue
		}
		visibleStyles++
		if style.MinLevel <= lastLevel {
			t.Fatalf("name style levels must increase, got %d after %d", style.MinLevel, lastLevel)
		}
		lastLevel = style.MinLevel
	}
	if got, want := visibleStyles, 24; got != want {
		t.Fatalf("visible name styles = %d, want %d", got, want)
	}
	if got, want := len(honorBadgeRequirements), 51; got != want {
		t.Fatalf("badge requirements = %d, want %d", got, want)
	}
	if honorBadgeRequirements["infinity_engine"].minLevel != MaxHonorLevel {
		t.Fatal("Infinity Engine must remain the maximum-level completion badge")
	}
}

func TestMaskSecretBadgeHidesItsUnlockRuleUntilUnlocked(t *testing.T) {
	t.Parallel()

	definition := db.HonorBadgeDef{
		Title:       "Hidden Architect",
		Description: "Complete the hidden architecture challenge.",
		SvgKey:      "architect",
		Secret:      true,
		UnlockRule:  "complete.secret_architecture_challenge",
	}

	title, description, svgKey, unlockRule := maskSecretBadge(definition, false)
	if title != "Secret Badge" || description != "Unlock to reveal this badge." || svgKey != "stardust" {
		t.Fatalf("locked secret presentation leaked metadata: %q %q %q", title, description, svgKey)
	}
	if unlockRule != "" {
		t.Fatalf("locked secret unlock rule = %q, want empty", unlockRule)
	}

	title, description, svgKey, unlockRule = maskSecretBadge(definition, true)
	if title != definition.Title ||
		description != definition.Description ||
		svgKey != definition.SvgKey ||
		unlockRule != definition.UnlockRule {
		t.Fatalf(
			"unlocked secret presentation = %q %q %q %q, want original metadata",
			title,
			description,
			svgKey,
			unlockRule,
		)
	}
}
