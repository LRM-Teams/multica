package researchrun

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestHTTPRetrievalAdapterFetchAcceptsPublicDocument(t *testing.T) {
	content := []byte("verified source text")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/source" || r.Host != "example.com" || r.Header.Get("Authorization") != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(content)
	}))
	defer server.Close()

	document, err := newTestHTTPRetrievalAdapter(t, server).Fetch(context.Background(), RetrievalFetchRequest{
		Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateRetrievalDocument(RetrievalFetchRequest{
		Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024,
	}, document); err != nil {
		t.Fatal(err)
	}
	if string(document.Content) != string(content) || document.MIME != "text/plain" || document.Safety.ScanDisposition != "safe" {
		t.Fatalf("document=%+v", document)
	}
}

func TestHTTPRetrievalAdapterFetchRejectsUnsafeOrUnusableTargets(t *testing.T) {
	content := []byte("verified source text")
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(content)
	}))
	defer okServer.Close()
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/secret", http.StatusFound)
	}))
	defer redirectServer.Close()
	oversizeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("too-large"))
	}))
	defer oversizeServer.Close()
	binaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer binaryServer.Close()
	missingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer missingServer.Close()

	cases := []struct {
		name    string
		adapter *HTTPRetrievalAdapter
		request RetrievalFetchRequest
		class   string
	}{
		{
			name:    "private address",
			adapter: newHTTPRetrievalAdapterWithLookup([]netip.Addr{netip.MustParseAddr("10.0.0.8")}, nil),
			request: RetrievalFetchRequest{Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024},
			class:   "unsafe_target",
		},
		{
			name:    "loopback address",
			adapter: newHTTPRetrievalAdapterWithLookup([]netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil),
			request: RetrievalFetchRequest{Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024},
			class:   "unsafe_target",
		},
		{
			name:    "mapped loopback",
			adapter: newHTTPRetrievalAdapterWithLookup([]netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}, nil),
			request: RetrievalFetchRequest{Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024},
			class:   "unsafe_target",
		},
		{
			name:    "redirect leaves canonical URL",
			adapter: newTestHTTPRetrievalAdapter(t, redirectServer),
			request: RetrievalFetchRequest{Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024},
			class:   "unsafe_target",
		},
		{
			name:    "content too large",
			adapter: newTestHTTPRetrievalAdapter(t, oversizeServer),
			request: RetrievalFetchRequest{Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 4},
			class:   "content_too_large",
		},
		{
			name:    "unsupported MIME",
			adapter: newTestHTTPRetrievalAdapter(t, binaryServer),
			request: RetrievalFetchRequest{Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024},
			class:   "unsupported_content",
		},
		{
			name:    "not found",
			adapter: newTestHTTPRetrievalAdapter(t, missingServer),
			request: RetrievalFetchRequest{Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024},
			class:   "not_found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.adapter.Fetch(context.Background(), tc.request)
			var failure RetrievalFailure
			if !errors.As(err, &failure) || failure.Class != tc.class || ValidateRetrievalFailure(failure) != nil {
				t.Fatalf("err=%v want class %s", err, tc.class)
			}
		})
	}
	_ = okServer
}

func TestHTTPRetrievalAdapterSearchFailsClosedWithoutProvider(t *testing.T) {
	adapter := NewHTTPRetrievalAdapter(HTTPRetrievalAdapterConfig{})
	_, err := adapter.Search(context.Background(), RetrievalSearchRequest{Adapter: "web-v1", Query: "research question", Limit: 10})
	var failure RetrievalFailure
	if !errors.As(err, &failure) || failure.Class != "provider_unavailable" || !failure.Retryable || ValidateRetrievalFailure(failure) != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPRetrievalAdapterFetchHashesExactBytes(t *testing.T) {
	content := []byte("verified source text")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(content)
	}))
	defer server.Close()
	document, err := newTestHTTPRetrievalAdapter(t, server).Fetch(context.Background(), RetrievalFetchRequest{
		Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	if document.ContentHash != want || document.Safety.ResponseBytes != int64(len(content)) || document.Safety.FinalURL != "http://example.com/source" {
		t.Fatalf("document=%+v hash=%s", document, want)
	}
}

func newTestHTTPRetrievalAdapter(t *testing.T, server *httptest.Server) *HTTPRetrievalAdapter {
	t.Helper()
	return newHTTPRetrievalAdapterWithLookup([]netip.Addr{netip.MustParseAddr("93.184.216.34")}, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	})
}

func newHTTPRetrievalAdapterWithLookup(addrs []netip.Addr, dial func(context.Context, string, string) (net.Conn, error)) *HTTPRetrievalAdapter {
	return NewHTTPRetrievalAdapter(HTTPRetrievalAdapterConfig{
		LookupIP: func(context.Context, string) ([]netip.Addr, error) { return addrs, nil },
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if dial == nil {
				return nil, errors.New("dial should not run for rejected targets")
			}
			return dial(ctx, network, address)
		},
		Timeout: 2 * time.Second,
	})
}

func TestHTTPRetrievalAdapterDoesNotReadPastLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", 64)))
	}))
	defer server.Close()
	_, err := newTestHTTPRetrievalAdapter(t, server).Fetch(context.Background(), RetrievalFetchRequest{
		Adapter: "web-v1", CanonicalURL: "http://example.com/source", CanonicalIdentity: "url:http://example.com/source", MaximumContentSize: 8,
	})
	var failure RetrievalFailure
	if !errors.As(err, &failure) || failure.Class != "content_too_large" {
		t.Fatalf("err=%v", err)
	}
}
