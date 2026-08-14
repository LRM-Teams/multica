package researchrun

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	maxRetrievalResults       = 100
	maxRetrievalCursorBytes   = 4096
	maxRetrievalDocumentBytes = 10 << 20
)

// RetrievalAdapter is the provider seam for search and immutable full-text
// retrieval. Implementations return facts; Research Run owns their validation.
type RetrievalAdapter interface {
	Search(context.Context, RetrievalSearchRequest) (RetrievalSearchPage, error)
	Fetch(context.Context, RetrievalFetchRequest) (RetrievalDocument, error)
}

type RetrievalSearchRequest struct {
	Adapter    string
	Query      string
	Cursor     string
	Limit      int
	Languages  []string
	Scopes     []string
	WindowFrom *time.Time
	WindowTo   *time.Time
}

type RetrievalFetchRequest struct {
	Adapter            string
	CanonicalURL       string
	CanonicalIdentity  string
	MaximumContentSize int64
}

type RetrievalSearchPage struct {
	Adapter    string
	CursorIn   string
	CursorOut  string
	Candidates []RetrievalCandidate
	Cost       RetrievalCost
	Safety     RetrievalSafety
}

type RetrievalCandidate struct {
	CanonicalURL       string
	CanonicalIdentity  string
	Title              string
	Snippet            string
	Publisher          string
	IndependenceFamily string
	ContentHash        string
	RiskFlags          []string
	Position           int
}

type RetrievalDocument struct {
	Adapter           string
	CanonicalURL      string
	CanonicalIdentity string
	MIME              string
	Content           []byte
	ContentHash       string
	Cost              RetrievalCost
	Safety            RetrievalSafety
}

type RetrievalCost struct {
	Requests      int64
	InputBytes    int64
	OutputBytes   int64
	ProviderUnits float64
	Currency      string
	Amount        float64
}

type RetrievalSafety struct {
	RequestedURL         string
	FinalURL             string
	RedirectChain        []string
	ResolvedAddresses    []string
	CredentialsForwarded bool
	ScanDisposition      string
	ResponseBytes        int64
}

type RetrievalFailure struct {
	Class      string
	Retryable  bool
	RetryAfter time.Duration
	Message    string
}

func (failure RetrievalFailure) Error() string {
	return fmt.Sprintf("retrieval %s: %s", failure.Class, failure.Message)
}

func ValidateRetrievalSearchRequest(request RetrievalSearchRequest) error {
	if !validRetrievalToken(request.Adapter, 160) || strings.TrimSpace(request.Query) == "" || len(request.Query) > maxTaskObjectiveBytes {
		return fmt.Errorf("%w: retrieval adapter or query is invalid", ErrInvalidContract)
	}
	if len(request.Cursor) > maxRetrievalCursorBytes || request.Limit < 1 || request.Limit > maxRetrievalResults {
		return fmt.Errorf("%w: retrieval cursor or limit is invalid", ErrInvalidContract)
	}
	if err := validateRetrievalStringSet("languages", request.Languages, 32); err != nil {
		return err
	}
	if err := validateRetrievalStringSet("scopes", request.Scopes, 64); err != nil {
		return err
	}
	if request.WindowFrom != nil && request.WindowTo != nil && request.WindowFrom.After(*request.WindowTo) {
		return fmt.Errorf("%w: retrieval time window is inverted", ErrInvalidContract)
	}
	return nil
}

