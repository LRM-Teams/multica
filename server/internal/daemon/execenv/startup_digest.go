package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// StartupStaticDigest hashes the slow-changing, provider-startup-visible bundle
// that must stay stable for resident process reuse (D6-1b option A).
//
// Per-turn fields (Initiator, ChatMessage, Issue/Trigger assignment bodies,
// surface summary, attachments) are intentionally excluded — those go in the
// Execute prompt envelope every turn.
//
// When this digest changes, the canonical runtime fingerprint changes and the
// resident process is disposed + recreated so the new process can re-read the
// startup materialize snapshot.
func StartupStaticDigest(ctx TaskContextForEnv) string {
	type memory struct {
		Name    string `json:"name,omitempty"`
		Content string `json:"content,omitempty"`
	}
	type skill struct {
		Name    string `json:"name,omitempty"`
		Content string `json:"content,omitempty"`
	}
	type repo struct {
		URL         string `json:"url,omitempty"`
		Description string `json:"description,omitempty"`
	}
	type resource struct {
		ID           string `json:"id,omitempty"`
		ResourceType string `json:"resource_type,omitempty"`
		Label        string `json:"label,omitempty"`
		ResourceRef  string `json:"resource_ref,omitempty"`
	}
	type payload struct {
		AgentInstructions                string     `json:"agent_instructions,omitempty"`
		ManagedRole                      string     `json:"managed_role,omitempty"`
		AgentName                        string     `json:"agent_name,omitempty"`
		AgentID                          string     `json:"agent_id,omitempty"`
		WorkspaceContext                 string     `json:"workspace_context,omitempty"`
		RequestingUserName               string     `json:"requesting_user_name,omitempty"`
		RequestingUserProfileDescription string     `json:"requesting_user_profile,omitempty"`
		ProjectID                        string     `json:"project_id,omitempty"`
		ProjectTitle                     string     `json:"project_title,omitempty"`
		Memories                         []memory   `json:"memories,omitempty"`
		Skills                           []skill    `json:"skills,omitempty"`
		Repos                            []repo     `json:"repos,omitempty"`
		ProjectResources                 []resource `json:"project_resources,omitempty"`
	}
	p := payload{
		AgentInstructions:                ctx.AgentInstructions,
		ManagedRole:                      ctx.ManagedRole,
		AgentName:                        ctx.AgentName,
		AgentID:                          ctx.AgentID,
		RequestingUserName:               ctx.RequestingUserName,
		RequestingUserProfileDescription: ctx.RequestingUserProfileDescription,
		ProjectID:                        ctx.ProjectID,
		ProjectTitle:                     ctx.ProjectTitle,
	}
	for _, m := range ctx.AgentMemories {
		p.Memories = append(p.Memories, memory{Name: m.Name, Content: m.Content})
	}
	// Stable order for digest.
	sort.Slice(p.Memories, func(i, j int) bool {
		if p.Memories[i].Name != p.Memories[j].Name {
			return p.Memories[i].Name < p.Memories[j].Name
		}
		return p.Memories[i].Content < p.Memories[j].Content
	})
	for _, s := range ctx.AgentSkills {
		p.Skills = append(p.Skills, skill{Name: s.Name, Content: s.Content})
	}
	sort.Slice(p.Skills, func(i, j int) bool {
		if p.Skills[i].Name != p.Skills[j].Name {
			return p.Skills[i].Name < p.Skills[j].Name
		}
		return p.Skills[i].Content < p.Skills[j].Content
	})
	for _, r := range ctx.Repos {
		p.Repos = append(p.Repos, repo{URL: r.URL, Description: r.Description})
	}
	sort.Slice(p.Repos, func(i, j int) bool {
		return p.Repos[i].URL < p.Repos[j].URL
	})
	for _, r := range ctx.ProjectResources {
		p.ProjectResources = append(p.ProjectResources, resource{
			ID: r.ID, ResourceType: r.ResourceType, Label: r.Label, ResourceRef: string(r.ResourceRef),
		})
	}
	sort.Slice(p.ProjectResources, func(i, j int) bool {
		return p.ProjectResources[i].ID < p.ProjectResources[j].ID
	})
	raw, err := json.Marshal(p)
	if err != nil {
		// Fail closed to a non-empty digest so a marshal bug cannot collapse
		// distinct bundles into an empty shared fingerprint.
		return fmt.Sprintf("sha256:error:%s", strings.TrimSpace(err.Error()))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
