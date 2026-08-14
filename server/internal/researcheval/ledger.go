package researcheval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrEvaluationConflict = errors.New("research evaluation ledger conflict")

type EvaluationRunCreateInput struct {
	WorkspaceID, ClientRequestID, CorpusVersion, StrategyVersion, BaselineStrategyVersion string
	Seeds                                                                                 []int64
	Environment                                                                           json.RawMessage
}

type EvaluationRunRecord struct {
	ID, WorkspaceID, CorpusVersion, StrategyVersion, BaselineStrategyVersion string
	Seeds                                                                    []int64
	EnvironmentHash, Status                                                  string
	Replayed                                                                 bool
}

type PostgresLedger struct{ pool *pgxpool.Pool }

func NewPostgresLedger(pool *pgxpool.Pool) *PostgresLedger { return &PostgresLedger{pool: pool} }

func (ledger *PostgresLedger) CreateRun(ctx context.Context, input EvaluationRunCreateInput) (EvaluationRunRecord, error) {
	if ledger == nil || ledger.pool == nil || strings.TrimSpace(input.WorkspaceID) == "" ||
		strings.TrimSpace(input.ClientRequestID) == "" || len(input.ClientRequestID) > 512 ||
		strings.TrimSpace(input.CorpusVersion) == "" || strings.TrimSpace(input.StrategyVersion) == "" {
		return EvaluationRunRecord{}, fmt.Errorf("%w: evaluation run identity is incomplete", ErrInvalidEvaluation)
	}
	seeds, err := canonicalEvaluationSeeds(input.Seeds)
	if err != nil {
		return EvaluationRunRecord{}, err
	}
	environment, err := canonicalEvaluationObject(input.Environment)
	if err != nil {
		return EvaluationRunRecord{}, fmt.Errorf("%w: invalid evaluation environment: %v", ErrInvalidEvaluation, err)
	}
	environmentHash, err := canonicalEvaluationHash(json.RawMessage(environment))
	if err != nil {
		return EvaluationRunRecord{}, err
	}
	requestHash, err := canonicalEvaluationHash(struct {
		WorkspaceID, CorpusVersion, StrategyVersion, BaselineStrategyVersion, EnvironmentHash string
		Seeds                                                                                 []int64
	}{input.WorkspaceID, input.CorpusVersion, input.StrategyVersion, input.BaselineStrategyVersion, environmentHash, seeds})
	if err != nil {
		return EvaluationRunRecord{}, err
	}
	tx, err := ledger.pool.Begin(ctx)
	if err != nil {
		return EvaluationRunRecord{}, err
	}
	defer tx.Rollback(ctx)
	var record EvaluationRunRecord
	err = scanEvaluationRun(tx.QueryRow(ctx, `
		INSERT INTO research_evaluation_run (
		  workspace_id,client_request_id,request_hash,corpus_version,strategy_version,
		  baseline_strategy_version,seeds,environment,environment_hash
		) VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
		ON CONFLICT (workspace_id,client_request_id) DO NOTHING
		RETURNING id::text,workspace_id::text,corpus_version,strategy_version,
		          baseline_strategy_version,seeds,environment_hash,status
	`, input.WorkspaceID, input.ClientRequestID, requestHash, input.CorpusVersion,
		input.StrategyVersion, input.BaselineStrategyVersion, seeds, environment, environmentHash), &record)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash string
		err = tx.QueryRow(ctx, `
			SELECT id::text,workspace_id::text,corpus_version,strategy_version,
			       baseline_strategy_version,seeds,environment_hash,status,request_hash
			FROM research_evaluation_run
			WHERE workspace_id=$1::uuid AND client_request_id=$2 FOR UPDATE
		`, input.WorkspaceID, input.ClientRequestID).Scan(
			&record.ID, &record.WorkspaceID, &record.CorpusVersion, &record.StrategyVersion,
			&record.BaselineStrategyVersion, &record.Seeds, &record.EnvironmentHash, &record.Status, &storedHash)
		if err == nil && storedHash != requestHash {
			return EvaluationRunRecord{}, fmt.Errorf("%w: client request payload changed", ErrEvaluationConflict)
		}
		record.Replayed = err == nil
	}
	if err != nil {
		return EvaluationRunRecord{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EvaluationRunRecord{}, err
	}
	return record, nil
}

