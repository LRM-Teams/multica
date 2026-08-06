package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

const (
	memoryCurationLedgerDefaultDays  = 30
	memoryCurationLedgerMaxDays      = 90
	memoryCurationLedgerDefaultLimit = 50
	memoryCurationLedgerMaxLimit     = 200
	memoryCurationLedgerDefaultTZ    = "Asia/Shanghai"
)

type memoryCurationDailySummaryDay struct {
	Date               string `json:"date"`
	MemoryCandidates   int    `json:"memory_candidates"`
	SkillCandidates    int    `json:"skill_candidates"`
	TeamKnowledgeItems int    `json:"team_knowledge_items"`
	TeamSkills         int    `json:"team_skills"`
}

type memoryCurationDailySummaryResponse struct {
	Timezone string                          `json:"timezone"`
	Since    string                          `json:"since"`
	Until    string                          `json:"until"`
	Days     []memoryCurationDailySummaryDay `json:"days"`
}

type memoryCurationCandidateItem struct {
	ID            string  `json:"id"`
	SourceAgentID string  `json:"source_agent_id,omitempty"`
	SourceAgent   string  `json:"source_agent_name,omitempty"`
	RunID         string  `json:"run_id,omitempty"`
	CandidateType string  `json:"candidate_type"`
	Scope         string  `json:"scope"`
	Title         string  `json:"title"`
	Snippet       string  `json:"snippet"`
	Content       string  `json:"content,omitempty"`
	Confidence    float64 `json:"confidence"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
}

type memoryCurationCandidateListResponse struct {
	Items []memoryCurationCandidateItem `json:"items"`
	Total int                           `json:"total"`
}

type teamKnowledgeListItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet"`
	Content   string `json:"content,omitempty"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type teamKnowledgeListResponse struct {
	Items []teamKnowledgeListItem `json:"items"`
	Total int                     `json:"total"`
}

// ListMemoryCurationDailySummary returns per-day counts of self-review
// candidates and team knowledge promotions for the Evolution Center ledger.
func (h *Handler) ListMemoryCurationDailySummary(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	tz := strings.TrimSpace(r.URL.Query().Get("timezone"))
	if tz == "" {
		tz = memoryCurationLedgerDefaultTZ
	}
	if _, err := time.LoadLocation(tz); err != nil {
		writeError(w, http.StatusBadRequest, "invalid timezone")
		return
	}

	until := time.Now().UTC()
	if raw := strings.TrimSpace(r.URL.Query().Get("until")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until date")
			return
		}
		until = parsed
	}
	since := until.AddDate(0, 0, -(memoryCurationLedgerDefaultDays - 1))
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since date")
			return
		}
		since = parsed
	}
	if until.Before(since) {
		writeError(w, http.StatusBadRequest, "until must be on or after since")
		return
	}
	if until.Sub(since) > time.Duration(memoryCurationLedgerMaxDays)*24*time.Hour {
		writeError(w, http.StatusBadRequest, "date range cannot exceed 90 days")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		WITH cand AS (
		  SELECT (created_at AT TIME ZONE 'UTC' AT TIME ZONE $4)::date AS day,
		         count(*) FILTER (WHERE candidate_type IN ('skill', 'team_skill'))::int AS skill_candidates,
		         count(*) FILTER (WHERE candidate_type NOT IN ('skill', 'team_skill'))::int AS memory_candidates
		    FROM agent_memory_curation_candidate
		   WHERE workspace_id = $1::uuid
		     AND (created_at AT TIME ZONE 'UTC' AT TIME ZONE $4)::date BETWEEN $2::date AND $3::date
		   GROUP BY 1
		), team AS (
		  SELECT (created_at AT TIME ZONE 'UTC' AT TIME ZONE $4)::date AS day,
		         count(*)::int AS team_knowledge_items,
		         count(*) FILTER (WHERE kind = 'skill')::int AS team_skills
		    FROM team_knowledge_item
		   WHERE workspace_id = $1::uuid
		     AND (created_at AT TIME ZONE 'UTC' AT TIME ZONE $4)::date BETWEEN $2::date AND $3::date
		   GROUP BY 1
		)
		SELECT COALESCE(c.day, t.day)::text,
		       COALESCE(c.memory_candidates, 0),
		       COALESCE(c.skill_candidates, 0),
		       COALESCE(t.team_knowledge_items, 0),
		       COALESCE(t.team_skills, 0)
		  FROM cand c
		  FULL OUTER JOIN team t ON t.day = c.day
		 ORDER BY 1 DESC
	`, workspaceID, since.Format("2006-01-02"), until.Format("2006-01-02"), tz)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load daily summary")
		return
	}
	defer rows.Close()

	resp := memoryCurationDailySummaryResponse{
		Timezone: tz,
		Since:    since.Format("2006-01-02"),
		Until:    until.Format("2006-01-02"),
		Days:     []memoryCurationDailySummaryDay{},
	}
	for rows.Next() {
		var day memoryCurationDailySummaryDay
		if err := rows.Scan(&day.Date, &day.MemoryCandidates, &day.SkillCandidates, &day.TeamKnowledgeItems, &day.TeamSkills); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan daily summary")
			return
		}
		resp.Days = append(resp.Days, day)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListMemoryCurationCandidates lists self-review / pending candidates for a day.
