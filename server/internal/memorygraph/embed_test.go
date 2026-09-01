package memorygraph

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func testEmbedScope(workspaceID, policyVersion string) ProviderScope {
	return ProviderScope{
		WorkspaceID:   workspaceID,
		Purpose:       ProviderPurposeEmbed,
		Provider:      "approved",
		Model:         "test-embed-model",
		Region:        "us-east-1",
		PolicyVersion: policyVersion,
	}
}

func testEmbedEndpointConfig(baseURL string) OpenAIEmbedderConfig {
	return OpenAIEmbedderConfig{
		BaseURL: baseURL, Provider: "approved", Model: "test-embed-model", Region: "us-east-1",
	}
}

func mustCachedEmbedder(t *testing.T, inner EmbeddingProvider, store *Store) *CachedEmbedder {
	t.Helper()
	emb, err := NewCachedEmbedder(inner, store, testEmbedScope("test-workspace", "test-policy"))
	if err != nil {
		t.Fatalf("NewCachedEmbedder: %v", err)
	}
	return emb
}
func TestHashEmbedderDeterministicAndNormalized(t *testing.T) {
	h := NewHashEmbedder()
	if h.Dim() != hashEmbedderDim {
		t.Fatalf("Dim = %d, want %d", h.Dim(), hashEmbedderDim)
	}
	a, err := h.Embed(context.Background(), []string{"graph memory reviewer"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	b, err := h.Embed(context.Background(), []string{"graph memory reviewer"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(a) != 1 || len(a[0]) != hashEmbedderDim {
		t.Fatalf("Embed shape = %dx%d, want 1x%d", len(a), len(a[0]), hashEmbedderDim)
	}
	for i := range a[0] {
		if a[0][i] != b[0][i] {
			t.Fatalf("HashEmbedder not deterministic at dim %d: %v vs %v", i, a[0], b[0])
		}
	}
	var norm float64
	for _, v := range a[0] {
		norm += float64(v) * float64(v)
	}
	if math.Abs(norm-1) > 1e-5 {
		t.Fatalf("vector norm = %f, want 1", norm)
	}
	if sim := cosineSimilarity(a[0], HashEmbedText("graph memory retrieval")); sim <= 0 {
		t.Fatalf("cosine with overlapping text = %f, want > 0", sim)
	}
}

type countingProvider struct {
	calls atomic.Int64
}

func (p *countingProvider) Dim() int { return hashEmbedderDim }

func (p *countingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.calls.Add(1)
	return NewHashEmbedder().Embed(ctx, texts)
}

func TestCachedEmbedderSecondEmbedHitsDiskCache(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	provider := &countingProvider{}
	cached, err := NewCachedEmbedder(provider, store, testEmbedScope("workspace-a", "policy-1"))
	if err != nil {
		t.Fatalf("NewCachedEmbedder: %v", err)
	}

	texts := []string{"alpha beta gamma", "delta epsilon"}
	first, err := cached.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("first Embed: %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls.Load())
	}
	second, err := cached.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("second Embed: %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls after cache hit = %d, want 1", provider.calls.Load())
	}
	for i := range texts {
		for d := range first[i] {
			if first[i][d] != second[i][d] {
				t.Fatalf("cached vector mismatch for text %d", i)
			}
		}
	}
	if _, err := cached.EmbedQuery(context.Background(), "alpha beta gamma"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls after cached query = %d, want 1", provider.calls.Load())
	}
}

func TestCachedEmbedderWorkspaceIsolation(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	provider := &countingProvider{}
	workspaceA, err := NewCachedEmbedder(provider, store, testEmbedScope("workspace-a", "policy-1"))
	if err != nil {
		t.Fatalf("workspace A cache: %v", err)
	}
	workspaceB, err := NewCachedEmbedder(provider, store, testEmbedScope("workspace-b", "policy-1"))
	if err != nil {
		t.Fatalf("workspace B cache: %v", err)
	}
	const identicalPlaintext = "identical sanitized content"
	if _, err := workspaceA.Embed(context.Background(), []string{identicalPlaintext}); err != nil {
		t.Fatalf("workspace A Embed: %v", err)
	}
	if _, err := workspaceB.Embed(context.Background(), []string{identicalPlaintext}); err != nil {
		t.Fatalf("workspace B Embed: %v", err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 isolated Workspace cache misses", provider.calls.Load())
	}
}

func TestCachedEmbedderIgnoresLegacyContentOnlyPath(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	const text = "legacy cache collision"
	if err := writeVecFile(store.EmbeddingPath(ComputeContentHash(text)), []float32{99}); err != nil {
		t.Fatalf("write legacy cache: %v", err)
	}
	provider := &countingProvider{}
	cached, err := NewCachedEmbedder(provider, store, testEmbedScope("workspace-a", "policy-2"))
	if err != nil {
		t.Fatalf("NewCachedEmbedder: %v", err)
	}
	if _, err := cached.Embed(context.Background(), []string{text}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("legacy content-only path was reused across policy scope")
	}
}

func TestVecFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.vec")
	want := []float32{0.5, -1.25, 3.75, 0}
	if err := writeVecFile(path, want); err != nil {
		t.Fatalf("writeVecFile: %v", err)
	}
	got, ok := readVecFile(path)
	if !ok {
		t.Fatalf("readVecFile: not ok")
	}
	if len(got) != len(want) {
		t.Fatalf("readVecFile dim = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("vec[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if _, ok := readVecFile(filepath.Join(t.TempDir(), "missing.vec")); ok {
		t.Fatalf("readVecFile on missing file reported ok")
	}
}

func TestNewOpenAIEmbedderFromEnvUnset(t *testing.T) {
	t.Setenv(envEmbedBaseURL, "")
	t.Setenv(envEmbedAPIKey, "")
	t.Setenv(envEmbedProvider, "")
	t.Setenv(envEmbedModel, "")
	t.Setenv(envEmbedRegion, "")
	_, err := NewOpenAIEmbedderFromEnv(testEmbedScope("workspace-a", "policy-1"))
	if !errors.Is(err, ErrEmbedNotConfigured) {
		t.Fatalf("err = %v, want ErrEmbedNotConfigured", err)
	}
}

func TestNewOpenAIEmbedderMalformedBaseURL(t *testing.T) {
	_, err := NewOpenAIEmbedder(testEmbedEndpointConfig("://not-a-url"), testEmbedScope("workspace-a", "policy-1"))
	if err == nil || errors.Is(err, ErrEmbedNotConfigured) {
		t.Fatalf("err = %v, want malformed-value error", err)
	}
}

func TestNoProviderFallbackOpenAIEmbedderRejectsEndpointIdentityMismatch(t *testing.T) {
	for name, mutate := range map[string]func(*OpenAIEmbedderConfig){
		"provider": func(cfg *OpenAIEmbedderConfig) { cfg.Provider = "different-provider" },
		"model":    func(cfg *OpenAIEmbedderConfig) { cfg.Model = "different-model" },
		"region":   func(cfg *OpenAIEmbedderConfig) { cfg.Region = "different-region" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testEmbedEndpointConfig("https://example.invalid")
			mutate(&cfg)
			if _, err := NewOpenAIEmbedder(cfg, testEmbedScope("workspace-a", "policy-1")); err == nil {
				t.Fatalf("NewOpenAIEmbedder accepted a different %s from resolved policy", name)
			}
		})
	}
}

func TestOpenAIEmbedderAgainstTestServer(t *testing.T) {
	var gotModel string
	var gotInputs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q, want /v1/embeddings", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotModel = req.Model
		gotInputs = len(req.Input)
		resp := embedResponse{Data: make([]struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}, len(req.Input))}
		for i := range req.Input {
			resp.Data[i].Index = i
			resp.Data[i].Embedding = []float32{float32(i) + 1, 0.5}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := testEmbedEndpointConfig(srv.URL)
	cfg.APIKey = "test-key"
	emb, err := NewOpenAIEmbedder(cfg, testEmbedScope("workspace-a", "policy-1"))
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	vecs, err := emb.Embed(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotModel != "test-embed-model" || gotInputs != 2 {
		t.Fatalf("server saw model=%q inputs=%d", gotModel, gotInputs)
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Fatalf("Embed = %v, want parsed vectors in input order", vecs)
	}
	if emb.Dim() != 2 {
		t.Fatalf("Dim = %d, want 2", emb.Dim())
	}
}

func TestOpenAIEmbedderBatchesAt32(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		resp := embedResponse{Data: make([]struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}, len(req.Input))}
		for i := range req.Input {
			resp.Data[i].Index = i
			resp.Data[i].Embedding = []float32{1}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	scope := testEmbedScope("workspace-a", "policy-1")
	scope.Model = "m"
	cfg := testEmbedEndpointConfig(srv.URL)
	cfg.Model = "m"
	emb, err := NewOpenAIEmbedder(cfg, scope)
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	texts := make([]string, 33)
	for i := range texts {
		texts[i] = "t"
	}
	vecs, err := emb.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 33 {
		t.Fatalf("Embed returned %d vectors, want 33", len(vecs))
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2 (32+1)", requests.Load())
	}
}
