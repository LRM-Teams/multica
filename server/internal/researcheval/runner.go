package researcheval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const ReportSchemaVersion = "research-eval-report-v1"

var ErrInvalidEvaluation = errors.New("invalid research evaluation")

type Runner struct {
	executor Executor
	graders  []Grader
}

func NewRunner(executor Executor, graders ...Grader) (*Runner, error) {
	if executor == nil || len(graders) == 0 {
		return nil, fmt.Errorf("%w: executor and graders are required", ErrInvalidEvaluation)
	}
	seen := map[string]struct{}{}
	for _, grader := range graders {
		if grader == nil || strings.TrimSpace(grader.Name()) == "" {
			return nil, fmt.Errorf("%w: grader name is required", ErrInvalidEvaluation)
		}
		if _, duplicate := seen[grader.Name()]; duplicate {
			return nil, fmt.Errorf("%w: duplicate grader %q", ErrInvalidEvaluation, grader.Name())
		}
		seen[grader.Name()] = struct{}{}
	}
	return &Runner{executor: executor, graders: append([]Grader(nil), graders...)}, nil
}

func (runner *Runner) Run(ctx context.Context, corpus Corpus, options RunOptions) (Report, error) {
	if runner == nil || runner.executor == nil {
		return Report{}, fmt.Errorf("%w: runner is not initialized", ErrInvalidEvaluation)
	}
	if err := ValidateCorpus(corpus); err != nil {
		return Report{}, err
	}
	normalized, err := normalizeRunOptions(options)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion, CorpusVersion: corpus.Version,
		Seeds: append([]int64(nil), normalized.Seeds...), Options: normalized, ByGrader: map[string]Aggregate{},
	}
	graderScores := map[string]float64{}
	graderPasses := map[string]int{}
	for _, evaluationCase := range corpus.Cases {
		for _, seed := range normalized.Seeds {
			if err = ctx.Err(); err != nil {
				return Report{}, err
			}
			trial := TrialResult{TaskID: evaluationCase.Task.ID, Seed: seed, Grades: map[string]Grade{}}
			artifact, executionErr := runner.executor.Execute(ctx, evaluationCase.SubjectInput(), seed)
			if executionErr == nil {
				executionErr = ValidateArtifact(evaluationCase, artifact)
			}
			if executionErr != nil {
				trial.ExecutionError = executionErr.Error()
				report.Trials = append(report.Trials, trial)
				continue
			}
			allPassed := true
			for _, grader := range runner.graders {
				grade, gradeErr := grader.Grade(ctx, evaluationCase, artifact)
				if gradeErr != nil {
					return Report{}, fmt.Errorf("grade task %q seed %d with %q: %w", evaluationCase.Task.ID, seed, grader.Name(), gradeErr)
				}
				if err = validateGrade(grade); err != nil {
					return Report{}, fmt.Errorf("grader %q: %w", grader.Name(), err)
				}
				trial.Grades[grader.Name()] = grade
				trial.Score += grade.Score
				graderScores[grader.Name()] += grade.Score
				if grade.Passed {
					graderPasses[grader.Name()]++
				} else {
					allPassed = false
				}
			}
			trial.Score /= float64(len(runner.graders))
			trial.Passed = allPassed && trial.Score >= normalized.MinimumScore
			report.Trials = append(report.Trials, trial)
		}
	}
	totalTrials := len(report.Trials)
	passedTrials := 0
	for _, trial := range report.Trials {
		report.Overall.MeanScore += trial.Score
		if trial.Passed {
			passedTrials++
		}
	}
	if totalTrials > 0 {
		report.Overall.MeanScore /= float64(totalTrials)
		report.Overall.PassRate = float64(passedTrials) / float64(totalTrials)
	}
	report.Overall.Trials = totalTrials
	for _, grader := range runner.graders {
		report.ByGrader[grader.Name()] = Aggregate{
			MeanScore: graderScores[grader.Name()] / float64(totalTrials),
			PassRate:  float64(graderPasses[grader.Name()]) / float64(totalTrials),
			Trials:    totalTrials,
		}
	}
	report.Passed = report.Overall.MeanScore >= normalized.MinimumScore && report.Overall.PassRate >= normalized.MinimumPassRate
	return report, nil
}

