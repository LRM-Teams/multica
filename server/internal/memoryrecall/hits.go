package memoryrecall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const searchHitsRel = ".multica/memory-search-hits.jsonl"

type searchHitRecord struct {
	Query     string  `json:"query"`
	Path      string  `json:"path"`
	Score     float64 `json:"score"`
	CreatedAt string  `json:"created_at"`
}

// RecordHits appends one JSONL line per hit so L3 can raise frequently
// recalled facts. Fail-open: filesystem errors are ignored.
func RecordHits(agentRoot, query string, hits []Hit) {
	if agentRoot == "" || query == "" || len(hits) == 0 {
		return
	}
	path := filepath.Join(agentRoot, filepath.FromSlash(searchHitsRel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, hit := range hits {
		payload, err := json.Marshal(searchHitRecord{
			Query: query, Path: hit.Path, Score: hit.Score, CreatedAt: now,
		})
		if err != nil {
			continue
		}
		_, _ = f.Write(append(payload, '\n'))
	}
}
