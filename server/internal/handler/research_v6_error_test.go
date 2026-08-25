package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchV6InvalidContractReturnsActionableDetail(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := fmt.Errorf("%w: second-stage schema %q: $.task_specific_payload.sources is required", researchrun.ErrInvalidContract, "research.source_discovery.v1")

	writeResearchV6DomainError(recorder, err)

	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	var response struct {
		Code      string `json:"code"`
		Error     string `json:"error"`
		Retryable bool   `json:"retryable"`
	}
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &response); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if response.Code != "research.v6.invalid_contract" || response.Retryable {
		t.Fatalf("response = %+v", response)
	}
	for _, fragment := range []string{"invalid research V6 contract", "research.source_discovery.v1", "task_specific_payload.sources is required"} {
		if !strings.Contains(response.Error, fragment) {
			t.Fatalf("error %q does not contain %q", response.Error, fragment)
		}
	}
}
