package researchrun

import (
	"context"
	"testing"
)

type stubRetrievalAdapter struct {
	document RetrievalDocument
	err      error
	fetches  int
}

func (s *stubRetrievalAdapter) Search(context.Context, RetrievalSearchRequest) (RetrievalSearchPage, error) {
	return RetrievalSearchPage{}, RetrievalFailure{Class: "provider_unavailable", Retryable: true, Message: "stub search is unavailable"}
}

func (s *stubRetrievalAdapter) Fetch(_ context.Context, request RetrievalFetchRequest) (RetrievalDocument, error) {
	s.fetches++
	if s.err != nil {
		return RetrievalDocument{}, s.err
	}
	document := s.document
	document.Adapter = request.Adapter
	document.CanonicalURL = request.CanonicalURL
	document.CanonicalIdentity = request.CanonicalIdentity
	return document, nil
}

func TestNewEngineWithRuntimeAdaptersWiresRetrieval(t *testing.T) {
	adapter := NewHTTPRetrievalAdapter(HTTPRetrievalAdapterConfig{})
	engine, ok := NewEngineWithRuntimeAdapters(nil, nil, nil, nil, nil, nil, nil, nil, adapter).(*Engine)
	if !ok || engine.retrieval != adapter {
		t.Fatalf("runtime engine lost the Retrieval Adapter")
	}
}

func TestIngestPendingScreenedSourcesRequiresAdapter(t *testing.T) {
	engine := newEngine(nil, nil, nil)
	processed, err := engine.IngestPendingScreenedSources(context.Background(), 8)
	if processed != 0 || err != nil {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
}