func (h *Handler) ListMemoryCurationCandidates(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	tz := strings.TrimSpace(r.URL.Query().Get("timezone"))
	if tz == "" {
		tz = memoryCurationLedgerDefaultTZ
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		writeError(w, http.StatusBadRequest, "date is required")
		return
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid date")
		return
	}

	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	limit := memoryCurationLedgerDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > memoryCurationLedgerMaxLimit {
			n = memoryCurationLedgerMaxLimit
		}
		limit = n
	}

	typeFilter := ""
	switch kind {
	case "", "all":
	case "memory":
		typeFilter = "memory"
	case "skill":
		typeFilter = "skill"
	default:
		writeError(w, http.StatusBadRequest, "kind must be memory, skill, or all")
		return
	}

	var total int
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*)
		  FROM agent_memory_curation_candidate c
		 WHERE c.workspace_id = $1::uuid
		   AND (c.created_at AT TIME ZONE 'UTC' AT TIME ZONE $2)::date = $3::date
		   AND ($4 = '' OR c.status = $4)
		   AND (
		     $5 = ''
		     OR ($5 = 'skill' AND c.candidate_type IN ('skill', 'team_skill'))
		     OR ($5 = 'memory' AND c.candidate_type NOT IN ('skill', 'team_skill'))
		   )
	`, workspaceID, tz, date, status, typeFilter).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count candidates")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT c.id::text,
		       COALESCE(c.source_agent_id::text, ''),
		       COALESCE(NULLIF(a.display_name, ''), a.name, ''),
		       COALESCE(c.run_id::text, ''),
		       c.candidate_type, c.scope, c.title,
		       left(c.content, 240), c.confidence, c.status,
		       c.created_at
		  FROM agent_memory_curation_candidate c
		  LEFT JOIN agent a ON a.id = c.source_agent_id
		 WHERE c.workspace_id = $1::uuid
		   AND (c.created_at AT TIME ZONE 'UTC' AT TIME ZONE $2)::date = $3::date
		   AND ($4 = '' OR c.status = $4)
		   AND (
		     $5 = ''
		     OR ($5 = 'skill' AND c.candidate_type IN ('skill', 'team_skill'))
		     OR ($5 = 'memory' AND c.candidate_type NOT IN ('skill', 'team_skill'))
		   )
		 ORDER BY c.created_at DESC, c.id DESC
		 LIMIT $6
	`, workspaceID, tz, date, status, typeFilter, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list candidates")
		return
	}
	defer rows.Close()

	items := make([]memoryCurationCandidateItem, 0, limit)
	for rows.Next() {
		var item memoryCurationCandidateItem
		var createdAt time.Time
		if err := rows.Scan(
			&item.ID, &item.SourceAgentID, &item.SourceAgent, &item.RunID,
			&item.CandidateType, &item.Scope, &item.Title, &item.Snippet,
			&item.Confidence, &item.Status, &createdAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan candidates")
			return
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, memoryCurationCandidateListResponse{Items: items, Total: total})
}

