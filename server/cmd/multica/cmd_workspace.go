package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Work with workspaces",
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces you belong to",
	RunE:  runWorkspaceList,
}

var workspaceGetCmd = &cobra.Command{
	Use:   "get [workspace-id|slug|prefix]",
	Short: "Get workspace details",
	Long: "Prints the full details of a workspace. The argument accepts a full " +
		"UUID, a slug, or a short UUID prefix (≥4 hex chars) as shown in " +
		"'workspace list'. If omitted, the current default workspace is used.",
	Args: cobra.MaximumNArgs(1),
	RunE: runWorkspaceGet,
}

var workspaceMemberCmd = &cobra.Command{
	Use:   "member",
	Short: "Manage workspace members",
}

var workspaceMemberListCmd = &cobra.Command{
	Use:   "list [workspace-id|slug|prefix]",
	Short: "List workspace members",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceMembers,
}

var workspaceUpdateCmd = &cobra.Command{
	Use:   "update [workspace-id|slug|prefix]",
	Short: "Update workspace metadata (admin/owner only)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceUpdate,
}

var workspaceSwitchCmd = &cobra.Command{
	Use:   "switch <workspace-id|slug|prefix>",
	Short: "Set the default workspace for this profile",
	Long: "Sets the default workspace for the current profile after verifying you " +
		"have access to it. Accepts a full UUID, a slug, or a short UUID " +
		"prefix (≥4 hex chars) as shown in 'workspace list'. Subsequent " +
		"commands without --workspace-id or MULTICA_WORKSPACE_ID will target " +
		"this workspace.\n\n" +
		"Resolution priority (highest to lowest): --workspace-id flag, " +
		"MULTICA_WORKSPACE_ID env, profile default (set by this command).\n\n" +
		"For low-level use, 'multica config set workspace_id <id>' writes the " +
		"same setting without verification.",
	Args: exactArgs(1),
	RunE: runWorkspaceSwitch,
}

// workspaceInfoCmd is the member-usable overview of a workspace (agents +
// computers/runtimes with status and sticky error text). Composed from
// existing member-scoped list APIs — no admin-only endpoints.
var workspaceInfoCmd = &cobra.Command{
	Use:   "info [workspace-id|slug|prefix]",
	Short: "Show workspace agents and computers with status and errors",
	Long: "Prints a member-usable overview of the current (or specified) workspace: " +
		"agents and computers (runtimes), including live status and sticky " +
		"error text when the latest outcome is a failure (e.g. provider quota).\n\n" +
		"Aligned with `raft server info` list flags:\n" +
		"  --agents / --computers  list only that section (default: both)\n" +
		"  --query                 filter rows by visible text (name, status, error)\n" +
		"  --limit / --offset      page list output (0 = unlimited, default; set limit to page)\n\n" +
		"Accepts a full UUID, slug, or short UUID prefix; omit to use the default workspace.",
	Args: cobra.MaximumNArgs(1),
	RunE: runWorkspaceInfo,
}

