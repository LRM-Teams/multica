package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func evolutionVersionMetadata(version, summary string, tags, tools, taskTypes, projectTypes, languages, frameworks []string) string {
	encoded, _ := json.Marshal(map[string]any{
		"version": version,
		"matcher_snapshot": map[string]any{
			"canonical_summary": summary,
			"tags":              tags, "tools": tools, "task_types": taskTypes,
			"project_types": projectTypes, "languages": languages, "frameworks": frameworks,
		},
	})
	return string(encoded)
}

type evolutionVersionFixture struct {
	unitID       string
	currentID    string
	rollbackID   string
	skillID      string
	currentName  string
	rollbackName string
	currentMain  string
	rollbackMain string
}

func seedEvolutionVersionFixture(t *testing.T) evolutionVersionFixture {
	t.Helper()
	ctx := context.Background()
	fixture := evolutionVersionFixture{
		currentName:  "Versioned Skill Current " + randomID(),
		rollbackName: "Versioned Skill Rollback " + randomID(),
	}
	fixture.currentMain = "---\nname: " + fixture.currentName + "\ndescription: current description\n---\n# Current\n"
	fixture.rollbackMain = "---\nname: " + fixture.rollbackName + "\ndescription: rollback description\n---\n# Rollback\n"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO shared_evolution_unit (
			workspace_id, unit_type, title, canonical_summary, content, metadata, applies, failure_cases, status,
			tags, tools, task_types, project_types, languages, frameworks
		) VALUES ($1, 'skill', 'Versioned Skill v2', 'current summary', 'current unit content', $2::jsonb, '{}'::jsonb, '[]'::jsonb, 'active',
			ARRAY['current-tag'], ARRAY['current-tool'], ARRAY['current-task'], ARRAY['current-project'], ARRAY['go'], ARRAY['chi'])
		RETURNING id
	`, testWorkspaceID, evolutionVersionMetadata("two", "current summary", []string{"current-tag"}, []string{"current-tool"}, []string{"current-task"}, []string{"current-project"}, []string{"go"}, []string{"chi"})).Scan(&fixture.unitID); err != nil {
		t.Fatalf("seed unit: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO shared_evolution_unit_version (
			workspace_id, unit_id, version, title, content, metadata, applies, failure_cases, change_reason, created_by
		) VALUES ($1, $2, 1, 'Versioned Skill v1', 'rollback unit content', $3::jsonb, '{}'::jsonb, '[]'::jsonb, 'initial', 'test')
		RETURNING id
	`, testWorkspaceID, fixture.unitID, evolutionVersionMetadata("one", "rollback summary", []string{"rollback-tag"}, []string{"rollback-tool"}, []string{"rollback-task"}, []string{"rollback-project"}, []string{"rust"}, []string{"axum"})).Scan(&fixture.rollbackID); err != nil {
		t.Fatalf("seed rollback version: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO shared_evolution_unit_version (
			workspace_id, unit_id, version, title, content, metadata, applies, failure_cases, change_reason, created_by
		) VALUES ($1, $2, 2, 'Versioned Skill v2', 'current unit content', $3::jsonb, '{}'::jsonb, '[]'::jsonb, 'update', 'test')
		RETURNING id
	`, testWorkspaceID, fixture.unitID, evolutionVersionMetadata("two", "current summary", []string{"current-tag"}, []string{"current-tool"}, []string{"current-task"}, []string{"current-project"}, []string{"go"}, []string{"chi"})).Scan(&fixture.currentID); err != nil {
		t.Fatalf("seed current version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE shared_evolution_unit SET current_version_id=$2 WHERE id=$1`, fixture.unitID, fixture.currentID); err != nil {
		t.Fatalf("set current version: %v", err)
	}
	for versionID, main := range map[string]string{fixture.rollbackID: fixture.rollbackMain, fixture.currentID: fixture.currentMain} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO shared_evolution_unit_file (workspace_id, unit_id, version_id, path, content, content_hash, mime_type, size_bytes)
			VALUES ($1, $2, $3, 'SKILL.md', $4, 'hash-main', 'text/markdown', length($4)),
			       ($1, $2, $3, 'references/guide.md', $5, 'hash-guide', 'text/markdown', length($5))
		`, testWorkspaceID, fixture.unitID, versionID, main, "guide for "+versionID); err != nil {
			t.Fatalf("seed version files: %v", err)
		}
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by, source_evolution_unit_id)
		VALUES ($1, $2, 'current description', $3, '{}'::jsonb, $4, $5)
		RETURNING id
	`, testWorkspaceID, fixture.currentName, fixture.currentMain, testUserID, fixture.unitID).Scan(&fixture.skillID); err != nil {
		t.Fatalf("seed materialized skill: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO skill_file (skill_id, path, content) VALUES ($1, 'references/guide.md', 'current guide')`, fixture.skillID); err != nil {
		t.Fatalf("seed materialized skill file: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO evolution_unit_feedback_event (workspace_id, unit_type, unit_id, event, outcome, source, metadata)
		VALUES ($1, 'skill', $2, 'used', '', 'manual', jsonb_build_object('version_id', $3::text)),
		       ($1, 'skill', $2, 'success', 'success', 'manual', jsonb_build_object('version_id', $3::text)),
		       ($1, 'skill', $2, 'failure', 'failure', 'manual', '{}'::jsonb)
	`, testWorkspaceID, fixture.unitID, fixture.rollbackID); err != nil {
		t.Fatalf("seed feedback: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM activity_log WHERE workspace_id=$1 AND action='evolution_skill_version_rolled_back' AND details->>'unit_id'=$2`, testWorkspaceID, fixture.unitID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM shared_evolution_unit WHERE id=$1`, fixture.unitID)
	})
	return fixture
}

func evolutionVersionRequest(method, path string, body any, unitID, versionID string) *http.Request {
	req := newRequest(method, path, body)
	member := middleware.SetMemberContext(req.Context(), testWorkspaceID, dbMemberForEvolutionVersionTest())
	req = req.WithContext(member)
	return withRouteParams(req, "unitId", unitID, "versionId", versionID)
}

func stringSlicesEqualForTest(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func dbMemberForEvolutionVersionTest() db.Member {
	return db.Member{Role: "owner", WorkspaceID: parseUUID(testWorkspaceID), UserID: parseUUID(testUserID)}
}

func TestEvolutionSkillRuntimeFeedbackUsesExecutionVersion(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	agentID := createHandlerTestAgent(t, "version-feedback-"+randomID(), nil)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, agentID, fixture.skillID); err != nil {
		t.Fatalf("assign skill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM evolution_unit_feedback_event WHERE agent_id=$1`, agentID)
	})

	inboxExecutionID := parseUUID(uuid.NewString())
	skills := testHandler.TaskService.LoadAgentSkillsForInbox(context.Background(), parseUUID(agentID), inboxExecutionID)
	if len(skills) != 1 {
		t.Fatalf("loaded skills=%d, want 1", len(skills))
	}
	testHandler.TaskService.LoadAgentSkillsForInbox(context.Background(), parseUUID(agentID), inboxExecutionID)
	testHandler.TaskService.RecordEvolutionSkillOutcome(context.Background(), inboxExecutionID, "success", "success")
	testHandler.TaskService.RecordEvolutionSkillOutcome(context.Background(), inboxExecutionID, "success", "success")

	var injected, succeeded int
	var taskIDs int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE event='injected'),
		       count(*) FILTER (WHERE event='success'),
		       count(task_id)
		FROM evolution_unit_feedback_event
		WHERE agent_id=$1 AND unit_id=$2
		  AND metadata->>'execution_id'=$3
		  AND metadata->>'version_id'=$4
	`, agentID, fixture.unitID, uuidToString(inboxExecutionID), fixture.currentID).Scan(&injected, &succeeded, &taskIDs); err != nil {
		t.Fatalf("load attributed feedback: %v", err)
	}
	if injected != 1 || succeeded != 1 || taskIDs != 0 {
		t.Fatalf("feedback injected=%d success=%d task_ids=%d, want 1/1/0", injected, succeeded, taskIDs)
	}

	var rollbackAttributed int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM evolution_unit_feedback_event WHERE agent_id=$1 AND metadata->>'version_id'=$2`, agentID, fixture.rollbackID).Scan(&rollbackAttributed); err != nil || rollbackAttributed != 0 {
		t.Fatalf("rollback-attributed events=%d err=%v, want 0", rollbackAttributed, err)
	}
}

