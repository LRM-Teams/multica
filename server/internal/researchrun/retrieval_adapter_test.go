package researchrun

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"
)

func retrievalFixture() (RetrievalSearchRequest, RetrievalSearchPage, RetrievalFetchRequest, RetrievalDocument) {
	content := []byte("verified source text")
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	safety := RetrievalSafety{RequestedURL: "https://example.com/source", FinalURL: "https://example.com/source", ResolvedAddresses: []string{"93.184.216.34"}, ScanDisposition: "safe", ResponseBytes: int64(len(content))}
	request := RetrievalSearchRequest{Adapter: "web-v1", Query: "research question", Limit: 10, Languages: []string{"en"}, Scopes: []string{"example.com"}}
	page := RetrievalSearchPage{Adapter: request.Adapter, Candidates: []RetrievalCandidate{{CanonicalURL: "https://example.com/source", CanonicalIdentity: "url:https://example.com/source", Title: "Source", Snippet: "Relevant result", Publisher: "Example", IndependenceFamily: "publisher:example", ContentHash: hash, Position: 1}}, Cost: RetrievalCost{Requests: 1, OutputBytes: 1024}, Safety: safety}
	fetch := RetrievalFetchRequest{Adapter: request.Adapter, CanonicalURL: "https://example.com/source", CanonicalIdentity: "url:https://example.com/source", MaximumContentSize: 1024}
	document := RetrievalDocument{Adapter: fetch.Adapter, CanonicalURL: fetch.CanonicalURL, CanonicalIdentity: fetch.CanonicalIdentity, MIME: "text/plain", Content: content, ContentHash: hash, Cost: RetrievalCost{Requests: 1, OutputBytes: int64(len(content))}, Safety: safety}
	return request, page, fetch, document
}

func TestRetrievalAdapterContractAcceptsBoundedAuditableResults(t *testing.T) {
	request, page, fetch, document := retrievalFixture()
	if err := ValidateRetrievalSearchPage(request, page); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRetrievalDocument(fetch, document); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRetrievalFailure(RetrievalFailure{Class: "rate_limited", Retryable: true, RetryAfter: time.Minute, Message: "provider quota"}); err != nil {
		t.Fatal(err)
	}
}

func TestRetrievalAdapterContractRejectsUnsafeOrUntraceableResults(t *testing.T) {
	for name, mutate := range map[string]func(*RetrievalSearchRequest, *RetrievalSearchPage, *RetrievalFetchRequest, *RetrievalDocument){
		"adapter mismatch": func(_ *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, _ *RetrievalDocument) {
			p.Adapter = "other"
		},
		"cursor mismatch": func(r *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, _ *RetrievalDocument) {
			r.Cursor, p.CursorIn = "a", "b"
		},
		"noncanonical URL": func(_ *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, _ *RetrievalDocument) {
			p.Candidates[0].CanonicalURL = "HTTPS://example.com/source#fragment"
		},
		"duplicate identity": func(_ *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, _ *RetrievalDocument) {
			p.Candidates = append(p.Candidates, p.Candidates[0])
			p.Candidates[1].Position = 2
		},
		"private address": func(_ *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, d *RetrievalDocument) {
			p.Safety.ResolvedAddresses, d.Safety.ResolvedAddresses = []string{"127.0.0.1"}, []string{"127.0.0.1"}
		},
		"mapped private address": func(_ *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, d *RetrievalDocument) {
			p.Safety.ResolvedAddresses, d.Safety.ResolvedAddresses = []string{"::ffff:127.0.0.1"}, []string{"::ffff:127.0.0.1"}
		},
		"credential forwarding": func(_ *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, d *RetrievalDocument) {
			p.Safety.CredentialsForwarded, d.Safety.CredentialsForwarded = true, true
		},
		"quarantined content": func(_ *RetrievalSearchRequest, p *RetrievalSearchPage, _ *RetrievalFetchRequest, d *RetrievalDocument) {
			p.Safety.ScanDisposition, d.Safety.ScanDisposition = "quarantined", "quarantined"
		},
		"hash mismatch": func(_ *RetrievalSearchRequest, _ *RetrievalSearchPage, _ *RetrievalFetchRequest, d *RetrievalDocument) {
			d.ContentHash = "sha256:" + fmt.Sprintf("%064d", 0)
		},
		"content exceeds request": func(_ *RetrievalSearchRequest, _ *RetrievalSearchPage, f *RetrievalFetchRequest, _ *RetrievalDocument) {
			f.MaximumContentSize = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			request, page, fetch, document := retrievalFixture()
			mutate(&request, &page, &fetch, &document)
			pageErr, documentErr := ValidateRetrievalSearchPage(request, page), ValidateRetrievalDocument(fetch, document)
			if pageErr == nil && documentErr == nil {
				t.Fatal("malformed adapter result was accepted")
			}
			if pageErr != nil && !errors.Is(pageErr, ErrInvalidContract) || documentErr != nil && !errors.Is(documentErr, ErrInvalidContract) {
				t.Fatalf("pageErr=%v documentErr=%v", pageErr, documentErr)
			}
		})
	}
}

func TestRetrievalFailurePolicyFailsClosed(t *testing.T) {
	for name, failure := range map[string]RetrievalFailure{
		"unknown class":       {Class: "maybe", Message: "unknown"},
		"permanent retry":     {Class: "unsafe_target", Retryable: true, Message: "private address"},
		"delay without retry": {Class: "timeout", RetryAfter: time.Second, Message: "deadline"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateRetrievalFailure(failure); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
