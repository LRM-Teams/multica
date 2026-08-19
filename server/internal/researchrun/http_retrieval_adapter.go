package researchrun

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const defaultHTTPRetrievalTimeout = 15 * time.Second

// HTTPRetrievalAdapter is the production Fetch seam. It resolves and dials
// only globally routable addresses, never forwards credentials, and refuses
// redirects that would leave the requested canonical URL. Search stays
// fail-closed until a dedicated search provider is configured.
type HTTPRetrievalAdapter struct {
	lookupIP    func(context.Context, string) ([]netip.Addr, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
	timeout     time.Duration
}

type HTTPRetrievalAdapterConfig struct {
	LookupIP    func(context.Context, string) ([]netip.Addr, error)
	DialContext func(context.Context, string, string) (net.Conn, error)
	Timeout     time.Duration
}

func NewHTTPRetrievalAdapter(cfg HTTPRetrievalAdapterConfig) *HTTPRetrievalAdapter {
	adapter := &HTTPRetrievalAdapter{lookupIP: cfg.LookupIP, dialContext: cfg.DialContext, timeout: cfg.Timeout}
	if adapter.lookupIP == nil {
		adapter.lookupIP = defaultRetrievalLookupIP
	}
	if adapter.dialContext == nil {
		adapter.dialContext = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	if adapter.timeout <= 0 {
		adapter.timeout = defaultHTTPRetrievalTimeout
	}
	return adapter
}

func (a *HTTPRetrievalAdapter) Search(_ context.Context, request RetrievalSearchRequest) (RetrievalSearchPage, error) {
	if err := ValidateRetrievalSearchRequest(request); err != nil {
		return RetrievalSearchPage{}, err
	}
	return RetrievalSearchPage{}, RetrievalFailure{
		Class: "provider_unavailable", Retryable: true,
		Message: "HTTP retrieval adapter has no search provider configured",
	}
}

func (a *HTTPRetrievalAdapter) Fetch(ctx context.Context, request RetrievalFetchRequest) (RetrievalDocument, error) {
	if a == nil {
		return RetrievalDocument{}, RetrievalFailure{Class: "provider_unavailable", Retryable: true, Message: "HTTP retrieval adapter is unavailable"}
	}
	if err := ValidateRetrievalFetchRequest(request); err != nil {
		return RetrievalDocument{}, err
	}
	parsed, err := url.Parse(request.CanonicalURL)
	if err != nil {
		return RetrievalDocument{}, RetrievalFailure{Class: "invalid_response", Message: "canonical URL could not be parsed"}
	}
	addrs, err := a.lookupIP(ctx, parsed.Hostname())
	if err != nil {
		return RetrievalDocument{}, retrievalTransportFailure(err)
	}
	resolved, err := validatedRetrievalAddresses(addrs)
	if err != nil {
		return RetrievalDocument{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, request.CanonicalURL, nil)
	if err != nil {
		return RetrievalDocument{}, RetrievalFailure{Class: "invalid_response", Message: "retrieval request could not be built"}
	}
	httpRequest.Header.Set("Accept", strings.Join([]string{
		"text/html", "text/plain", "text/markdown", "text/csv", "text/xml",
		"application/json", "application/xml", "application/pdf",
	}, ","))
	response, err := a.client().Do(httpRequest)
	if err != nil {
		return RetrievalDocument{}, retrievalTransportFailure(err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return RetrievalDocument{}, RetrievalFailure{Class: "unsafe_target", Message: "redirect left the canonical URL"}
	}
	if failure, ok := retrievalHTTPStatusFailure(response.StatusCode); ok {
		return RetrievalDocument{}, failure
	}
	mediaType := retrievalResponseMIME(response.Header.Get("Content-Type"))
	if !allowedRetrievalMIME(mediaType) {
		return RetrievalDocument{}, RetrievalFailure{Class: "unsupported_content", Message: "response MIME is not an allowed retrieval type"}
	}
	limited := io.LimitReader(response.Body, request.MaximumContentSize+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return RetrievalDocument{}, retrievalTransportFailure(err)
	}
	if int64(len(content)) > request.MaximumContentSize || len(content) == 0 {
		class := "content_too_large"
		if len(content) == 0 {
			class = "invalid_response"
		}
		return RetrievalDocument{}, RetrievalFailure{Class: class, Message: "retrieved content is empty or larger than the request limit"}
	}
	document := RetrievalDocument{
		Adapter: request.Adapter, CanonicalURL: request.CanonicalURL, CanonicalIdentity: request.CanonicalIdentity,
		MIME: mediaType, Content: content, ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(content)),
		Cost: RetrievalCost{Requests: 1, OutputBytes: int64(len(content))},
		Safety: RetrievalSafety{
			RequestedURL: request.CanonicalURL, FinalURL: request.CanonicalURL, ResolvedAddresses: resolved,
			ScanDisposition: "safe", ResponseBytes: int64(len(content)),
		},
	}
	if err = ValidateRetrievalDocument(request, document); err != nil {
		return RetrievalDocument{}, RetrievalFailure{Class: "invalid_response", Message: err.Error()}
	}
	return document, nil
}

func (a *HTTPRetrievalAdapter) client() *http.Client {
	return &http.Client{
		Timeout: a.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           a.dialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          4,
			IdleConnTimeout:       5 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			DisableCompression:    true,
		},
	}
}

func defaultRetrievalLookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func validatedRetrievalAddresses(addrs []netip.Addr) ([]string, error) {
	if len(addrs) == 0 || len(addrs) > 32 {
		return nil, RetrievalFailure{Class: "unsafe_target", Message: "DNS resolution returned no usable public address"}
	}
	resolved := make([]string, 0, len(addrs))
	seen := map[string]bool{}
	for _, addr := range addrs {
		addr = addr.Unmap()
		if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
			return nil, RetrievalFailure{Class: "unsafe_target", Message: "resolved address is not a public unicast target"}
		}
		value := addr.String()
		if seen[value] {
			continue
		}
		seen[value] = true
		resolved = append(resolved, value)
	}
	if len(resolved) == 0 {
		return nil, RetrievalFailure{Class: "unsafe_target", Message: "resolved address is not a public unicast target"}
	}
	return resolved, nil
}

func retrievalHTTPStatusFailure(status int) (RetrievalFailure, bool) {
	switch {
	case status == http.StatusOK:
		return RetrievalFailure{}, false
	case status == http.StatusNotFound:
		return RetrievalFailure{Class: "not_found", Message: "source returned not found"}, true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return RetrievalFailure{Class: "permission_denied", Message: "source denied retrieval"}, true
	case status == http.StatusTooManyRequests:
		return RetrievalFailure{Class: "rate_limited", Retryable: true, RetryAfter: time.Minute, Message: "source rate limited retrieval"}, true
	case status >= http.StatusInternalServerError:
		return RetrievalFailure{Class: "provider_unavailable", Retryable: true, Message: "source returned a server error"}, true
	default:
		return RetrievalFailure{Class: "invalid_response", Message: fmt.Sprintf("source returned HTTP %d", status)}, true
	}
}

func retrievalTransportFailure(err error) RetrievalFailure {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return RetrievalFailure{Class: "timeout", Retryable: true, Message: "retrieval timed out"}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return RetrievalFailure{Class: "timeout", Retryable: true, Message: "retrieval timed out"}
	}
	return RetrievalFailure{Class: "provider_unavailable", Retryable: true, Message: "retrieval transport failed"}
}

func retrievalResponseMIME(value string) string {
	media, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return strings.ToLower(media)
}

var _ RetrievalAdapter = (*HTTPRetrievalAdapter)(nil)
