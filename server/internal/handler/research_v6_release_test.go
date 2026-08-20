package handler

import "testing"

func TestResearchV6CreateAllowedDefaultsOpenWithoutControlRow(t *testing.T) {
	h := &Handler{cfg: Config{ResearchV6BootstrapEnabled: false}}
	if !h.researchV6CreateAllowed(t.Context(), "11111111-1111-1111-1111-111111111111") {
		t.Fatal("missing release row must keep explicit V6 create open")
	}
}