func init() {
	workspaceCmd.AddCommand(workspaceListCmd)
	workspaceCmd.AddCommand(workspaceGetCmd)
	workspaceCmd.AddCommand(workspaceInfoCmd)
	workspaceCmd.AddCommand(workspaceMemberCmd)
	workspaceMemberCmd.AddCommand(workspaceMemberListCmd)
	workspaceCmd.AddCommand(workspaceUpdateCmd)
	workspaceCmd.AddCommand(workspaceSwitchCmd)

	workspaceListCmd.Flags().String("output", "table", "Output format: table or json")
	workspaceListCmd.Flags().Bool("full-id", false, "Show full UUIDs in table output")
	workspaceGetCmd.Flags().String("output", "json", "Output format: table or json")
	workspaceInfoCmd.Flags().String("output", "table", "Output format: table or json")
	workspaceInfoCmd.Flags().Bool("include-archived", false, "Include archived agents")
	workspaceInfoCmd.Flags().Bool("agents", false, "List agents only (like raft server info --agents)")
	workspaceInfoCmd.Flags().Bool("computers", false, "List computers/runtimes only (like raft server info narrow lists)")
	workspaceInfoCmd.Flags().String("query", "", "Filter agents/computers by visible text")
	workspaceInfoCmd.Flags().Int("limit", 0, "Maximum rows per list section (0 = unlimited; raft default is 50 when paging)")
	workspaceInfoCmd.Flags().Int("offset", 0, "Rows to skip per list section")
	workspaceMemberListCmd.Flags().String("output", "table", "Output format: table or json")

	workspaceUpdateCmd.Flags().String("name", "", "New workspace name")
	workspaceUpdateCmd.Flags().String("description", "", "New description (decodes \\n, \\r, \\t, \\\\; pipe via --description-stdin to preserve literal backslashes)")
	workspaceUpdateCmd.Flags().Bool("description-stdin", false, "Read description from stdin (preserves multi-line content verbatim)")
	workspaceUpdateCmd.Flags().String("context", "", "New workspace context (decodes \\n, \\r, \\t, \\\\; pipe via --context-stdin to preserve literal backslashes)")
	workspaceUpdateCmd.Flags().Bool("context-stdin", false, "Read context from stdin (preserves multi-line content verbatim)")
	workspaceUpdateCmd.Flags().String("issue-prefix", "", "New issue prefix (uppercased server-side)")
	workspaceUpdateCmd.Flags().String("output", "json", "Output format: table or json")
}

// workspaceSummary is the subset of fields the CLI needs from /api/workspaces
// to drive list and switch. Keeping it here (instead of using the full
// WorkspaceResponse) avoids a dependency on the handler package.
type workspaceSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// fetchWorkspaces lists all workspaces the authenticated user belongs to. It
// is shared by `list` and `switch` so both see the same access-controlled view
// of workspaces.
func fetchWorkspaces(ctx context.Context, cmd *cobra.Command) ([]workspaceSummary, error) {
	if !inAgentExecutionContext() && resolveToken(cmd) == "" {
		return nil, fmt.Errorf("not authenticated: run 'multica login' first")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return nil, err
	}
	if isAgentAPIToken(cmd) {
		var one map[string]any
		if err := client.GetJSON(ctx, "/api/agent/workspace", &one); err != nil {
			return nil, fmt.Errorf("list workspaces: %w", err)
		}
		return []workspaceSummary{{
			ID: strVal(one, "id"), Name: strVal(one, "name"), Slug: strVal(one, "slug"),
		}}, nil
	}
	var workspaces []workspaceSummary
	if err := client.GetJSON(ctx, "/api/workspaces", &workspaces); err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	return workspaces, nil
}

func runWorkspaceList(cmd *cobra.Command, _ []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	workspaces, err := fetchWorkspaces(ctx, cmd)
	if err != nil {
		return err
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, workspaces)
	}

	if len(workspaces) == 0 {
		fmt.Fprintln(os.Stderr, "No workspaces found.")
		return nil
	}

	currentID := resolveWorkspaceID(cmd)
	fullID, _ := cmd.Flags().GetBool("full-id")
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "\tID\tNAME\tSLUG")
	for _, ws := range workspaces {
		marker := " "
		if ws.ID == currentID {
			marker = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", marker, displayID(ws.ID, fullID), ws.Name, ws.Slug)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if currentID != "" {
		fmt.Fprintln(os.Stderr, "\n* = current default workspace (use 'multica workspace switch <id|slug|prefix>' to change)")
	} else {
		fmt.Fprintln(os.Stderr, "\nNo default workspace set. Use 'multica workspace switch <id|slug|prefix>' to pick one.")
	}
	fmt.Fprintln(os.Stderr, "Tip: pass the ID column, SLUG, or full UUID (--full-id) to 'workspace get/update/switch'.")
	return nil
}

