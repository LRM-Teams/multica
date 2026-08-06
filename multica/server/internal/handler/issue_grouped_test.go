package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListGroupedIssuesAssigneePaginatesPerGroup(t *testing.T) {
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	var assigneeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Grouped Issues Test User", fmt.Sprintf("grouped-%d@multica.ai", suffix)).Scan(&assigneeID); err != nil {
		t.Fatalf("create assignee user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, assigneeID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, assigneeID); err != nil {
		t.Fatalf("create assignee member: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id
		, model) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "Grouped Issues Test Agent", testRuntimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	createIssue := func(title, assigneeType, assigneeID string, position float64) string {
		t.Helper()
		var number int32
		if err := testPool.QueryRow(ctx, `
			UPDATE workspace
			SET issue_counter = GREATEST(
				issue_counter,
				(SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)
			) + 1
			WHERE id = $1
			RETURNING issue_counter
		`, testWorkspaceID).Scan(&number); err != nil {
			t.Fatalf("next issue number: %v", err)
		}

		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (
				workspace_id, title, description, status, priority,
				assignee_type, assignee_id, creator_type, creator_id,
				position, number
			)
			VALUES ($1, $2, NULL, 'todo', 'none', $3, $4, 'member', $5, $6, $7)
			RETURNING id
		`, testWorkspaceID, title, assigneeType, assigneeID, testUserID, position, number).Scan(&id); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, id)
		})
		return id
	}

	createIssue("Grouped member one", "member", assigneeID, 1)
	createIssue("Grouped member two", "member", assigneeID, 2)
	createIssue("Grouped member three", "member", assigneeID, 3)
	createIssue("Grouped agent one", "agent", agentID, 1)

	path := fmt.Sprintf(
		"/api/issues/grouped?workspace_id=%s&group_by=assignee&statuses=todo&limit=2&assignee_filters=member:%s,agent:%s",
		testWorkspaceID,
		assigneeID,
		agentID,
	)
	w := httptest.NewRecorder()
	testHandler.ListGroupedIssues(w, newRequest("GET", path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp GroupedIssuesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode grouped response: %v", err)
	}

	memberGroupID := "assignee:member:" + assigneeID
	agentGroupID := "assignee:agent:" + agentID
	groups := map[string]IssueAssigneeGroupResponse{}
	for _, group := range resp.Groups {
		groups[group.ID] = group
	}

	memberGroup, ok := groups[memberGroupID]
	if !ok {
		t.Fatalf("missing member group %s in %#v", memberGroupID, resp.Groups)
	}
	if memberGroup.Total != 3 || len(memberGroup.Issues) != 2 {
		t.Fatalf("member group total/page mismatch: total=%d len=%d", memberGroup.Total, len(memberGroup.Issues))
	}
	if memberGroup.Issues[0].Title != "Grouped member one" || memberGroup.Issues[1].Title != "Grouped member two" {
		t.Fatalf("member group order mismatch: %#v", memberGroup.Issues)
	}

	agentGroup, ok := groups[agentGroupID]
	if !ok {
		t.Fatalf("missing agent group %s in %#v", agentGroupID, resp.Groups)
	}
	if agentGroup.Total != 1 || len(agentGroup.Issues) != 1 {
		t.Fatalf("agent group total/page mismatch: total=%d len=%d", agentGroup.Total, len(agentGroup.Issues))
	}

	nextPath := fmt.Sprintf(
		"/api/issues/grouped?workspace_id=%s&group_by=assignee&statuses=todo&limit=2&offset=2&group_assignee_type=member&group_assignee_id=%s",
		testWorkspaceID,
		assigneeID,
	)
	next := httptest.NewRecorder()
	testHandler.ListGroupedIssues(next, newRequest("GET", nextPath, nil))
	if next.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues next page: expected 200, got %d: %s", next.Code, next.Body.String())
	}

	var nextResp GroupedIssuesResponse
	if err := json.NewDecoder(next.Body).Decode(&nextResp); err != nil {
		t.Fatalf("decode next grouped response: %v", err)
	}
	if len(nextResp.Groups) != 1 {
		t.Fatalf("expected one next-page group, got %#v", nextResp.Groups)
	}
	if nextResp.Groups[0].ID != memberGroupID || nextResp.Groups[0].Total != 3 || len(nextResp.Groups[0].Issues) != 1 {
		t.Fatalf("unexpected next-page group: %#v", nextResp.Groups[0])
	}
	if nextResp.Groups[0].Issues[0].Title != "Grouped member three" {
		t.Fatalf("unexpected next-page issue: %#v", nextResp.Groups[0].Issues[0])
	}
}

func TestListGroupedIssuesProjectPaginatesPerGroup(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var creatorID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Project Grouped Issues User", fmt.Sprintf("project-grouped-%d@multica.ai", suffix)).Scan(&creatorID); err != nil {
		t.Fatalf("create project-group creator: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, creatorID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, creatorID); err != nil {
		t.Fatalf("create project-group member: %v", err)
	}

	createProject := func(title string) string {
		t.Helper()
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO project (workspace_id, title, status, priority)
			VALUES ($1, $2, 'planned', 'none')
			RETURNING id
		`, testWorkspaceID, title).Scan(&id); err != nil {
			t.Fatalf("create project %q: %v", title, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, id)
		})
		return id
	}
	alphaProjectID := createProject("Alpha project group")
	zuluProjectID := createProject("Zulu project group")

	createIssue := func(title, status string, projectID *string, position float64) {
		t.Helper()
		var number int32
		if err := testPool.QueryRow(ctx, `
			UPDATE workspace
			SET issue_counter = GREATEST(
				issue_counter,
				(SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)
			) + 1
			WHERE id = $1
			RETURNING issue_counter
		`, testWorkspaceID).Scan(&number); err != nil {
			t.Fatalf("next issue number: %v", err)
		}

		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (
				workspace_id, title, description, status, priority,
				creator_type, creator_id, position, number, project_id
			)
			VALUES ($1, $2, NULL, $3, 'none', 'member', $4, $5, $6, $7)
			RETURNING id
		`, testWorkspaceID, title, status, creatorID, position, number, projectID).Scan(&id); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, id)
		})
	}

	createIssue("Alpha cancelled", "cancelled", &alphaProjectID, 1)
	createIssue("Alpha backlog", "backlog", &alphaProjectID, 2)
	createIssue("Alpha done", "done", &alphaProjectID, 3)
	createIssue("Alpha todo", "todo", &alphaProjectID, 4)
	createIssue("Alpha blocked", "blocked", &alphaProjectID, 5)
	createIssue("Alpha in progress", "in_progress", &alphaProjectID, 6)
	createIssue("Alpha in review", "in_review", &alphaProjectID, 7)
	createIssue("Zulu one", "todo", &zuluProjectID, 1)
	createIssue("No project one", "todo", nil, 1)
	createIssue("No project two", "todo", nil, 2)

	path := fmt.Sprintf(
		"/api/issues/grouped?workspace_id=%s&group_by=project&sort=status&limit=2&creator_id=%s",
		testWorkspaceID,
		creatorID,
	)
	w := httptest.NewRecorder()
	testHandler.ListGroupedIssues(w, newRequest("GET", path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues by project: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp ProjectGroupedIssuesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode project-grouped response: %v", err)
	}
	if len(resp.Groups) != 3 {
		t.Fatalf("expected three project groups, got %#v", resp.Groups)
	}
	if resp.Groups[0].ID != "project:"+alphaProjectID || resp.Groups[1].ID != "project:"+zuluProjectID || resp.Groups[2].ID != "project:none" {
		t.Fatalf("project group order mismatch: %#v", resp.Groups)
	}
	alphaGroup := resp.Groups[0]
	if alphaGroup.ProjectID == nil || *alphaGroup.ProjectID != alphaProjectID || alphaGroup.ProjectTitle == nil || *alphaGroup.ProjectTitle != "Alpha project group" {
		t.Fatalf("alpha group identity mismatch: %#v", alphaGroup)
	}
	if alphaGroup.Total != 7 || len(alphaGroup.Issues) != 2 {
		t.Fatalf("alpha group total/page mismatch: total=%d len=%d", alphaGroup.Total, len(alphaGroup.Issues))
	}
	if alphaGroup.Issues[0].Title != "Alpha in review" || alphaGroup.Issues[1].Title != "Alpha in progress" {
		t.Fatalf("alpha group issue order mismatch: %#v", alphaGroup.Issues)
	}
	noProjectGroup := resp.Groups[2]
	if noProjectGroup.ProjectID != nil || noProjectGroup.ProjectTitle != nil || noProjectGroup.Total != 2 || len(noProjectGroup.Issues) != 2 {
		t.Fatalf("no-project group mismatch: %#v", noProjectGroup)
	}

	nextPath := fmt.Sprintf(
		"/api/issues/grouped?workspace_id=%s&group_by=project&sort=status&limit=2&offset=2&creator_id=%s&group_project_id=%s",
		testWorkspaceID,
		creatorID,
		alphaProjectID,
	)
	next := httptest.NewRecorder()
	testHandler.ListGroupedIssues(next, newRequest("GET", nextPath, nil))
	if next.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues project next page: expected 200, got %d: %s", next.Code, next.Body.String())
	}
	var nextResp ProjectGroupedIssuesResponse
	if err := json.NewDecoder(next.Body).Decode(&nextResp); err != nil {
		t.Fatalf("decode project next page: %v", err)
	}
	if len(nextResp.Groups) != 1 || nextResp.Groups[0].ID != "project:"+alphaProjectID || nextResp.Groups[0].Total != 7 || len(nextResp.Groups[0].Issues) != 2 || nextResp.Groups[0].Issues[0].Title != "Alpha blocked" || nextResp.Groups[0].Issues[1].Title != "Alpha todo" {
		t.Fatalf("unexpected project next-page response: %#v", nextResp.Groups)
	}

	tailPath := fmt.Sprintf(
		"/api/issues/grouped?workspace_id=%s&group_by=project&sort=status&limit=3&offset=4&creator_id=%s&group_project_id=%s",
		testWorkspaceID,
		creatorID,
		alphaProjectID,
	)
	tail := httptest.NewRecorder()
	testHandler.ListGroupedIssues(tail, newRequest("GET", tailPath, nil))
	if tail.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues project tail page: expected 200, got %d: %s", tail.Code, tail.Body.String())
	}
	var tailResp ProjectGroupedIssuesResponse
	if err := json.NewDecoder(tail.Body).Decode(&tailResp); err != nil {
		t.Fatalf("decode project tail page: %v", err)
	}
	if len(tailResp.Groups) != 1 || tailResp.Groups[0].Total != 7 || len(tailResp.Groups[0].Issues) != 3 || tailResp.Groups[0].Issues[0].Title != "Alpha backlog" || tailResp.Groups[0].Issues[1].Title != "Alpha done" || tailResp.Groups[0].Issues[2].Title != "Alpha cancelled" {
		t.Fatalf("unexpected project tail-page response: %#v", tailResp.Groups)
	}

	flatPath := fmt.Sprintf(
		"/api/issues?workspace_id=%s&project_id=%s&creator_id=%s&sort=status&limit=7",
		testWorkspaceID,
		alphaProjectID,
		creatorID,
	)
	flat := httptest.NewRecorder()
	testHandler.ListIssues(flat, newRequest("GET", flatPath, nil))
	if flat.Code != http.StatusOK {
		t.Fatalf("ListIssues status order: expected 200, got %d: %s", flat.Code, flat.Body.String())
	}
	var flatResp struct {
		Issues []IssueResponse `json:"issues"`
		Total  int             `json:"total"`
	}
	if err := json.NewDecoder(flat.Body).Decode(&flatResp); err != nil {
		t.Fatalf("decode flat status order: %v", err)
	}
	wantFlatTitles := []string{
		"Alpha in review",
		"Alpha in progress",
		"Alpha blocked",
		"Alpha todo",
		"Alpha backlog",
		"Alpha done",
		"Alpha cancelled",
	}
	if flatResp.Total != len(wantFlatTitles) || len(flatResp.Issues) != len(wantFlatTitles) {
		t.Fatalf("flat status order count mismatch: total=%d len=%d", flatResp.Total, len(flatResp.Issues))
	}
	for i, want := range wantFlatTitles {
		if flatResp.Issues[i].Title != want {
			t.Fatalf("flat status order at %d = %q, want %q", i, flatResp.Issues[i].Title, want)
		}
	}

	nonePath := fmt.Sprintf(
		"/api/issues/grouped?workspace_id=%s&group_by=project&statuses=todo&limit=1&offset=1&creator_id=%s&group_project_id=none",
		testWorkspaceID,
		creatorID,
	)
	none := httptest.NewRecorder()
	testHandler.ListGroupedIssues(none, newRequest("GET", nonePath, nil))
	if none.Code != http.StatusOK {
		t.Fatalf("ListGroupedIssues no-project next page: expected 200, got %d: %s", none.Code, none.Body.String())
	}
	var noneResp ProjectGroupedIssuesResponse
	if err := json.NewDecoder(none.Body).Decode(&noneResp); err != nil {
		t.Fatalf("decode no-project next page: %v", err)
	}
	if len(noneResp.Groups) != 1 || noneResp.Groups[0].ID != "project:none" || noneResp.Groups[0].Total != 2 || len(noneResp.Groups[0].Issues) != 1 || noneResp.Groups[0].Issues[0].Title != "No project two" {
		t.Fatalf("unexpected no-project next-page response: %#v", noneResp.Groups)
	}
}
