package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
)

const minShortIDPrefixLen = 4
const resolverListPageLimit = 50

// uuidRegexp matches a canonical UUID (8-4-4-4-12 hex). Shared by CLI resolvers.
var uuidRegexp = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")


type resolvedID struct {
	ID      string
	Display string
}

type idCandidate struct {
	ID      string
	Display string
	Detail  string
}

func displayID(id string, full bool) string {
	if full {
		return id
	}
	return truncateID(id)
}

func issueDisplayKey(issue map[string]any) string {
	if key := strVal(issue, "identifier"); key != "" {
		return key
	}
	id := strVal(issue, "id")
	if id != "" {
		slog.Warn("issue response missing identifier", "issue_id", id)
	}
	return id
}

func issueCandidate(issue map[string]any) idCandidate {
	return idCandidate{
		ID:      strVal(issue, "id"),
		Display: issueDisplayKey(issue),
		Detail:  strVal(issue, "title"),
	}
}

func normalizeUUIDPrefix(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("id is required")
	}
	prefix := strings.ToLower(strings.ReplaceAll(trimmed, "-", ""))
	if len(prefix) < minShortIDPrefixLen {
		return "", fmt.Errorf("expected a full UUID or at least %d hex characters, got %q", minShortIDPrefixLen, input)
	}
	for _, r := range prefix {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", fmt.Errorf("expected a UUID prefix containing only hex characters, got %q", input)
		}
	}
	return prefix, nil
}

func compactUUID(id string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
}

func resolveIDByPrefix(ctx context.Context, client *cli.APIClient, kind, input string, fetch func(context.Context, *cli.APIClient) ([]idCandidate, error)) (resolvedID, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return resolvedID{}, fmt.Errorf("%s id is required", kind)
	}
	if uuidRegexp.MatchString(trimmed) {
		return resolvedID{ID: trimmed, Display: trimmed}, nil
	}

	prefix, err := normalizeUUIDPrefix(trimmed)
	if err != nil {
		return resolvedID{}, fmt.Errorf("resolve %s: %w", kind, err)
	}

	candidates, err := fetch(ctx, client)
	if err != nil {
		return resolvedID{}, fmt.Errorf("resolve %s: %w", kind, err)
	}

	matches := make([]idCandidate, 0, 1)
	for _, c := range candidates {
		if c.ID == "" {
			continue
		}
		if strings.HasPrefix(compactUUID(c.ID), prefix) {
			matches = append(matches, c)
		}
	}

	switch len(matches) {
	case 0:
		return resolvedID{}, fmt.Errorf("no %s found matching id prefix %q; run the list command with --full-id to copy the full UUID", kind, input)
	case 1:
		display := matches[0].Display
		if display == "" {
			display = matches[0].ID
		}
		return resolvedID{ID: matches[0].ID, Display: display}, nil
	default:
		return resolvedID{}, ambiguousIDPrefixError(kind, input, matches)
	}
}

func ambiguousIDPrefixError(kind, input string, matches []idCandidate) error {
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].ID < matches[j].ID
	})
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, "  "+m.ID)
	}
	return fmt.Errorf("ambiguous %s id prefix %q; matches:\n%s\nUse more characters or run the list command with --full-id", kind, input, strings.Join(parts, "\n"))
}

func resolveIssueRef(ctx context.Context, client *cli.APIClient, input string) (resolvedID, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return resolvedID{}, fmt.Errorf("issue id is required")
	}

	// Preserve issue-key semantics before considering UUID prefixes. This
	// mirrors the server-side loadIssueForUser order and avoids treating
	// strings like MUL-1852 as a UUID prefix.
	if looksLikeIssueIdentifier(trimmed) {
		return fetchIssueRef(ctx, client, trimmed)
	}
	if uuidRegexp.MatchString(trimmed) {
		return fetchIssueRef(ctx, client, trimmed)
	}
	return resolveIDByPrefix(ctx, client, "issue", trimmed, fetchIssueCandidates)
}

func fetchIssueRef(ctx context.Context, client *cli.APIClient, ref string) (resolvedID, error) {
	var issue map[string]any
	issuePath := "/api/issues/" + url.PathEscape(ref)
	if isAgentAPITokenAmbient() {
		issuePath = "/api/agent/issues/" + url.PathEscape(ref)
	}
	if err := client.GetJSON(ctx, issuePath, &issue); err != nil {
		return resolvedID{}, err
	}
	c := issueCandidate(issue)
	if c.Display == "" {
		c.Display = c.ID
	}
	return resolvedID{ID: c.ID, Display: c.Display}, nil
}

