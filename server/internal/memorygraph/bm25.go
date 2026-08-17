package memorygraph

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// BM25 Okapi tuning constants (design §5.2 lexical channel).
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// bm25StopWords is a small English-only stopword list. CJK tokens are
// bigram-based and never stopword-filtered.
var bm25StopWords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "to": true,
	"in": true, "is": true, "are": true, "was": true, "were": true,
	"and": true, "or": true, "for": true, "on": true, "at": true,
	"by": true, "with": true,
}

// ScoredDoc is one retrieval hit: a document id plus its score.
type ScoredDoc struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// tokenize splits text into BM25 terms: latin/digit runs lowercased (minus
// English stopwords), plus overlapping bigrams of every CJK run. A CJK run
// of length one contributes the single character as a unigram.
func tokenize(text string) []string {
	var terms []string
	var latin strings.Builder
	var cjk []rune

	flushLatin := func() {
		if latin.Len() == 0 {
			return
		}
		tok := latin.String()
		latin.Reset()
		if !bm25StopWords[tok] {
			terms = append(terms, tok)
		}
	}
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		if len(cjk) == 1 {
			terms = append(terms, string(cjk[0]))
		} else {
			for i := 0; i+1 < len(cjk); i++ {
				terms = append(terms, string(cjk[i:i+2]))
			}
		}
		cjk = cjk[:0]
	}

	for _, r := range strings.ToLower(text) {
		switch {
		case isCJK(r):
			flushLatin()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			latin.WriteRune(r)
		default:
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return terms
}

// isCJK reports whether r is a CJK ideograph or kana/hangul syllable, i.e.
// a character that participates in bigram tokenization.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// bm25Doc is the per-document term statistics kept by BM25Index.
type bm25Doc struct {
	tf     map[string]int // term frequency within the document
	length int            // total term count (sum of tf)
}

// BM25Index is a pure-Go Okapi BM25 inverted index with incremental
// document-frequency maintenance. It is safe for concurrent use.
type BM25Index struct {
	mu       sync.RWMutex
	docs     map[string]bm25Doc
	df       map[string]int // document frequency per term
	totalLen int            // sum of document lengths
}

// NewBM25Index returns an empty index.
func NewBM25Index() *BM25Index {
	return &BM25Index{
		docs: make(map[string]bm25Doc),
		df:   make(map[string]int),
	}
}

// Add indexes text under id. Re-adding an existing id replaces the previous
// document (its term statistics are subtracted first).
func (ix *BM25Index) Add(id, text string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if _, ok := ix.docs[id]; ok {
		ix.removeLocked(id)
	}
	doc := bm25Doc{tf: make(map[string]int)}
	for _, term := range tokenize(text) {
		doc.tf[term]++
		doc.length++
	}
	ix.docs[id] = doc
	for term := range doc.tf {
		ix.df[term]++
	}
	ix.totalLen += doc.length
}

// Remove deletes id from the index. Removing an unknown id is a no-op.
func (ix *BM25Index) Remove(id string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.removeLocked(id)
}

func (ix *BM25Index) removeLocked(id string) {
	doc, ok := ix.docs[id]
	if !ok {
		return
	}
	for term := range doc.tf {
		ix.df[term]--
		if ix.df[term] <= 0 {
			delete(ix.df, term)
		}
	}
	ix.totalLen -= doc.length
	delete(ix.docs, id)
}

// Len returns the number of indexed documents.
func (ix *BM25Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.docs)
}

// Search scores every document sharing at least one term with the query
// using Okapi BM25 and returns the topK highest, sorted by descending
// score. Documents with a zero score (no shared terms) are excluded.
func (ix *BM25Index) Search(query string, topK int) []ScoredDoc {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	if topK <= 0 || len(ix.docs) == 0 {
		return nil
	}
	n := float64(len(ix.docs))
	avgDL := float64(ix.totalLen) / n

	scores := make(map[string]float64)
	seen := make(map[string]bool) // query terms scored once each
	for _, term := range tokenize(query) {
		if seen[term] {
			continue
		}
		seen[term] = true
		df, ok := ix.df[term]
		if !ok {
			continue
		}
		idf := math.Log(1 + (n-float64(df)+0.5)/(float64(df)+0.5))
		for id, doc := range ix.docs {
			tf, ok := doc.tf[term]
			if !ok {
				continue
			}
			tfNorm := float64(tf) * (bm25K1 + 1) /
				(float64(tf) + bm25K1*(1-bm25B+bm25B*float64(doc.length)/avgDL))
			scores[id] += idf * tfNorm
		}
	}

	hits := make([]ScoredDoc, 0, len(scores))
	for id, score := range scores {
		if score > 0 {
			hits = append(hits, ScoredDoc{ID: id, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits
}
