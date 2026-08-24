package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/memorygrowth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	FleetRulesVersion   = "2026-07-30"
	FleetWindowDays     = 30
	FleetMinSampleTasks = 5

	fleetWeightDelivery   = 55.0 / 105.0
	fleetWeightEvolution  = 25.0 / 105.0
	fleetWeightGrowth     = 15.0 / 105.0
	fleetWeightEfficiency = 10.0 / 105.0
)

var fleetClassLabels = map[string]string{
	"reserve":     "Reserve",
	"corvette":    "Corvette",
	"frigate":     "Frigate",
	"cruiser":     "Cruiser",
	"battleship":  "Battleship",
	"dreadnought": "Dreadnought",
}

type FleetPillarScores struct {
	Delivery   float64 `json:"delivery"`
	Evolution  float64 `json:"evolution"`
	Growth     float64 `json:"growth"`
	Efficiency float64 `json:"efficiency"`
}

type AgentFleetRankView struct {
	AgentID          string            `json:"agent_id"`
	FleetScore       float64           `json:"fleet_score"`
	ClassID          string            `json:"class_id"`
	ClassLabel       string            `json:"class_label"`
	FleetRank        int               `json:"fleet_rank"`
	FleetSize        int               `json:"fleet_size"`
	SampleTasks      int               `json:"sample_tasks"`
	MinSampleTasks   int               `json:"min_sample_tasks"`
	SampleSufficient bool              `json:"sample_sufficient"`
	Frozen           bool              `json:"frozen"`
	Pillars          FleetPillarScores `json:"pillars"`
}

type FleetClassThreshold struct {
	ClassID  string  `json:"class_id"`
	MinScore float64 `json:"min_score"`
	Label    string  `json:"label"`
	SvgKey   string  `json:"svg_key"`
}

type FleetRulesDocument struct {
	Version         string                `json:"version"`
	WindowDays      int                   `json:"window_days"`
	MinSampleTasks  int                   `json:"min_sample_tasks"`
	PillarWeights   map[string]float64    `json:"pillar_weights"`
	ClassThresholds []FleetClassThreshold `json:"class_thresholds"`
	Changelog       []string              `json:"changelog"`
}

type AgentFleetRankChange struct {
	CurrentAgentID  pgtype.UUID
	PreviousClassID string
	Current         AgentFleetRankView
}

type AgentFleetRankService struct {
	Queries        *db.Queries
	workspaceLocks sync.Map

	archiveRefreshMu      sync.Mutex
	archiveRefreshQueued  map[string]bool
	archiveRefreshPending map[string]bool
	archiveRefresh        func(context.Context, pgtype.UUID) error
}

func NewAgentFleetRankService(queries *db.Queries) *AgentFleetRankService {
	return &AgentFleetRankService{
		Queries:               queries,
		archiveRefreshQueued:  make(map[string]bool),
		archiveRefreshPending: make(map[string]bool),
	}
}

func fleetRulesDocumentFromHonorRules(rules AgentHonorRules) FleetRulesDocument {
	thresholds := make([]FleetClassThreshold, 0, len(rules.FleetClasses))
	for _, class := range rules.FleetClasses {
		thresholds = append(thresholds, FleetClassThreshold{
			ClassID: class.ClassID, MinScore: class.Score, Label: class.Label, SvgKey: class.ClassID,
		})
	}
	return FleetRulesDocument{
		Version: rules.Version, WindowDays: rules.FleetWindowDays,
		MinSampleTasks: rules.FleetMinSampleTasks, PillarWeights: rules.FleetWeights,
		ClassThresholds: thresholds, Changelog: rules.Changelog,
	}
}

func (s *AgentFleetRankService) GetRulesDocument(
	ctx context.Context,
	workspaceID pgtype.UUID,
) (FleetRulesDocument, error) {
	rules, _, err := loadAgentHonorRules(ctx, s.Queries, workspaceID)
	if err != nil {
		return FleetRulesDocument{}, err
	}
	return fleetRulesDocumentFromHonorRules(rules), nil
}

