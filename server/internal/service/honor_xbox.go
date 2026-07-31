package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxShowcaseBadges = 3

// HonorBadgeUnlockEvent is emitted when a user newly unlocks a badge.
type HonorBadgeUnlockEvent struct {
	UserID    pgtype.UUID
	Badge     HonorBadgeView
	UnlockPct float64
}

// HonorBadgeCatalogItem is one row in the Xbox-style achievement list.
type HonorBadgeCatalogItem struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	SvgKey      string              `json:"svg_key"`
	Rarity      int                 `json:"rarity"`
	UnlockRule  string              `json:"unlock_rule"`
	Secret      bool                `json:"secret"`
	Unlocked    bool                `json:"unlocked"`
	UnlockedAt  *string             `json:"unlocked_at,omitempty"`
	UnlockPct   float64             `json:"unlock_pct,omitempty"`
	Progress    *HonorBadgeProgress `json:"progress,omitempty"`
}

type HonorBadgeProgress struct {
	Current int64  `json:"current"`
	Target  int64  `json:"target"`
	Label   string `json:"label"`
}

type HonorRecentUnlock struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SvgKey      string `json:"svg_key"`
	UnlockedAt  string `json:"unlocked_at"`
}

type HonorCompareSide struct {
	UserID        string `json:"user_id"`
	Level         int    `json:"level"`
	UnlockedCount int    `json:"unlocked_count"`
	TotalBadges   int    `json:"total_badges"`
}

type HonorCompareResult struct {
	Self      HonorCompareSide `json:"self"`
	Other     HonorCompareSide `json:"other"`
	Shared    []HonorBadgeView `json:"shared_badges"`
	SelfOnly  []HonorBadgeView `json:"self_only_badges"`
	OtherOnly []HonorBadgeView `json:"other_only_badges"`
}

type badgeUnlockRequirement struct {
	minLevel int
	pillar   HonorPillar
	minTier  int
	founding bool
}

var honorBadgeRequirements = map[string]badgeUnlockRequirement{
	"founding":              {founding: true},
	"stardust":              {minLevel: 3},
	"mercury":               {minLevel: 5},
	"venus":                 {minLevel: 8},
	"earth":                 {minLevel: 10},
	"mars":                  {minLevel: 12},
	"jupiter":               {minLevel: 15},
	"saturn":                {minLevel: 18},
	"veteran":               {minLevel: 20},
	"uranus":                {minLevel: 22},
	"neptune":               {minLevel: 26},
	"pluto":                 {minLevel: 30},
	"red_dwarf":             {minLevel: 35},
	"blue_giant":            {minLevel: 40},
	"quasar":                {minLevel: 50},
	"builder":               {pillar: HonorPillarDelivery, minTier: 4},
	"collaborator":          {pillar: HonorPillarCommunity, minTier: 3},
	"lunar_spark":           {minLevel: 2},
	"comet_trail":           {minLevel: 4},
	"asteroid_scout":        {minLevel: 6},
	"eclipse_watcher":       {minLevel: 7},
	"pulsar_ping":           {minLevel: 9},
	"solar_sailor":          {minLevel: 11},
	"orbital_cadet":         {minLevel: 13},
	"lunar_architect":       {minLevel: 14},
	"pathfinder":            {minLevel: 16},
	"voyager":               {minLevel: 17},
	"beacon_keeper":         {minLevel: 19},
	"relay_master":          {minLevel: 21},
	"archive_seed":          {minLevel: 23},
	"constellation_map":     {minLevel: 24},
	"aurora_weaver":         {minLevel: 25},
	"galaxy_roamer":         {minLevel: 27},
	"wormhole_cartographer": {minLevel: 28},
	"terraformer":           {minLevel: 29},
	"foundry_heart":         {minLevel: 32},
	"nexus_link":            {minLevel: 34},
	"helix_mind":            {minLevel: 37},
	"prism_core":            {minLevel: 42},
	"plasma_orb":            {minLevel: 45},
	"quantum_gate":          {minLevel: 48},
	"singularity":           {minLevel: 52},
	"celestial_crown":       {minLevel: 54},
	"event_horizon":         {minLevel: 56},
	"cosmic_tree":           {minLevel: 58},
	"infinity_engine":       {minLevel: 60},
	"signal_architect":      {pillar: HonorPillarUsage, minTier: 3},
	"chronicle_engine":      {pillar: HonorPillarUsage, minTier: 6},
	"steady_light":          {pillar: HonorPillarPresence, minTier: 4},
	"everpresent":           {pillar: HonorPillarPresence, minTier: 8},
	"delivery_singularity":  {pillar: HonorPillarDelivery, minTier: 8},
}

