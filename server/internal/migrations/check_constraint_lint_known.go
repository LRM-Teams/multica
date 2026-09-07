package migrations

// knownUnsafeNarrowingKey identifies one pre-existing down-migration
// narrowing this checker has already flagged and is deliberately not
// blocking on.
type knownUnsafeNarrowingKey struct {
	Migration  string
	Constraint string
}

// knownPreExistingUnsafeNarrowings is historical debt discovered by this
// same checker while it was being built (task #97, 2026-08-02): a
// repo-wide scan found 33 down migrations, predating this checker, that
// already narrow a CHECK constraint without remapping existing rows first —
// the same shape as 268_agent_workspace_file_audit.down.sql before review
// caught it. Fixing these is explicitly OUT OF SCOPE for task #97 itself
// (Parker, 2026-08-02: "if the full-history scan finds other broken
// down.sql files, that's a separate new finding — don't bundle it into this
// PR"). This checker exists to stop the list from growing, not to
// retroactively fix history.
//
// Down to 26 as of task #101 (PR #1850, merged): 143/181/182/186/207
// (agent_inbox_event_reason_check only — its terminal_outcome sibling is
// separate, unfixed debt)/254 were converted from silent DELETE to a
// conditional RAISE EXCEPTION guard, which task #104 taught this checker to
// recognize as a second valid resolution alongside a remap UPDATE — so
// those 6 no longer even reach filterKnownUnsafeNarrowings, and were
// removed here. 107 (task #99 / PR #1842, fixed earlier the same day with
// the same RAISE EXCEPTION pattern) dropped out for the same reason — it
// was in the original 33 only because this checker didn't recognize its
// fix's shape yet either.
//
// Each entry must be removed the same time its down migration is actually
// fixed (add the remap UPDATE or RAISE EXCEPTION guard, then delete the
// entry here) — never delete an entry just because the list "looks long";
// TestAllMigrations_* will catch that mismatch immediately (the real
// down.sql would still trip the checker with the entry gone).
var knownPreExistingUnsafeNarrowings = map[knownUnsafeNarrowingKey]bool{
	{"060_issue_origin_quick_create", "issue_origin_type_check"}:                                  true,
	{"084_squad", "issue_assignee_type_check"}:                                                    true,
	{"111_issue_origin_lark_chat", "issue_origin_type_check"}:                                     true,
	{"141_sandbox_resume", "sandbox_job_type_check"}:                                              true,
	{"169_memory_curator_profile", "memory_curation_run_status_check"}:                            true,
	{"171_agent_inbox_channel_message_reason", "agent_inbox_event_reason_check"}:                  true,
	{"172_wendy_start_work_and_ambient", "pending_handoff_reason_code_check"}:                     true,
	{"173_agent_transport_thread_unfollow", "agent_task_transport_audit_action_check"}:            true,
	{"181_agent_self_review_team_curation", "memory_curation_watermark_stage_check"}:              true,
	{"181_agent_self_review_team_curation", "memory_curation_run_stage_check"}:                    true,
	{"187_sandbox_job_restore_template_types", "sandbox_job_type_check"}:                          true,
	{"197_agent_inbox_attention_reasons", "agent_inbox_event_reason_check"}:                       true,
	{"198_env_dispatch_derived_agents", "environment_agent_sandbox_status_check"}:                 true,
	{"201_drop_channel_attention", "agent_inbox_event_reason_check"}:                              true,
	{"205_beckham_product_delivery_actions", "agent_radar_action_action_type_check"}:              true,
	{"207_channel_agent_onboarding", "agent_inbox_event_terminal_outcome_check"}:                  true,
	{"210_agent_memory_self_review_runs", "agent_memory_write_event_scope_type_check"}:            true,
	{"210_agent_memory_self_review_runs", "agent_memory_curation_candidate_candidate_type_check"}: true,
	{"212_agent_channel_visibility", "agent_creation_draft_visibility_check"}:                     true,
	{"212_agent_channel_visibility", "agent_visibility_check"}:                                    true,
	{"221_group_manager_reminders", "agent_reminder_lifecycle_event_actor_type_check"}:            true,
	{"223_agent_wake_clean_cutover", "agent_inbox_event_reason_check"}:                            true,
	{"223_agent_wake_clean_cutover", "agent_inbox_event_terminal_outcome_check"}:                  true,
	{"223_agent_wake_clean_cutover", "agent_session_scope_check"}:                                 true,
	{"247_channel_manager_role_wake", "agent_inbox_event_reason_check"}:                           true,
	{"255_research_product_rounds", "research_graph_node_node_type_check"}:                        true,
	// 492 predates this linter; its down path intentionally fails closed for
	// new-kind atoms (ADR 0021 D8), rather than silently remapping audit data.
	{"492_graph_memory_atom_kind_vocabulary", "graph_memory_atom_kind_check"}: true,
}

// filterKnownUnsafeNarrowings splits found narrowings into new (must block
// CI) and known (pre-existing debt, already tracked, not blocking).
//
// This is a separate, directly testable function — mirroring the
// cursordeadlock checker's filterKnown/filterKnownWith split (task #90,
// 2026-08-02) — specifically so this file's own tests can pass a FIXTURE
// known-map instead of the real 33-entry one above. Hardcoding the real map
// into a test would make "someone fixes one of the 33 entries" break this
// checker's own test suite, which is exactly the fragility that prompted
// the cursordeadlock fix earlier the same day.
func filterKnownUnsafeNarrowings(found []UnsafeNarrowing, known map[knownUnsafeNarrowingKey]bool) (newFindings, knownFindings []UnsafeNarrowing) {
	for _, n := range found {
		if known[knownUnsafeNarrowingKey{Migration: n.Migration, Constraint: n.Constraint}] {
			knownFindings = append(knownFindings, n)
			continue
		}
		newFindings = append(newFindings, n)
	}
	return newFindings, knownFindings
}
