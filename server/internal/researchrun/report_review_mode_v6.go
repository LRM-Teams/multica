package researchrun

const (
	V6ReportReviewModeIsolated     = "isolated"
	V6ReportReviewModeDocumentOnly = "document_only"
)

// ResolveV6ReportReviewMode prefers the isolated renderer when it can actually
// run. Missing renderer or frame ancestors must not block Director publish.
func ResolveV6ReportReviewMode(rendererConfigured bool, frameAncestors []string) string {
	if !rendererConfigured {
		return V6ReportReviewModeDocumentOnly
	}
	ancestors, err := NormalizeV6ReportFrameAncestors(frameAncestors)
	if err != nil || len(ancestors) == 0 {
		return V6ReportReviewModeDocumentOnly
	}
	return V6ReportReviewModeIsolated
}

// V6ReportCompiledAPIHeaders are for the authenticated compiled-document GET.
// They must not include Clear-Site-Data: that header would wipe the main app.
func V6ReportCompiledAPIHeaders(csp string) map[string]string {
	return map[string]string{
		"Content-Type":                 "text/html; charset=utf-8",
		"Content-Security-Policy":      csp,
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
		"Cache-Control":                "private, no-store",
		"Content-Disposition":          `inline; filename="report.html"`,
		"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
}