func ValidateRetrievalSearchPage(request RetrievalSearchRequest, page RetrievalSearchPage) error {
	if err := ValidateRetrievalSearchRequest(request); err != nil {
		return err
	}
	if page.Adapter != request.Adapter || page.CursorIn != request.Cursor || len(page.CursorOut) > maxRetrievalCursorBytes || len(page.Candidates) > request.Limit {
		return fmt.Errorf("%w: retrieval page does not match its request", ErrInvalidContract)
	}
	if err := validateRetrievalCost(page.Cost); err != nil {
		return err
	}
	if err := validateRetrievalSafety(page.Safety); err != nil {
		return err
	}
	if len(page.Candidates) > 0 && page.Safety.ScanDisposition != "safe" {
		return fmt.Errorf("%w: unsafe retrieval page cannot expose candidates", ErrInvalidContract)
	}
	identities := map[string]bool{}
	positions := map[int]bool{}
	for _, candidate := range page.Candidates {
		if err := validateRetrievalCandidate(candidate); err != nil {
			return err
		}
		if identities[candidate.CanonicalIdentity] || positions[candidate.Position] {
			return fmt.Errorf("%w: retrieval page repeats identity or position", ErrInvalidContract)
		}
		identities[candidate.CanonicalIdentity] = true
		positions[candidate.Position] = true
	}
	return nil
}

func ValidateRetrievalFetchRequest(request RetrievalFetchRequest) error {
	if !validRetrievalToken(request.Adapter, 160) || !validRetrievalToken(request.CanonicalIdentity, 512) {
		return fmt.Errorf("%w: retrieval fetch identity is invalid", ErrInvalidContract)
	}
	canonical, err := CanonicalURL(request.CanonicalURL)
	if err != nil || canonical != request.CanonicalURL {
		return fmt.Errorf("%w: retrieval fetch URL is not canonical", ErrInvalidContract)
	}
	if request.MaximumContentSize < 1 || request.MaximumContentSize > maxRetrievalDocumentBytes {
		return fmt.Errorf("%w: retrieval fetch content limit is invalid", ErrInvalidContract)
	}
	return nil
}

func ValidateRetrievalDocument(request RetrievalFetchRequest, document RetrievalDocument) error {
	if err := ValidateRetrievalFetchRequest(request); err != nil {
		return err
	}
	if document.Adapter != request.Adapter || document.CanonicalURL != request.CanonicalURL || document.CanonicalIdentity != request.CanonicalIdentity {
		return fmt.Errorf("%w: retrieval document does not match its request", ErrInvalidContract)
	}
	if len(document.Content) == 0 || int64(len(document.Content)) > request.MaximumContentSize || !allowedRetrievalMIME(document.MIME) {
		return fmt.Errorf("%w: retrieval document content or MIME is invalid", ErrInvalidContract)
	}
	wantHash := fmt.Sprintf("sha256:%x", sha256.Sum256(document.Content))
	if document.ContentHash != wantHash {
		return fmt.Errorf("%w: retrieval document content hash mismatch", ErrInvalidContract)
	}
	if err := validateRetrievalCost(document.Cost); err != nil {
		return err
	}
	if err := validateRetrievalSafety(document.Safety); err != nil {
		return err
	}
	if document.Safety.ScanDisposition != "safe" || document.Safety.FinalURL != document.CanonicalURL || document.Safety.ResponseBytes != int64(len(document.Content)) {
		return fmt.Errorf("%w: retrieval document safety facts do not match content", ErrInvalidContract)
	}
	return nil
}

func ValidateRetrievalFailure(failure RetrievalFailure) error {
	valid := map[string]bool{"rate_limited": true, "timeout": true, "provider_unavailable": true, "cursor_expired": true, "not_found": true, "permission_denied": true, "unsafe_target": true, "unsupported_content": true, "content_too_large": true, "invalid_response": true}
	if !valid[failure.Class] || strings.TrimSpace(failure.Message) == "" || len(failure.Message) > 4096 || failure.RetryAfter < 0 {
		return fmt.Errorf("%w: retrieval failure is invalid", ErrInvalidContract)
	}
	permanent := failure.Class == "not_found" || failure.Class == "permission_denied" || failure.Class == "unsafe_target" || failure.Class == "unsupported_content" || failure.Class == "content_too_large" || failure.Class == "invalid_response"
	if permanent && (failure.Retryable || failure.RetryAfter > 0) || !failure.Retryable && failure.RetryAfter > 0 {
		return fmt.Errorf("%w: retrieval failure retry policy is inconsistent", ErrInvalidContract)
	}
	return nil
}

