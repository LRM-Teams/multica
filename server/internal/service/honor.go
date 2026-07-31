package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// HonorSnapshot is the compact honor payload embedded in profile/member APIs.
type HonorSnapshot struct {
	Level     int             `json:"level"`
	NameStyle string          `json:"name_style"`
	Badge     *HonorBadgeView `json:"equipped_badge,omitempty"`
}

type HonorBadgeView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SvgKey      string `json:"svg_key"`
}

type HonorPillarProgressView struct {
	Pillar       string `json:"pillar"`
	CounterValue int64  `json:"counter_value"`
	Tier         int    `json:"tier"`
	NextTierAt   int64  `json:"next_tier_at,omitempty"`
}

type HonorUnlockView struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Source    string `json:"source"`
	GrantedAt string `json:"granted_at"`
}

type HonorDashboard struct {
	Level               int                       `json:"level"`
	TotalXP             int64                     `json:"total_xp"`
	XpToNextLevel       int64                     `json:"xp_to_next_level"`
	NameStyle           string                    `json:"name_style"`
	EquippedBadgeID     *string                   `json:"equipped_badge_id"`
	EquippedBadgeManual bool                      `json:"equipped_badge_manual"`
	ShowcaseBadgeIDs    []string                  `json:"showcase_badge_ids"`
	BadgesUnlocked      int                       `json:"badges_unlocked"`
	BadgesTotal         int                       `json:"badges_total"`
	BadgeCatalog        []HonorBadgeCatalogItem   `json:"badge_catalog"`
	RecentUnlocks       []HonorRecentUnlock       `json:"recent_unlocks"`
	Pillars             []HonorPillarProgressView `json:"pillars"`
	UnlockedBadges      []HonorBadgeView          `json:"unlocked_badges"`
	UnlockedStyles      []string                  `json:"unlocked_styles"`
	RecentXP            []HonorXPEventView        `json:"recent_xp"`
}

type HonorPublicWall struct {
	Level          int                 `json:"level"`
	NameStyle      string              `json:"name_style"`
	EquippedBadge  *HonorBadgeView     `json:"equipped_badge,omitempty"`
	ShowcaseBadges []HonorBadgeView    `json:"showcase_badges,omitempty"`
	RecentUnlocks  []HonorRecentUnlock `json:"recent_unlocks,omitempty"`
	BadgesUnlocked int                 `json:"badges_unlocked"`
	BadgesTotal    int                 `json:"badges_total"`
	UnlockedBadges []HonorBadgeView    `json:"unlocked_badges"`
}

