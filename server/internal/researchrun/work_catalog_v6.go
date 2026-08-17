package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type V6CatalogView string

const (
	V6CatalogSameTier         V6CatalogView = "same_tier"
	V6CatalogHigherCandidates V6CatalogView = "higher_candidates"
)

type V6CatalogRequest struct {
	V6AttemptAccess
	View   V6CatalogView
	Cursor string
}

type V6CatalogPage struct {
	PageKey, PageHash, NextCursor string
	HasMore                       bool
	Bytes                         json.RawMessage
}

type AcknowledgeV6CatalogInput struct {
	V6AttemptAccess
	ClientRequestID, PageKey, PageHash string
}

type workCatalogStore interface {
	LoadV6WorkCatalog(context.Context, V6CatalogRequest) (V6CatalogPage, error)
	AcknowledgeV6WorkCatalog(context.Context, AcknowledgeV6CatalogInput) error
}

type workCatalogModule struct{ store workCatalogStore }

func (m workCatalogModule) Get(ctx context.Context, in V6CatalogRequest) (V6CatalogPage, error) {
	if m.store == nil || (in.View != V6CatalogSameTier && in.View != V6CatalogHigherCandidates) {
		return V6CatalogPage{}, fmt.Errorf("%w: invalid catalog view", ErrInvalidContract)
	}
	return m.store.LoadV6WorkCatalog(ctx, in)
}

func (m workCatalogModule) Acknowledge(ctx context.Context, in AcknowledgeV6CatalogInput) error {
	if m.store == nil || strings.TrimSpace(in.ClientRequestID) == "" || strings.TrimSpace(in.PageKey) == "" || strings.TrimSpace(in.PageHash) == "" {
		return fmt.Errorf("%w: incomplete catalog acknowledgement", ErrInvalidContract)
	}
	return m.store.AcknowledgeV6WorkCatalog(ctx, in)
}
