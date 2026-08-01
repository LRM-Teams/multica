package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestValidateAgentModelCatalog(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryModelListStore()
	h := &Handler{ModelListStore: store}
	runtimeID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	otherRuntimeID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}

	request, err := store.Create(ctx, uuidToString(runtimeID), false, false)
	if err != nil {
		t.Fatalf("create catalog request: %v", err)
	}
	if err := store.Complete(ctx, request.ID, []ModelEntry{{
		ID:       "gpt-5",
		Thinking: &ModelThinking{SupportedLevels: []ThinkingLevel{{Value: "low"}, {Value: "high"}}},
	}}, true); err != nil {
		t.Fatalf("complete catalog request: %v", err)
	}

	tests := []struct {
		name      string
		requestID string
		runtimeID pgtype.UUID
		model     string
		thinking  string
		want      string
	}{
		{name: "advertised combination", requestID: request.ID, runtimeID: runtimeID, model: "gpt-5", thinking: "high"},
		{name: "runtime default is allowed", requestID: request.ID, runtimeID: runtimeID},
		{name: "unknown model", requestID: request.ID, runtimeID: runtimeID, model: "other", want: "not advertised"},
		{name: "unsupported thinking", requestID: request.ID, runtimeID: runtimeID, model: "gpt-5", thinking: "max", want: "not advertised"},
		{name: "thinking without model", requestID: request.ID, runtimeID: runtimeID, thinking: "high", want: "select a model"},
		{name: "cross runtime replay", requestID: request.ID, runtimeID: otherRuntimeID, model: "gpt-5", want: "does not belong"},
		{name: "missing request", runtimeID: runtimeID, model: "gpt-5", want: "required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.validateAgentModelCatalog(ctx, tt.requestID, tt.runtimeID, tt.model, tt.thinking)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateAgentModelCatalog_UnsupportedRuntimeOnlyAcceptsDefaults(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryModelListStore()
	h := &Handler{ModelListStore: store}
	runtimeID := pgtype.UUID{Bytes: [16]byte{3}, Valid: true}
	request, err := store.Create(ctx, uuidToString(runtimeID), false, false)
	if err != nil {
		t.Fatalf("create catalog request: %v", err)
	}
	if err := store.Complete(ctx, request.ID, nil, false); err != nil {
		t.Fatalf("complete catalog request: %v", err)
	}
	if err := h.validateAgentModelCatalog(ctx, request.ID, runtimeID, "", ""); err != nil {
		t.Fatalf("runtime default config: %v", err)
	}
	if err := h.validateAgentModelCatalog(ctx, request.ID, runtimeID, "gpt-5", ""); err == nil {
		t.Fatal("expected unsupported runtime to reject explicit model")
	}
}
