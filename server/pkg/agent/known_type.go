package agent

import "strings"

// IsKnownType reports whether New() can construct this provider.
func IsKnownType(agentType string) bool {
	_, ok := agentConstructors[strings.TrimSpace(agentType)]
	return ok
}

// MissingRequiredRuntimeIDs returns accepted runtime IDs that are absent from
// live and still required. A missing ID whose provider is no longer in the
// shipped catalog is omitted (catalog shrink). unknownIsRequired controls
// IDs whose provider cannot be resolved: the server fail-closes; the daemon
// defers those to the server.
func MissingRequiredRuntimeIDs(accepted, live []string, providerOf func(string) string, unknownIsRequired bool) []string {
	present := make(map[string]struct{}, len(live))
	for _, id := range live {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		present[id] = struct{}{}
	}
	var missing []string
	for _, id := range accepted {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := present[id]; ok {
			continue
		}
		provider := ""
		if providerOf != nil {
			provider = strings.TrimSpace(providerOf(id))
		}
		if provider != "" && !IsKnownType(provider) {
			continue
		}
		if provider == "" && !unknownIsRequired {
			continue
		}
		missing = append(missing, id)
	}
	return missing
}
