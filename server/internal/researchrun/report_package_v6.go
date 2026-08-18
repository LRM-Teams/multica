package researchrun

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"mime"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const v6ReportMaxBytes = 16 << 20
const V6ReportMaxCompiledBytes = 24 << 20

var reportForbidden = regexp.MustCompile(`(?is)<\s*(base|form|object|embed|iframe|frame|frameset|link|meta|video|audio)\b|\bsrcdoc\s*=|\bon[a-z]+\s*=|\bstyle\s*=|\b(fetch|XMLHttpRequest|WebSocket|EventSource|Worker|SharedWorker|ServiceWorker|sendBeacon|window\.open|localStorage|sessionStorage|indexedDB|document\.cookie|document\.domain|document\.createElement|document\.write|innerHTML|outerHTML|insertAdjacentHTML|setAttribute|history\.|navigation\.|location|eval|WebAssembly)\b|\b(?:parent|top)\s*\.|\b(?:import|Function)\s*\(|\.click\s*\(|\.(?:src|href)\s*=|(?:src|href)\s*=\s*["']\s*(?:https?:|//|blob:)|@import\b|url\(\s*["']?\s*(?:https?:|//|blob:)`)

var reportAgentProvidedURL = regexp.MustCompile(`(?is)(?:src|href)\s*=\s*["']\s*(?:https?:|//|data:|blob:)`)

func CompileV6ReportPackage(resources []V6ReportResource, documentID, plainText string) (CompiledV6Report, error) {
	return CompileV6ReportPackageWithMetadata(resources, documentID, plainText, V6ReportPackageMetadata{})
}

