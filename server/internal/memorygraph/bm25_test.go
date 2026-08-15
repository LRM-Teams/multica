package memorygraph

import (
	"testing"
)

func TestTokenizerCJKBigrams(t *testing.T) {
	terms := tokenize("图记忆")
	want := map[string]bool{"图记": true, "记忆": true}
	if len(terms) != len(want) {
		t.Fatalf("tokenize(图记忆) = %v, want %d terms", terms, len(want))
	}
	for _, term := range terms {
		if !want[term] {
			t.Fatalf("unexpected term %q in %v", term, terms)
		}
	}

	// A lone CJK character contributes a unigram.
	terms = tokenize("图")
	if len(terms) != 1 || terms[0] != "图" {
		t.Fatalf("tokenize(图) = %v, want [图]", terms)
	}
}

func TestTokenizerLatinCaseAndStopwords(t *testing.T) {
	terms := tokenize("The Graph MEMORY index")
	want := []string{"graph", "memory", "index"}
	if len(terms) != len(want) {
		t.Fatalf("tokenize = %v, want %v", terms, want)
	}
	for i := range want {
		if terms[i] != want[i] {
			t.Fatalf("tokenize = %v, want %v", terms, want)
		}
	}
}

func TestBM25RankingSanity(t *testing.T) {
	ix := NewBM25Index()
	ix.Add("low", "the graph memory reviewer stores nodes")
	ix.Add("high", "graph graph graph memory retrieval")
	ix.Add("none", "completely unrelated sandbox tooling")

	hits := ix.Search("graph", 10)
	if len(hits) != 2 {
		t.Fatalf("Search returned %d hits, want 2: %v", len(hits), hits)
	}
	if hits[0].ID != "high" {
		t.Fatalf("top hit = %q, want %q", hits[0].ID, "high")
	}
	if hits[1].ID != "low" {
		t.Fatalf("second hit = %q, want %q", hits[1].ID, "low")
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores not descending: %v", hits)
	}
}

func TestBM25CJKQueryMatchesBigramDoc(t *testing.T) {
	ix := NewBM25Index()
	ix.Add("cjk", "图记忆 reviewer")
	ix.Add("latin", "graph memory reviewer")

	hits := ix.Search("记忆", 10)
	if len(hits) != 1 || hits[0].ID != "cjk" {
		t.Fatalf("Search(记忆) = %v, want only doc cjk", hits)
	}
}

func TestBM25Remove(t *testing.T) {
	ix := NewBM25Index()
	ix.Add("a", "graph memory")
	ix.Add("b", "graph retrieval")
	if ix.Len() != 2 {
		t.Fatalf("Len = %d, want 2", ix.Len())
	}
	ix.Remove("a")
	if ix.Len() != 1 {
		t.Fatalf("Len after Remove = %d, want 1", ix.Len())
	}
	hits := ix.Search("memory", 10)
	if len(hits) != 0 {
		t.Fatalf("Search(memory) after Remove = %v, want no hits", hits)
	}
	hits = ix.Search("graph", 10)
	if len(hits) != 1 || hits[0].ID != "b" {
		t.Fatalf("Search(graph) = %v, want only doc b", hits)
	}
	// Removing an unknown id is a no-op.
	ix.Remove("missing")
	if ix.Len() != 1 {
		t.Fatalf("Len after removing unknown id = %d, want 1", ix.Len())
	}
}

func TestBM25AddReplacesExistingDoc(t *testing.T) {
	ix := NewBM25Index()
	ix.Add("a", "graph memory")
	ix.Add("a", "sandbox tooling")
	if ix.Len() != 1 {
		t.Fatalf("Len = %d, want 1", ix.Len())
	}
	if hits := ix.Search("graph", 10); len(hits) != 0 {
		t.Fatalf("old terms still searchable: %v", hits)
	}
	if hits := ix.Search("sandbox", 10); len(hits) != 1 {
		t.Fatalf("new terms not searchable: %v", hits)
	}
}
