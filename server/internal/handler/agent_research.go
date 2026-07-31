package handler

import "net/http"

// Agent research data-plane (LRM-904 / #801).
//
// Research Fleet CLI runs under mat_* in daemon tasks. Human /api/research/*
// is fail-closed by RejectAgentOnHumanAPI; these dedicated routes remount the
// same handlers under /api/agent/research/* after RequireAgentPrincipal.

func (h *Handler) GetAgentResearchFleet(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.GetResearchFleet(w, r)
}

func (h *Handler) HireAgentResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.HireResearchFleetMember(w, r)
}

func (h *Handler) OptimizeAgentResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.OptimizeResearchFleetMember(w, r)
}

func (h *Handler) ArchiveAgentResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ArchiveResearchFleetMemberHandler(w, r)
}

func (h *Handler) GetAgentResearchSessionSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.GetResearchSessionSnapshot(w, r)
}

func (h *Handler) AppendAgentResearchGraphNode(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.AppendResearchGraphNode(w, r)
}

func (h *Handler) UpsertAgentResearchSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.UpsertResearchSourceHandler(w, r)
}

func (h *Handler) PatchAgentResearchReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.PatchResearchReport(w, r)
}

func (h *Handler) PostAgentResearchPresence(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.PostResearchPresence(w, r)
}

func (h *Handler) RequestAgentResearchStageEval(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.RequestResearchStageEval(w, r)
}

func (h *Handler) PostAgentResearchMessage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.PostResearchMessage(w, r)
}
