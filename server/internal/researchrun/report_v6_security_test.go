package researchrun

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func reportTestResource(id, path, role, media, body string) V6ReportResource {
	return V6ReportResource{ResourceID: id, Path: path, Role: role, MediaType: media, ByteSize: int64(len(body)), ContentHash: ArtifactContentHashFromCanonicalJSON([]byte(body)), Bytes: []byte(body)}
}
func TestV6ReportCompilerRejectsNetworkAndPrivilegeEscapes(t *testing.T) {
	attacks := []string{`<img src="https://evil.test/x">`, `<iframe srcdoc="x"></iframe>`, `<form action="/api"></form>`, `<div onclick="fetch('/api')">x</div>`, `<script>window.open('x')</script>`, `<base href="/">`}
	for _, attack := range attacks {
		_, err := CompileV6ReportPackage([]V6ReportResource{reportTestResource("doc", "index.html", "document", "text/html", attack)}, "doc", "fallback")
		if !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("accepted %q: %v", attack, err)
		}
	}
}
func TestV6ReportCompilerInlinesPackageResourcesAndBuildsCSP(t *testing.T) {
	doc := `<html><head><link rel="stylesheet" href="app.css"></head><body><img src="plot.png"><script src="app.js"></script></body></html>`
	out, err := CompileV6ReportPackage([]V6ReportResource{reportTestResource("doc", "index.html", "document", "text/html", doc), reportTestResource("css", "app.css", "style", "text/css", "body{color:#fff}"), reportTestResource("js", "app.js", "script", "text/javascript", "document.body.dataset.ok='1'"), reportTestResource("img", "plot.png", "image", "image/png", "png")}, "doc", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.HTML), "app.js") || !strings.Contains(string(out.HTML), "data:image/png") || strings.Contains(out.CSP, "unsafe-") {
		t.Fatalf("unsafe output: %s %s", out.HTML, out.CSP)
	}
}
func assertReportSource(t *testing.T, file string, parts ...string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		if !strings.Contains(string(raw), part) {
			t.Fatalf("%s missing %q", file, part)
		}
	}
}
func TestV6ReportUploadCreateTransactionBoundary(t *testing.T) {
	assertReportSource(t, "postgres_report_v6.go", "research_report_upload_session", "commitResearchTx")
}
func TestV6ReportUploadCompleteTransactionBoundary(t *testing.T) {
	assertReportSource(t, "postgres_report_v6.go", "VerifyImmutableUpload", "research_report_resource")
}
func TestV6ReportPackageClaimTransactionBoundary(t *testing.T) {
	assertReportSource(t, "postgres_report_package_v6.go", "FOR UPDATE SKIP LOCKED", "status='processing'")
}
func TestV6ReportPackageAcceptTransactionBoundary(t *testing.T) {
	assertReportSource(t, "postgres_report_package_v6.go", "research_report SET", "v6_report_draft_accepted")
}
func TestV6ReportReviewTransactionBoundary(t *testing.T) {
	assertReportSource(t, "postgres_report_review_v6.go", "RenderReport", "research_report_review", "status=$4")
}

func TestV6ReportWorkCreateTransactionBoundary(t *testing.T) {
	assertReportSource(t, "postgres_report_work_v6.go", "lockRunForMutation", "role='reporter'", "selectV6ReportInputs(ctx, tx", "registerDraftReportRevisionPassportTx", "research_report_input", "research_work_item", "'report_package_submission',$11::jsonb,1,now()", "commitResearchTx")
	assertReportSource(t, "artifact_report.go", "'report_revision', NULL", "current_version", "ErrResultConflict")
}

func TestV6DirectorProposalApplyFailureIsObservable(t *testing.T) {
	assertReportSource(t, "postgres_director_action_v6.go", "research V6 Director proposal canonical apply failed", "submission_id", "run_id", "error")
}