func TestEvolutionSkillVersionAPIListGetAndEval(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)

	listReq := evolutionVersionRequest(http.MethodGet, "/api/evolution/units/"+fixture.unitID+"/versions", nil, fixture.unitID, "")
	listRec := httptest.NewRecorder()
	testHandler.ListEvolutionSkillVersions(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var list []EvolutionSkillVersionResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 2 || list[0].Version != 2 || !list[0].IsCurrent || list[0].Content != "" {
		t.Fatalf("versions = %#v", list)
	}

	getReq := evolutionVersionRequest(http.MethodGet, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.rollbackID, nil, fixture.unitID, fixture.rollbackID)
	getRec := httptest.NewRecorder()
	testHandler.GetEvolutionSkillVersion(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var detail EvolutionSkillVersionDetailResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ID != fixture.rollbackID || len(detail.Files) != 2 || detail.Eval.Basis != "version_attributed" || detail.Eval.Counts.Success != 1 || detail.Eval.Counts.Failure != 0 {
		t.Fatalf("rollback detail = %#v", detail)
	}

	currentReq := evolutionVersionRequest(http.MethodGet, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.currentID, nil, fixture.unitID, fixture.currentID)
	currentRec := httptest.NewRecorder()
	testHandler.GetEvolutionSkillVersion(currentRec, currentReq)
	if currentRec.Code != http.StatusOK {
		t.Fatalf("current get status=%d body=%s", currentRec.Code, currentRec.Body.String())
	}
	if err := json.Unmarshal(currentRec.Body.Bytes(), &detail); err != nil || detail.Eval.Basis != "unit_lifetime_fallback" || detail.Eval.VersionAttributed.Total != 0 || detail.Eval.UnitUnattributedEvents != 1 {
		t.Fatalf("cross-version current detail=%#v err=%v", detail, err)
	}
}

func TestEvolutionSkillVersionRollbackIsTransactionalAndIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	body := map[string]string{"expected_current_version_id": fixture.currentID}
	req := evolutionVersionRequest(http.MethodPost, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.rollbackID+"/rollback", body, fixture.unitID, fixture.rollbackID)
	rec := httptest.NewRecorder()
	testHandler.RollbackEvolutionSkillVersion(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response evolutionSkillVersionRollbackResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode rollback: %v", err)
	}
	if !response.Changed || response.CurrentVersionID != fixture.rollbackID {
		t.Fatalf("rollback response = %#v", response)
	}
	var currentID, unitContent, skillName, skillContent, description, guide, summary string
	var tags, tools, taskTypes, projectTypes, languages, frameworks []string
	if err := testPool.QueryRow(context.Background(), `
		SELECT u.current_version_id::text, u.content, s.name, s.content, s.description, sf.content,
		       u.canonical_summary, u.tags, u.tools, u.task_types, u.project_types, u.languages, u.frameworks
		FROM shared_evolution_unit u
		JOIN skill s ON s.source_evolution_unit_id=u.id AND s.workspace_id=u.workspace_id
		JOIN skill_file sf ON sf.skill_id=s.id AND sf.path='references/guide.md'
		WHERE u.id=$1
	`, fixture.unitID).Scan(&currentID, &unitContent, &skillName, &skillContent, &description, &guide, &summary, &tags, &tools, &taskTypes, &projectTypes, &languages, &frameworks); err != nil {
		t.Fatalf("load rollback state: %v", err)
	}
	if currentID != fixture.rollbackID || unitContent != "rollback unit content" || skillName != fixture.rollbackName || skillContent != fixture.rollbackMain || description != "rollback description" || !strings.Contains(guide, fixture.rollbackID) ||
		summary != "rollback summary" || !stringSlicesEqualForTest(tags, []string{"rollback-tag"}) || !stringSlicesEqualForTest(tools, []string{"rollback-tool"}) ||
		!stringSlicesEqualForTest(taskTypes, []string{"rollback-task"}) || !stringSlicesEqualForTest(projectTypes, []string{"rollback-project"}) ||
		!stringSlicesEqualForTest(languages, []string{"rust"}) || !stringSlicesEqualForTest(frameworks, []string{"axum"}) {
		t.Fatalf("rollback state current=%q unit=%q skill_name=%q skill=%q description=%q guide=%q summary=%q tags=%v tools=%v task=%v project=%v languages=%v frameworks=%v", currentID, unitContent, skillName, skillContent, description, guide, summary, tags, tools, taskTypes, projectTypes, languages, frameworks)
	}
	var audits int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM activity_log WHERE action='evolution_skill_version_rolled_back' AND details->>'unit_id'=$1`, fixture.unitID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit count=%d err=%v", audits, err)
	}

	idempotentReq := evolutionVersionRequest(http.MethodPost, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.rollbackID+"/rollback", body, fixture.unitID, fixture.rollbackID)
	idempotentRec := httptest.NewRecorder()
	testHandler.RollbackEvolutionSkillVersion(idempotentRec, idempotentReq)
	if idempotentRec.Code != http.StatusOK {
		t.Fatalf("idempotent status=%d body=%s", idempotentRec.Code, idempotentRec.Body.String())
	}
	if err := json.Unmarshal(idempotentRec.Body.Bytes(), &response); err != nil || response.Changed {
		t.Fatalf("idempotent response=%#v err=%v", response, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM activity_log WHERE action='evolution_skill_version_rolled_back' AND details->>'unit_id'=$1`, fixture.unitID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("idempotent audit count=%d err=%v", audits, err)
	}
}