func ValidateArtifact(evaluationCase Case, artifact Artifact) error {
	documents := map[string]Document{}
	for _, document := range evaluationCase.Environment.Documents {
		documents[document.ID] = document
	}
	sources := map[string]ArtifactSource{}
	for _, source := range artifact.Sources {
		document, exists := documents[source.DocumentID]
		if !exists || strings.TrimSpace(source.DocumentID) == "" {
			return fmt.Errorf("%w: artifact source references unknown document %q", ErrInvalidEvaluation, source.DocumentID)
		}
		if source.Family != document.Family {
			return fmt.Errorf("%w: artifact source %q changed family", ErrInvalidEvaluation, source.DocumentID)
		}
		if _, duplicate := sources[source.DocumentID]; duplicate {
			return fmt.Errorf("%w: duplicate artifact source %q", ErrInvalidEvaluation, source.DocumentID)
		}
		sources[source.DocumentID] = source
	}
	facts := map[string]struct{}{}
	for _, fact := range artifact.Facts {
		if strings.TrimSpace(fact.Key) == "" || strings.TrimSpace(fact.Value) == "" {
			return fmt.Errorf("%w: artifact fact key and value are required", ErrInvalidEvaluation)
		}
		if _, duplicate := facts[fact.Key]; duplicate {
			return fmt.Errorf("%w: duplicate artifact fact %q", ErrInvalidEvaluation, fact.Key)
		}
		facts[fact.Key] = struct{}{}
		for _, sourceID := range fact.SourceIDs {
			if _, exists := sources[sourceID]; !exists {
				return fmt.Errorf("%w: fact %q references unknown artifact source %q", ErrInvalidEvaluation, fact.Key, sourceID)
			}
		}
	}
	claims := map[string]struct{}{}
	for _, claim := range artifact.Claims {
		if strings.TrimSpace(claim.Key) == "" {
			return fmt.Errorf("%w: artifact claim key is required", ErrInvalidEvaluation)
		}
		if _, duplicate := claims[claim.Key]; duplicate {
			return fmt.Errorf("%w: duplicate artifact claim %q", ErrInvalidEvaluation, claim.Key)
		}
		claims[claim.Key] = struct{}{}
		for _, factKey := range claim.FactKeys {
			if _, exists := facts[factKey]; !exists {
				return fmt.Errorf("%w: claim %q references unknown fact %q", ErrInvalidEvaluation, claim.Key, factKey)
			}
		}
		for _, sourceID := range claim.SourceIDs {
			if _, exists := sources[sourceID]; !exists {
				return fmt.Errorf("%w: claim %q references unknown source %q", ErrInvalidEvaluation, claim.Key, sourceID)
			}
		}
	}
	conflicts := map[string]struct{}{}
	for _, conflict := range artifact.Conflicts {
		if strings.TrimSpace(conflict.Key) == "" || strings.TrimSpace(conflict.Type) == "" {
			return fmt.Errorf("%w: conflict key and type are required", ErrInvalidEvaluation)
		}
		if _, duplicate := conflicts[conflict.Key]; duplicate {
			return fmt.Errorf("%w: duplicate artifact conflict %q", ErrInvalidEvaluation, conflict.Key)
		}
		conflicts[conflict.Key] = struct{}{}
		for _, factKey := range conflict.FactKeys {
			if _, exists := facts[factKey]; !exists {
				return fmt.Errorf("%w: conflict %q references unknown fact %q", ErrInvalidEvaluation, conflict.Key, factKey)
			}
		}
	}
	return nil
}