type HonorXPEventView struct {
	Pillar     string `json:"pillar"`
	ActionType string `json:"action_type"`
	XPDelta    int32  `json:"xp_delta"`
	RefID      string `json:"ref_id,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type HonorService struct {
	Queries         *db.Queries
	OnBadgeUnlocked func(ctx context.Context, evt HonorBadgeUnlockEvent)
}

func NewHonorService(queries *db.Queries) *HonorService {
	return &HonorService{Queries: queries}
}

func (s *HonorService) GetRules(ctx context.Context) (HonorRulesDocument, error) {
	if s == nil || s.Queries == nil {
		return BuildHonorRulesDocument(nil), nil
	}
	rows, err := s.Queries.ListHonorBadgeDefs(ctx)
	if err != nil {
		return HonorRulesDocument{}, err
	}
	catalog := make([]HonorBadgeCatalogEntry, len(rows))
	for i, row := range rows {
		title, description, svgKey, _ := maskSecretBadge(row, false)
		catalog[i] = HonorBadgeCatalogEntry{
			ID:          row.ID,
			Title:       title,
			Description: description,
			SvgKey:      svgKey,
			Rarity:      int(row.Rarity),
		}
	}
	return BuildHonorRulesDocument(catalog), nil
}

func (s *HonorService) EnsureUserHonor(ctx context.Context, user db.User) error {
	if s == nil || s.Queries == nil {
		return nil
	}
	if _, err := s.Queries.CreateUserHonorIfMissing(ctx, user.ID); err != nil {
		return err
	}
	if IsFoundingMember(user.CreatedAt.Time) {
		s.tryUnlockBadge(ctx, user.ID, "founding", "founding")
		if _, err := s.Queries.InsertUserHonorUnlock(ctx, db.InsertUserHonorUnlockParams{
			UserID:     user.ID,
			UnlockKind: "style",
			DefID:      "founding",
			Source:     "founding",
		}); err != nil {
			return err
		}
	}
	return s.reconcileUserHonor(ctx, user.ID)
}

func (s *HonorService) AwardXP(ctx context.Context, userID pgtype.UUID, actionType string, refID string) error {
	if s == nil || s.Queries == nil || !userID.Valid {
		return nil
	}
	rule, ok := honorActionRules[actionType]
	if !ok {
		return nil
	}
	user, err := s.Queries.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.EnsureUserHonor(ctx, user); err != nil {
		return err
	}
	todayXP, err := s.Queries.SumUserXpLedgerTodayByAction(ctx, db.SumUserXpLedgerTodayByActionParams{
		UserID:     userID,
		ActionType: actionType,
	})
	if err != nil {
		return err
	}
	if int32(todayXP) >= rule.DailyCap {
		return nil
	}
	xp := rule.XPDelta
	if int32(todayXP)+xp > rule.DailyCap {
		xp = rule.DailyCap - int32(todayXP)
	}
	if xp <= 0 {
		return nil
	}
	if _, err := s.Queries.InsertUserXpLedger(ctx, db.InsertUserXpLedgerParams{
		UserID:     userID,
		Pillar:     string(rule.Pillar),
		ActionType: actionType,
		XpDelta:    xp,
		RefID:      pgtype.Text{String: refID, Valid: refID != ""},
	}); err != nil {
		return err
	}
	progress, err := s.Queries.GetUserPillarProgress(ctx, db.GetUserPillarProgressParams{
		UserID: userID,
		Pillar: string(rule.Pillar),
	})
	counter := rule.Counter
	tier := int32(0)
	if err == nil {
		counter += progress.CounterValue
		tier = progress.Tier
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	newTier := int32(PillarTierFromCounter(rule.Pillar, counter))
	if _, err := s.Queries.UpsertUserPillarProgress(ctx, db.UpsertUserPillarProgressParams{
		UserID:       userID,
		Pillar:       string(rule.Pillar),
		CounterValue: counter,
		Tier:         newTier,
	}); err != nil {
		return err
	}
	if newTier > tier {
		s.unlockAchievementBadges(ctx, userID, rule.Pillar, int(newTier))
	}
	return s.reconcileUserHonor(ctx, userID)
}

func (s *HonorService) unlockLevelBadges(ctx context.Context, userID pgtype.UUID, level int) {
	unlocks := make([]struct {
		minLevel int
		badgeID  string
	}, 0, len(honorBadgeRequirements))
	for badgeID, requirement := range honorBadgeRequirements {
		if requirement.minLevel > 0 {
			unlocks = append(unlocks, struct {
				minLevel int
				badgeID  string
			}{minLevel: requirement.minLevel, badgeID: badgeID})
		}
	}
	sort.Slice(unlocks, func(i, j int) bool {
		if unlocks[i].minLevel == unlocks[j].minLevel {
			return unlocks[i].badgeID < unlocks[j].badgeID
		}
		return unlocks[i].minLevel < unlocks[j].minLevel
	})
	for _, u := range unlocks {
		if level >= u.minLevel {
			s.tryUnlockBadge(ctx, userID, u.badgeID, "auto")
		}
	}
}

func (s *HonorService) unlockAchievementBadges(ctx context.Context, userID pgtype.UUID, pillar HonorPillar, tier int) {
	badgeIDs := make([]string, 0)
	for badgeID, requirement := range honorBadgeRequirements {
		if requirement.pillar == pillar && requirement.minTier > 0 && tier >= requirement.minTier {
			badgeIDs = append(badgeIDs, badgeID)
		}
	}
	sort.Strings(badgeIDs)
	for _, badgeID := range badgeIDs {
		s.tryUnlockBadge(ctx, userID, badgeID, "auto")
	}
}

func (s *HonorService) reconcileUserHonor(ctx context.Context, userID pgtype.UUID) error {
	if _, err := s.Queries.GetUserHonor(ctx, userID); err != nil {
		return err
	}
	total, err := s.Queries.SumUserXpLedger(ctx, userID)
	if err != nil {
		return err
	}
	level := LevelFromTotalXP(total)
	s.unlockLevelBadges(ctx, userID, level)
	styleRows, err := s.Queries.ListHonorNameStyleDefs(ctx)
	if err != nil {
		return err
	}
	for _, style := range styleRows {
		if int32(level) >= style.MinLevel {
			_, _ = s.Queries.InsertUserHonorUnlock(ctx, db.InsertUserHonorUnlockParams{
				UserID: userID, UnlockKind: "style", DefID: style.ID, Source: "auto",
			})
		}
	}
	pillarRows, err := s.Queries.ListUserPillarProgress(ctx, userID)
	if err != nil {
		return err
	}
	for _, progress := range pillarRows {
		s.unlockAchievementBadges(ctx, userID, HonorPillar(progress.Pillar), int(progress.Tier))
	}
	_, err = s.Queries.UpdateUserHonorStats(ctx, db.UpdateUserHonorStatsParams{
		UserID:  userID,
		TotalXp: total,
		Level:   int32(level),
	})
	if err != nil {
		return err
	}
	return s.syncEquippedBadge(ctx, userID)
}

func badgeUnlocked(unlocks []db.UserHonorUnlock, badgeID string) bool {
	for _, u := range unlocks {
		if u.UnlockKind == "badge" && u.DefID == badgeID {
			return true
		}
	}
	return false
}

type equippedBadgeResolution struct {
	BadgeID pgtype.Text
	Manual  bool
	Changed bool
}

func resolveEquippedBadge(honor db.UserHonor, unlocks []db.UserHonorUnlock, bestID string, hasBest bool) equippedBadgeResolution {
	if honor.EquippedBadgeManual && honor.EquippedBadgeID.Valid && badgeUnlocked(unlocks, honor.EquippedBadgeID.String) {
		return equippedBadgeResolution{
			BadgeID: honor.EquippedBadgeID,
			Manual:  true,
			Changed: false,
		}
	}
	var badge pgtype.Text
	if hasBest {
		badge = pgtype.Text{String: bestID, Valid: true}
	}
	changed := honor.EquippedBadgeManual ||
		honor.EquippedBadgeID.Valid != badge.Valid ||
		(honor.EquippedBadgeID.Valid && badge.Valid && honor.EquippedBadgeID.String != badge.String)
	return equippedBadgeResolution{
		BadgeID: badge,
		Manual:  false,
		Changed: changed,
	}
}

func (s *HonorService) syncEquippedBadge(ctx context.Context, userID pgtype.UUID) error {
	honor, err := s.Queries.GetUserHonor(ctx, userID)
	if err != nil {
		return err
	}
	unlocks, err := s.Queries.ListUserHonorUnlocks(ctx, userID)
	if err != nil {
		return err
	}
	best, err := s.bestUnlockedBadgeView(ctx, unlocks)
	if err != nil {
		return err
	}
	bestID := ""
	hasBest := best != nil
	if hasBest {
		bestID = best.ID
	}
	res := resolveEquippedBadge(honor, unlocks, bestID, hasBest)
	if !res.Changed {
		return nil
	}
	_, err = s.Queries.UpdateUserHonorEquippedBadge(ctx, db.UpdateUserHonorEquippedBadgeParams{
		UserID:              userID,
		EquippedBadgeID:     res.BadgeID,
		EquippedBadgeManual: res.Manual,
	})
	return err
}

func (s *HonorService) SetEquippedBadge(ctx context.Context, userID pgtype.UUID, badgeID string) error {
	if s == nil || s.Queries == nil {
		return nil
	}
	unlocks, err := s.Queries.ListUserHonorUnlocks(ctx, userID)
	if err != nil {
		return err
	}
	found := false
	for _, u := range unlocks {
		if u.UnlockKind == "badge" && u.DefID == badgeID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("badge not unlocked")
	}
	_, err = s.Queries.UpdateUserHonorEquippedBadge(ctx, db.UpdateUserHonorEquippedBadgeParams{
		UserID:              userID,
		EquippedBadgeID:     pgtype.Text{String: badgeID, Valid: true},
		EquippedBadgeManual: true,
	})
	return err
}

func (s *HonorService) ClearEquippedBadge(ctx context.Context, userID pgtype.UUID) error {
	if s == nil || s.Queries == nil {
		return nil
	}
	if err := s.syncEquippedBadgeAfterManualReset(ctx, userID); err != nil {
		return err
	}
	return nil
}

func (s *HonorService) syncEquippedBadgeAfterManualReset(ctx context.Context, userID pgtype.UUID) error {
	_, err := s.Queries.UpdateUserHonorEquippedBadge(ctx, db.UpdateUserHonorEquippedBadgeParams{
		UserID:              userID,
		EquippedBadgeID:     pgtype.Text{},
		EquippedBadgeManual: false,
	})
	if err != nil {
		return err
	}
	return s.syncEquippedBadge(ctx, userID)
}

func (s *HonorService) GetDashboard(ctx context.Context, user db.User) (HonorDashboard, error) {
	if err := s.EnsureUserHonor(ctx, user); err != nil {
		return HonorDashboard{}, err
	}
	snapshot, err := s.buildSnapshot(ctx, user.ID)
	if err != nil {
		return HonorDashboard{}, err
	}
	honor, err := s.Queries.GetUserHonor(ctx, user.ID)
	if err != nil {
		return HonorDashboard{}, err
	}
	pillars, err := s.listPillarViews(ctx, user.ID)
	if err != nil {
		return HonorDashboard{}, err
	}
	badges, styles, err := s.listUnlockViews(ctx, user.ID)
	if err != nil {
		return HonorDashboard{}, err
	}
	ledger, err := s.Queries.ListUserXpLedgerRecent(ctx, db.ListUserXpLedgerRecentParams{
		UserID: user.ID,
		Limit:  20,
	})
	if err != nil {
		return HonorDashboard{}, err
	}
	events := make([]HonorXPEventView, len(ledger))
	for i, row := range ledger {
		events[i] = HonorXPEventView{
			Pillar:     row.Pillar,
			ActionType: row.ActionType,
			XPDelta:    row.XpDelta,
			RefID:      textFromPg(row.RefID),
			CreatedAt:  row.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
	}
	var equipped *string
	if honor.EquippedBadgeID.Valid {
		v := honor.EquippedBadgeID.String
		equipped = &v
	}
	unlocks, err := s.Queries.ListUserHonorUnlocks(ctx, user.ID)
	if err != nil {
		return HonorDashboard{}, err
	}
	catalog, unlockedCount, totalBadges, err := s.buildBadgeCatalog(ctx, user, honor, unlocks, pillars)
	if err != nil {
		return HonorDashboard{}, err
	}
	recent, err := s.listRecentUnlocks(ctx, user.ID, 5)
	if err != nil {
		return HonorDashboard{}, err
	}
	showcase := honor.ShowcaseBadgeIds
	if showcase == nil {
		showcase = []string{}
	}
	return HonorDashboard{
		Level:               snapshot.Level,
		TotalXP:             honor.TotalXp,
		XpToNextLevel:       XPToNextLevel(honor.TotalXp, snapshot.Level),
		NameStyle:           snapshot.NameStyle,
		EquippedBadgeID:     equipped,
		EquippedBadgeManual: honor.EquippedBadgeManual,
		ShowcaseBadgeIDs:    showcase,
		BadgesUnlocked:      unlockedCount,
		BadgesTotal:         totalBadges,
		BadgeCatalog:        catalog,
		RecentUnlocks:       recent,
		Pillars:             pillars,
		UnlockedBadges:      badges,
		UnlockedStyles:      styles,
		RecentXP:            events,
	}, nil
}

func (s *HonorService) GetPublicWall(ctx context.Context, user db.User) (HonorPublicWall, error) {
	if err := s.EnsureUserHonor(ctx, user); err != nil {
		return HonorPublicWall{}, err
	}
	snapshot, err := s.buildSnapshot(ctx, user.ID)
	if err != nil {
		return HonorPublicWall{}, err
	}
	badges, _, err := s.listUnlockViews(ctx, user.ID)
	if err != nil {
		return HonorPublicWall{}, err
	}
	honor, err := s.Queries.GetUserHonor(ctx, user.ID)
	if err != nil {
		return HonorPublicWall{}, err
	}
	unlocks, err := s.Queries.ListUserHonorUnlocks(ctx, user.ID)
	if err != nil {
		return HonorPublicWall{}, err
	}
	showcase, err := s.listShowcaseBadges(ctx, honor, unlocks)
	if err != nil {
		return HonorPublicWall{}, err
	}
	recent, err := s.listRecentUnlocks(ctx, user.ID, 5)
	if err != nil {
		return HonorPublicWall{}, err
	}
	defs, err := s.Queries.ListHonorBadgeDefs(ctx)
	if err != nil {
		return HonorPublicWall{}, err
	}
	return HonorPublicWall{
		Level:          snapshot.Level,
		NameStyle:      snapshot.NameStyle,
		EquippedBadge:  snapshot.Badge,
		ShowcaseBadges: showcase,
		RecentUnlocks:  recent,
		BadgesUnlocked: len(badges),
		BadgesTotal:    len(defs),
		UnlockedBadges: badges,
	}, nil
}

func (s *HonorService) BuildSnapshots(ctx context.Context, userIDs []pgtype.UUID) (map[string]HonorSnapshot, error) {
	out := make(map[string]HonorSnapshot, len(userIDs))
	for _, id := range userIDs {
		if !id.Valid {
			continue
		}
		user, err := s.Queries.GetUser(ctx, id)
		if err != nil {
			slog.Warn("honor snapshot: user missing", "user_id", util.UUIDToString(id), "error", err)
			continue
		}
		if err := s.EnsureUserHonor(ctx, user); err != nil {
			slog.Warn("honor snapshot: ensure failed", "user_id", util.UUIDToString(id), "error", err)
			continue
		}
		snap, err := s.buildSnapshot(ctx, id)
		if err != nil {
			continue
		}
		out[util.UUIDToString(id)] = snap
	}
	return out, nil
}

func honorBadgeViewFromDef(def db.HonorBadgeDef) *HonorBadgeView {
	return &HonorBadgeView{
		ID:          def.ID,
		Title:       def.Title,
		Description: def.Description,
		SvgKey:      def.SvgKey,
	}
}

func (s *HonorService) bestUnlockedBadgeView(ctx context.Context, unlocks []db.UserHonorUnlock) (*HonorBadgeView, error) {
	unlocked := map[string]struct{}{}
	for _, u := range unlocks {
		if u.UnlockKind == "badge" {
			unlocked[u.DefID] = struct{}{}
		}
	}
	if len(unlocked) == 0 {
		return nil, nil
	}
	defs, err := s.Queries.ListHonorBadgeDefs(ctx)
	if err != nil {
		return nil, err
	}
	var best *HonorBadgeView
	var bestRank int32 = -1
	for _, def := range defs {
		if _, ok := unlocked[def.ID]; !ok {
			continue
		}
		if def.SortRank > bestRank {
			bestRank = def.SortRank
			best = honorBadgeViewFromDef(def)
		}
	}
	return best, nil
}

func (s *HonorService) buildSnapshot(ctx context.Context, userID pgtype.UUID) (HonorSnapshot, error) {
	honor, err := s.Queries.GetUserHonor(ctx, userID)
	if err != nil {
		return HonorSnapshot{}, err
	}
	unlocks, err := s.Queries.ListUserHonorUnlocks(ctx, userID)
	if err != nil {
		return HonorSnapshot{}, err
	}
	styleRows, err := s.Queries.ListHonorNameStyleDefs(ctx)
	if err != nil {
		return HonorSnapshot{}, err
	}
	styleRank := map[string]int32{}
	maxRank := int32(-1)
	nameStyle := "default"
	unlockedStyles := map[string]struct{}{}
	for _, u := range unlocks {
		if u.UnlockKind == "style" {
			unlockedStyles[u.DefID] = struct{}{}
		}
	}
	for _, row := range styleRows {
		styleRank[row.ID] = row.SortRank
		if _, ok := unlockedStyles[row.ID]; !ok {
			continue
		}
		if row.SortRank > maxRank {
			maxRank = row.SortRank
			nameStyle = row.ID
		}
	}
	var badge *HonorBadgeView
	if honor.EquippedBadgeID.Valid {
		def, err := s.Queries.GetHonorBadgeDef(ctx, honor.EquippedBadgeID.String)
		if err == nil {
			badge = honorBadgeViewFromDef(def)
		}
	}
	if badge == nil {
		badge, _ = s.bestUnlockedBadgeView(ctx, unlocks)
	}
	return HonorSnapshot{
		Level:     int(honor.Level),
		NameStyle: nameStyle,
		Badge:     badge,
	}, nil
}

func (s *HonorService) listPillarViews(ctx context.Context, userID pgtype.UUID) ([]HonorPillarProgressView, error) {
	rows, err := s.Queries.ListUserPillarProgress(ctx, userID)
	if err != nil {
		return nil, err
	}
	existing := map[string]db.UserPillarProgress{}
	for _, row := range rows {
		existing[row.Pillar] = row
	}
	pillars := []HonorPillar{HonorPillarUsage, HonorPillarPresence, HonorPillarDelivery, HonorPillarCommunity}
	out := make([]HonorPillarProgressView, 0, len(pillars))
	for _, pillar := range pillars {
		rowPillar := string(pillar)
		row, ok := existing[rowPillar]
		counter := int64(0)
		tier := 0
		if ok {
			counter = row.CounterValue
			tier = int(row.Tier)
		}
		next := int64(0)
		if thresholds, ok := honorPillarTierThresholds[pillar]; ok && tier < len(thresholds) {
			next = thresholds[tier]
		}
		out = append(out, HonorPillarProgressView{
			Pillar:       rowPillar,
			CounterValue: counter,
			Tier:         tier,
			NextTierAt:   next,
		})
	}
	return out, nil
}

func (s *HonorService) listUnlockViews(ctx context.Context, userID pgtype.UUID) ([]HonorBadgeView, []string, error) {
	unlocks, err := s.Queries.ListUserHonorUnlocks(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	badgeDefs, err := s.Queries.ListHonorBadgeDefs(ctx)
	if err != nil {
		return nil, nil, err
	}
	defByID := map[string]db.HonorBadgeDef{}
	for _, def := range badgeDefs {
		defByID[def.ID] = def
	}
	badges := make([]HonorBadgeView, 0)
	styles := make([]string, 0)
	for _, u := range unlocks {
		switch u.UnlockKind {
		case "badge":
			if def, ok := defByID[u.DefID]; ok {
				badges = append(badges, HonorBadgeView{
					ID:          def.ID,
					Title:       def.Title,
					Description: def.Description,
					SvgKey:      def.SvgKey,
				})
			}
		case "style":
			styles = append(styles, u.DefID)
		}
	}
	return badges, styles, nil
}

func textFromPg(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
