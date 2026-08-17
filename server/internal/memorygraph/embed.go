package memorygraph

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ErrEmbedNotConfigured is returned when embedding configuration is absent
// (base URL or model unset). Callers should treat it as a silent-disable
// signal and fall back to BM25-only retrieval; malformed values produce a
// descriptive error instead.
var ErrEmbedNotConfigured = errors.New("memorygraph: embedding provider not configured")

// openAIEmbedBatchSize is the maximum number of texts per /v1/embeddings
// request.
const openAIEmbedBatchSize = 32

// Environment variables read by NewOpenAIEmbedderFromEnv.
const (
	envEmbedBaseURL = "MULTICA_GRAPH_EMBED_BASE_URL"
	envEmbedAPIKey  = "MULTICA_GRAPH_EMBED_API_KEY"
	envEmbedModel   = "MULTICA_GRAPH_EMBED_MODEL"
)

// EmbeddingProvider converts texts into embedding vectors (design §5.2
// vector channel). Implementations must return one vector per input text,
// all of dimension Dim.
type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
}

// ---------------------------------------------------------------------------
// OpenAI-compatible HTTP embedder
// ---------------------------------------------------------------------------

// OpenAIEmbedderConfig configures an OpenAI-compatible embeddings endpoint.
type OpenAIEmbedderConfig struct {
	BaseURL string        // e.g. "https://api.openai.com" ("/v1/embeddings" is appended)
	APIKey  string        // optional bearer token
	Model   string        // embedding model name
	Timeout time.Duration // per-request timeout; <= 0 uses a default
}

// defaultEmbedTimeout applies when OpenAIEmbedderConfig.Timeout is unset.
const defaultEmbedTimeout = 30 * time.Second

// OpenAIEmbedder is an EmbeddingProvider against an OpenAI-compatible
// /v1/embeddings HTTP endpoint. Texts are sent in batches of at most 32.
type OpenAIEmbedder struct {
	cfg    OpenAIEmbedderConfig
	client *http.Client

	mu  sync.Mutex
	dim int // learned from the first response; 0 until then
}

// NewOpenAIEmbedder validates cfg and returns the embedder. Missing
// BaseURL or Model yields ErrEmbedNotConfigured; malformed values (bad
// URL, negative timeout) are hard errors.
func NewOpenAIEmbedder(cfg OpenAIEmbedderConfig) (*OpenAIEmbedder, error) {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, ErrEmbedNotConfigured
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("memorygraph: invalid embed base URL %q", cfg.BaseURL)
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("memorygraph: invalid embed timeout %s", cfg.Timeout)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultEmbedTimeout
	}
	return &OpenAIEmbedder{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// NewOpenAIEmbedderFromEnv builds an OpenAIEmbedder from
// MULTICA_GRAPH_EMBED_BASE_URL / _API_KEY / _MODEL. When the endpoint is
// not configured it returns ErrEmbedNotConfigured so callers can silently
// disable the vector channel.
func NewOpenAIEmbedderFromEnv() (*OpenAIEmbedder, error) {
	return NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: os.Getenv(envEmbedBaseURL),
		APIKey:  os.Getenv(envEmbedAPIKey),
		Model:   os.Getenv(envEmbedModel),
	})
}

// Dim returns the embedding dimension learned from the first response, or
// zero before the first successful Embed call.
func (e *OpenAIEmbedder) Dim() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dim
}

// Embed returns one vector per text, requesting at most 32 texts per HTTP
// call. The result order matches the input order.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += openAIEmbedBatchSize {
		end := min(start+openAIEmbedBatchSize, len(texts))
		vecs, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// embedRequest / embedResponse model the OpenAI-compatible wire format.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (e *OpenAIEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: e.cfg.Model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("memorygraph: marshal embed request: %w", err)
	}
	endpoint := strings.TrimSuffix(e.cfg.BaseURL, "/") + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("memorygraph: build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("memorygraph: embed request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("memorygraph: read embed response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("memorygraph: embed endpoint returned %s: %s", resp.Status, truncate(string(respBody), 200))
	}
	var parsed embedResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("memorygraph: parse embed response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("memorygraph: embed response has %d vectors for %d texts", len(parsed.Data), len(texts))
	}
	vecs := make([][]float32, len(texts))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("memorygraph: embed response index %d out of range", item.Index)
		}
		vecs[item.Index] = item.Embedding
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("memorygraph: embed response missing vector %d", i)
		}
	}
	e.mu.Lock()
	e.dim = len(vecs[0])
	e.mu.Unlock()
	return vecs, nil
}

// ---------------------------------------------------------------------------
// Content-hash disk cache
// ---------------------------------------------------------------------------

