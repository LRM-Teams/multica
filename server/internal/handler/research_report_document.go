package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func (h *Handler) researchReportCapability(reportID, packageHash string, expiry int64) string {
	mac := hmac.New(sha256.New, []byte(h.cfg.ResearchReportCapabilitySecret))
	_, _ = io.WriteString(mac, reportID+"\n"+packageHash+"\n"+strconv.FormatInt(expiry, 10))
	return hex.EncodeToString(mac.Sum(nil))
}
func (h *Handler) researchReportSandboxURL(reportID, packageHash string) (string, error) {
	base, err := url.Parse(h.cfg.ResearchReportOrigin)
	if err != nil || base.Scheme != "https" || base.Host == "" || len(h.cfg.ResearchReportCapabilitySecret) < 32 {
		return "", fmt.Errorf("report origin unavailable")
	}
	ancestors, err := researchrun.NormalizeV6ReportFrameAncestors(h.cfg.ResearchReportFrameAncestors)
	if err != nil || len(ancestors) == 0 {
		return "", fmt.Errorf("report frame policy unavailable")
	}
	applicationOrigins := append([]string(nil), ancestors...)
	if strings.TrimSpace(h.cfg.PublicURL) != "" {
		applicationOrigins = append(applicationOrigins, h.cfg.PublicURL)
	}
	if !researchrun.ValidateV6ReportOrigin(h.cfg.ResearchReportOrigin, applicationOrigins) {
		return "", fmt.Errorf("report origin must be isolated from application origins")
	}
	exp := time.Now().Add(5 * time.Minute).Unix()
	base.Path = "/research/" + reportID + "/" + packageHash + "/index.html"
	base.RawQuery = url.Values{"exp": {strconv.FormatInt(exp, 10)}, "sig": {h.researchReportCapability(reportID, packageHash, exp)}}.Encode()
	return base.String(), nil
}
func (h *Handler) GetResearchV6Reports(w http.ResponseWriter, r *http.Request) {
	workspace := h.resolveWorkspaceID(r)
	runUUID, valid := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "runId")
	if !valid {
		return
	}
	run := uuidToString(runUUID)
	rows, err := h.DB.Query(r.Context(), `SELECT r.id::text,r.revision,r.status,r.title,r.summary,COALESCE(r.package_hash,''),COALESCE(r.document_content_hash,''),r.published_at,r.created_at,
		COALESCE((SELECT a.assigned_agent_id::text FROM research_work_item w JOIN research_work_item_attempt a ON a.work_item_id=w.id WHERE w.target_kind='report' AND w.target_id=r.id AND a.status='succeeded' ORDER BY a.completed_at DESC LIMIT 1),''),
		(SELECT count(*)::int FROM research_report_input i WHERE i.report_id=r.id AND i.report_revision=r.revision),
		COALESCE((SELECT review.decision FROM research_report_review review WHERE review.report_id=r.id AND review.report_revision=r.revision ORDER BY review.created_at DESC LIMIT 1),''),
		COALESCE((SELECT review.reason FROM research_report_review review WHERE review.report_id=r.id AND review.report_revision=r.revision ORDER BY review.created_at DESC LIMIT 1),'')
		FROM research_report r WHERE r.workspace_id=$1::uuid AND r.session_id=$2::uuid ORDER BY r.revision DESC`, workspace, run)
	if err != nil {
		writeError(w, 500, "failed to list reports")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, status, title, summary, pkg, doc, authorAgentID, reviewDecision, reviewReason string
		var revision, inputCount int
		var published *time.Time
		var created time.Time
		if rows.Scan(&id, &revision, &status, &title, &summary, &pkg, &doc, &published, &created, &authorAgentID, &inputCount, &reviewDecision, &reviewReason) != nil {
			writeError(w, 500, "failed to list reports")
			return
		}
		item := map[string]any{"id": id, "revision": revision, "status": status, "title": title, "summary": summary, "package_hash": pkg, "document_content_hash": doc, "published_at": published, "created_at": created, "author_agent_id": authorAgentID, "input_count": inputCount, "latest_review": map[string]any{"decision": reviewDecision, "reason": reviewReason}}
		if pkg != "" {
			if sandbox, e := h.researchReportSandboxURL(id, pkg); e == nil {
				item["sandbox_url"] = sandbox
				item["report_origin"] = strings.TrimRight(h.cfg.ResearchReportOrigin, "/")
			}
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"reports": items})
}
func (h *Handler) GetResearchV6Report(w http.ResponseWriter, r *http.Request) {
	workspace := h.resolveWorkspaceID(r)
	runUUID, valid := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "runId")
	if !valid {
		return
	}
	reportUUID, valid := parseUUIDOrBadRequest(w, chi.URLParam(r, "reportId"), "reportId")
	if !valid {
		return
	}
	run, id := uuidToString(runUUID), uuidToString(reportUUID)
	var revision int
	var status, title, summary, plain, pkg, doc string
	var outline, citations json.RawMessage
	err := h.DB.QueryRow(r.Context(), `SELECT revision,status,title,summary,plain_text,COALESCE(package_hash,''),COALESCE(document_content_hash,''),outline,citations FROM research_report WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspace, run, id).Scan(&revision, &status, &title, &summary, &plain, &pkg, &doc, &outline, &citations)
	if err != nil {
		writeError(w, 404, "report not found")
		return
	}
	out := map[string]any{"id": id, "revision": revision, "status": status, "title": title, "summary": summary, "plain_text": plain, "package_hash": pkg, "document_content_hash": doc, "outline": outline, "citations": citations}
	inputRows, queryErr := h.DB.Query(r.Context(), `SELECT branch_id::text,node_artifact_version_id::text,input_role,ordinal,content_hash FROM research_report_input WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND report_id=$3::uuid AND report_revision=$4 ORDER BY ordinal`, workspace, run, id, revision)
	if queryErr != nil {
		writeError(w, 500, "failed to load report inputs")
		return
	}
	inputs := []map[string]any{}
	for inputRows.Next() {
		var branchID, versionID, role, hash string
		var ordinal int
		if inputRows.Scan(&branchID, &versionID, &role, &ordinal, &hash) != nil {
			inputRows.Close()
			writeError(w, 500, "failed to load report inputs")
			return
		}
		inputs = append(inputs, map[string]any{"branch_id": branchID, "node_artifact_version_id": versionID, "input_role": role, "ordinal": ordinal, "content_hash": hash})
	}
	inputRows.Close()
	reviewRows, queryErr := h.DB.Query(r.Context(), `SELECT id::text,decision,reason,input_state_version,COALESCE(render_artifact_version_id::text,''),render_diagnostics,follow_up_work_item_refs,created_at FROM research_report_review WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND report_id=$3::uuid AND report_revision=$4 ORDER BY created_at,id`, workspace, run, id, revision)
	if queryErr != nil {
		writeError(w, 500, "failed to load report reviews")
		return
	}
	reviews := []map[string]any{}
	for reviewRows.Next() {
		var reviewID, decision, reason, renderVersionID string
		var reviewState int64
		var diagnostics, followUps json.RawMessage
		var created time.Time
		if reviewRows.Scan(&reviewID, &decision, &reason, &reviewState, &renderVersionID, &diagnostics, &followUps, &created) != nil {
			reviewRows.Close()
			writeError(w, 500, "failed to load report reviews")
			return
		}
		reviews = append(reviews, map[string]any{"id": reviewID, "decision": decision, "reason": reason, "input_state_version": reviewState, "render_artifact_version_id": renderVersionID, "render_diagnostics": diagnostics, "follow_up_work_item_refs": followUps, "created_at": created})
	}
	reviewRows.Close()
	out["input_refs"] = inputs
	out["reviews"] = reviews
	if pkg != "" {
		if sandbox, e := h.researchReportSandboxURL(id, pkg); e == nil {
			out["sandbox_url"] = sandbox
			out["report_origin"] = strings.TrimRight(h.cfg.ResearchReportOrigin, "/")
		}
	}
	writeJSON(w, 200, out)
}
func (h *Handler) ServeResearchV6ReportDocument(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	reportUUID, valid := parseUUIDOrBadRequest(w, chi.URLParam(r, "reportId"), "reportId")
	if !valid {
		return
	}
	id, pkg := uuidToString(reportUUID), chi.URLParam(r, "packageHash")
	exp, _ := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	sig, decodeErr := hex.DecodeString(r.URL.Query().Get("sig"))
	expected, _ := hex.DecodeString(h.researchReportCapability(id, pkg, exp))
	origin, originErr := url.Parse(h.cfg.ResearchReportOrigin)
	if decodeErr != nil || originErr != nil || !strings.EqualFold(r.Host, origin.Host) || exp < time.Now().Unix() || exp > time.Now().Add(10*time.Minute).Unix() || !hmac.Equal(sig, expected) {
		http.Error(w, "not found", 404)
		return
	}
	var key, generation, documentHash string
	var documentSize int64
	var scripts, styles json.RawMessage
	err := h.DB.QueryRow(r.Context(), `SELECT document_storage_key,document_storage_generation,document_content_hash,document_byte_size,csp_script_hashes,csp_style_hashes FROM research_report WHERE id=$1::uuid AND package_hash=$2 AND status IN('draft','published')`, id, pkg).Scan(&key, &generation, &documentHash, &documentSize, &scripts, &styles)
	if err != nil || documentSize < 0 || documentSize > researchrun.V6ReportMaxCompiledBytes {
		http.Error(w, "not found", 404)
		return
	}
	var scriptList, styleList []string
	if json.Unmarshal(scripts, &scriptList) != nil || json.Unmarshal(styles, &styleList) != nil || researchrun.ValidateV6ReportCSPHashes(scriptList, styleList) != nil {
		http.Error(w, "document unavailable", 503)
		return
	}
	ancestors, policyErr := researchrun.NormalizeV6ReportFrameAncestors(h.cfg.ResearchReportFrameAncestors)
	if policyErr != nil || len(ancestors) == 0 {
		http.Error(w, "document unavailable", 503)
		return
	}
	csp := researchrun.V6ReportDocumentCSP(scriptList, styleList, ancestors)
	reader, err := researchReportStorageAdapter{store: h.Storage}.ReadVerified(r.Context(), key, generation)
	if err != nil {
		http.Error(w, "document unavailable", 503)
		return
	}
	defer reader.Close()
	for k, v := range researchrun.V6ReportResponseHeaders(csp) {
		w.Header().Set(k, v)
	}
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.FormatInt(documentSize, 10))
	w.Header().Set("ETag", `"`+documentHash+`"`)
	_, _ = io.Copy(w, io.LimitReader(reader, documentSize))
}