func looksLikeIssueIdentifier(input string) bool {
	if input == "" {
		return false
	}
	dash := strings.LastIndex(input, "-")
	if dash <= 0 || dash >= len(input)-1 {
		return false
	}
	prefix := input[:dash]
	for _, r := range prefix {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	_, ok := parsePositiveInt(input[dash+1:])
	return ok
}

func parsePositiveInt(input string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func fetchIssueCandidates(ctx context.Context, client *cli.APIClient) ([]idCandidate, error) {
	if client.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required to resolve issue id prefixes")
	}
	const limit = resolverListPageLimit
	candidates := []idCandidate{}
	for offset := 0; ; {
		params := url.Values{}
		params.Set("workspace_id", client.WorkspaceID)
		params.Set("include_closed", "true")
		params.Set("limit", strconv.Itoa(limit))
		if offset > 0 {
			params.Set("offset", strconv.Itoa(offset))
		}
		var result map[string]any
		listPath := "/api/issues?" + params.Encode()
		if isAgentAPITokenAmbient() {
			listPath = "/api/agent/issues?" + params.Encode()
		}
		if err := client.GetJSON(ctx, listPath, &result); err != nil {
			return nil, err
		}
		issuesRaw, _ := result["issues"].([]any)
		for _, raw := range issuesRaw {
			issue, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			candidates = append(candidates, issueCandidate(issue))
		}
		offset += len(issuesRaw)
		total, _ := result["total"].(float64)
		if len(issuesRaw) == 0 || (total > 0 && offset >= int(total)) || (total == 0 && len(issuesRaw) < limit) {
			break
		}
	}
	return candidates, nil
}

func resolveAutopilotID(ctx context.Context, client *cli.APIClient, input string) (resolvedID, error) {
	return resolvedID{}, fmt.Errorf("autopilot has been removed (LRM-1049); use multica reminder")
}

func fetchAutopilotCandidates(ctx context.Context, client *cli.APIClient) ([]idCandidate, error) {
	return nil, fmt.Errorf("autopilot has been removed (LRM-1049)")
}

func resolveTaskRunID(ctx context.Context, client *cli.APIClient, issueID, input string) (resolvedID, error) {
	trimmed := strings.TrimSpace(input)
	if uuidRegexp.MatchString(trimmed) {
		return resolvedID{ID: trimmed, Display: trimmed}, nil
	}
	if strings.TrimSpace(issueID) == "" {
		return resolvedID{}, fmt.Errorf("short task run prefixes require --issue <issue-id>; pass a full task UUID or run `multica issue runs <issue-id> --full-id`")
	}
	fetch := func(ctx context.Context, client *cli.APIClient) ([]idCandidate, error) {
		return fetchTaskRunCandidatesForIssue(ctx, client, issueID)
	}
	return resolveIDByPrefix(ctx, client, "task run", input, fetch)
}

func fetchTaskRunCandidatesForIssue(ctx context.Context, client *cli.APIClient, issueID string) ([]idCandidate, error) {
	var runs []map[string]any
	runsPath := "/api/issues/" + url.PathEscape(issueID) + "/task-runs"
	if isAgentAPITokenAmbient() {
		runsPath = "/api/agent/issues/" + url.PathEscape(issueID) + "/task-runs"
	}
	if err := client.GetJSON(ctx, runsPath, &runs); err != nil {
		return nil, err
	}
	candidates := make([]idCandidate, 0, len(runs))
	for _, r := range runs {
		id := strVal(r, "id")
		if id == "" {
			continue
		}
		candidates = append(candidates, idCandidate{
			ID:      id,
			Display: id,
		})
	}
	return candidates, nil
}

func resolveAutopilotTriggerID(ctx context.Context, client *cli.APIClient, autopilotID, input string) (resolvedID, error) {
	return resolvedID{}, fmt.Errorf("autopilot has been removed (LRM-1049); use multica reminder")
}

func resolveProjectID(ctx context.Context, client *cli.APIClient, input string) (resolvedID, error) {
	trimmed := strings.TrimSpace(input)
	if uuidRegexp.MatchString(trimmed) || looksLikeFullUUID(trimmed) {
		return resolvedID{ID: trimmed, Display: trimmed}, nil
	}
	return resolveIDByPrefix(ctx, client, "project", input, fetchProjectCandidates)
}

func looksLikeFullUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
		}
	}
	return true
}

func fetchProjectCandidates(ctx context.Context, client *cli.APIClient) ([]idCandidate, error) {
	if client.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required to resolve project id prefixes")
	}
	params := url.Values{"workspace_id": {client.WorkspaceID}}
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/projects?"+params.Encode(), &result); err != nil {
		return nil, err
	}
	projectsRaw, _ := result["projects"].([]any)
	candidates := make([]idCandidate, 0, len(projectsRaw))
	for _, raw := range projectsRaw {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		candidates = append(candidates, idCandidate{
			ID:      strVal(p, "id"),
			Display: strVal(p, "title"),
			Detail:  strVal(p, "status"),
		})
	}
	return candidates, nil
}

