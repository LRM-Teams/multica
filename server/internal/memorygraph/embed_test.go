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
	// Token overlap yields positive cosine; unrelated text does not.
	sim := cosineSimilarity(a[0], HashEmbedText("graph memory retrieval"))
	if sim <= 0 {
		t.Fatalf("cosine with overlapping text = %f, want > 0", sim)
	}
}

// countingProvider is a fake EmbeddingProvider recording Embed call counts.
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
	cached := NewCachedEmbedder(provider, store)

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
	// Queries are cached too.
	if _, err := cached.EmbedQuery(context.Background(), "alpha beta gamma"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls after cached query = %d, want 1", provider.calls.Load())
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
	t.Setenv(envEmbedModel, "")
	_, err := NewOpenAIEmbedderFromEnv()
	if !errors.Is(err, ErrEmbedNotConfigured) {
		t.Fatalf("err = %v, want ErrEmbedNotConfigured", err)
	}
}

func TestNewOpenAIEmbedderMalformedBaseURL(t *testing.T) {
	_, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{BaseURL: "://not-a-url", Model: "m"})
	if err == nil || errors.Is(err, ErrEmbedNotConfigured) {
		t.Fatalf("err = %v, want malformed-value error", err)
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

	emb, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "test-embed-model",
	})
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

	emb, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{BaseURL: srv.URL, Model: "m"})
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