func CompileV6ReportPackageWithMetadata(resources []V6ReportResource, documentID, plainText string, metadata V6ReportPackageMetadata) (CompiledV6Report, error) {
	if len(resources) == 0 || !utf8.ValidString(plainText) || strings.TrimSpace(plainText) == "" || len(plainText) > 1<<20 {
		return CompiledV6Report{}, ErrInvalidContract
	}
	byID, folded, total := map[string]V6ReportResource{}, map[string]struct{}{}, 0
	roleCounts := map[string]int{}
	for _, r := range resources {
		clean := path.Clean(r.Path)
		lower := strings.ToLower(clean)
		if clean != r.Path || strings.HasPrefix(clean, "/") || clean == "." || strings.Contains(clean, "..") {
			return CompiledV6Report{}, fmt.Errorf("%w: report path", ErrInvalidContract)
		}
		if _, ok := folded[lower]; ok {
			return CompiledV6Report{}, fmt.Errorf("%w: duplicate report path", ErrInvalidContract)
		}
		folded[lower] = struct{}{}
		if r.ResourceID == "" {
			return CompiledV6Report{}, fmt.Errorf("%w: empty resource id", ErrInvalidContract)
		}
		if _, duplicate := byID[r.ResourceID]; duplicate {
			return CompiledV6Report{}, fmt.Errorf("%w: duplicate resource id", ErrInvalidContract)
		}
		if !validV6ReportMediaType(r.Role, r.MediaType) {
			return CompiledV6Report{}, fmt.Errorf("%w: report media type", ErrInvalidContract)
		}
		roleCounts[r.Role]++
		if (r.Role == "script" || r.Role == "style") && !utf8.Valid(r.Bytes) {
			return CompiledV6Report{}, fmt.Errorf("%w: non-UTF-8 active resource", ErrInvalidContract)
		}
		if int64(len(r.Bytes)) != r.ByteSize || ArtifactContentHashFromCanonicalJSON(r.Bytes) != r.ContentHash {
			return CompiledV6Report{}, fmt.Errorf("%w: resource integrity", ErrInvalidContract)
		}
		total += len(r.Bytes)
		byID[r.ResourceID] = r
	}
	if total > v6ReportMaxBytes || roleCounts["document"] != 1 || roleCounts["script"] > 256 || roleCounts["style"] > 256 || roleCounts["font"] > 64 || roleCounts["image"] > 512 {
		return CompiledV6Report{}, fmt.Errorf("%w: report size", ErrInvalidContract)
	}
	doc, ok := byID[documentID]
	if !ok || doc.Role != "document" || !utf8.Valid(doc.Bytes) {
		return CompiledV6Report{}, ErrInvalidContract
	}
	html := string(doc.Bytes)
	if strings.Count(html, "<") > 100000 {
		return CompiledV6Report{}, fmt.Errorf("%w: report DOM limit", ErrInvalidContract)
	}
	if reportAgentProvidedURL.MatchString(html) {
		return CompiledV6Report{}, fmt.Errorf("%w: agent-provided URL", ErrInvalidContract)
	}
	scripts, styles, cspScripts, cspStyles := []string{}, []string{}, []string{}, []string{}
	ordered := append([]V6ReportResource(nil), resources...)
	sort.SliceStable(ordered, func(i, j int) bool {
		active := func(role string) int {
			if role == "script" || role == "style" {
				return 0
			}
			return 1
		}
		return active(ordered[i].Role) < active(ordered[j].Role)
	})
	for _, r := range ordered {
		if r.ResourceID == documentID {
			continue
		}
		switch r.Role {
		case "script":
			for _, quote := range []string{"\"", "'"} {
				html = strings.ReplaceAll(html, "<script src="+quote+r.Path+quote+"></script>", "<script>"+string(r.Bytes)+"</script>")
			}
		case "style":
			for _, quote := range []string{"\"", "'"} {
				html = strings.ReplaceAll(html, "<link rel="+quote+"stylesheet"+quote+" href="+quote+r.Path+quote+">", "<style>"+string(r.Bytes)+"</style>")
			}
		default:
			encoded := base64.StdEncoding.EncodeToString(r.Bytes)
			mediaType := r.MediaType
			if mediaType == "" {
				mediaType = mime.TypeByExtension(path.Ext(r.Path))
			}
			uri := "data:" + mediaType + ";base64," + encoded
			html = strings.ReplaceAll(html, r.Path, uri)
		}
	}
	if reportForbidden.MatchString(html) {
		return CompiledV6Report{}, fmt.Errorf("%w: unsafe report document", ErrInvalidContract)
	}
	attributes := regexp.MustCompile(`(?is)\b(src|href)\s*=\s*["']([^"']*)["']`).FindAllStringSubmatch(html, -1)
	for _, attribute := range attributes {
		if !strings.HasPrefix(attribute[2], "data:") && !strings.HasPrefix(attribute[2], "#") {
			return CompiledV6Report{}, fmt.Errorf("%w: unresolved report resource", ErrInvalidContract)
		}
	}
	for _, r := range resources {
		if r.ResourceID != documentID && strings.Contains(html, r.Path) {
			return CompiledV6Report{}, fmt.Errorf("%w: unresolved report resource", ErrInvalidContract)
		}
	}
	re := regexp.MustCompile(`(?is)<script>(.*?)</script>`)
	if len(regexp.MustCompile(`(?is)<script\b`).FindAllStringIndex(html, -1)) != len(re.FindAllStringSubmatch(html, -1)) {
		return CompiledV6Report{}, fmt.Errorf("%w: unsupported script element", ErrInvalidContract)
	}
	for _, m := range re.FindAllStringSubmatch(html, -1) {
		scripts = append(scripts, ArtifactContentHashFromCanonicalJSON([]byte(m[1])))
		cspScripts = append(cspScripts, cspHash([]byte(m[1])))
	}
	re = regexp.MustCompile(`(?is)<style>(.*?)</style>`)
	if len(regexp.MustCompile(`(?is)<style\b`).FindAllStringIndex(html, -1)) != len(re.FindAllStringSubmatch(html, -1)) {
		return CompiledV6Report{}, fmt.Errorf("%w: unsupported style element", ErrInvalidContract)
	}
	for _, m := range re.FindAllStringSubmatch(html, -1) {
		styles = append(styles, ArtifactContentHashFromCanonicalJSON([]byte(m[1])))
		cspStyles = append(cspStyles, cspHash([]byte(m[1])))
	}
	sort.Strings(scripts)
	sort.Strings(styles)
	sort.Strings(cspScripts)
	sort.Strings(cspStyles)
	scripts = slicesCompact(scripts)
	styles = slicesCompact(styles)
	cspScripts = slicesCompact(cspScripts)
	cspStyles = slicesCompact(cspStyles)
	out := []byte(html)
	if len(out) > V6ReportMaxCompiledBytes {
		return CompiledV6Report{}, fmt.Errorf("%w: compiled report size", ErrInvalidContract)
	}
	manifestResources := append([]V6ReportResource(nil), resources...)
	sort.Slice(manifestResources, func(i, j int) bool { return manifestResources[i].Path < manifestResources[j].Path })
	metadata.InputNodes = append([]V6NodeRef(nil), metadata.InputNodes...)
	sort.Slice(metadata.InputNodes, func(i, j int) bool { return metadata.InputNodes[i].VersionID < metadata.InputNodes[j].VersionID })
	pkg, _ := marshalV6CanonicalJSON(map[string]any{"document": ArtifactContentHashFromCanonicalJSON(out), "plain_text": plainText, "resources": manifestResources, "metadata": metadata})
	return CompiledV6Report{HTML: out, PlainText: plainText, DocumentHash: ArtifactContentHashFromCanonicalJSON(out), PackageHash: ArtifactContentHashFromCanonicalJSON(pkg), CSP: v6ReportCSP(cspScripts, cspStyles), ScriptHashes: scripts, StyleHashes: styles, CSPScriptHashes: cspScripts, CSPStyleHashes: cspStyles}, nil
}

func slicesCompact(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func validV6ReportMediaType(role, raw string) bool {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || mediaType != strings.ToLower(mediaType) {
		return false
	}
	switch role {
	case "document":
		return mediaType == "text/html"
	case "script":
		return mediaType == "text/javascript" || mediaType == "application/javascript"
	case "style":
		return mediaType == "text/css"
	case "image":
		return mediaType == "image/png" || mediaType == "image/jpeg" || mediaType == "image/gif" || mediaType == "image/webp"
	case "font":
		return mediaType == "font/woff" || mediaType == "font/woff2"
	case "data":
		return mediaType == "application/json" || mediaType == "application/octet-stream" || strings.HasPrefix(mediaType, "text/")
	default:
		return false
	}
}
func cspHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}