func TestEvolutionSkillVersionAlreadyCurrentRepairsDriftAndAudits(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	if _, err := testPool.Exec(context.Background(), `UPDATE skill SET content='drifted', description='drifted' WHERE id=$1`, fixture.skillID); err != nil {
		t.Fatalf("seed skill drift: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM skill_file WHERE skill_id=$1`, fixture.skillID); err != nil {
		t.Fatalf("seed file drift: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE shared_evolution_unit SET canonical_summary='drifted', tags=ARRAY['drifted'] WHERE id=$1`, fixture.unitID); err != nil {
		t.Fatalf("seed unit drift: %v", err)
	}
	req := evolutionVersionRequest(http.MethodPost, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.currentID+"/rollback", map[string]string{"expected_current_version_id": fixture.currentID}, fixture.unitID, fixture.currentID)
	rec := httptest.NewRecorder()
	testHandler.RollbackEvolutionSkillVersion(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("repair status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response evolutionSkillVersionRollbackResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || !response.Changed {
		t.Fatalf("repair response=%#v err=%v", response, err)
	}
	var content, summary string
	var audits int
	if err := testPool.QueryRow(context.Background(), `SELECT s.content, u.canonical_summary FROM skill s JOIN shared_evolution_unit u ON u.id=s.source_evolution_unit_id WHERE s.id=$1`, fixture.skillID).Scan(&content, &summary); err != nil || content != fixture.currentMain || summary != "current summary" {
		t.Fatalf("repaired content=%q summary=%q err=%v", content, summary, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM activity_log WHERE action='evolution_skill_version_rolled_back' AND details->>'unit_id'=$1 AND details->>'repaired_drift'='true'`, fixture.unitID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("repair audit count=%d err=%v", audits, err)
	}
}

func TestEvolutionSkillVersionRollbackLegacySnapshotFailsClosed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	if _, err := testPool.Exec(context.Background(), `UPDATE shared_evolution_unit_version SET metadata='{}'::jsonb WHERE id=$1`, fixture.rollbackID); err != nil {
		t.Fatalf("remove snapshot: %v", err)
	}
	req := evolutionVersionRequest(http.MethodPost, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.rollbackID+"/rollback", map[string]string{"expected_current_version_id": fixture.currentID}, fixture.unitID, fixture.rollbackID)
	rec := httptest.NewRecorder()
	testHandler.RollbackEvolutionSkillVersion(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("legacy snapshot status=%d body=%s", rec.Code, rec.Body.String())
	}
	var currentID string
	if err := testPool.QueryRow(context.Background(), `SELECT current_version_id::text FROM shared_evolution_unit WHERE id=$1`, fixture.unitID).Scan(&currentID); err != nil || currentID != fixture.currentID {
		t.Fatalf("legacy rollback changed current=%q err=%v", currentID, err)
	}
}

func TestEvolutionSkillVersionRollbackRequiresExpectedCurrent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	req := evolutionVersionRequest(http.MethodPost, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.rollbackID+"/rollback", map[string]string{}, fixture.unitID, fixture.rollbackID)
	rec := httptest.NewRecorder()
	testHandler.RollbackEvolutionSkillVersion(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing expected current status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvolutionSkillVersionRollbackRejectsStaleExpectedCurrent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	req := evolutionVersionRequest(http.MethodPost, "/api/evolution/units/"+fixture.unitID+"/versions/"+fixture.rollbackID+"/rollback", map[string]string{"expected_current_version_id": fixture.rollbackID}, fixture.unitID, fixture.rollbackID)
	rec := httptest.NewRecorder()
	testHandler.RollbackEvolutionSkillVersion(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale rollback status=%d body=%s", rec.Code, rec.Body.String())
	}
	var currentID string
	if err := testPool.QueryRow(context.Background(), `SELECT current_version_id::text FROM shared_evolution_unit WHERE id=$1`, fixture.unitID).Scan(&currentID); err != nil || currentID != fixture.currentID {
		t.Fatalf("current version=%q err=%v, want unchanged %q", currentID, err, fixture.currentID)
	}
}

func TestEvolutionSkillVersionRouteRequiresAdminRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	ctx := context.Background()
	var memberUserID string
	suffix := randomID()
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, "Version Member "+suffix, "version-member-"+suffix+"@multica.test").Scan(&memberUserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, memberUserID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, memberUserID)
	})

	router := chi.NewRouter()
	router.Route("/api/evolution/units/{unitId}/versions", func(r chi.Router) {
		r.Use(middleware.RequireWorkspaceRole(testHandler.Queries, "owner", "admin"))
		r.Get("/", testHandler.ListEvolutionSkillVersions)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/evolution/units/"+fixture.unitID+"/versions?workspace_id="+testWorkspaceID, nil)
	req.Header.Set("X-User-ID", memberUserID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient permissions") {
		t.Fatalf("member route status=%d body=%s", rec.Code, rec.Body.String())
	}
}
