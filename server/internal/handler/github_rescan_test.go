package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRescanIssuePullRequestWithoutGitHubApp(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("GITHUB_APP_SLUG", "")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	ctx := context.Background()
	repo := "rescan-" + randomID()
	projectID := createGitHubRescanProject(t, repo)
	issue := createGitHubRescanIssue(t, projectID, "Recover canonical PR link")
	prNumber := int32(69)
	secondPRNumber := int32(70)
	headSHA := strings.Repeat("a", 40)
	secondHeadSHA := strings.Repeat("b", 40)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case fmt.Sprintf("/repos/acme/%s/pulls/%d", repo, prNumber):
			writeJSON(w, http.StatusOK, map[string]any{
				"number": prNumber, "html_url": "https://github.com/acme/" + repo + "/pull/69",
				"title": "Fix " + issue.Identifier, "body": "Closes " + issue.Identifier,
				"state": "open", "draft": false, "merged": false,
				"created_at": "2026-08-24T08:00:00Z", "updated_at": "2026-08-24T09:00:00Z",
				"mergeable_state": "clean", "additions": 12, "deletions": 3, "changed_files": 2,
				"head": map[string]any{"ref": "agent/test/" + strings.ToLower(issue.Identifier), "sha": headSHA},
				"user": map[string]any{"login": "octocat", "avatar_url": "https://avatars.example/octocat"},
			})
		case fmt.Sprintf("/repos/acme/%s/pulls/%d", repo, secondPRNumber):
			writeJSON(w, http.StatusOK, map[string]any{
				"number": secondPRNumber, "html_url": "https://github.com/acme/" + repo + "/pull/70",
				"title": "Follow-up without close intent", "body": "No issue reference in the body",
				"state": "open", "draft": false, "merged": false,
				"created_at": "2026-08-24T10:00:00Z", "updated_at": "2026-08-24T11:00:00Z",
				"mergeable_state": "clean", "additions": 4, "deletions": 1, "changed_files": 1,
				"head": map[string]any{"ref": "agent/test/" + strings.ToLower(issue.Identifier), "sha": secondHeadSHA},
				"user": map[string]any{"login": "octocat", "avatar_url": "https://avatars.example/octocat"},
			})
		case fmt.Sprintf("/repos/acme/%s/commits/%s/check-suites", repo, headSHA):
			writeJSON(w, http.StatusOK, map[string]any{"check_suites": []map[string]any{
				{
					"id": 899, "head_sha": headSHA, "status": "queued", "conclusion": nil,
					"updated_at": "2026-08-24T08:59:00Z", "app": map[string]any{"id": 75},
				},
				{
					"id": 900, "head_sha": headSHA, "status": "in_progress", "conclusion": nil,
					"updated_at": "2026-08-24T09:00:00Z", "app": map[string]any{"id": 76},
				},
				{
					"id": 901, "head_sha": headSHA, "status": "completed", "conclusion": "success",
					"updated_at": "2026-08-24T09:01:00Z", "app": map[string]any{"id": 77},
				},
			}})
		case fmt.Sprintf("/repos/acme/%s/commits/%s/check-suites", repo, secondHeadSHA):
			writeJSON(w, http.StatusOK, map[string]any{"check_suites": []map[string]any{
				{
					"id": 902, "head_sha": secondHeadSHA, "status": "completed", "conclusion": "success",
					"updated_at": "2026-08-24T11:01:00Z", "app": map[string]any{"id": 77},
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	previousAPIBase := githubAPIBase
	githubAPIBase = api.URL
	t.Cleanup(func() { githubAPIBase = previousAPIBase })

	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/pull-requests/rescan", map[string]any{
		"pull_request_number": prNumber,
	})
	req = withURLParam(req, "id", issue.ID)
	testHandler.RescanIssuePullRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("RescanIssuePullRequest: got %d: %s", rec.Code, rec.Body.String())
	}

	linked, err := testHandler.Queries.ListPullRequestsByIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("ListPullRequestsByIssue: %v", err)
	}
	if len(linked) != 1 {
		t.Fatalf("linked PR count = %d, want 1", len(linked))
	}
	if linked[0].PrNumber != prNumber || linked[0].HeadSha != headSHA {
		t.Fatalf("linked PR = #%d@%s, want #%d@%s", linked[0].PrNumber, linked[0].HeadSha, prNumber, headSHA)
	}
	if linked[0].InstallationID != githubRESTRescanInstallationID {
		t.Fatalf("installation_id = %d, want REST rescan sentinel %d", linked[0].InstallationID, githubRESTRescanInstallationID)
	}
	if linked[0].ChecksPassed != 1 || linked[0].ChecksFailed != 0 || linked[0].ChecksPending != 0 {
		t.Fatalf("check counts = passed:%d failed:%d pending:%d", linked[0].ChecksPassed, linked[0].ChecksFailed, linked[0].ChecksPending)
	}
	// Simulate a row written by the original REST recovery implementation.
	// A repeated scan must remove this non-terminal suite from the current
	// head rather than leaving a permanent false-pending gate.
	if _, err = testPool.Exec(ctx, `
		INSERT INTO github_pull_request_check_suite
			(pr_id, suite_id, head_sha, app_id, conclusion, status, updated_at)
		VALUES ($1, 898, $2, 74, NULL, 'queued', now())`, linked[0].ID, headSHA); err != nil {
		t.Fatalf("seed stale queued suite: %v", err)
	}

	// Retrying the same authoritative scan through the agent surface is
	// idempotent and does not require borrowing the owner's human identity.
	rec = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/pull-requests/rescan", map[string]any{
		"pull_request_number": prNumber,
	})
	agentID := createHandlerTestAgent(t, "GitHub PR Rescan", []byte("[]"))
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withURLParam(req, "id", issue.ID)
	testHandler.RescanAgentIssuePullRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("RescanAgentIssuePullRequest: got %d: %s", rec.Code, rec.Body.String())
	}
	linked, err = testHandler.Queries.ListPullRequestsByIssue(ctx, parseUUID(issue.ID))
	if err != nil || len(linked) != 1 {
		t.Fatalf("idempotent linked PRs = %d, err=%v", len(linked), err)
	}
	if linked[0].ChecksPassed != 1 || linked[0].ChecksPending != 0 {
		t.Fatalf("idempotent check counts = passed:%d pending:%d, want 1/0", linked[0].ChecksPassed, linked[0].ChecksPending)
	}

	// A second PR for the same Issue creates a second canonical link. A key
	// present only in the branch links the PR but must never set close_intent.
	rec = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/agent/issues/"+issue.ID+"/pull-requests/rescan", map[string]any{
		"pull_request_number": secondPRNumber,
	})
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withURLParam(req, "id", issue.ID)
	testHandler.RescanAgentIssuePullRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second PR rescan: got %d: %s", rec.Code, rec.Body.String())
	}
	linked, err = testHandler.Queries.ListPullRequestsByIssue(ctx, parseUUID(issue.ID))
	if err != nil || len(linked) != 2 {
		t.Fatalf("same-Issue linked PRs = %d, err=%v, want 2", len(linked), err)
	}
	var firstCloseIntent, secondCloseIntent bool
	if err = testPool.QueryRow(ctx, `
		SELECT ipr.close_intent
		FROM issue_pull_request ipr
		JOIN github_pull_request pr ON pr.id = ipr.pull_request_id
		WHERE ipr.issue_id = $1 AND pr.pr_number = $2`, issue.ID, prNumber).Scan(&firstCloseIntent); err != nil {
		t.Fatalf("read first close_intent: %v", err)
	}
	if err = testPool.QueryRow(ctx, `
		SELECT ipr.close_intent
		FROM issue_pull_request ipr
		JOIN github_pull_request pr ON pr.id = ipr.pull_request_id
		WHERE ipr.issue_id = $1 AND pr.pr_number = $2`, issue.ID, secondPRNumber).Scan(&secondCloseIntent); err != nil {
		t.Fatalf("read second close_intent: %v", err)
	}
	if !firstCloseIntent || secondCloseIntent {
		t.Fatalf("close_intent first=%t second=%t, want true/false", firstCloseIntent, secondCloseIntent)
	}
}