// resolveWorkspaceByIDOrSlug looks up a workspace in the caller's accessible
// list by full UUID, slug (case-insensitive), or short UUID prefix (≥4 hex
// chars). The matching order is exact UUID → exact slug → prefix, so a slug
// that happens to be a hex string can never be shadowed by a colliding UUID
// prefix. Returns an error if no workspace matches, which doubles as the
// "access denied / does not exist" check — the server only returns workspaces
// the user is a member of, so a match implies access.
func resolveWorkspaceByIDOrSlug(workspaces []workspaceSummary, target string) (workspaceSummary, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return workspaceSummary{}, fmt.Errorf("workspace id, slug, or id prefix is required")
	}
	// Slug comparison is case-insensitive (slugs are stored lowercase on the
	// server, but tolerate user-typed uppercase). UUIDs are also case-
	// insensitive in canonical form, so the lowering is safe for both.
	lowered := strings.ToLower(target)
	for _, ws := range workspaces {
		if strings.ToLower(ws.ID) == lowered {
			return ws, nil
		}
	}
	for _, ws := range workspaces {
		if ws.Slug != "" && strings.ToLower(ws.Slug) == lowered {
			return ws, nil
		}
	}

	// Fall back to short UUID prefix matching, so values copied from
	// `workspace list`'s default (truncated) ID column round-trip back into
	// get/update/switch. normalizeUUIDPrefix enforces ≥4 hex chars to avoid
	// surprises from arbitrary substrings.
	if prefix, err := normalizeUUIDPrefix(target); err == nil {
		matches := make([]workspaceSummary, 0, 1)
		for _, ws := range workspaces {
			if strings.HasPrefix(compactUUID(ws.ID), prefix) {
				matches = append(matches, ws)
			}
		}
		switch len(matches) {
		case 0:
			// fall through to the not-found error below
		case 1:
			return matches[0], nil
		default:
			return workspaceSummary{}, ambiguousWorkspacePrefixError(target, matches)
		}
	}

	return workspaceSummary{}, fmt.Errorf("workspace %q not found or you do not have access; run 'multica workspace list' to see options", target)
}

func ambiguousWorkspacePrefixError(input string, matches []workspaceSummary) error {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		label := m.Name
		if m.Slug != "" {
			label = fmt.Sprintf("%s (%s)", m.Name, m.Slug)
		}
		parts = append(parts, fmt.Sprintf("  %s  %s", m.ID, label))
	}
	return fmt.Errorf("ambiguous workspace id prefix %q; matches:\n%s\nUse more characters, the slug, or the full UUID", input, strings.Join(parts, "\n"))
}

// resolveWorkspaceRef fetches the caller's workspaces and resolves the input
// (UUID, slug, or short UUID prefix) to a workspaceSummary. Shared by
// `workspace get`, `workspace update`, `workspace member list`, and
// `workspace switch` so all four accept the same identifiers users see in
// `workspace list`.
func resolveWorkspaceRef(ctx context.Context, cmd *cobra.Command, input string) (workspaceSummary, error) {
	target := strings.TrimSpace(input)
	if target == "" {
		return workspaceSummary{}, fmt.Errorf("workspace id, slug, or id prefix is required")
	}
	workspaces, err := fetchWorkspaces(ctx, cmd)
	if err != nil {
		return workspaceSummary{}, err
	}
	return resolveWorkspaceByIDOrSlug(workspaces, target)
}

func runWorkspaceSwitch(cmd *cobra.Command, args []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	ws, err := resolveWorkspaceRef(ctx, cmd, args[0])
	if err != nil {
		return err
	}

	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}
	cfg.WorkspaceID = ws.ID
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Switched to workspace: %s (%s)\n", ws.Name, ws.ID)
	return nil
}

// resolveWorkspaceArg returns the canonical UUID for a workspace command that
// takes an optional `[workspace-id]` arg. When the arg is supplied it is
// resolved against the caller's workspace list (UUID, slug, or short prefix);
// when omitted it falls back to the standard --workspace-id / env / profile
// resolution chain — the caller is responsible for guarding against the empty
// case. A full UUID is forwarded as-is to avoid an extra /api/workspaces
// round trip; access control is enforced by the downstream endpoint.
func resolveWorkspaceArg(cmd *cobra.Command, args []string) (string, error) {
	if len(args) > 0 {
		trimmed := strings.TrimSpace(args[0])
		if uuidRegexp.MatchString(trimmed) {
			return trimmed, nil
		}
		ctx, cancel := cli.APIContext(context.Background())
		defer cancel()
		ws, err := resolveWorkspaceRef(ctx, cmd, trimmed)
		if err != nil {
			return "", err
		}
		return ws.ID, nil
	}
	return resolveWorkspaceID(cmd), nil
}