// tryUnlockBadge inserts a badge unlock when new and fires OnBadgeUnlocked.
func (s *HonorService) tryUnlockBadge(ctx context.Context, userID pgtype.UUID, badgeID, source string) {
	if s == nil || s.Queries == nil || !userID.Valid {
		return
	}
	row, err := s.Queries.InsertUserHonorUnlockIfNew(ctx, db.InsertUserHonorUnlockIfNewParams{
		UserID:     userID,
		UnlockKind: "badge",
		DefID:      badgeID,
		Source:     source,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		return
	}
	_ = row
	def, err := s.Queries.GetHonorBadgeDef(ctx, badgeID)
	if err != nil {
		return
	}
	pct, _ := s.unlockPctForBadge(ctx, badgeID)
	if s.OnBadgeUnlocked != nil {
		s.OnBadgeUnlocked(ctx, HonorBadgeUnlockEvent{
			UserID:    userID,
			Badge:     *honorBadgeViewFromDef(def),
			UnlockPct: pct,
		})
	}
}

func (s *HonorService) loadUnlockPctMap(ctx context.Context) (map[string]float64, error) {
	counts, err := s.Queries.CountHonorBadgeUnlocks(ctx)
	if err != nil {
		return nil, err
	}
	totalUsers, err := s.Queries.CountHonorUsers(ctx)
	if err != nil || totalUsers <= 0 {
		return map[string]float64{}, err
	}
	out := make(map[string]float64, len(counts))
	for _, row := range counts {
		out[row.DefID] = float64(row.UnlockCount) / float64(totalUsers) * 100
	}
	return out, nil
}

func (s *HonorService) unlockPctForBadge(ctx context.Context, badgeID string) (float64, error) {
	m, err := s.loadUnlockPctMap(ctx)
	if err != nil {
		return 0, err
	}
	return m[badgeID], nil
}

func maskSecretBadge(
	def db.HonorBadgeDef,
	unlocked bool,
) (title, description, svgKey, unlockRule string) {
	if def.Secret && !unlocked {
		return "Secret Badge", "Unlock to reveal this badge.", "stardust", ""
	}
	return def.Title, def.Description, def.SvgKey, def.UnlockRule
}

func (s *HonorService) buildBadgeCatalog(
	ctx context.Context,
	user db.User,
	honor db.UserHonor,
	unlocks []db.UserHonorUnlock,
	pillars []HonorPillarProgressView,
) ([]HonorBadgeCatalogItem, int, int, error) {
	defs, err := s.Queries.ListHonorBadgeDefs(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	unlockMap := map[string]db.UserHonorUnlock{}
	for _, u := range unlocks {
		if u.UnlockKind == "badge" {
			unlockMap[u.DefID] = u
		}
	}
	pctMap, _ := s.loadUnlockPctMap(ctx)
	pillarMap := map[string]HonorPillarProgressView{}
	for _, p := range pillars {
		pillarMap[p.Pillar] = p
	}
	items := make([]HonorBadgeCatalogItem, 0, len(defs))
	unlockedCount := 0
	for _, def := range defs {
		u, ok := unlockMap[def.ID]
		unlocked := ok
		if unlocked {
			unlockedCount++
		}
		title, desc, svgKey, unlockRule := maskSecretBadge(def, unlocked)
		item := HonorBadgeCatalogItem{
			ID:          def.ID,
			Title:       title,
			Description: desc,
			SvgKey:      svgKey,
			Rarity:      int(def.Rarity),
			UnlockRule:  unlockRule,
			Secret:      def.Secret,
			Unlocked:    unlocked,
			UnlockPct:   pctMap[def.ID],
		}
		if unlocked {
			at := u.GrantedAt.Time.UTC().Format(time.RFC3339)
			item.UnlockedAt = &at
		} else if prog := badgeProgressFor(def.ID, int(honor.Level), pillarMap, user); prog != nil {
			item.Progress = prog
		}
		items = append(items, item)
	}
	return items, unlockedCount, len(defs), nil
}

func badgeProgressFor(badgeID string, level int, pillars map[string]HonorPillarProgressView, user db.User) *HonorBadgeProgress {
	req, ok := honorBadgeRequirements[badgeID]
	if !ok {
		return nil
	}
	if req.founding {
		if IsFoundingMember(user.CreatedAt.Time) {
			return &HonorBadgeProgress{Current: 1, Target: 1, Label: "founding"}
		}
		return &HonorBadgeProgress{Current: 0, Target: 1, Label: "founding"}
	}
	if req.minLevel > 0 {
		current := int64(level)
		target := int64(req.minLevel)
		if current > target {
			current = target
		}
		return &HonorBadgeProgress{Current: current, Target: target, Label: "level"}
	}
	if req.minTier > 0 {
		row := pillars[string(req.pillar)]
		current := int64(row.Tier)
		target := int64(req.minTier)
		if current > target {
			current = target
		}
		return &HonorBadgeProgress{Current: current, Target: target, Label: string(req.pillar)}
	}
	return nil
}

func (s *HonorService) listRecentUnlocks(ctx context.Context, userID pgtype.UUID, limit int32) ([]HonorRecentUnlock, error) {
	rows, err := s.Queries.ListRecentBadgeUnlocks(ctx, db.ListRecentBadgeUnlocksParams{
		UserID: userID,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]HonorRecentUnlock, len(rows))
	for i, row := range rows {
		out[i] = HonorRecentUnlock{
			ID:          row.DefID,
			Title:       row.Title,
			Description: row.Description,
			SvgKey:      row.SvgKey,
			UnlockedAt:  row.GrantedAt.Time.UTC().Format(time.RFC3339),
		}
	}
	return out, nil
}

func (s *HonorService) listShowcaseBadges(ctx context.Context, honor db.UserHonor, unlocks []db.UserHonorUnlock) ([]HonorBadgeView, error) {
	if len(honor.ShowcaseBadgeIds) == 0 {
		return []HonorBadgeView{}, nil
	}
	unlocked := map[string]struct{}{}
	for _, u := range unlocks {
		if u.UnlockKind == "badge" {
			unlocked[u.DefID] = struct{}{}
		}
	}
	out := make([]HonorBadgeView, 0, len(honor.ShowcaseBadgeIds))
	for _, id := range honor.ShowcaseBadgeIds {
		if _, ok := unlocked[id]; !ok {
			continue
		}
		def, err := s.Queries.GetHonorBadgeDef(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, *honorBadgeViewFromDef(def))
	}
	return out, nil
}

func (s *HonorService) SetShowcaseBadges(ctx context.Context, userID pgtype.UUID, badgeIDs []string) error {
	if s == nil || s.Queries == nil {
		return nil
	}
	if len(badgeIDs) > maxShowcaseBadges {
		return fmt.Errorf("showcase supports at most %d badges", maxShowcaseBadges)
	}
	unlocks, err := s.Queries.ListUserHonorUnlocks(ctx, userID)
	if err != nil {
		return err
	}
	unlocked := map[string]struct{}{}
	for _, u := range unlocks {
		if u.UnlockKind == "badge" {
			unlocked[u.DefID] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(badgeIDs))
	for _, id := range badgeIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if _, ok := unlocked[id]; !ok {
			return errors.New("badge not unlocked")
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	_, err = s.Queries.UpdateUserHonorShowcase(ctx, db.UpdateUserHonorShowcaseParams{
		UserID:           userID,
		ShowcaseBadgeIds: clean,
	})
	return err
}

func (s *HonorService) CompareWithUser(ctx context.Context, self db.User, other db.User) (HonorCompareResult, error) {
	if err := s.EnsureUserHonor(ctx, self); err != nil {
		return HonorCompareResult{}, err
	}
	if err := s.EnsureUserHonor(ctx, other); err != nil {
		return HonorCompareResult{}, err
	}
	selfBadges, _, err := s.listUnlockViews(ctx, self.ID)
	if err != nil {
		return HonorCompareResult{}, err
	}
	otherBadges, _, err := s.listUnlockViews(ctx, other.ID)
	if err != nil {
		return HonorCompareResult{}, err
	}
	defs, err := s.Queries.ListHonorBadgeDefs(ctx)
	if err != nil {
		return HonorCompareResult{}, err
	}
	selfHonor, _ := s.Queries.GetUserHonor(ctx, self.ID)
	otherHonor, _ := s.Queries.GetUserHonor(ctx, other.ID)
	selfSet := map[string]HonorBadgeView{}
	for _, b := range selfBadges {
		selfSet[b.ID] = b
	}
	otherSet := map[string]HonorBadgeView{}
	for _, b := range otherBadges {
		otherSet[b.ID] = b
	}
	shared := make([]HonorBadgeView, 0)
	selfOnly := make([]HonorBadgeView, 0)
	otherOnly := make([]HonorBadgeView, 0)
	for id, b := range selfSet {
		if _, ok := otherSet[id]; ok {
			shared = append(shared, b)
		} else {
			selfOnly = append(selfOnly, b)
		}
	}
	for id, b := range otherSet {
		if _, ok := selfSet[id]; !ok {
			otherOnly = append(otherOnly, b)
		}
	}
	return HonorCompareResult{
		Self: HonorCompareSide{
			UserID:        util.UUIDToString(self.ID),
			Level:         int(selfHonor.Level),
			UnlockedCount: len(selfBadges),
			TotalBadges:   len(defs),
		},
		Other: HonorCompareSide{
			UserID:        util.UUIDToString(other.ID),
			Level:         int(otherHonor.Level),
			UnlockedCount: len(otherBadges),
			TotalBadges:   len(defs),
		},
		Shared:    shared,
		SelfOnly:  selfOnly,
		OtherOnly: otherOnly,
	}, nil
}