func TestRescanIssuePullRequestRejectsUnrelatedPR(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("GITHUB_APP_SLUG", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	repo := "rescan-unrelated-" + randomID()
	projectID := createGitHubRescanProject(t, repo)
	issue := createGitHubRescanIssue(t, projectID, "Reject unrelated PR")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"number": 70, "html_url": "https://github.com/acme/" + repo + "/pull/70",
			"title": "Fix OTHER-999", "body": "Closes OTHER-999", "state": "open",
			"created_at": "2026-08-24T08:00:00Z", "updated_at": "2026-08-24T09:00:00Z",
			"head": map[string]any{"ref": "fix/other-999", "sha": strings.Repeat("b", 40)},
			"user": map[string]any{"login": "octocat"},
		})
	}))
	defer api.Close()
	previousAPIBase := githubAPIBase
	githubAPIBase = api.URL
	t.Cleanup(func() { githubAPIBase = previousAPIBase })

	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/pull-requests/rescan", map[string]any{
		"pull_request_number": 70,
	})
	req = withURLParam(req, "id", issue.ID)
	testHandler.RescanIssuePullRequest(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("RescanIssuePullRequest unrelated PR: got %d: %s", rec.Code, rec.Body.String())
	}

	linked, err := testHandler.Queries.ListPullRequestsByIssue(context.Background(), parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("ListPullRequestsByIssue: %v", err)
	}
	if len(linked) != 0 {
		t.Fatalf("unrelated PR created %d links, want 0", len(linked))
	}
}

func TestRescanIssuePullRequestRequiresWorkspaceAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}

	repo := "rescan-role-" + randomID()
	projectID := createGitHubRescanProject(t, repo)
	issue := createGitHubRescanIssue(t, projectID, "Restrict PR rescan")
	memberID := createWorkspaceMemberUser(t, "PR Rescan Member", "pr-rescan-"+randomID()+"@multica.test")

	rec := httptest.NewRecorder()
	req := newRequestAs(memberID, http.MethodPost, "/api/issues/"+issue.ID+"/pull-requests/rescan", map[string]any{
		"pull_request_number": 71,
	})
	req = withURLParam(req, "id", issue.ID)
	testHandler.RescanIssuePullRequest(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member RescanIssuePullRequest: got %d: %s", rec.Code, rec.Body.String())
	}
}

func createGitHubRescanProject(t *testing.T, repo string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "GitHub rescan project " + repo,
	})
	testHandler.CreateProject(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateProject: %d %s", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	rec = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/projects/"+project.ID+"/resources", map[string]any{
		"resource_type": "github_repo",
		"resource_ref":  map[string]any{"url": "https://github.com/acme/" + repo},
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.CreateProjectResource(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateProjectResource: %d %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, project.ID)
	})
	return project.ID
}

func createGitHubRescanIssue(t *testing.T, projectID, title string) IssueResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      title + " " + time.Now().Format(time.RFC3339Nano),
		"status":     "in_review",
		"project_id": projectID,
	})
	testHandler.CreateIssue(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", rec.Code, rec.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(rec.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue_pull_request WHERE issue_id = $1`, issue.ID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM github_pull_request WHERE workspace_id = $1 AND repo_name LIKE 'rescan-%'`, testWorkspaceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
	})
	return issue
}
