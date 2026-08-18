package researchrun

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const V6ReportIframeSandbox = "allow-scripts"

func V6ReportResponseHeaders(csp string) map[string]string {
	return map[string]string{
		"Content-Security-Policy":      csp,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Embedder-Policy": "require-corp",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Origin-Agent-Cluster":         "?1",
		"Referrer-Policy":              "no-referrer",
		"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
		"X-Content-Type-Options":       "nosniff",
		"Cache-Control":                "private, no-store",
		"Clear-Site-Data":              `"cache", "cookies", "storage"`,
	}
}

func v6ReportCSP(scripts, styles []string) string {
	csp := "sandbox allow-scripts; default-src 'none'; base-uri 'none'; connect-src 'none'; form-action 'none'; navigate-to 'none'; frame-ancestors 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; img-src data:; font-src data:; media-src 'none'; object-src 'none'; manifest-src 'none'; trusted-types 'none'; require-trusted-types-for 'script'; script-src-attr 'none'; style-src-attr 'none'; script-src"
	for _, hash := range scripts {
		if validV6CSPHash(hash) {
			csp += " '" + hash + "'"
		}
	}
	csp += "; style-src"
	for _, hash := range styles {
		if validV6CSPHash(hash) {
			csp += " '" + hash + "'"
		}
	}
	return csp + ";"
}

var v6CSPHashPattern = regexp.MustCompile(`^sha256-[A-Za-z0-9+/]{43}=$`)

func validV6CSPHash(value string) bool { return v6CSPHashPattern.MatchString(value) }

func ValidateV6ReportCSPHashes(scripts, styles []string) error {
	if len(scripts) > 256 || len(styles) > 256 {
		return fmt.Errorf("%w: report CSP hash limit", ErrInvalidContract)
	}
	for _, value := range append(append([]string(nil), scripts...), styles...) {
		if !validV6CSPHash(value) {
			return fmt.Errorf("%w: invalid report CSP hash", ErrInvalidContract)
		}
	}
	return nil
}

func V6ReportDocumentCSP(scripts, styles []string, frameAncestors []string) string {
	csp := v6ReportCSP(scripts, styles)
	ancestors := "'none'"
	if len(frameAncestors) > 0 {
		ancestors = strings.Join(frameAncestors, " ")
	}
	return strings.Replace(csp, "frame-ancestors 'none'", "frame-ancestors "+ancestors, 1)
}

func NormalizeV6ReportFrameAncestors(values []string) ([]string, error) {
	unique := map[string]struct{}{}
	for _, raw := range values {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%w: invalid report frame ancestor", ErrInvalidContract)
		}
		unique[parsed.Scheme+"://"+parsed.Host] = struct{}{}
	}
	out := make([]string, 0, len(unique))
	for value := range unique {
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}