func runWorkspaceGet(cmd *cobra.Command, args []string) error {
	wsID, err := resolveWorkspaceArg(cmd, args)
	if err != nil {
		return err
	}
	if wsID == "" {
		return fmt.Errorf("workspace ID is required: pass an id/slug/prefix as argument or set MULTICA_WORKSPACE_ID")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var ws map[string]any
	wsPath := "/api/workspaces/" + wsID
	if isAgentAPIToken(cmd) {
		wsPath = "/api/agent/workspace"
	}
	if err := client.GetJSON(ctx, wsPath, &ws); err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		desc := strVal(ws, "description")
		if utf8.RuneCountInString(desc) > 60 {
			runes := []rune(desc)
			desc = string(runes[:57]) + "..."
		}
		wsContext := strVal(ws, "context")
		if utf8.RuneCountInString(wsContext) > 60 {
			runes := []rune(wsContext)
			wsContext = string(runes[:57]) + "..."
		}
		headers := []string{"ID", "NAME", "SLUG", "DESCRIPTION", "CONTEXT"}
		rows := [][]string{{
			strVal(ws, "id"),
			strVal(ws, "name"),
			strVal(ws, "slug"),
			desc,
			wsContext,
		}}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}

	return cli.PrintJSON(os.Stdout, ws)
}

// buildWorkspaceUpdateBody assembles the PATCH payload from the flags the
// caller actually set, mirroring server/internal/handler/workspace.go's
// UpdateWorkspaceRequest. Only fields whose flag is Changed() are emitted, so
// the caller cannot accidentally clobber a field they did not pass.
func buildWorkspaceUpdateBody(cmd *cobra.Command) (map[string]any, error) {
	body := map[string]any{}
	if cmd.Flags().Changed("name") {
		v, _ := cmd.Flags().GetString("name")
		body["name"] = v
	}
	if cmd.Flags().Changed("description") || cmd.Flags().Changed("description-stdin") {
		desc, _, err := resolveTextFlag(cmd, "description")
		if err != nil {
			return nil, err
		}
		body["description"] = desc
	}
	if cmd.Flags().Changed("context") || cmd.Flags().Changed("context-stdin") {
		ctxText, _, err := resolveTextFlag(cmd, "context")
		if err != nil {
			return nil, err
		}
		body["context"] = ctxText
	}
	if cmd.Flags().Changed("issue-prefix") {
		v, _ := cmd.Flags().GetString("issue-prefix")
		// The handler silently skips an empty prefix (workspace.go:274), so
		// `--issue-prefix ""` would otherwise return 200 without changing
		// anything. Reject it here so the failure is visible.
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("--issue-prefix cannot be empty; clearing the prefix is not supported")
		}
		body["issue_prefix"] = v
	}
	return body, nil
}