func validateRetrievalCandidate(candidate RetrievalCandidate) error {
	canonical, err := CanonicalURL(candidate.CanonicalURL)
	if err != nil || canonical != candidate.CanonicalURL || !validRetrievalToken(candidate.CanonicalIdentity, 512) || !validRetrievalToken(candidate.IndependenceFamily, 160) {
		return fmt.Errorf("%w: retrieval candidate identity is invalid", ErrInvalidContract)
	}
	if strings.TrimSpace(candidate.Title) == "" || len(candidate.Title) > 4096 || strings.TrimSpace(candidate.Snippet) == "" || len(candidate.Snippet) > maxTaskObjectiveBytes || len(candidate.Publisher) > 4096 || candidate.Position < 1 {
		return fmt.Errorf("%w: retrieval candidate presentation is invalid", ErrInvalidContract)
	}
	if candidate.ContentHash != "" && !validRetrievalSHA256(candidate.ContentHash) {
		return fmt.Errorf("%w: retrieval candidate content hash is invalid", ErrInvalidContract)
	}
	return validateRetrievalStringSet("risk_flags", candidate.RiskFlags, 64)
}

func validateRetrievalSafety(safety RetrievalSafety) error {
	if safety.CredentialsForwarded || safety.ResponseBytes < 0 || safety.ResponseBytes > maxRetrievalDocumentBytes || len(safety.RedirectChain) > 10 || len(safety.ResolvedAddresses) == 0 || len(safety.ResolvedAddresses) > 32 {
		return fmt.Errorf("%w: retrieval safety metadata is invalid", ErrInvalidContract)
	}
	if safety.ScanDisposition != "safe" && safety.ScanDisposition != "quarantined" && safety.ScanDisposition != "rejected" {
		return fmt.Errorf("%w: retrieval scan disposition is invalid", ErrInvalidContract)
	}
	urls := append(append([]string{}, safety.RedirectChain...), safety.RequestedURL, safety.FinalURL)
	for _, raw := range urls {
		canonical, err := CanonicalURL(raw)
		if err != nil || canonical != raw {
			return fmt.Errorf("%w: retrieval safety URL is not canonical", ErrInvalidContract)
		}
	}
	for _, raw := range safety.ResolvedAddresses {
		address, err := netip.ParseAddr(raw)
		address = address.Unmap()
		if err != nil || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
			return fmt.Errorf("%w: retrieval resolved address is unsafe", ErrInvalidContract)
		}
	}
	return nil
}

func validateRetrievalCost(cost RetrievalCost) error {
	if cost.Requests < 0 || cost.InputBytes < 0 || cost.OutputBytes < 0 || cost.ProviderUnits < 0 || cost.Amount < 0 || cost.Currency != "" && !validRetrievalToken(cost.Currency, 16) || cost.Amount > 0 && cost.Currency == "" {
		return fmt.Errorf("%w: retrieval cost is invalid", ErrInvalidContract)
	}
	return nil
}

func validateRetrievalStringSet(name string, values []string, limit int) error {
	if len(values) > limit {
		return fmt.Errorf("%w: retrieval %s exceeds limit", ErrInvalidContract, name)
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !validRetrievalToken(value, 512) || seen[value] {
			return fmt.Errorf("%w: retrieval %s contains invalid or duplicate value", ErrInvalidContract, name)
		}
		seen[value] = true
	}
	return nil
}

func validRetrievalToken(value string, limit int) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= limit
}

func validRetrievalSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return false
		}
	}
	return true
}

func allowedRetrievalMIME(value string) bool {
	allowed := []string{"application/json", "application/pdf", "application/xml", "text/csv", "text/html", "text/markdown", "text/plain", "text/xml"}
	index := sort.SearchStrings(allowed, value)
	return index < len(allowed) && allowed[index] == value
}
