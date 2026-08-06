package agent

const proactiveContextCompactionPercent = 60.0

const proactiveContextCompactionInstructions = `Preserve a structured checkpoint of the current conversation. Retain user intent, decisions, constraints, unresolved questions, active work, external side effects, changed files, test results, and source references. Distinguish verified facts from assumptions. Keep the checkpoint concise and sufficient for the next turn.`

func shouldProactivelyCompact(stats *RuntimeTokenStats) bool {
	if stats == nil {
		return false
	}
	if stats.ContextPercent != nil {
		return *stats.ContextPercent >= proactiveContextCompactionPercent
	}
	if stats.ContextTokens == nil || stats.ContextWindow == nil || *stats.ContextWindow <= 0 {
		return false
	}
	return float64(*stats.ContextTokens)*100/float64(*stats.ContextWindow) >= proactiveContextCompactionPercent
}