type rowScanner interface{ Scan(...any) error }

func scanEvaluationRun(row rowScanner, record *EvaluationRunRecord) error {
	return row.Scan(&record.ID, &record.WorkspaceID, &record.CorpusVersion, &record.StrategyVersion,
		&record.BaselineStrategyVersion, &record.Seeds, &record.EnvironmentHash, &record.Status)
}

func (ledger *PostgresLedger) StartRun(ctx context.Context, workspaceID, runID string) error {
	if ledger == nil || ledger.pool == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(runID) == "" {
		return fmt.Errorf("%w: evaluation run identity is incomplete", ErrInvalidEvaluation)
	}
	tag, err := ledger.pool.Exec(ctx, `
		UPDATE research_evaluation_run SET status='running',started_at=now(),updated_at=now()
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND status='pending'
	`, workspaceID, runID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: evaluation run cannot start", ErrEvaluationConflict)
	}
	return nil
}

func (ledger *PostgresLedger) CompleteRun(ctx context.Context, workspaceID, runID string, report Report, comparison *Comparison) (bool, error) {
	if ledger == nil || ledger.pool == nil {
		return false, fmt.Errorf("%w: evaluation ledger pool is required", ErrInvalidEvaluation)
	}
	if err := validateDurableReport(report); err != nil {
		return false, err
	}
	if err := validateDurableComparison(report, comparison); err != nil {
		return false, err
	}
	reportBytes, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	var comparisonBytes []byte
	if comparison != nil {
		comparisonBytes, err = json.Marshal(comparison)
		if err != nil {
			return false, err
		}
	}
	reportHash, err := canonicalEvaluationHash(struct {
		Report     json.RawMessage `json:"report"`
		Comparison json.RawMessage `json:"comparison,omitempty"`
	}{Report: reportBytes, Comparison: comparisonBytes})
	if err != nil {
		return false, err
	}
	tx, err := ledger.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var status, corpusVersion string
	var seeds []int64
	if err = tx.QueryRow(ctx, `SELECT status,corpus_version,seeds FROM research_evaluation_run
		WHERE workspace_id=$1::uuid AND id=$2::uuid FOR UPDATE`, workspaceID, runID).Scan(&status, &corpusVersion, &seeds); err != nil {
		return false, err
	}
	if status == "completed" {
		var storedHash string
		if err = tx.QueryRow(ctx, `SELECT report_hash FROM research_evaluation_report
			WHERE workspace_id=$1::uuid AND evaluation_run_id=$2::uuid`, workspaceID, runID).Scan(&storedHash); err != nil {
			return false, err
		}
		if storedHash != reportHash {
			return false, fmt.Errorf("%w: completed evaluation report changed", ErrEvaluationConflict)
		}
		return true, nil
	}
	if status != "running" || corpusVersion != report.CorpusVersion || !equalSeeds(seeds, report.Seeds) {
		return false, fmt.Errorf("%w: report does not match running evaluation", ErrEvaluationConflict)
	}
	for _, trial := range report.Trials {
		var artifactBytes []byte
		if trial.Artifact != nil {
			artifactBytes, err = json.Marshal(trial.Artifact)
			if err != nil {
				return false, fmt.Errorf("%w: encode trial artifact", ErrInvalidEvaluation)
			}
		}
		var trialID string
		if err = tx.QueryRow(ctx, `INSERT INTO research_evaluation_trial (
			workspace_id,evaluation_run_id,task_id,seed,execution_error,score,passed,artifact,artifact_hash
		) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8::jsonb,$9) RETURNING id::text`,
			workspaceID, runID, trial.TaskID, trial.Seed, trial.ExecutionError, trial.Score, trial.Passed,
			artifactBytes, trial.ArtifactHash).Scan(&trialID); err != nil {
			return false, err
		}
		graderNames := make([]string, 0, len(trial.Grades))
		for name := range trial.Grades {
			graderNames = append(graderNames, name)
		}
		sort.Strings(graderNames)
		for _, name := range graderNames {
			grade := trial.Grades[name]
			metricsValue := grade.Metrics
			if metricsValue == nil {
				metricsValue = map[string]float64{}
			}
			findingsValue := grade.Findings
			if findingsValue == nil {
				findingsValue = []Finding{}
			}
			metrics, metricsErr := json.Marshal(metricsValue)
			findings, findingsErr := json.Marshal(findingsValue)
			if metricsErr != nil || findingsErr != nil {
				return false, fmt.Errorf("%w: encode grader evidence", ErrInvalidEvaluation)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO research_evaluation_grade (
				workspace_id,evaluation_run_id,trial_id,grader_name,score,passed,metrics,findings
			) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::jsonb,$8::jsonb)`,
				workspaceID, runID, trialID, name, grade.Score, grade.Passed, metrics, findings); err != nil {
				return false, err
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_evaluation_report (
		workspace_id,evaluation_run_id,report_hash,report,comparison,passed
	) VALUES ($1::uuid,$2::uuid,$3,$4::jsonb,$5::jsonb,$6)`,
		workspaceID, runID, reportHash, reportBytes, comparisonBytes, report.Passed); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE research_evaluation_run
		SET status='completed',completed_at=now(),updated_at=now()
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND status='running'`, workspaceID, runID)
	if err != nil || tag.RowsAffected() != 1 {
		return false, fmt.Errorf("%w: evaluation completion CAS failed: %v", ErrEvaluationConflict, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func validateDurableReport(report Report) error {
	if report.SchemaVersion != ReportSchemaVersion || strings.TrimSpace(report.CorpusVersion) == "" ||
		len(report.Seeds) == 0 || len(report.Trials) == 0 || len(report.ByGrader) == 0 {
		return fmt.Errorf("%w: incomplete durable evaluation report", ErrInvalidEvaluation)
	}
	seeds, err := canonicalEvaluationSeeds(report.Seeds)
	if err != nil || !equalSeeds(seeds, report.Seeds) || !equalSeeds(report.Options.Seeds, report.Seeds) ||
		invalidUnit(report.Options.MinimumScore) || invalidUnit(report.Options.MinimumPassRate) {
		return fmt.Errorf("%w: report options or canonical seeds are invalid", ErrInvalidEvaluation)
	}
	seedSet := make(map[int64]struct{}, len(seeds))
	for _, seed := range seeds {
		seedSet[seed] = struct{}{}
	}
	graderNames := make(map[string]struct{}, len(report.ByGrader))
	for name, aggregate := range report.ByGrader {
		if strings.TrimSpace(name) == "" || invalidAggregate(aggregate, len(report.Trials)) {
			return fmt.Errorf("%w: grader aggregate %q is invalid", ErrInvalidEvaluation, name)
		}
		graderNames[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(report.Trials))
	graderScores := make(map[string]float64, len(graderNames))
	graderPasses := make(map[string]int, len(graderNames))
	var overallScore float64
	overallPasses := 0
	for _, trial := range report.Trials {
		key := fmt.Sprintf("%s\x00%d", trial.TaskID, trial.Seed)
		if strings.TrimSpace(trial.TaskID) == "" {
			return fmt.Errorf("%w: trial task ID is required", ErrInvalidEvaluation)
		}
		if _, exists := seedSet[trial.Seed]; !exists || invalidScore(trial.Score) {
			return fmt.Errorf("%w: trial seed or score is invalid", ErrInvalidEvaluation)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate trial task/seed", ErrInvalidEvaluation)
		}
		seen[key] = struct{}{}
		if (trial.ExecutionError == "") != (trial.Artifact != nil) ||
			(trial.ExecutionError == "") != (trial.ArtifactHash != "") ||
			(trial.ArtifactHash != "" && !validEvaluationHash(trial.ArtifactHash)) {
			return fmt.Errorf("%w: trial artifact/error identity is invalid", ErrInvalidEvaluation)
		}
		if trial.Artifact != nil {
			artifactHash, hashErr := canonicalEvaluationHash(*trial.Artifact)
			if hashErr != nil || artifactHash != trial.ArtifactHash {
				return fmt.Errorf("%w: trial artifact hash does not match content", ErrInvalidEvaluation)
			}
		}
		if trial.ExecutionError == "" && len(trial.Grades) == 0 {
			return fmt.Errorf("%w: successful trial requires grades", ErrInvalidEvaluation)
		}
		if trial.ExecutionError != "" && (trial.Artifact != nil || len(trial.Grades) != 0 || trial.Score != 0 || trial.Passed) {
			return fmt.Errorf("%w: failed trial contains successful evidence", ErrInvalidEvaluation)
		}
		if trial.ExecutionError == "" && len(trial.Grades) != len(graderNames) {
			return fmt.Errorf("%w: successful trial grader set is incomplete", ErrInvalidEvaluation)
		}
		trialScore := 0.0
		allPassed := true
		for name, grade := range trial.Grades {
			if _, exists := graderNames[name]; !exists {
				return fmt.Errorf("%w: trial contains unknown grader %q", ErrInvalidEvaluation, name)
			}
			if err := validateGrade(grade); err != nil {
				return err
			}
			trialScore += grade.Score
			graderScores[name] += grade.Score
			if grade.Passed {
				graderPasses[name]++
			} else {
				allPassed = false
			}
		}
		if trial.ExecutionError == "" {
			trialScore /= float64(len(graderNames))
			wantPassed := allPassed && trialScore >= report.Options.MinimumScore
			if !sameScore(trial.Score, trialScore) || trial.Passed != wantPassed {
				return fmt.Errorf("%w: trial aggregate is inconsistent", ErrInvalidEvaluation)
			}
		}
		overallScore += trial.Score
		if trial.Passed {
			overallPasses++
		}
	}
	total := len(report.Trials)
	wantOverall := Aggregate{
		MeanScore: overallScore / float64(total),
		PassRate:  float64(overallPasses) / float64(total),
		Trials:    total,
	}
	if !sameAggregate(report.Overall, wantOverall) {
		return fmt.Errorf("%w: overall aggregate is inconsistent", ErrInvalidEvaluation)
	}
	for name, aggregate := range report.ByGrader {
		want := Aggregate{
			MeanScore: graderScores[name] / float64(total),
			PassRate:  float64(graderPasses[name]) / float64(total),
			Trials:    total,
		}
		if !sameAggregate(aggregate, want) {
			return fmt.Errorf("%w: grader aggregate %q is inconsistent", ErrInvalidEvaluation, name)
		}
	}
	wantPassed := wantOverall.MeanScore >= report.Options.MinimumScore && wantOverall.PassRate >= report.Options.MinimumPassRate
	if report.Passed != wantPassed {
		return fmt.Errorf("%w: report pass decision is inconsistent", ErrInvalidEvaluation)
	}
	return nil
}

func validateDurableComparison(report Report, comparison *Comparison) error {
	if comparison == nil {
		return nil
	}
	if strings.TrimSpace(comparison.BaselineCorpusVersion) == "" ||
		comparison.CandidateCorpusVersion != report.CorpusVersion ||
		invalidDelta(comparison.OverallScoreDelta) || invalidDelta(comparison.OverallPassRateDelta) {
		return fmt.Errorf("%w: comparison identity or delta is invalid", ErrInvalidEvaluation)
	}
	for name, delta := range comparison.GraderScoreDelta {
		if strings.TrimSpace(name) == "" || invalidDelta(delta) {
			return fmt.Errorf("%w: comparison grader delta is invalid", ErrInvalidEvaluation)
		}
	}
	return nil
}

func invalidScore(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1
}

func invalidDelta(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0) || value < -1 || value > 1
}

func invalidAggregate(aggregate Aggregate, trials int) bool {
	return invalidScore(aggregate.MeanScore) || invalidScore(aggregate.PassRate) || aggregate.Trials != trials
}

func sameScore(left, right float64) bool { return math.Abs(left-right) <= 1e-12 }

func sameAggregate(left, right Aggregate) bool {
	return left.Trials == right.Trials && sameScore(left.MeanScore, right.MeanScore) && sameScore(left.PassRate, right.PassRate)
}

func canonicalEvaluationSeeds(seeds []int64) ([]int64, error) {
	if len(seeds) == 0 {
		return nil, fmt.Errorf("%w: evaluation seeds are required", ErrInvalidEvaluation)
	}
	out := append([]int64(nil), seeds...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			return nil, fmt.Errorf("%w: duplicate evaluation seed %d", ErrInvalidEvaluation, out[i])
		}
	}
	return out, nil
}

func canonicalEvaluationObject(raw json.RawMessage) ([]byte, error) {
	var value map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, errors.New("environment must be a JSON object")
	}
	return json.Marshal(value)
}

func canonicalEvaluationHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validEvaluationHash(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
