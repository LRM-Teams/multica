package service

import (
	"context"
	"errors"
	"math"
	"sort"

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
	SampleSufficient bool              `json:"sample_sufficient"`
	Frozen           bool              `json:"frozen"`
	Pillars          FleetPillarScores `json:"pillars"`
}

type FleetClassThreshold struct {
	ClassID   string  `json:"class_id"`
	MinScore  float64 `json:"min_score"`
	Label     string  `json:"label"`
	SvgKey    string  `json:"svg_key"`
}

type FleetRulesDocument struct {
	Version         string              `json:"version"`
	WindowDays      int                 `json:"window_days"`
	MinSampleTasks  int                 `json:"min_sample_tasks"`
	PillarWeights   map[string]float64  `json:"pillar_weights"`
	ClassThresholds []FleetClassThreshold `json:"class_thresholds"`
	Changelog       []string            `json:"changelog"`
}

type AgentFleetRankService struct {
	Queries *db.Queries
}

func NewAgentFleetRankService(queries *db.Queries) *AgentFleetRankService {
	return &AgentFleetRankService{Queries: queries}
}

func BuildFleetRulesDocument() FleetRulesDocument {
	return FleetRulesDocument{
		Version:        FleetRulesVersion,
		WindowDays:     FleetWindowDays,
		MinSampleTasks: FleetMinSampleTasks,
		PillarWeights: map[string]float64{
			"delivery":   fleetWeightDelivery,
			"evolution":  fleetWeightEvolution,
			"growth":     fleetWeightGrowth,
			"efficiency": fleetWeightEfficiency,
		},
		ClassThresholds: []FleetClassThreshold{
			{ClassID: "dreadnought", MinScore: 85, Label: "Dreadnought", SvgKey: "dreadnought"},
			{ClassID: "battleship", MinScore: 70, Label: "Battleship", SvgKey: "battleship"},
			{ClassID: "cruiser", MinScore: 55, Label: "Cruiser", SvgKey: "cruiser"},
			{ClassID: "frigate", MinScore: 40, Label: "Frigate", SvgKey: "frigate"},
			{ClassID: "corvette", MinScore: 25, Label: "Corvette", SvgKey: "corvette"},
			{ClassID: "reserve", MinScore: 0, Label: "Reserve", SvgKey: "reserve"},
		},
		Changelog: []string{
			"2026-07-30: Initial fleet rank — 30d window, 4 pillars, archive freeze.",
		},
	}
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
	tokenScore := clamp01((500000 - tokensPerTask) / 450000) * 100

	secondsPerTask := totalSeconds / float64(completed)
	timeScore := clamp01((7200 - secondsPerTask) / 7000) * 100

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
	if !sampleSufficient {
		return "reserve"
	}
	switch {
	case score >= 85:
		return "dreadnought"
	case score >= 70:
		return "battleship"
	case score >= 55:
		return "cruiser"
	case score >= 40:
		return "frigate"
	case score >= 25:
		return "corvette"
	default:
		return "reserve"
	}
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
	_ = n.Scan(v)
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
	if s == nil || s.Queries == nil {
		return errors.New("agent fleet rank service unavailable")
	}

	window := int32(FleetWindowDays)
	params := db.GetFleetDeliveryStatsParams{WorkspaceID: workspaceID, WindowDays: window}

	activeIDs, err := s.Queries.ListWorkspaceActiveAgentIDs(ctx, workspaceID)
	if err != nil {
		return err
	}

	deliveryRows, err := s.Queries.GetFleetDeliveryStats(ctx, params)
	if err != nil {
		return err
	}
	evolutionFeedback, err := s.Queries.GetFleetEvolutionFeedbackStats(ctx, params)
	if err != nil {
		return err
	}
	evolutionPromos, err := s.Queries.GetFleetEvolutionPromotionStats(ctx, params)
	if err != nil {
		return err
	}
	growthRows, err := s.Queries.GetFleetGrowthStats(ctx, params)
	if err != nil {
		return err
	}
	efficiencyRows, err := s.Queries.GetFleetEfficiencyStats(ctx, params)
	if err != nil {
		return err
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
		sampleSufficient := int64(sampleTasks) >= FleetMinSampleTasks

		ev := evolutionByAgent[id]
		g := growthByAgent[id]
		ef := efficiencyByAgent[id]

		pillars := FleetPillarScores{
			Delivery:   deliveryPillar(d.CompletedCount, d.FailedCount) / 100,
			Evolution:  evolutionPillar(ev.success, ev.total, ev.promotions) / 100,
			Growth:     growthPillar(g.TotalWrites, g.Writes30d) / 100,
			Efficiency: efficiencyPillar(ef.CompletedCount, ef.TotalTokens, ef.TotalSeconds) / 100,
		}

		score := pillars.Delivery*fleetWeightDelivery +
			pillars.Evolution*fleetWeightEvolution +
			pillars.Growth*fleetWeightGrowth +
			pillars.Efficiency*fleetWeightEfficiency
		score *= 100

		classID := fleetClassFromScore(score, sampleSufficient)
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
	for i, row := range computed {
		rank := int32(i + 1)
		if err := s.Queries.UpsertAgentFleetSnapshot(ctx, db.UpsertAgentFleetSnapshotParams{
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
		}); err != nil {
			return err
		}
	}
	return nil
}

func snapshotToView(row db.AgentFleetSnapshot) AgentFleetRankView {
	sampleTasks := int(row.SampleTasks)
	return AgentFleetRankView{
		AgentID:          util.UUIDToString(row.AgentID),
		FleetScore:       numericToFloat64(row.FleetScore),
		ClassID:          row.ClassID,
		ClassLabel:       fleetClassLabel(row.ClassID),
		FleetRank:        int(row.FleetRank),
		FleetSize:        int(row.FleetSize),
		SampleTasks:      sampleTasks,
		SampleSufficient: sampleTasks >= FleetMinSampleTasks,
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
	if err := s.RecomputeWorkspace(ctx, workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.Queries.ListAgentFleetSnapshots(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]AgentFleetRankView, 0, len(rows))
	for _, row := range rows {
		out = append(out, snapshotToView(row))
	}
	return out, nil
}

func (s *AgentFleetRankService) GetAgentRank(ctx context.Context, workspaceID, agentID pgtype.UUID) (AgentFleetRankView, error) {
	if err := s.RecomputeWorkspace(ctx, workspaceID); err != nil {
		return AgentFleetRankView{}, err
	}
	row, err := s.Queries.GetAgentFleetSnapshot(ctx, db.GetAgentFleetSnapshotParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentFleetRankView{
				AgentID:    util.UUIDToString(agentID),
				ClassID:    "reserve",
				ClassLabel: fleetClassLabel("reserve"),
				Pillars:    FleetPillarScores{},
			}, nil
		}
		return AgentFleetRankView{}, err
	}
	return snapshotToView(row), nil
}

func (s *AgentFleetRankService) FreezeAgentOnArchive(ctx context.Context, workspaceID, agentID pgtype.UUID) error {
	if s == nil || s.Queries == nil {
		return nil
	}
	if err := s.RecomputeWorkspace(ctx, workspaceID); err != nil {
		return err
	}
	if err := s.Queries.FreezeAgentFleetSnapshot(ctx, db.FreezeAgentFleetSnapshotParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	}); err != nil {
		return err
	}
	return s.RecomputeWorkspace(ctx, workspaceID)
}