func CompareReports(baseline, candidate Report) Comparison {
	comparison := Comparison{
		BaselineCorpusVersion: baseline.CorpusVersion, CandidateCorpusVersion: candidate.CorpusVersion,
		OverallScoreDelta:    candidate.Overall.MeanScore - baseline.Overall.MeanScore,
		OverallPassRateDelta: candidate.Overall.PassRate - baseline.Overall.PassRate,
		GraderScoreDelta:     map[string]float64{}, NonRegressing: true,
	}
	if baseline.CorpusVersion != candidate.CorpusVersion {
		comparison.IncomparableReasons = append(comparison.IncomparableReasons, "corpus_version")
		comparison.NonRegressing = false
	}
	if baseline.SchemaVersion != candidate.SchemaVersion {
		comparison.IncomparableReasons = append(comparison.IncomparableReasons, "report_schema")
		comparison.NonRegressing = false
	}
	if !equalSeeds(baseline.Seeds, candidate.Seeds) {
		comparison.IncomparableReasons = append(comparison.IncomparableReasons, "seeds")
		comparison.NonRegressing = false
	}
	if baseline.Overall.Trials != candidate.Overall.Trials {
		comparison.IncomparableReasons = append(comparison.IncomparableReasons, "trial_count")
		comparison.NonRegressing = false
	}
	if baseline.Options.MinimumScore != candidate.Options.MinimumScore || baseline.Options.MinimumPassRate != candidate.Options.MinimumPassRate {
		comparison.IncomparableReasons = append(comparison.IncomparableReasons, "thresholds")
		comparison.NonRegressing = false
	}
	for name, aggregate := range baseline.ByGrader {
		candidateAggregate, exists := candidate.ByGrader[name]
		if !exists {
			comparison.MissingGraders = append(comparison.MissingGraders, name)
			comparison.NonRegressing = false
			continue
		}
		delta := candidateAggregate.MeanScore - aggregate.MeanScore
		comparison.GraderScoreDelta[name] = delta
		if delta < 0 || candidateAggregate.PassRate < aggregate.PassRate {
			comparison.NonRegressing = false
		}
	}
	if comparison.OverallScoreDelta < 0 || comparison.OverallPassRateDelta < 0 || !candidate.Passed {
		comparison.NonRegressing = false
	}
	sort.Strings(comparison.MissingGraders)
	sort.Strings(comparison.IncomparableReasons)
	return comparison
}

func equalSeeds(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func normalizeRunOptions(options RunOptions) (RunOptions, error) {
	if len(options.Seeds) == 0 {
		options.Seeds = []int64{1}
	} else {
		options.Seeds = append([]int64(nil), options.Seeds...)
	}
	if options.MinimumScore == 0 {
		options.MinimumScore = 1
	}
	if options.MinimumPassRate == 0 {
		options.MinimumPassRate = 1
	}
	if invalidUnit(options.MinimumScore) || invalidUnit(options.MinimumPassRate) {
		return RunOptions{}, fmt.Errorf("%w: score and pass-rate thresholds must be in (0,1]", ErrInvalidEvaluation)
	}
	seen := map[int64]struct{}{}
	for _, seed := range options.Seeds {
		if _, duplicate := seen[seed]; duplicate {
			return RunOptions{}, fmt.Errorf("%w: duplicate seed %d", ErrInvalidEvaluation, seed)
		}
		seen[seed] = struct{}{}
	}
	sort.Slice(options.Seeds, func(i, j int) bool { return options.Seeds[i] < options.Seeds[j] })
	return options, nil
}

func validateGrade(grade Grade) error {
	if math.IsNaN(grade.Score) || math.IsInf(grade.Score, 0) || grade.Score < 0 || grade.Score > 1 {
		return fmt.Errorf("%w: grader score must be in [0,1]", ErrInvalidEvaluation)
	}
	return nil
}

func invalidUnit(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 1
}
