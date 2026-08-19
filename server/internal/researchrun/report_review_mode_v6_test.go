package researchrun

import "testing"

func TestResolveV6ReportReviewModeFallsBackWhenRendererMissing(t *testing.T) {
	if got := ResolveV6ReportReviewMode(false, []string{"https://app.example.test"}); got != V6ReportReviewModeDocumentOnly {
		t.Fatalf("got %q", got)
	}
}

func TestResolveV6ReportReviewModeFallsBackWhenFrameAncestorsMissing(t *testing.T) {
	if got := ResolveV6ReportReviewMode(true, nil); got != V6ReportReviewModeDocumentOnly {
		t.Fatalf("got %q", got)
	}
}

func TestResolveV6ReportReviewModeUsesIsolatedRendererWhenConfigured(t *testing.T) {
	if got := ResolveV6ReportReviewMode(true, []string{"https://app.example.test"}); got != V6ReportReviewModeIsolated {
		t.Fatalf("got %q", got)
	}
}

func TestV6ReportCompiledAPIHeadersNeverClearSiteData(t *testing.T) {
	headers := V6ReportCompiledAPIHeaders(v6ReportCSP(nil, nil))
	if headers["Content-Type"] != "text/html; charset=utf-8" {
		t.Fatalf("content type: %q", headers["Content-Type"])
	}
	if headers["X-Content-Type-Options"] != "nosniff" {
		t.Fatalf("missing nosniff")
	}
	if headers["Cache-Control"] != "private, no-store" {
		t.Fatalf("cache: %q", headers["Cache-Control"])
	}
	if _, ok := headers["Clear-Site-Data"]; ok {
		t.Fatal("compiled API must not wipe the authenticated origin")
	}
}

func TestV6ReportCompiledDocumentUsesVerifiedRead(t *testing.T) {
	assertReportSource(t, "../handler/research_report_document.go", "GetResearchV6ReportCompiled", "ReadVerified", "V6ReportCompiledAPIHeaders")
	assertReportSource(t, "../../cmd/server/router.go", `r.Get("/v6/runs/{runId}/reports/{reportId}/compiled", h.GetResearchV6ReportCompiled)`)
}
