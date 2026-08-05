package researcheval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var ErrInvalidCorpus = errors.New("invalid research evaluation corpus")

func LoadCorpus(path string) (Corpus, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Corpus{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var corpus Corpus
	if err = decoder.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("%w: decode: %v", ErrInvalidCorpus, err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Corpus{}, fmt.Errorf("%w: trailing JSON", ErrInvalidCorpus)
	}
	if err = ValidateCorpus(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

func ValidateCorpus(corpus Corpus) error {
	if corpus.SchemaVersion != CorpusSchemaVersion {
		return fmt.Errorf("%w: schema_version=%q", ErrInvalidCorpus, corpus.SchemaVersion)
	}
	if strings.TrimSpace(corpus.Version) == "" || len(corpus.Cases) == 0 {
		return fmt.Errorf("%w: version and cases are required", ErrInvalidCorpus)
	}
	modes := make(map[ResearchMode]struct{}, len(AllResearchModes))
	for _, mode := range AllResearchModes {
		modes[mode] = struct{}{}
	}
	caseIDs := map[string]struct{}{}
	for index, evaluationCase := range corpus.Cases {
		prefix := fmt.Sprintf("case[%d]", index)
		if err := validateCase(evaluationCase, modes); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidCorpus, prefix, err)
		}
		if _, duplicate := caseIDs[evaluationCase.Task.ID]; duplicate {
			return fmt.Errorf("%w: duplicate task ID %q", ErrInvalidCorpus, evaluationCase.Task.ID)
		}
		caseIDs[evaluationCase.Task.ID] = struct{}{}
	}
	return nil
}

func validateCase(evaluationCase Case, modes map[ResearchMode]struct{}) error {
	task := evaluationCase.Task
	if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Goal) == "" || strings.TrimSpace(task.Language) == "" {
		return errors.New("task id, goal, and language are required")
	}
	if _, valid := modes[task.Mode]; !valid {
		return fmt.Errorf("unsupported mode %q", task.Mode)
	}
	if len(task.AllowedTools) == 0 {
		return errors.New("at least one allowed tool is required")
	}
	if err := requireUniqueNonEmpty("allowed tool", task.AllowedTools); err != nil {
		return err
	}
	if err := requireUniqueNonEmpty("tag", task.Tags); err != nil {
		return err
	}
	documents := map[string]Document{}
	families := map[string]struct{}{}
	for _, document := range evaluationCase.Environment.Documents {
		if strings.TrimSpace(document.ID) == "" || strings.TrimSpace(document.Family) == "" ||
			strings.TrimSpace(document.Title) == "" || strings.TrimSpace(document.Content) == "" {
			return errors.New("document id, family, title, and content are required")
		}
		if _, duplicate := documents[document.ID]; duplicate {
			return fmt.Errorf("duplicate document ID %q", document.ID)
		}
		documents[document.ID] = document
		families[document.Family] = struct{}{}
	}
	if len(documents) == 0 {
		return errors.New("environment documents are required")
	}
	for _, fault := range evaluationCase.Environment.Faults {
		if strings.TrimSpace(fault.Kind) == "" {
			return errors.New("fault kind is required")
		}
		if fault.TargetID != "" {
			if _, exists := documents[fault.TargetID]; !exists {
				return fmt.Errorf("fault references unknown document %q", fault.TargetID)
			}
		}
	}
	oracle := evaluationCase.Oracle
	if len(oracle.RequiredFacts)+len(oracle.ForbiddenFactKeys)+len(oracle.RequiredConflicts)+len(oracle.RequiredReportClaims)+autonomyCriteria(oracle.Autonomy) == 0 {
		return errors.New("oracle has no scored outcomes")
	}
	factKeys := map[string]struct{}{}
	requiredSources := map[string]struct{}{}
	for _, fact := range oracle.RequiredFacts {
		if strings.TrimSpace(fact.Key) == "" || strings.TrimSpace(fact.Value) == "" || len(fact.RequiredSourceIDs) == 0 {
			return errors.New("required fact key, value, and source IDs are required")
		}
		if _, duplicate := factKeys[fact.Key]; duplicate {
			return fmt.Errorf("duplicate required fact %q", fact.Key)
		}
		factKeys[fact.Key] = struct{}{}
		if err := requireDocumentRefs("required fact "+fact.Key, fact.RequiredSourceIDs, documents); err != nil {
			return err
		}
		for _, sourceID := range fact.RequiredSourceIDs {
			requiredSources[sourceID] = struct{}{}
		}
	}
	if err := requireUniqueNonEmpty("forbidden fact key", oracle.ForbiddenFactKeys); err != nil {
		return err
	}
	for _, key := range oracle.ForbiddenFactKeys {
		if _, required := factKeys[key]; required {
			return fmt.Errorf("fact %q is both required and forbidden", key)
		}
	}
	if err := requireDocumentRefs("forbidden document", oracle.ForbiddenDocumentIDs, documents); err != nil {
		return err
	}
	for _, id := range oracle.ForbiddenDocumentIDs {
		if _, required := requiredSources[id]; required {
			return fmt.Errorf("document %q is both required and forbidden", id)
		}
	}
	conflicts := map[string]struct{}{}
	for _, conflict := range oracle.RequiredConflicts {
		if strings.TrimSpace(conflict.Key) == "" || strings.TrimSpace(conflict.Type) == "" {
			return errors.New("required conflict key and type are required")
		}
		if _, duplicate := conflicts[conflict.Key]; duplicate {
			return fmt.Errorf("duplicate required conflict %q", conflict.Key)
		}
		conflicts[conflict.Key] = struct{}{}
	}
	for family, maximum := range oracle.MaxAcceptedPerFamily {
		if _, exists := families[family]; !exists || maximum < 0 {
			return fmt.Errorf("invalid accepted-source family limit %q=%d", family, maximum)
		}
		required := 0
		for sourceID := range requiredSources {
			if documents[sourceID].Family == family {
				required++
			}
		}
		if required > maximum {
			return fmt.Errorf("family %q requires %d sources but maximum accepted is %d", family, required, maximum)
		}
	}
	reportClaims := map[string]struct{}{}
	for _, claim := range oracle.RequiredReportClaims {
		if strings.TrimSpace(claim.Key) == "" || len(claim.RequiredFactKeys) == 0 {
			return errors.New("required report claim key and fact keys are required")
		}
		if _, duplicate := reportClaims[claim.Key]; duplicate {
			return fmt.Errorf("duplicate required report claim %q", claim.Key)
		}
		reportClaims[claim.Key] = struct{}{}
		if err := requireUniqueNonEmpty("required report claim fact key", claim.RequiredFactKeys); err != nil {
			return err
		}
		for _, factKey := range claim.RequiredFactKeys {
			if _, exists := factKeys[factKey]; !exists {
				return fmt.Errorf("required report claim %q references unknown expected fact %q", claim.Key, factKey)
			}
		}
	}
	return validateAutonomyOracle(oracle.Autonomy)
}