func bayesianSuccessRate(successes, failures int64) float64 {
	return float64(successes+2) / float64(successes+failures+4)
}

func deliveryPillar(completed, failed int64) float64 {
	total := completed + failed
	if total == 0 {
		return 0
	}
	rate := bayesianSuccessRate(completed, failed)
	volumeBonus := math.Min(30, float64(total)*2)
	return math.Min(100, rate*70+volumeBonus)
}

func evolutionPillar(success, feedbackTotal, promotions int64) float64 {
	var feedbackScore float64
	if feedbackTotal > 0 {
		feedbackScore = bayesianSuccessRate(success, feedbackTotal-success) * 60
	}
	promoScore := math.Min(40, float64(promotions)*10)
	return math.Min(100, feedbackScore+promoScore)
}

func growthPillar(totalWrites, writes30d int64) float64 {
	if totalWrites <= 0 && writes30d <= 0 {
		return 0
	}
	tierScore := 0.0
	if snap := memorygrowth.Compute(int(totalWrites), memorygrowth.DefaultBase, memorygrowth.DefaultRatio); snap != nil {
		switch snap.Tier {
		case "bronze":
			tierScore = 20
		case "silver":
			tierScore = 40
		case "gold":
			tierScore = 60
		case "platinum":
			tierScore = 80
		}
	}
	velocity := math.Min(20, float64(writes30d)*2)
	return math.Min(100, tierScore+velocity)
}

func efficiencyPillar(completed int64, totalTokens int64, totalSeconds float64) float64 {
	if completed <= 0 {
		return 0
	}
	tokensPerTask := float64(totalTokens) / float64(completed)
	tokenScore := clamp01((500000-tokensPerTask)/450000) * 100

	secondsPerTask := totalSeconds / float64(completed)
	timeScore := clamp01((7200-secondsPerTask)/7000) * 100

	return (tokenScore + timeScore) / 2
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func fleetClassFromScore(score float64, sampleSufficient bool) string {
	return fleetClassFromRules(score, sampleSufficient, DefaultAgentHonorRules().FleetClasses)
}

func fleetClassFromRules(
	score float64,
	sampleSufficient bool,
	classes []AgentHonorClassThreshold,
) string {
	if !sampleSufficient {
		return "reserve"
	}
	sorted := append([]AgentHonorClassThreshold(nil), classes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Score > sorted[j].Score })
	for _, class := range sorted {
		if score >= class.Score {
			return class.ClassID
		}
	}
	return "reserve"
}

func fleetClassLabel(classID string) string {
	if label, ok := fleetClassLabels[classID]; ok {
		return label
	}
	return "Reserve"
}

func numericToFloat64(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

func float64ToNumeric(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(v, 'f', 2, 64))
	return n
}

type fleetComputedRow struct {
	agentID          pgtype.UUID
	sampleTasks      int32
	sampleSufficient bool
	pillars          FleetPillarScores
	score            float64
	classID          string
}

func (s *AgentFleetRankService) RecomputeWorkspace(ctx context.Context, workspaceID pgtype.UUID) error {
	_, err := s.RefreshWorkspace(ctx, workspaceID, "refresh")
	return err
}