// GetMemoryCurationCandidate returns one candidate with full content.
func (h *Handler) GetMemoryCurationCandidate(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	candidateID := chi.URLParam(r, "candidateId")
	if _, ok := parseUUIDOrBadRequest(w, candidateID, "candidate id"); !ok {
		return
	}

	var item memoryCurationCandidateItem
	var createdAt time.Time
	err := h.DB.QueryRow(r.Context(), `
		SELECT c.id::text,
		       COALESCE(c.source_agent_id::text, ''),
		       COALESCE(NULLIF(a.display_name, ''), a.name, ''),
		       COALESCE(c.run_id::text, ''),
		       c.candidate_type, c.scope, c.title,
		       left(c.content, 240), c.content, c.confidence, c.status,
		       c.created_at
		  FROM agent_memory_curation_candidate c
		  LEFT JOIN agent a ON a.id = c.source_agent_id
		 WHERE c.workspace_id = $1::uuid AND c.id = $2::uuid
	`, workspaceID, candidateID).Scan(
		&item.ID, &item.SourceAgentID, &item.SourceAgent, &item.RunID,
		&item.CandidateType, &item.Scope, &item.Title, &item.Snippet, &item.Content,
		&item.Confidence, &item.Status, &createdAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "candidate not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load candidate")
		return
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, item)
}

// ListTeamKnowledgeItems lists shared team knowledge rows, optionally for one day.
func (h *Handler) ListTeamKnowledgeItems(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	tz := strings.TrimSpace(r.URL.Query().Get("timezone"))
	if tz == "" {
		tz = memoryCurationLedgerDefaultTZ
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			writeError(w, http.StatusBadRequest, "invalid date")
			return
		}
	}
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	limit := memoryCurationLedgerDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > memoryCurationLedgerMaxLimit {
			n = memoryCurationLedgerMaxLimit
		}
		limit = n
	}

	includeContent := strings.EqualFold(r.URL.Query().Get("include_content"), "true")

	var total int
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*)
		  FROM team_knowledge_item
		 WHERE workspace_id = $1::uuid
		   AND ($2 = '' OR (created_at AT TIME ZONE 'UTC' AT TIME ZONE $3)::date = $2::date)
		   AND ($4 = '' OR kind = $4)
	`, workspaceID, date, tz, kind).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count team knowledge")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT id::text, kind, title, left(content, 240),
		       CASE WHEN $5 THEN content ELSE '' END,
		       status, created_at
		  FROM team_knowledge_item
		 WHERE workspace_id = $1::uuid
		   AND ($2 = '' OR (created_at AT TIME ZONE 'UTC' AT TIME ZONE $3)::date = $2::date)
		   AND ($4 = '' OR kind = $4)
		 ORDER BY created_at DESC, id DESC
		 LIMIT $6
	`, workspaceID, date, tz, kind, includeContent, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list team knowledge")
		return
	}
	defer rows.Close()

	items := make([]teamKnowledgeListItem, 0, limit)
	for rows.Next() {
		var item teamKnowledgeListItem
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Snippet, &item.Content, &item.Status, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan team knowledge")
			return
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, teamKnowledgeListResponse{Items: items, Total: total})
}

// GetTeamKnowledgeItem returns one team knowledge row with full content.
func (h *Handler) GetTeamKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	itemID := chi.URLParam(r, "itemId")
	if _, ok := parseUUIDOrBadRequest(w, itemID, "item id"); !ok {
		return
	}

	var item teamKnowledgeListItem
	var createdAt time.Time
	err := h.DB.QueryRow(r.Context(), `
		SELECT id::text, kind, title, left(content, 240), content, status, created_at
		  FROM team_knowledge_item
		 WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, itemID).Scan(&item.ID, &item.Kind, &item.Title, &item.Snippet, &item.Content, &item.Status, &createdAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "team knowledge item not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load team knowledge item")
		return
	}
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, item)
}
