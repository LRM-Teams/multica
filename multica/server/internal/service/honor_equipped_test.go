package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestResolveEquippedBadge_AutoPicksBest(t *testing.T) {
	honor := db.UserHonor{EquippedBadgeManual: false}
	unlocks := []db.UserHonorUnlock{
		{UnlockKind: "badge", DefID: "stardust"},
		{UnlockKind: "badge", DefID: "mars"},
	}
	res := resolveEquippedBadge(honor, unlocks, "mars", true)
	if res.Manual {
		t.Fatal("expected auto mode")
	}
	if !res.Changed {
		t.Fatal("expected change from empty to best badge")
	}
	if !res.BadgeID.Valid || res.BadgeID.String != "mars" {
		t.Fatalf("expected mars badge, got %+v", res.BadgeID)
	}
}

func TestResolveEquippedBadge_KeepsValidManualChoice(t *testing.T) {
	honor := db.UserHonor{
		EquippedBadgeManual: true,
		EquippedBadgeID:     pgtype.Text{String: "stardust", Valid: true},
	}
	unlocks := []db.UserHonorUnlock{
		{UnlockKind: "badge", DefID: "stardust"},
		{UnlockKind: "badge", DefID: "mars"},
	}
	res := resolveEquippedBadge(honor, unlocks, "mars", true)
	if !res.Manual {
		t.Fatal("expected manual mode preserved")
	}
	if res.Changed {
		t.Fatal("expected no change while manual choice remains valid")
	}
	if res.BadgeID.String != "stardust" {
		t.Fatalf("expected stardust badge, got %s", res.BadgeID.String)
	}
}

func TestResolveEquippedBadge_InvalidManualFallsBackToBest(t *testing.T) {
	honor := db.UserHonor{
		EquippedBadgeManual: true,
		EquippedBadgeID:     pgtype.Text{String: "quasar", Valid: true},
	}
	unlocks := []db.UserHonorUnlock{
		{UnlockKind: "badge", DefID: "stardust"},
	}
	res := resolveEquippedBadge(honor, unlocks, "stardust", true)
	if res.Manual {
		t.Fatal("expected auto mode after invalid manual badge")
	}
	if !res.Changed {
		t.Fatal("expected change to best unlocked badge")
	}
	if res.BadgeID.String != "stardust" {
		t.Fatalf("expected stardust badge, got %s", res.BadgeID.String)
	}
}

func TestResolveEquippedBadge_AutoUpgradesWhenBetterUnlocks(t *testing.T) {
	honor := db.UserHonor{
		EquippedBadgeManual: false,
		EquippedBadgeID:     pgtype.Text{String: "stardust", Valid: true},
	}
	unlocks := []db.UserHonorUnlock{
		{UnlockKind: "badge", DefID: "stardust"},
		{UnlockKind: "badge", DefID: "mars"},
	}
	res := resolveEquippedBadge(honor, unlocks, "mars", true)
	if res.Manual {
		t.Fatal("expected auto mode")
	}
	if !res.Changed {
		t.Fatal("expected upgrade to mars")
	}
	if res.BadgeID.String != "mars" {
		t.Fatalf("expected mars badge, got %s", res.BadgeID.String)
	}
}