func (s *AgentFleetRankService) RefreshWorkspace(
	ctx context.Context,
	workspaceID pgtype.UUID,
	triggerReason string,
) ([]AgentFleetRankChange, error) {
	if s == nil || s.Queries == nil {
		return nil, errors.New("agent fleet rank service unavailable")
	}
	lockValue, _ := s.workspaceLocks.LoadOrStore(util.UUIDToString(workspaceID), &sync.Mutex{})
	workspaceLock := lockValue.(*sync.Mutex)
	workspaceLock.Lock()
	defer workspaceLock.Unlock()

	rules, _, err := loadAgentHonorRules(ctx, s.Queries, workspaceID)
	if err != nil {
		return nil, err
	}
	window := int32(rules.FleetWindowDays)

	activeIDs, err := s.Queries.ListWorkspaceActiveAgentIDs(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	previousRows, err := s.Queries.ListAgentFleetSnapshots(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	previousByAgent := make(map[string]db.AgentFleetSnapshot, len(previousRows))
	for _, row := range previousRows {
		previousByAgent[util.UUIDToString(row.AgentID)] = row
	}

	deliveryRows, err := s.Queries.GetFleetDeliveryStats(ctx, db.GetFleetDeliveryStatsParams{WorkspaceID: workspaceID, WindowDays: window})
	if err != nil {
		return nil, err
	}
	evolutionFeedback, err := s.Queries.GetFleetEvolutionFeedbackStats(ctx, db.GetFleetEvolutionFeedbackStatsParams{WorkspaceID: workspaceID, WindowDays: window})
	if err != nil {
		return nil, err
	}
	evolutionPromos, err := s.Queries.GetFleetEvolutionPromotionStats(ctx, db.GetFleetEvolutionPromotionStatsParams{WorkspaceID: workspaceID, WindowDays: window})
	if err != nil {
		return nil, err
	}
	growthRows, err := s.Queries.GetFleetGrowthStats(ctx, db.GetFleetGrowthStatsParams{WorkspaceID: workspaceID, WindowDays: window})
	if err != nil {
		return nil, err
	}
	efficiencyRows, err := s.Queries.GetFleetEfficiencyStats(ctx, db.GetFleetEfficiencyStatsParams{WorkspaceID: workspaceID, WindowDays: window})
	if err != nil {
		return nil, err
	}

	deliveryByAgent := map[string]db.GetFleetDeliveryStatsRow{}
	for _, row := range deliveryRows {
		deliveryByAgent[util.UUIDToString(row.AgentID)] = row
	}
	evolutionByAgent := map[string]struct {
		success, total, promotions int64
	}{}
	for _, row := range evolutionFeedback {
		id := util.UUIDToString(row.AgentID)
		cur := evolutionByAgent[id]
		cur.success = row.SuccessCount
		cur.total = row.FeedbackTotal
		evolutionByAgent[id] = cur
	}
	for _, row := range evolutionPromos {
		id := util.UUIDToString(row.AgentID)
		cur := evolutionByAgent[id]
		cur.promotions = row.PromotionCount
		evolutionByAgent[id] = cur
	}
	growthByAgent := map[string]db.GetFleetGrowthStatsRow{}
	for _, row := range growthRows {
		growthByAgent[util.UUIDToString(row.AgentID)] = row
	}
	efficiencyByAgent := map[string]db.GetFleetEfficiencyStatsRow{}
	for _, row := range efficiencyRows {
		efficiencyByAgent[util.UUIDToString(row.AgentID)] = row
	}

	computed := make([]fleetComputedRow, 0, len(activeIDs))
	for _, agentID := range activeIDs {
		id := util.UUIDToString(agentID)
		d := deliveryByAgent[id]
		sampleTasks := int32(d.CompletedCount + d.FailedCount)
		sampleSufficient := int(sampleTasks) >= rules.FleetMinSampleTasks

		ev := evolutionByAgent[id]
		g := growthByAgent[id]
		ef := efficiencyByAgent[id]

		pillars := FleetPillarScores{
			Delivery:   deliveryPillar(d.CompletedCount, d.FailedCount) / 100,
			Evolution:  evolutionPillar(ev.success, ev.total, ev.promotions) / 100,
			Growth:     growthPillar(g.TotalWrites, g.Writes30d) / 100,
			Efficiency: efficiencyPillar(ef.CompletedCount, ef.TotalTokens, ef.TotalSeconds) / 100,
		}

		score := pillars.Delivery*rules.FleetWeights["delivery"] +
			pillars.Evolution*rules.FleetWeights["evolution"] +
			pillars.Growth*rules.FleetWeights["growth"] +
			pillars.Efficiency*rules.FleetWeights["efficiency"]
		score *= 100

		classID := fleetClassFromRules(score, sampleSufficient, rules.FleetClasses)
		computed = append(computed, fleetComputedRow{
			agentID:          agentID,
			sampleTasks:      sampleTasks,
			sampleSufficient: sampleSufficient,
			pillars:          pillars,
			score:            score,
			classID:          classID,
		})
	}

	sort.Slice(computed, func(i, j int) bool {
		if computed[i].score != computed[j].score {
			return computed[i].score > computed[j].score
		}
		return util.UUIDToString(computed[i].agentID) < util.UUIDToString(computed[j].agentID)
	})

	fleetSize := int32(len(computed))
	changes := make([]AgentFleetRankChange, 0, len(computed))
	for i, row := range computed {
		rank := int32(i + 1)
		params := db.UpsertAgentFleetSnapshotParams{
			WorkspaceID:      workspaceID,
			AgentID:          row.agentID,
			FleetScore:       float64ToNumeric(row.score),
			ClassID:          row.classID,
			FleetRank:        rank,
			FleetSize:        fleetSize,
			SampleTasks:      row.sampleTasks,
			PillarDelivery:   float64ToNumeric(row.pillars.Delivery),
			PillarEvolution:  float64ToNumeric(row.pillars.Evolution),
			PillarGrowth:     float64ToNumeric(row.pillars.Growth),
			PillarEfficiency: float64ToNumeric(row.pillars.Efficiency),
		}
		if err := s.Queries.UpsertAgentFleetSnapshot(ctx, params); err != nil {
			return nil, err
		}
		previous, existed := previousByAgent[util.UUIDToString(row.agentID)]
		changed := !existed ||
			previous.ClassID != row.classID ||
			previous.FleetRank != rank ||
			previous.FleetSize != fleetSize ||
			previous.SampleTasks != row.sampleTasks ||
			math.Abs(numericToFloat64(previous.FleetScore)-row.score) >= 0.01
		if !changed {
			continue
		}
		if _, err := s.Queries.InsertAgentFleetHistory(ctx, db.InsertAgentFleetHistoryParams{
			WorkspaceID:      workspaceID,
			AgentID:          row.agentID,
			FleetScore:       params.FleetScore,
			ClassID:          params.ClassID,
			FleetRank:        rank,
			FleetSize:        fleetSize,
			SampleTasks:      params.SampleTasks,
			PillarDelivery:   params.PillarDelivery,
			PillarEvolution:  params.PillarEvolution,
			PillarGrowth:     params.PillarGrowth,
			PillarEfficiency: params.PillarEfficiency,
			TriggerReason:    triggerReason,
		}); err != nil {
			return nil, err
		}
		changes = append(changes, AgentFleetRankChange{
			CurrentAgentID:  row.agentID,
			PreviousClassID: previous.ClassID,
			Current: AgentFleetRankView{
				AgentID: util.UUIDToString(row.agentID), FleetScore: row.score,
				ClassID: row.classID, ClassLabel: fleetClassLabel(row.classID),
				FleetRank: int(rank), FleetSize: int(fleetSize), SampleTasks: int(row.sampleTasks),
				MinSampleTasks:   rules.FleetMinSampleTasks,
				SampleSufficient: row.sampleSufficient, Pillars: row.pillars,
			},
		})
	}
	return changes, nil
}

func snapshotToView(row db.AgentFleetSnapshot, minSampleTasks int) AgentFleetRankView {
	sampleTasks := int(row.SampleTasks)
	return AgentFleetRankView{
		AgentID:          util.UUIDToString(row.AgentID),
		FleetScore:       numericToFloat64(row.FleetScore),
		ClassID:          row.ClassID,
		ClassLabel:       fleetClassLabel(row.ClassID),
		FleetRank:        int(row.FleetRank),
		FleetSize:        int(row.FleetSize),
		SampleTasks:      sampleTasks,
		MinSampleTasks:   minSampleTasks,
		SampleSufficient: sampleTasks >= minSampleTasks,
		Frozen:           row.Frozen,
		Pillars: FleetPillarScores{
			Delivery:   numericToFloat64(row.PillarDelivery),
			Evolution:  numericToFloat64(row.PillarEvolution),
			Growth:     numericToFloat64(row.PillarGrowth),
			Efficiency: numericToFloat64(row.PillarEfficiency),
		},
	}
}

func (s *AgentFleetRankService) ListRankings(ctx context.Context, workspaceID pgtype.UUID) ([]AgentFleetRankView, error) {
	rules, _, err := loadAgentHonorRules(ctx, s.Queries, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Queries.ListAgentFleetSnapshots(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]AgentFleetRankView, 0, len(rows))
	for _, row := range rows {
		out = append(out, snapshotToView(row, rules.FleetMinSampleTasks))
	}
	return out, nil
}

func (s *AgentFleetRankService) GetAgentRank(ctx context.Context, workspaceID, agentID pgtype.UUID) (AgentFleetRankView, error) {
	rules, _, err := loadAgentHonorRules(ctx, s.Queries, workspaceID)
	if err != nil {
		return AgentFleetRankView{}, err
	}
	row, err := s.Queries.GetAgentFleetSnapshot(ctx, db.GetAgentFleetSnapshotParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentFleetRankView{
				AgentID:        util.UUIDToString(agentID),
				ClassID:        "reserve",
				ClassLabel:     fleetClassLabel("reserve"),
				MinSampleTasks: rules.FleetMinSampleTasks,
				Pillars:        FleetPillarScores{},
			}, nil
		}
		return AgentFleetRankView{}, err
	}
	return snapshotToView(row, rules.FleetMinSampleTasks), nil
}

func (s *AgentFleetRankService) FreezeAgentOnArchive(ctx context.Context, workspaceID, agentID pgtype.UUID) error {
	if s == nil || s.Queries == nil {
		return nil
	}
	return s.Queries.FreezeAgentFleetSnapshot(ctx, db.FreezeAgentFleetSnapshotParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
}

// RefreshWorkspaceAfterArchiveAsync recomputes ranks outside the archive request
// path. Repeated archives for one workspace coalesce, but a refresh requested
// while one is running triggers one final pass so the snapshot converges.
func (s *AgentFleetRankService) RefreshWorkspaceAfterArchiveAsync(workspaceID pgtype.UUID) {
	if s == nil || s.Queries == nil {
		return
	}
	workspaceKey := util.UUIDToString(workspaceID)
	if workspaceKey == "" {
		return
	}

	s.archiveRefreshMu.Lock()
	if s.archiveRefreshQueued == nil {
		s.archiveRefreshQueued = make(map[string]bool)
		s.archiveRefreshPending = make(map[string]bool)
	}
	if s.archiveRefreshQueued[workspaceKey] {
		s.archiveRefreshPending[workspaceKey] = true
		s.archiveRefreshMu.Unlock()
		return
	}
	s.archiveRefreshQueued[workspaceKey] = true
	s.archiveRefreshMu.Unlock()

	go s.runArchiveRefresh(workspaceID, workspaceKey)
}

func (s *AgentFleetRankService) runArchiveRefresh(workspaceID pgtype.UUID, workspaceKey string) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		refresh := s.archiveRefresh
		var err error
		if refresh != nil {
			err = refresh(ctx, workspaceID)
		} else {
			_, err = s.RefreshWorkspace(ctx, workspaceID, "agent_archived")
		}
		cancel()
		if err != nil {
			slog.Warn("refresh agent fleet ranks after archive failed", "workspace_id", workspaceKey, "error", err)
		}

		s.archiveRefreshMu.Lock()
		if s.archiveRefreshPending[workspaceKey] {
			delete(s.archiveRefreshPending, workspaceKey)
			s.archiveRefreshMu.Unlock()
			continue
		}
		delete(s.archiveRefreshQueued, workspaceKey)
		s.archiveRefreshMu.Unlock()
		return
	}
}

func (s *AgentFleetRankService) RestoreAgent(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
) error {
	if s == nil || s.Queries == nil {
		return nil
	}
	if err := s.Queries.UnfreezeAgentFleetSnapshot(ctx, db.UnfreezeAgentFleetSnapshotParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	}); err != nil {
		return err
	}
	_, err := s.RefreshWorkspace(ctx, workspaceID, "agent_restored")
	return err
}