func runWorkspaceUpdate(cmd *cobra.Command, args []string) error {
	wsID, err := resolveWorkspaceArg(cmd, args)
	if err != nil {
		return err
	}
	if wsID == "" {
		return fmt.Errorf("workspace ID is required: pass an id/slug/prefix as argument or set MULTICA_WORKSPACE_ID")
	}

	body, err := buildWorkspaceUpdateBody(cmd)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("no fields to update; use --name, --description, --context, or --issue-prefix")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var ws map[string]any
	if err := client.PatchJSON(ctx, "/api/workspaces/"+wsID, body, &ws); err != nil {
		return fmt.Errorf("update workspace: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		desc := strVal(ws, "description")
		if utf8.RuneCountInString(desc) > 60 {
			runes := []rune(desc)
			desc = string(runes[:57]) + "..."
		}
		wsContext := strVal(ws, "context")
		if utf8.RuneCountInString(wsContext) > 60 {
			runes := []rune(wsContext)
			wsContext = string(runes[:57]) + "..."
		}
		headers := []string{"ID", "NAME", "SLUG", "DESCRIPTION", "CONTEXT"}
		rows := [][]string{{
			strVal(ws, "id"),
			strVal(ws, "name"),
			strVal(ws, "slug"),
			desc,
			wsContext,
		}}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}

	return cli.PrintJSON(os.Stdout, ws)
}

func runWorkspaceMembers(cmd *cobra.Command, args []string) error {
	wsID, err := resolveWorkspaceArg(cmd, args)
	if err != nil {
		return err
	}
	if wsID == "" {
		return fmt.Errorf("workspace ID is required: pass an id/slug/prefix as argument or set MULTICA_WORKSPACE_ID")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var members []map[string]any
	if err := client.GetJSON(ctx, "/api/workspaces/"+wsID+"/members", &members); err != nil {
		return fmt.Errorf("list members: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, members)
	}

	headers := []string{"USER ID", "NAME", "EMAIL", "ROLE"}
	rows := make([][]string, 0, len(members))
	for _, m := range members {
		rows = append(rows, []string{
			strVal(m, "user_id"),
			strVal(m, "name"),
			strVal(m, "email"),
			strVal(m, "role"),
		})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}

// ---------------------------------------------------------------------------
// workspace info — member-usable agents + computers overview
// ---------------------------------------------------------------------------

// workspaceInfoActiveTaskStatuses are non-terminal task statuses that mean
// the agent still has work on the plate. When present they suppress sticky
// failure text (same rule as the FE presence snapshot).
var workspaceInfoActiveTaskStatuses = map[string]bool{
	"running":    true,
	"dispatched": true,
	"queued":     true,
}

// workspaceInfoAgentRow is one agent line in workspace info.
type workspaceInfoAgentRow struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	DisplayName          string `json:"display_name"`
	Status               string `json:"status"`
	RuntimeID            string `json:"runtime_id,omitempty"`
	RuntimeName          string `json:"runtime_name,omitempty"`
	RuntimeStatus        string `json:"runtime_status,omitempty"`
	RuntimeDisplayStatus string `json:"runtime_display_status,omitempty"`
	// Error is sticky failure text from the latest failed outcome when the
	// agent has no active task. Empty when none. Shown verbatim (quota copy
	// etc.) so members see why an agent is unusable, not only "online".
	Error         string `json:"error,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	ArchivedAt    string `json:"archived_at,omitempty"`
}

// workspaceInfoComputerRow is one computer/runtime line in workspace info.
type workspaceInfoComputerRow struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name,omitempty"`
	Provider       string `json:"provider,omitempty"`
	RuntimeMode    string `json:"runtime_mode,omitempty"`
	Status         string `json:"status"`
	RuntimeHealth  string `json:"runtime_health,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
	UpdateState    string `json:"update_state,omitempty"`
	// Error aggregates update_error and auto_update.error_message (non-empty
	// first wins preference: update_error, then auto_update message).
	Error             string `json:"error,omitempty"`
	DeviceName        string `json:"device_name,omitempty"`
	ComputerConnected *bool  `json:"computer_connected,omitempty"`
}

// workspaceInfoPayload is the structured --output json body.
type workspaceInfoPayload struct {
	Workspace map[string]any             `json:"workspace"`
	Agents    []workspaceInfoAgentRow    `json:"agents"`
	Computers []workspaceInfoComputerRow `json:"computers"`
}

func runWorkspaceInfo(cmd *cobra.Command, args []string) error {
	wsID, err := resolveWorkspaceArg(cmd, args)
	if err != nil {
		return err
	}
	if wsID == "" {
		return fmt.Errorf("workspace ID is required: pass an id/slug/prefix as argument or set MULTICA_WORKSPACE_ID")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	// Force the resolved workspace onto every subsequent request (header +
	// agents query). resolveWorkspaceArg may have come from a slug/prefix
	// while the profile default points elsewhere.
	client.WorkspaceID = wsID

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	// Workspace header.
	var ws map[string]any
	wsPath := "/api/workspaces/" + wsID
	if isAgentAPIToken(cmd) {
		wsPath = "/api/agent/workspace"
	}
	if err := client.GetJSON(ctx, wsPath, &ws); err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	// Agents (member-scoped; every workspace member can list).
	var agents []map[string]any
	agentParams := url.Values{}
	agentParams.Set("workspace_id", wsID)
	if v, _ := cmd.Flags().GetBool("include-archived"); v {
		agentParams.Set("include_archived", "true")
	}
	agentPath := "/api/agents?" + agentParams.Encode()
	if err := client.GetJSON(ctx, agentPath, &agents); err != nil {
		return fmt.Errorf("list agents: %w", err)
	}

	// Computers / runtimes (visible set for this member — private runtimes
	// owned by others are omitted by the server; that is intentional).
	var runtimes []map[string]any
	if err := client.GetJSON(ctx, "/api/runtimes", &runtimes); err != nil {
		return fmt.Errorf("list runtimes: %w", err)
	}

	// Sticky failure text: latest failed outcome per agent when no active
	// task. Soft-fail — if the snapshot endpoint is unavailable on an older
	// server we still print agents/computers without error columns filled.
	var tasks []map[string]any
	if err := client.GetJSON(ctx, "/api/agent-task-snapshot", &tasks); err != nil {
		// Keep going; sticky errors just stay empty.
		tasks = nil
	}
	sticky := stickyTaskErrorsByAgent(tasks)

	agentRows := make([]workspaceInfoAgentRow, 0, len(agents))
	for _, a := range agents {
		id := strVal(a, "id")
		row := workspaceInfoAgentRow{
			ID:                   id,
			Name:                 strVal(a, "name"),
			DisplayName:          strVal(a, "display_name"),
			Status:               strVal(a, "status"),
			RuntimeID:            strVal(a, "runtime_id"),
			RuntimeName:          strVal(a, "runtime_name"),
			RuntimeStatus:        strVal(a, "runtime_status"),
			RuntimeDisplayStatus: strVal(a, "runtime_display_status"),
			ArchivedAt:           strVal(a, "archived_at"),
		}
		if se, ok := sticky[id]; ok {
			row.Error = se.Error
			row.FailureReason = se.FailureReason
		}
		agentRows = append(agentRows, row)
	}
	sort.SliceStable(agentRows, func(i, j int) bool {
		return strings.ToLower(agentRows[i].Name) < strings.ToLower(agentRows[j].Name)
	})

	computerRows := make([]workspaceInfoComputerRow, 0, len(runtimes))
	for _, rt := range runtimes {
		row := workspaceInfoComputerRow{
			ID:             strVal(rt, "id"),
			Name:           strVal(rt, "name"),
			DisplayName:    strVal(rt, "display_name"),
			Provider:       strVal(rt, "provider"),
			RuntimeMode:    strVal(rt, "runtime_mode"),
			Status:         strVal(rt, "status"),
			RuntimeHealth:  strVal(rt, "runtime_health"),
			CurrentVersion: strVal(rt, "current_version"),
			UpdateState:    strVal(rt, "update_state"),
			DeviceName:     strVal(rt, "device_name"),
			Error:          computerErrorText(rt),
		}
		if v, ok := rt["computer_connected"].(bool); ok {
			row.ComputerConnected = &v
		}
		computerRows = append(computerRows, row)
	}
	sort.SliceStable(computerRows, func(i, j int) bool {
		return strings.ToLower(computerLabel(computerRows[i])) < strings.ToLower(computerLabel(computerRows[j]))
	})

	wantAgents, _ := cmd.Flags().GetBool("agents")
	wantComputers, _ := cmd.Flags().GetBool("computers")
	// Default: both sections. If either narrow flag is set, only those.
	if !wantAgents && !wantComputers {
		wantAgents, wantComputers = true, true
	}
	query, _ := cmd.Flags().GetString("query")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	if offset < 0 {
		return fmt.Errorf("--offset must be >= 0")
	}
	if limit < 0 {
		return fmt.Errorf("--limit must be >= 0")
	}

	if wantAgents {
		agentRows = filterWorkspaceInfoAgents(agentRows, query)
		agentRows = pageWorkspaceInfoSlice(agentRows, offset, limit)
	} else {
		agentRows = nil
	}
	if wantComputers {
		computerRows = filterWorkspaceInfoComputers(computerRows, query)
		computerRows = pageWorkspaceInfoSlice(computerRows, offset, limit)
	} else {
		computerRows = nil
	}

	payload := workspaceInfoPayload{
		Workspace: ws,
		Agents:    agentRows,
		Computers: computerRows,
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, payload)
	}
	printWorkspaceInfoTable(os.Stdout, payload)
	return nil
}

func filterWorkspaceInfoAgents(rows []workspaceInfoAgentRow, query string) []workspaceInfoAgentRow {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return rows
	}
	out := make([]workspaceInfoAgentRow, 0, len(rows))
	for _, r := range rows {
		hay := strings.ToLower(strings.Join([]string{
			r.Name, r.DisplayName, r.Status, r.RuntimeName, r.RuntimeStatus,
			r.RuntimeDisplayStatus, r.Error, r.FailureReason, r.ID,
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, r)
		}
	}
	return out
}

func filterWorkspaceInfoComputers(rows []workspaceInfoComputerRow, query string) []workspaceInfoComputerRow {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return rows
	}
	out := make([]workspaceInfoComputerRow, 0, len(rows))
	for _, r := range rows {
		hay := strings.ToLower(strings.Join([]string{
			r.Name, r.DisplayName, r.Provider, r.Status, r.RuntimeHealth,
			r.CurrentVersion, r.UpdateState, r.DeviceName, r.Error, r.ID,
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, r)
		}
	}
	return out
}

func pageWorkspaceInfoSlice[T any](rows []T, offset, limit int) []T {
	if offset >= len(rows) {
		return []T{}
	}
	rows = rows[offset:]
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

type stickyTaskError struct {
	Error         string
	FailureReason string
}

// stickyTaskErrorsByAgent mirrors the FE snapshot rule: active tasks win
// (no sticky error); else the latest completed/failed outcome by
// completed_at keeps a failed error string sticky until new work starts.
func stickyTaskErrorsByAgent(tasks []map[string]any) map[string]stickyTaskError {
	type pick struct {
		active        bool
		completedAt   string
		errorText     string
		failureReason string
		failed        bool
	}
	best := map[string]pick{}
	for _, t := range tasks {
		agentID := strVal(t, "agent_id")
		if agentID == "" {
			continue
		}
		status := strVal(t, "status")
		if workspaceInfoActiveTaskStatuses[status] {
			best[agentID] = pick{active: true}
			continue
		}
		// Only terminal outcomes compete for sticky failure text.
		if status != "failed" && status != "completed" && status != "cancelled" {
			// Unknown status: ignore for sticky selection.
			if cur, ok := best[agentID]; ok && cur.active {
				continue
			}
			// Non-terminal non-active: skip.
			continue
		}
		cur, ok := best[agentID]
		if ok && cur.active {
			continue
		}
		completedAt := strVal(t, "completed_at")
		if ok && completedAt != "" && cur.completedAt != "" && completedAt < cur.completedAt {
			continue
		}
		// Prefer equal-or-newer completed_at.
		if ok && completedAt == cur.completedAt && completedAt != "" {
			// Keep existing on tie (stable).
			continue
		}
		errText := strVal(t, "error")
		// Some payloads nest error differently; tolerate non-string already
		// handled by strVal.
		best[agentID] = pick{
			completedAt:   completedAt,
			errorText:     errText,
			failureReason: strVal(t, "failure_reason"),
			failed:        status == "failed",
		}
	}
	out := make(map[string]stickyTaskError, len(best))
	for id, p := range best {
		if p.active || !p.failed || strings.TrimSpace(p.errorText) == "" {
			continue
		}
		out[id] = stickyTaskError{Error: p.errorText, FailureReason: p.failureReason}
	}
	return out
}

func computerErrorText(rt map[string]any) string {
	if e := strings.TrimSpace(strVal(rt, "update_error")); e != "" {
		return e
	}
	if au, ok := rt["auto_update"].(map[string]any); ok {
		if e := strings.TrimSpace(strVal(au, "error_message")); e != "" {
			return e
		}
	}
	return ""
}

func computerLabel(c workspaceInfoComputerRow) string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Name
}

func agentLabel(a workspaceInfoAgentRow) string {
	if a.DisplayName != "" && a.DisplayName != a.Name {
		return fmt.Sprintf("%s (%s)", a.Name, a.DisplayName)
	}
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.Name
}

// formatAgentStatusLine builds the human status for one agent: agent status,
// runtime display (or raw) status, and sticky error when present.
func formatAgentStatusLine(a workspaceInfoAgentRow) string {
	parts := make([]string, 0, 3)
	if a.Status != "" {
		parts = append(parts, a.Status)
	}
	rt := a.RuntimeDisplayStatus
	if rt == "" {
		rt = a.RuntimeStatus
	}
	if rt != "" && rt != a.Status {
		parts = append(parts, "runtime="+rt)
	}
	if a.Error != "" {
		// One line; collapse internal newlines so table stays readable.
		errText := strings.ReplaceAll(a.Error, "\n", " ")
		errText = collapseSpaces(errText)
		if utf8.RuneCountInString(errText) > 120 {
			runes := []rune(errText)
			errText = string(runes[:117]) + "..."
		}
		parts = append(parts, "error: "+errText)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "; ")
}

func formatComputerStatusLine(c workspaceInfoComputerRow) string {
	parts := make([]string, 0, 4)
	if c.Status != "" {
		parts = append(parts, c.Status)
	}
	if c.RuntimeHealth != "" && c.RuntimeHealth != c.Status {
		parts = append(parts, "health="+c.RuntimeHealth)
	}
	if c.CurrentVersion != "" {
		parts = append(parts, "v"+c.CurrentVersion)
	}
	if c.Error != "" {
		errText := strings.ReplaceAll(c.Error, "\n", " ")
		errText = collapseSpaces(errText)
		if utf8.RuneCountInString(errText) > 100 {
			runes := []rune(errText)
			errText = string(runes[:97]) + "..."
		}
		parts = append(parts, "error: "+errText)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "; ")
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func printWorkspaceInfoTable(w io.Writer, p workspaceInfoPayload) {
	wsName := strVal(p.Workspace, "name")
	wsSlug := strVal(p.Workspace, "slug")
	wsID := strVal(p.Workspace, "id")
	fmt.Fprintln(w, "## Workspace")
	if wsName != "" {
		fmt.Fprintf(w, "- %s", wsName)
		if wsSlug != "" {
			fmt.Fprintf(w, " (%s)", wsSlug)
		}
		if wsID != "" {
			fmt.Fprintf(w, "  %s", wsID)
		}
		fmt.Fprintln(w)
	} else if wsID != "" {
		fmt.Fprintf(w, "- %s\n", wsID)
	}

	fmt.Fprintf(w, "\n## Agents (%d)\n", len(p.Agents))
	if len(p.Agents) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		for _, a := range p.Agents {
			label := agentLabel(a)
			status := formatAgentStatusLine(a)
			runtimeBit := ""
			if a.RuntimeName != "" {
				runtimeBit = " · " + a.RuntimeName
			}
			fmt.Fprintf(w, "  - %s%s — %s\n", label, runtimeBit, status)
		}
	}

	fmt.Fprintf(w, "\n## Computers (%d)\n", len(p.Computers))
	if len(p.Computers) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		for _, c := range p.Computers {
			label := computerLabel(c)
			prov := c.Provider
			if prov != "" {
				label = fmt.Sprintf("%s [%s]", label, prov)
			}
			fmt.Fprintf(w, "  - %s — %s\n", label, formatComputerStatusLine(c))
		}
	}
}
