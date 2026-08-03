package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildHonorSnapshotsUsesBatchRowsAndDefaults(t *testing.T) {
	userOne := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	userTwo := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}

	snapshots := buildHonorSnapshots(
		[]pgtype.UUID{userOne, userTwo},
		[]db.UserHonor{{
			UserID:          userOne,
			Level:           12,
			EquippedBadgeID: pgtype.Text{String: "stardust", Valid: true},
		}},
		[]db.UserHonorUnlock{
			{UserID: userOne, UnlockKind: "style", DefID: "member"},
			{UserID: userOne, UnlockKind: "style", DefID: "gold"},
			{UserID: userOne, UnlockKind: "badge", DefID: "mars"},
			{UserID: userTwo, UnlockKind: "badge", DefID: "founding"},
		},
		[]db.HonorNameStyleDef{
			{ID: "member", SortRank: 10},
			{ID: "gold", SortRank: 20},
		},
		[]db.HonorBadgeDef{
			{ID: "stardust", Title: "Stardust", SortRank: 10},
			{ID: "mars", Title: "Mars", SortRank: 20},
			{ID: "founding", Title: "Genesis Nebula", SortRank: 100},
		},
	)

	first := snapshots[util.UUIDToString(userOne)]
	if first.Level != 12 || first.NameStyle != "gold" {
		t.Fatalf("first snapshot = %+v", first)
	}
	if first.Badge == nil || first.Badge.ID != "stardust" {
		t.Fatalf("first badge = %+v, want equipped stardust", first.Badge)
	}

	second := snapshots[util.UUIDToString(userTwo)]
	if second.Level != 1 || second.NameStyle != "default" {
		t.Fatalf("second snapshot = %+v, want defaults", second)
	}
	if second.Badge == nil || second.Badge.ID != "founding" {
		t.Fatalf("second badge = %+v, want best unlocked founding", second.Badge)
	}
}