func resolveProjectResourceID(ctx context.Context, client *cli.APIClient, projectID, input string) (resolvedID, error) {
	fetch := func(ctx context.Context, client *cli.APIClient) ([]idCandidate, error) {
		var result map[string]any
		if err := client.GetJSON(ctx, agentProjectResourcesPathAmbient(projectID), &result); err != nil {
			return nil, err
		}
		resourcesRaw, _ := result["resources"].([]any)
		candidates := make([]idCandidate, 0, len(resourcesRaw))
		for _, raw := range resourcesRaw {
			r, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			display := strVal(r, "label")
			if display == "" {
				display = strVal(r, "resource_type")
			}
			candidates = append(candidates, idCandidate{
				ID:      strVal(r, "id"),
				Display: display,
				Detail:  summarizeResourceRef(r["resource_ref"]),
			})
		}
		return candidates, nil
	}
	return resolveIDByPrefix(ctx, client, "project resource", input, fetch)
}

func resolveLabelID(ctx context.Context, client *cli.APIClient, input string) (resolvedID, error) {
	return resolveIDByPrefix(ctx, client, "label", input, fetchLabelCandidates)
}

func fetchLabelCandidates(ctx context.Context, client *cli.APIClient) ([]idCandidate, error) {
	if client.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required to resolve label id prefixes")
	}
	params := url.Values{"workspace_id": {client.WorkspaceID}}
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/labels?"+params.Encode(), &result); err != nil {
		return nil, err
	}
	labelsRaw, _ := result["labels"].([]any)
	candidates := make([]idCandidate, 0, len(labelsRaw))
	for _, raw := range labelsRaw {
		l, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		candidates = append(candidates, idCandidate{
			ID:      strVal(l, "id"),
			Display: strVal(l, "name"),
			Detail:  strVal(l, "color"),
		})
	}
	return candidates, nil
}

type actorDisplayLookup struct {
	ctx    context.Context
	client *cli.APIClient
	state  *actorDisplayLookupState
}

type actorDisplayLookupState struct {
	members       map[string]string
	agents        map[string]string
	squads        map[string]string
	membersLoaded bool
	agentsLoaded  bool
	squadsLoaded  bool
}

func loadActorDisplayLookup(ctx context.Context, client *cli.APIClient) actorDisplayLookup {
	return actorDisplayLookup{
		ctx:    ctx,
		client: client,
		state:  &actorDisplayLookupState{},
	}
}

func (l actorDisplayLookup) loadMembers() {
	if l.state == nil || l.state.membersLoaded {
		return
	}
	l.state.membersLoaded = true
	l.state.members = map[string]string{}
	if l.client == nil || l.client.WorkspaceID == "" {
		return
	}
	// Parker R1 lock: under mat_*, never hit human members list — display
	// degrades to UUID (actor() already falls back to type:id).
	if isAgentAPITokenAmbient() {
		return
	}
	var members []map[string]any
	if err := l.client.GetJSON(l.ctx, "/api/workspaces/"+url.PathEscape(l.client.WorkspaceID)+"/members", &members); err == nil {
		for _, m := range members {
			if id := strVal(m, "user_id"); id != "" {
				l.state.members[id] = strVal(m, "name")
			}
		}
	}
}

func (l actorDisplayLookup) loadAgents() {
	if l.state == nil || l.state.agentsLoaded {
		return
	}
	l.state.agentsLoaded = true
	l.state.agents = map[string]string{}
	if l.client == nil || l.client.WorkspaceID == "" {
		return
	}
	var agents []map[string]any
	agentPath := agentAgentsListPathAmbient(l.client)
	if err := l.client.GetJSON(l.ctx, agentPath, &agents); err == nil {
		for _, a := range agents {
			if id := strVal(a, "id"); id != "" {
				l.state.agents[id] = strVal(a, "name")
			}
		}
	}
}

func (l actorDisplayLookup) loadSquads() {
	// Squad product retired: no /api/squads. Historical assignee_type=squad
	// displays as 原小队指派 via actor() without name resolution.
	if l.state == nil || l.state.squadsLoaded {
		return
	}
	l.state.squadsLoaded = true
	l.state.squads = map[string]string{}
}

func (l actorDisplayLookup) actor(actorType, id string) string {
	if actorType == "" || id == "" {
		return ""
	}
	switch actorType {
	case "member":
		l.loadMembers()
		if l.state != nil && l.state.members != nil {
			if name := l.state.members[id]; name != "" {
				return "member:" + name
			}
		}
	case "agent":
		l.loadAgents()
		if l.state != nil && l.state.agents != nil {
			if name := l.state.agents[id]; name != "" {
				return "agent:" + name
			}
		}
	case "squad":
		// Historical-only: squad product retired; do not resolve names via API.
		return "原小队指派"
	}
	return actorType + ":" + id
}

func (l actorDisplayLookup) agent(id string) string {
	if id == "" {
		return ""
	}
	l.loadAgents()
	if l.state != nil && l.state.agents != nil {
		if name := l.state.agents[id]; name != "" {
			return name
		}
	}
	return id
}
