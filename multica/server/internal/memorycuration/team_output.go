package memorycuration

import (
	"encoding/json"
	"strings"
)

// teamCurationJSON is the structured team curation contract returned by the
// stage agent. Counts must come from this JSON — never from substring scans of
// free-form curator prose (which inflated "team item" stats).
type teamCurationJSON struct {
	TeamKnowledge []json.RawMessage `json:"team_knowledge"`
	Conflicts     []json.RawMessage `json:"conflicts"`
}

// CountTeamCurationOutput returns the number of team_knowledge and conflicts
// entries in a curator output payload. Prose-only or unparseable output yields
// zeros so Evolution Center does not display fake promotions.
func CountTeamCurationOutput(output string) (teamKnowledge, conflicts int) {
	var parsed teamCurationJSON
	if !extractJSONObject(output, &parsed) {
		return 0, 0
	}
	return len(parsed.TeamKnowledge), len(parsed.Conflicts)
}

func extractJSONObject(output string, dst any) bool {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return false
	}
	return json.Unmarshal([]byte(output[start:end+1]), dst) == nil
}
