// Package secretscoped filters env/secret injection by channel and project
// scope (LRM-953). Channel A secrets must not enter channel B wakes; project
// secrets require a bound project id on the task.
package secretscoped

import "strings"

// Scope values for Secret.Scope.
const (
	ScopeAgent   = "agent"
	ScopeChannel = "channel"
	ScopeProject = "project"
)

// Secret is one injectable env entry with an optional hard scope boundary.
type Secret struct {
	Key       string
	Value     string
	Scope     string // agent (default) | channel | project
	ChannelID string
	ProjectID string
}

// TaskScope is the execution surface used for filtering.
type TaskScope struct {
	ChannelID string
	ProjectID string
}

// Filter returns env entries allowed for the given task scope.
// Agent-scoped secrets always pass. Channel/project secrets require an
// exact id match; missing task ids fail closed.
func Filter(secrets []Secret, task TaskScope) map[string]string {
	out := make(map[string]string, len(secrets))
	channelID := strings.TrimSpace(task.ChannelID)
	projectID := strings.TrimSpace(task.ProjectID)
	for _, secret := range secrets {
		key := strings.TrimSpace(secret.Key)
		if key == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(secret.Scope)) {
		case "", ScopeAgent:
			out[key] = secret.Value
		case ScopeChannel:
			secretChannel := strings.TrimSpace(secret.ChannelID)
			if secretChannel != "" && channelID != "" && secretChannel == channelID {
				out[key] = secret.Value
			}
		case ScopeProject:
			secretProject := strings.TrimSpace(secret.ProjectID)
			if secretProject != "" && projectID != "" && secretProject == projectID {
				out[key] = secret.Value
			}
		default:
			// Unknown scopes fail closed.
		}
	}
	return out
}

// FromAgentEnv lifts a flat custom_env map into agent-scoped secrets.
func FromAgentEnv(env map[string]string) []Secret {
	if len(env) == 0 {
		return nil
	}
	out := make([]Secret, 0, len(env))
	for key, value := range env {
		out = append(out, Secret{Key: key, Value: value, Scope: ScopeAgent})
	}
	return out
}
