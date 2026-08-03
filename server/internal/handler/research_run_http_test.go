package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeResearchJSON(t *testing.T) {
	type request struct {
		Goal string `json:"goal"`
	}
	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
	}{
		{name: "valid", body: `{"goal":"compare"}`, wantOK: true},
		{name: "unknown field", body: `{"goal":"compare","extra":true}`, wantStatus: http.StatusBadRequest},
		{name: "second object", body: `{"goal":"compare"}{"goal":"replace"}`, wantStatus: http.StatusBadRequest},
		{name: "too large", body: `{"goal":"` + strings.Repeat("x", int(maxResearchControlRequestBytes)) + `"}`, wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			httpRequest := httptest.NewRequest(http.MethodPost, "/research", strings.NewReader(tt.body))
			var got request
			ok := decodeResearchJSON(recorder, httpRequest, &got)
			if ok != tt.wantOK {
				t.Fatalf("decodeResearchJSON() = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK {
				if got.Goal != "compare" {
					t.Fatalf("goal = %q, want compare", got.Goal)
				}
				return
			}
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
		})
	}
}