func validateAutonomyOracle(oracle *AutonomyOracle) error {
	if oracle == nil {
		return nil
	}
	requiredActions := map[string]struct{}{}
	for _, action := range oracle.RequiredActions {
		key, err := expectedActionKey(action)
		if err != nil {
			return err
		}
		if _, duplicate := requiredActions[key]; duplicate {
			return fmt.Errorf("duplicate required action %q", key)
		}
		requiredActions[key] = struct{}{}
	}
	forbiddenActions := map[string]struct{}{}
	for _, action := range oracle.ForbiddenActions {
		key, err := expectedActionKey(action)
		if err != nil {
			return err
		}
		if _, duplicate := forbiddenActions[key]; duplicate {
			return fmt.Errorf("duplicate forbidden action %q", key)
		}
		for _, required := range oracle.RequiredActions {
			if expectedActionsOverlap(required, action) {
				return fmt.Errorf("action %q is both required and forbidden", key)
			}
		}
		forbiddenActions[key] = struct{}{}
	}
	nodes := map[string]struct{}{}
	for _, node := range oracle.RequiredNodes {
		if strings.TrimSpace(node.Key) == "" || strings.TrimSpace(node.Kind) == "" || strings.TrimSpace(node.Status) == "" || node.Level < 0 {
			return errors.New("required graph node key, kind, non-negative level, and status are required")
		}
		if _, duplicate := nodes[node.Key]; duplicate {
			return fmt.Errorf("duplicate required graph node %q", node.Key)
		}
		nodes[node.Key] = struct{}{}
	}
	edges := map[string]struct{}{}
	for _, edge := range oracle.RequiredEdges {
		if strings.TrimSpace(edge.FromKey) == "" || strings.TrimSpace(edge.ToKey) == "" || strings.TrimSpace(edge.Type) == "" {
			return errors.New("required graph edge endpoints and type are required")
		}
		if _, exists := nodes[edge.FromKey]; !exists {
			return fmt.Errorf("required graph edge references unknown from node %q", edge.FromKey)
		}
		if _, exists := nodes[edge.ToKey]; !exists {
			return fmt.Errorf("required graph edge references unknown to node %q", edge.ToKey)
		}
		key := edge.FromKey + "\x00" + edge.ToKey + "\x00" + edge.Type
		if _, duplicate := edges[key]; duplicate {
			return fmt.Errorf("duplicate required graph edge %q", key)
		}
		edges[key] = struct{}{}
	}
	if projection := oracle.Projection; projection != nil {
		if projection.MinimumTotalNodes < 0 || projection.MaximumPageNodes < 0 {
			return errors.New("projection node limits cannot be negative")
		}
		if projection.MinimumTotalNodes > 0 && projection.MaximumPageNodes == 0 {
			return errors.New("projection page limit is required for a bounded large-graph fixture")
		}
	}
	return nil
}

func autonomyCriteria(oracle *AutonomyOracle) int {
	if oracle == nil {
		return 0
	}
	total := len(oracle.RequiredActions) + len(oracle.ForbiddenActions) + len(oracle.RequiredNodes) + len(oracle.RequiredEdges)
	if oracle.Projection != nil {
		total++
	}
	return total
}

func expectedActionKey(action ExpectedAction) (string, error) {
	if strings.TrimSpace(action.Kind) == "" {
		return "", errors.New("expected action kind is required")
	}
	return action.Kind + "\x00" + action.Actor + "\x00" + action.Target + "\x00" + action.Outcome, nil
}

func expectedActionsOverlap(left, right ExpectedAction) bool {
	return left.Kind == right.Kind &&
		(left.Actor == "" || right.Actor == "" || left.Actor == right.Actor) &&
		(left.Target == "" || right.Target == "" || left.Target == right.Target) &&
		(left.Outcome == "" || right.Outcome == "" || left.Outcome == right.Outcome)
}

func requireDocumentRefs(label string, ids []string, documents map[string]Document) error {
	if err := requireUniqueNonEmpty(label, ids); err != nil {
		return err
	}
	for _, id := range ids {
		if _, exists := documents[id]; !exists {
			return fmt.Errorf("%s references unknown document %q", label, id)
		}
	}
	return nil
}

func requireUniqueNonEmpty(label string, values []string) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s contains an empty value", label)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s contains duplicate %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