// CachedEmbedder wraps an EmbeddingProvider with the cross-version on-disk
// embedding cache (shared/embeddings/<content_hash>.vec, design §4.1). The
// cache key is ComputeContentHash(text), so identical texts never hit the
// network twice. Queries are cached the same way: the cache is
// content-addressed, so caching them is safe.
type CachedEmbedder struct {
	inner EmbeddingProvider
	store *Store
}

// NewCachedEmbedder wraps inner with the disk cache rooted at store.
func NewCachedEmbedder(inner EmbeddingProvider, store *Store) *CachedEmbedder {
	return &CachedEmbedder{inner: inner, store: store}
}

// Dim delegates to the wrapped provider.
func (c *CachedEmbedder) Dim() int { return c.inner.Dim() }

// Embed returns one vector per text, serving cache hits from disk and
// embedding (then persisting) only the misses. Result order matches input.
func (c *CachedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	var missTexts []string
	seenMiss := make(map[string]bool)
	for i, text := range texts {
		hash := ComputeContentHash(text)
		if vec, ok := readVecFile(c.store.EmbeddingPath(hash)); ok {
			out[i] = vec
			continue
		}
		out[i] = nil // placeholder, filled below
		if !seenMiss[hash] {
			seenMiss[hash] = true
			missTexts = append(missTexts, text)
		}
	}
	if len(missTexts) > 0 {
		vecs, err := c.inner.Embed(ctx, missTexts)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(missTexts) {
			return nil, fmt.Errorf("memorygraph: provider returned %d vectors for %d texts", len(vecs), len(missTexts))
		}
		byHash := make(map[string][]float32, len(vecs))
		for j, vec := range vecs {
			byHash[ComputeContentHash(missTexts[j])] = vec
			if err := writeVecFile(c.store.EmbeddingPath(ComputeContentHash(missTexts[j])), vec); err != nil {
				return nil, err
			}
		}
		for i, text := range texts {
			if out[i] == nil {
				out[i] = byHash[ComputeContentHash(text)]
			}
		}
	}
	return out, nil
}

// EmbedQuery embeds a single query text. It uses the same content-addressed
// disk cache as documents.
func (c *CachedEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	vecs, err := c.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// readVecFile loads a .vec file: 4-byte little-endian uint32 dim followed
// by dim float32 little-endian values. ok is false when the file is absent
// or malformed.
func readVecFile(path string) (vec []float32, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 4 {
		return nil, false
	}
	dim := binary.LittleEndian.Uint32(b[:4])
	if dim == 0 || len(b) != 4+4*int(dim) {
		return nil, false
	}
	vec = make([]float32, dim)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4+4*i:]))
	}
	return vec, true
}

// writeVecFile persists vec in the .vec format (see readVecFile).
func writeVecFile(path string, vec []float32) error {
	buf := make([]byte, 4+4*len(vec))
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(vec)))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[4+4*i:], math.Float32bits(v))
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("memorygraph: write embedding cache %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Deterministic local fallback
// ---------------------------------------------------------------------------

// hashEmbedderDim is the fixed dimension of HashEmbedder vectors.
const hashEmbedderDim = 64

// HashEmbedder is a deterministic, network-free EmbeddingProvider for
// tests and development: it hashes each token (same tokenizer as BM25)
// into a fixed-dim bag-of-words vector and normalizes it to unit length.
// Semantically related texts share tokens and therefore score a positive
// cosine similarity.
type HashEmbedder struct{}

// NewHashEmbedder returns the deterministic fallback embedder.
func NewHashEmbedder() *HashEmbedder { return &HashEmbedder{} }

// Dim returns the fixed hash embedding dimension.
func (h *HashEmbedder) Dim() int { return hashEmbedderDim }

// Embed hashes every text independently; it never touches the network and
// never fails.
func (h *HashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = HashEmbedText(text)
	}
	return out, nil
}

// HashEmbedText maps text to a normalized dim-64 bag-of-words vector via
// FNV-1a token hashing with a signed contribution per token.
func HashEmbedText(text string) []float32 {
	vec := make([]float32, hashEmbedderDim)
	for _, term := range tokenize(text) {
		sum := fnv.New64a()
		_, _ = sum.Write([]byte(term))
		h := sum.Sum64()
		slot := h % hashEmbedderDim
		if h&(1<<63) != 0 {
			vec[slot]--
		} else {
			vec[slot]++
		}
	}
	normalizeVec(vec)
	return vec
}

// normalizeVec scales vec to unit length in place; a zero vector is left
// unchanged.
func normalizeVec(vec []float32) {
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= inv
	}
}

// cosineSimilarity returns the cosine similarity of a and b, or 0 when
// either vector is empty or zero-length-mismatched.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
